package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/run_approved_command"
	"agent_patches/endpoint-server/utils/config"
)

func newRunApprovedCommandTool(t *testing.T) (tool.Tool, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := run_approved_command.NewRunApprovedCommandTool(mem, nil)
	if err != nil {
		t.Fatalf("NewRunApprovedCommandTool: %v", err)
	}
	return tl, mem
}

func runApprovedCommandInput(t *testing.T, command string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"title":   "test",
		"command": command,
		"reason":  "test",
		"risk":    "low",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return b
}

// Reproduces the reported bug: a read-only PowerShell query
// (counting TIME_WAIT TCP connections) was routed through run_approved_command
// and sat waiting on an unnecessary operator approval. It must be rejected
// immediately, pointing the model at run_diagnostic_command, and must not
// create any pending approval.
func TestRunApprovedCommandTool_RejectsDiagnosticEligibleCommand(t *testing.T) {
	tool, mem := newRunApprovedCommandTool(t)

	cmd := `powershell -Command "(Get-NetTCPConnection -State TimeWait).Count"`
	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, cmd))
	if err == nil {
		t.Fatal("Execute: want error for diagnostic-eligible command, got nil")
	}
	if !strings.Contains(err.Error(), "run_diagnostic_command") {
		t.Errorf("error = %q, want it to mention run_diagnostic_command", err.Error())
	}

	var entries []map[string]any
	_ = mem.Domain("timeline").ReadCurrent(&entries)
	if len(entries) != 0 {
		t.Errorf("timeline entries = %d, want 0 (no approval should have been created)", len(entries))
	}
}

// Bare PowerShell Verb-Noun cmdlets (no "powershell -Command" wrapper) must
// also be caught, since run_diagnostic_command accepts them directly.
func TestRunApprovedCommandTool_RejectsBarePowerShellCmdlet(t *testing.T) {
	tool, _ := newRunApprovedCommandTool(t)

	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "Get-Process | Select-Object -First 1"))
	if err == nil {
		t.Fatal("Execute: want error for diagnostic-eligible command, got nil")
	}
	if !strings.Contains(err.Error(), "run_diagnostic_command") {
		t.Errorf("error = %q, want it to mention run_diagnostic_command", err.Error())
	}
}

func TestRunApprovedCommandTool_RejectsEmptyCommand(t *testing.T) {
	tool, _ := newRunApprovedCommandTool(t)

	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, ""))
	if err == nil {
		t.Fatal("Execute: want error for empty command, got nil")
	}
}

func TestRunApprovedCommandTool_RejectsNoOpPlaceholder(t *testing.T) {
	tool, _ := newRunApprovedCommandTool(t)

	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "no action required"))
	if err == nil {
		t.Fatal("Execute: want error for no-op placeholder, got nil")
	}
}

func TestRunApprovedCommandTool_RejectsEchoStatus(t *testing.T) {
	tool, _ := newRunApprovedCommandTool(t)

	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "echo all good"))
	if err == nil {
		t.Fatal("Execute: want error for bare echo, got nil")
	}
}
