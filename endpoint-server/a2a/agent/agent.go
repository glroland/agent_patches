package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/sanitize"
)

// Agent drives the tool-use loop against the OpenAI chat completions API.
type Agent struct {
	client       openai.Client
	tools        []tool.Tool
	cfg          *config.Settings
	systemPrompt string
}

// New creates an Agent using cfg.Agent.SystemPrompt as the system message.
func New(tools []tool.Tool, cfg *config.Settings) *Agent {
	return NewWithSystemPrompt(tools, cfg, cfg.Agent.SystemPrompt)
}

// NewWithSystemPrompt creates an Agent that uses systemPrompt as the system
// message instead of cfg.Agent.SystemPrompt.
func NewWithSystemPrompt(tools []tool.Tool, cfg *config.Settings, systemPrompt string) *Agent {
	slog.Debug("agent: initialised",
		"model", cfg.Agent.Model,
		"max_tokens", cfg.Agent.MaxTokens,
		"max_iterations", cfg.Agent.MaxIter,
		"tools", len(tools),
	)
	opts := []option.RequestOption{
		option.WithHTTPClient(&http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}),
	}
	if cfg.Agent.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.Agent.APIKey))
	}
	if cfg.Agent.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.Agent.BaseURL))
	}
	return &Agent{
		client:       openai.NewClient(opts...),
		tools:        tools,
		cfg:          cfg,
		systemPrompt: systemPrompt,
	}
}

// Run executes the agent loop for the given input and returns the final
// text response. It satisfies the a2a.Runner interface.
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	ctx, span := otel.Tracer("agent_patches/agent").Start(ctx, "agent.run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("model", a.cfg.Agent.Model),
			attribute.Int("llm.request.max_tokens", a.cfg.Agent.MaxTokens),
			attribute.Int("llm.request.max_iterations", a.cfg.Agent.MaxIter),
			attribute.String("llm.system_prompt", a.systemPrompt),
			attribute.String("llm.user_input", input),
		),
	)
	defer span.End()

	slog.Debug("agent: run started", "input_len", len(input))

	toolIndex := make(map[string]tool.Tool, len(a.tools))
	oaiTools := make([]openai.ChatCompletionToolParam, 0, len(a.tools))
	for _, t := range a.tools {
		toolIndex[t.Name()] = t
		var schema openai.FunctionParameters
		_ = json.Unmarshal(t.InputSchema(), &schema)
		oaiTools = append(oaiTools, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name(),
				Description: param.NewOpt(t.Description()),
				Parameters:  schema,
			},
		})
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(a.systemPrompt),
		openai.UserMessage(input),
	}

	// Some local models keep re-issuing an identical tool call instead of
	// producing a final answer once they have the information they asked
	// for. Track tool-call signatures (name + arguments) so repeats can be
	// short-circuited instead of burning through MaxIter.
	toolCallCounts := make(map[string]int)
	lastToolResult := make(map[string]string)

	for iter := 0; iter < a.cfg.Agent.MaxIter; iter++ {
		req := openai.ChatCompletionNewParams{
			Model:     a.cfg.Agent.Model,
			MaxTokens: param.NewOpt(int64(a.cfg.Agent.MaxTokens)),
			Messages:  messages,
		}
		if len(oaiTools) > 0 {
			req.Tools = oaiTools
		}

		inferCtx, inferSpan := otel.Tracer("agent_patches/agent").Start(ctx, "llm.inference",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.Int("iteration", iter),
				attribute.String("model", a.cfg.Agent.Model),
				attribute.Int("llm.request.tool_count", len(oaiTools)),
				attribute.String("llm.request.messages", marshalJSON(messages)),
			),
		)

		resp, err := a.client.Chat.Completions.New(inferCtx, req)
		if err != nil {
			inferSpan.RecordError(err)
			inferSpan.SetStatus(codes.Error, err.Error())
			inferSpan.End()
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			slog.Error("agent: API call failed", "error", err)
			return "", fmt.Errorf("agent run failed: %w", err)
		}

		inferSpan.SetAttributes(
			attribute.Int64("llm.usage.prompt_tokens", resp.Usage.PromptTokens),
			attribute.Int64("llm.usage.completion_tokens", resp.Usage.CompletionTokens),
			attribute.Int64("llm.usage.total_tokens", resp.Usage.TotalTokens),
		)

		if len(resp.Choices) == 0 {
			inferSpan.SetStatus(codes.Error, "empty response from API")
			inferSpan.End()
			err := fmt.Errorf("agent: empty response from API")
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return "", err
		}

		choice := resp.Choices[0]
		inferSpan.SetAttributes(attribute.String("llm.response.finish_reason", string(choice.FinishReason)))
		if choice.Message.Content != "" {
			inferSpan.SetAttributes(attribute.String("llm.response.content", choice.Message.Content))
		}
		if len(choice.Message.ToolCalls) > 0 {
			inferSpan.SetAttributes(attribute.String("llm.response.tool_calls", marshalJSON(choice.Message.ToolCalls)))
		}
		inferSpan.SetStatus(codes.Ok, "")
		inferSpan.End()

		if choice.FinishReason != "tool_calls" {
			output := choice.Message.Content
			slog.Debug("agent: run completed", "output_len", len(output))
			span.SetStatus(codes.Ok, "")
			return output, nil
		}

		// Append the assistant turn (with tool calls) to the conversation.
		messages = append(messages, choice.Message.ToParam())

		// Execute each requested tool call and append results.
		for _, tc := range choice.Message.ToolCalls {
			t, ok := toolIndex[tc.Function.Name]
			if !ok {
				slog.Warn("agent: unknown tool", "tool", tc.Function.Name)
				messages = append(messages, openai.ToolMessage("unknown tool: "+tc.Function.Name, tc.ID))
				continue
			}

			sig := tc.Function.Name + ":" + tc.Function.Arguments
			toolCallCounts[sig]++
			count := toolCallCounts[sig]

			if count >= 3 {
				cached := lastToolResult[sig]
				slog.Warn("agent: tool called repeatedly with identical arguments, returning last result instead of retrying",
					"tool", tc.Function.Name, "count", count)
				return fmt.Sprintf(
					"%s\n\n(Note: the model repeated this tool call %d times without producing a final report; returning the last result.)",
					cached, count,
				), nil
			}

			if count == 2 {
				slog.Debug("agent: tool called again with identical arguments, nudging model to finish", "tool", tc.Function.Name)
				messages = append(messages, openai.ToolMessage(
					lastToolResult[sig]+"\n\n[You already have this result from a previous call. "+
						"Do not call this tool again with the same arguments — use the information "+
						"above to write your final report now.]",
					tc.ID,
				))
				continue
			}

			slog.Debug("agent: executing tool", "tool", tc.Function.Name)
			toolCtx, toolSpan := otel.Tracer("agent_patches/agent").Start(ctx, "tool.call",
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(
					attribute.String("tool.name", tc.Function.Name),
					attribute.String("tool.arguments", tc.Function.Arguments),
				),
			)
			result, execErr := t.Execute(toolCtx, json.RawMessage(tc.Function.Arguments))
			if execErr != nil {
				toolSpan.RecordError(execErr)
				toolSpan.SetStatus(codes.Error, execErr.Error())
				toolSpan.End()
				slog.Warn("agent: tool error", "tool", tc.Function.Name, "error", execErr)
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("error: %v", execErr), tc.ID))
				continue
			}
			// Sanitize tool output before it enters the LLM context to guard
			// against prompt injection carried in system data (logs, package
			// descriptions, command output, etc.).
			result, sanitizeEvents := sanitize.ToolOutput(result)
			if sanitizeEvents > 0 {
				toolSpan.SetAttributes(
					attribute.Bool("security.sanitized", true),
					attribute.Int("security.sanitize_events", sanitizeEvents),
				)
			}
			toolSpan.SetAttributes(attribute.String("tool.result", result))
			toolSpan.SetStatus(codes.Ok, "")
			toolSpan.End()
			lastToolResult[sig] = result
			messages = append(messages, openai.ToolMessage(result, tc.ID))
		}
	}

	err := fmt.Errorf("agent: max iterations (%d) exceeded", a.cfg.Agent.MaxIter)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return "", err
}

// marshalJSON serialises v to a JSON string for use as a span attribute.
// Returns a descriptive placeholder on error so spans are never silently empty.
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(b)
}
