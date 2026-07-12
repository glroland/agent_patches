package loop

import (
	"context"
	"errors"
	"testing"

	tasks "agent_patches/endpoint-server/a2a/registry"
	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func newTestLoop(t *testing.T, respCfg config.ResponsibilitySettings) (*Loop, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	cfg := &config.Settings{Responsibilities: []config.ResponsibilitySettings{respCfg}}
	l := New(cfg, tasks.NewRegistry(), notifier.New(mem), mem, incidents.New(mem))
	return l, mem
}

// TestExecute_PreCheckSkipsLLMWhenHealthy verifies that a registered PreCheck
// reporting needsLLM=false short-circuits execute before it ever constructs
// an agent — i.e. no LLM call is made. If execute fell through to
// agent.Run, it would try to reach a real Anthropic endpoint with the zero
// value config.Settings.Agent and fail well before persisting the pre-check's
// summary text, so asserting the persisted state matches confirms the skip.
func TestExecute_PreCheckSkipsLLMWhenHealthy(t *testing.T) {
	l, mem := newTestLoop(t, config.ResponsibilitySettings{
		Name:        "test-resp",
		Frequency:   "1h",
		Instruction: "should never reach an agent",
	})

	var called bool
	l.RegisterPreCheck("test-resp", func(ctx context.Context) (bool, string, error) {
		called = true
		return false, "all clear", nil
	})

	r := l.Responsibilities()[0]
	l.execute(context.Background(), r)

	if !called {
		t.Fatal("pre-check was not invoked")
	}

	var state RunState
	if err := mem.Domain(skillstate.Domain).GetKey(AttrRunPrefix+"test-resp", &state); err != nil {
		t.Fatalf("run state not persisted: %v", err)
	}
	if state.Status != "ok" || state.Summary != "all clear" {
		t.Fatalf("unexpected run state: %+v", state)
	}
}

// TestExecute_PreCheckErrorFailsOpen verifies a failing PreCheck does not
// silently skip the run — needsLLM is treated as true so the (real) agent
// path is attempted instead of going quiet.
func TestExecute_PreCheckErrorFailsOpen(t *testing.T) {
	l, mem := newTestLoop(t, config.ResponsibilitySettings{
		Name:        "test-resp",
		Frequency:   "1h",
		Instruction: "irrelevant",
	})

	l.RegisterPreCheck("test-resp", func(ctx context.Context) (bool, string, error) {
		return false, "should be ignored", errors.New("boom")
	})

	r := l.Responsibilities()[0]
	l.execute(context.Background(), r)

	// The pre-check's report/needsLLM must be ignored on error; execute falls
	// through to the agent path, which fails against the zero-value Anthropic
	// config and persists an error state — never the pre-check's summary.
	var state RunState
	if err := mem.Domain(skillstate.Domain).GetKey(AttrRunPrefix+"test-resp", &state); err != nil {
		t.Fatalf("run state not persisted: %v", err)
	}
	if state.Summary == "should be ignored" {
		t.Fatal("pre-check error did not fail open: skipped LLM path using its report")
	}
}
