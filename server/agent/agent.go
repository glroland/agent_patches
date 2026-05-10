package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"agent_patches/server/config"
)

// Agent drives the tool-use loop against the Claude API.
type Agent struct {
	client anthropic.Client
	tools  []anthropic.BetaTool
	cfg    *config.Settings
}

func New(tools []anthropic.BetaTool, cfg *config.Settings) *Agent {
	slog.Debug("agent: initialised",
		"model", cfg.Agent.Model,
		"max_tokens", cfg.Agent.MaxTokens,
		"max_iterations", cfg.Agent.MaxIter,
		"tools", len(tools),
	)
	return &Agent{
		client: anthropic.NewClient(),
		tools:  tools,
		cfg:    cfg,
	}
}

// Run executes the agent loop for the given input and returns the final
// text response. It satisfies the a2a.Runner interface.
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	slog.Debug("agent: run started", "input_len", len(input))

	runner := a.client.Beta.Messages.NewToolRunner(
		a.tools,
		anthropic.BetaToolRunnerParams{
			BetaMessageNewParams: anthropic.BetaMessageNewParams{
				Model:     a.cfg.Agent.Model,
				MaxTokens: int64(a.cfg.Agent.MaxTokens),
				System:    []anthropic.BetaTextBlockParam{{Text: a.cfg.Agent.SystemPrompt}},
				Messages: []anthropic.BetaMessageParam{
					anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(input)),
				},
			},
			MaxIterations: a.cfg.Agent.MaxIter,
		},
	)

	message, err := runner.RunToCompletion(ctx)
	if err != nil {
		slog.Error("agent: run failed", "error", err)
		return "", fmt.Errorf("agent run failed: %w", err)
	}

	var sb strings.Builder
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(b.Text)
		}
	}

	output := sb.String()
	slog.Debug("agent: run completed", "output_len", len(output))
	return output, nil
}
