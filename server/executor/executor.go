package executor

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Runner is the interface the Executor uses to process a task.
type Runner interface {
	Run(ctx context.Context, input string) (string, error)
}

// Executor bridges the A2A AgentExecutor interface to our Runner.
type Executor struct {
	runner Runner
}

func New(runner Runner) *Executor {
	return &Executor{runner: runner}
}

// Execute satisfies a2asrv.AgentExecutor. It extracts the input text from the
// incoming message, calls the runner, and yields the result as a single agent message.
func (e *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		input := extractText(execCtx.Message)
		if input == "" {
			yield(nil, fmt.Errorf("message contains no text parts"))
			return
		}

		slog.Debug("executor: running", "input_len", len(input))
		output, err := e.runner.Run(ctx, input)
		if err != nil {
			slog.Error("executor: runner failed", "error", err)
			yield(nil, err)
			return
		}

		slog.Debug("executor: done", "output_len", len(output))
		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(output)), nil)
	}
}

// Cancel satisfies a2asrv.AgentExecutor.
func (e *Executor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

func extractText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Parts {
		sb.WriteString(part.Text())
	}
	return strings.TrimSpace(sb.String())
}
