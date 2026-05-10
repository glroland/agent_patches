package tests

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"agent_patches/server/executor"
)

// mockRunner is a test double for executor.Runner.
type mockRunner struct {
	resp string
	err  error
}

func (m *mockRunner) Run(_ context.Context, _ string) (string, error) {
	return m.resp, m.err
}

// collect drains an iter.Seq2 into slices of events and errors.
func collect(seq iter.Seq2[a2a.Event, error]) ([]a2a.Event, []error) {
	var events []a2a.Event
	var errs []error
	for ev, err := range seq {
		events = append(events, ev)
		errs = append(errs, err)
	}
	return events, errs
}

func TestExecutor_ReturnsAgentMessage(t *testing.T) {
	exec := executor.New(&mockRunner{resp: "world"})
	execCtx := &a2asrv.ExecutorContext{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
	}

	events, errs := collect(exec.Execute(context.Background(), execCtx))

	if len(events) != 1 {
		t.Fatalf("Execute() yielded %d events, want 1", len(events))
	}
	if errs[0] != nil {
		t.Fatalf("Execute() yielded error: %v", errs[0])
	}
	msg, ok := events[0].(*a2a.Message)
	if !ok {
		t.Fatalf("event type = %T, want *a2a.Message", events[0])
	}
	if got := msg.Parts[0].Text(); got != "world" {
		t.Errorf("message text = %q, want %q", got, "world")
	}
}

func TestExecutor_PropagatesRunnerError(t *testing.T) {
	exec := executor.New(&mockRunner{err: errors.New("claude unavailable")})
	execCtx := &a2asrv.ExecutorContext{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
	}

	_, errs := collect(exec.Execute(context.Background(), execCtx))

	if len(errs) == 0 || errs[0] == nil {
		t.Fatal("Execute() expected an error, got none")
	}
}

func TestExecutor_EmptyMessageYieldsError(t *testing.T) {
	exec := executor.New(&mockRunner{resp: "world"})
	execCtx := &a2asrv.ExecutorContext{
		Message: a2a.NewMessage(a2a.MessageRoleUser),
	}

	_, errs := collect(exec.Execute(context.Background(), execCtx))

	if len(errs) == 0 || errs[0] == nil {
		t.Fatal("Execute() with empty message expected an error, got none")
	}
}

func TestExecutor_NilMessageYieldsError(t *testing.T) {
	exec := executor.New(&mockRunner{resp: "world"})
	execCtx := &a2asrv.ExecutorContext{Message: nil}

	_, errs := collect(exec.Execute(context.Background(), execCtx))

	if len(errs) == 0 || errs[0] == nil {
		t.Fatal("Execute() with nil message expected an error, got none")
	}
}

func TestExecutor_Cancel_YieldsStatusEvent(t *testing.T) {
	exec := executor.New(&mockRunner{})
	execCtx := &a2asrv.ExecutorContext{}

	events, errs := collect(exec.Cancel(context.Background(), execCtx))

	if len(events) != 1 {
		t.Fatalf("Cancel() yielded %d events, want 1", len(events))
	}
	if errs[0] != nil {
		t.Fatalf("Cancel() yielded error: %v", errs[0])
	}
	ev, ok := events[0].(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("event type = %T, want *a2a.TaskStatusUpdateEvent", events[0])
	}
	if ev.Status.State != a2a.TaskStateCanceled {
		t.Errorf("state = %q, want %q", ev.Status.State, a2a.TaskStateCanceled)
	}
}
