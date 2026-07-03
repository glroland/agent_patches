package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/policy"
	"agent_patches/endpoint-server/skills/run_approved_command"
	"agent_patches/endpoint-server/utils/config"
)

func newRunApprovedCommandTool(t *testing.T) (tool.Tool, *memory.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := run_approved_command.NewRunApprovedCommandTool(mem, nil, policy.New(mem))
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

// Reproduces a second reported variant: on Linux, the model used the shell
// no-op builtin "true" (always succeeds, does nothing) as a placeholder
// command instead of skipping the approval entirely. Unlike the English
// no-op phrases above, "true" is a real, executable shell command, so it
// is not caught by isNoOpCommand — it needs its own check.
func TestRunApprovedCommandTool_RejectsNoOpShellBuiltin(t *testing.T) {
	for _, cmd := range []string{"true", "True", "  true  ", ":"} {
		tool, mem := newRunApprovedCommandTool(t)

		_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, cmd))
		if err == nil {
			t.Fatalf("Execute(%q): want error for no-op shell builtin, got nil", cmd)
		}

		var entries []map[string]any
		_ = mem.Domain("timeline").ReadCurrent(&entries)
		if len(entries) != 0 {
			t.Errorf("Execute(%q): timeline entries = %d, want 0 (no approval should have been created)", cmd, len(entries))
		}
	}
}

// A "true" that is part of a larger, genuinely state-modifying expression
// must not be caught by the no-op builtin check — only a bare "true" (or
// ":") as the entire command is a placeholder.
func TestRunApprovedCommandTool_DoesNotRejectTrueWithinLargerCommand(t *testing.T) {
	if run_approved_command.IsNoOpShellBuiltinForTest("systemctl restart foo || true") {
		t.Error(`"systemctl restart foo || true" should not be treated as a no-op placeholder`)
	}
}

func TestRunApprovedCommandTool_RejectsEchoStatus(t *testing.T) {
	tool, _ := newRunApprovedCommandTool(t)

	_, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "echo all good"))
	if err == nil {
		t.Fatal("Execute: want error for bare echo, got nil")
	}
}

// newRunApprovedCommandToolWithPolicies builds the tool with an explicit
// policy store so tests can pre-approve command classes.
func newRunApprovedCommandToolWithPolicies(t *testing.T) (tool.Tool, *memory.Store, *policy.Store) {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	policies := policy.New(mem)
	tl, err := run_approved_command.NewRunApprovedCommandTool(mem, nil, policies)
	if err != nil {
		t.Fatalf("NewRunApprovedCommandTool: %v", err)
	}
	return tl, mem, policies
}

// A command matching an operator-created standing approval policy executes
// immediately — no pending approval is created — and the run is recorded on
// the timeline as an action referencing the policy.
func TestRunApprovedCommandTool_StandingPolicyExecutesImmediately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executes via sh -c")
	}
	tool, mem, policies := newRunApprovedCommandToolWithPolicies(t)

	dir := t.TempDir()
	if _, err := policies.Add("touch test marker files", "touch "+dir+"/[a-z]+", "low"); err != nil {
		t.Fatalf("Add policy: %v", err)
	}

	marker := filepath.Join(dir, "created")
	out, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "touch "+marker))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "standing approval policy") {
		t.Errorf("output = %q, want it to mention the standing approval policy", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file not created — command did not execute: %v", err)
	}

	var entries []map[string]any
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("timeline entries = %d, want 1 action entry", len(entries))
	}
	if entries[0]["type"] != "action" {
		t.Errorf("timeline entry type = %v, want action", entries[0]["type"])
	}
	// No approval attrs entry should exist — the HITL flow was skipped.
	attrs, err := mem.Attrs().All()
	if err != nil {
		t.Fatalf("read attrs: %v", err)
	}
	for k := range attrs {
		if strings.HasPrefix(k, "approval:") {
			t.Errorf("unexpected pending approval %s — standing policy should skip HITL", k)
		}
	}
}

// A command that does NOT match any standing policy must not execute through
// the policy fast-path (it would instead block on HITL approval, which the
// anchored-match tests in policy_test.go cover at the store level).
func TestRunApprovedCommandTool_PolicyDoesNotMatchChainedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executes via sh -c")
	}
	_, _, policies := newRunApprovedCommandToolWithPolicies(t)

	dir := t.TempDir()
	if _, err := policies.Add("touch test marker files", "touch "+dir+"/[a-z]+", "low"); err != nil {
		t.Fatalf("Add policy: %v", err)
	}
	if p := policies.Match("touch " + dir + "/ok && rm -rf /"); p != nil {
		t.Errorf("chained command matched policy %q — pattern anchoring is broken", p.Pattern)
	}
}
