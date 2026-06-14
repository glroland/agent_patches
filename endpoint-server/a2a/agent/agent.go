package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/utils/config"
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
	opts := []option.RequestOption{}
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

		resp, err := a.client.Chat.Completions.New(ctx, req)
		if err != nil {
			slog.Error("agent: API call failed", "error", err)
			return "", fmt.Errorf("agent run failed: %w", err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("agent: empty response from API")
		}

		choice := resp.Choices[0]

		if choice.FinishReason != "tool_calls" {
			output := choice.Message.Content
			slog.Debug("agent: run completed", "output_len", len(output))
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
			result, execErr := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			if execErr != nil {
				slog.Warn("agent: tool error", "tool", tc.Function.Name, "error", execErr)
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("error: %v", execErr), tc.ID))
				continue
			}
			lastToolResult[sig] = result
			messages = append(messages, openai.ToolMessage(result, tc.ID))
		}
	}

	return "", fmt.Errorf("agent: max iterations (%d) exceeded", a.cfg.Agent.MaxIter)
}
