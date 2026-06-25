// Package run_approved_command provides a skill that proposes a shell command
// to the operator via the HITL approval flow and, once approved, executes it.
//
// The operator sees the full command, the reason it was chosen, and the
// assessed risk level before deciding. Nothing is executed unless they approve.
package run_approved_command

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/skills/run_diagnostic_command"
	"agent_patches/endpoint-server/utils/notifier"
)

// commandTimeout bounds how long an approved command may run.
const commandTimeout = 5 * time.Minute

// noOpPhrases are placeholder "commands" the model sometimes submits when it
// has nothing to execute. All comparisons are done after lowercasing and
// trimming the input.
var noOpPhrases = []string{
	"none", "n/a", "na", "no action", "no action required", "no action needed",
	"no command", "no remediation", "no remediation required", "not applicable",
	"nothing", "nothing to do", "nothing required",
}

// isNoOpCommand reports whether s is a well-known placeholder rather than a
// real shell command.
func isNoOpCommand(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, phrase := range noOpPhrases {
		if lower == phrase {
			return true
		}
	}
	return false
}

// isEchoStatusCommand reports whether s is a bare echo (or PowerShell
// Write-Output / Write-Host) with no output redirection or pipe — i.e. a
// status message that belongs in response text, not in an approval request.
// Echo commands that redirect or pipe to something else may genuinely modify
// state and are allowed through.
func isEchoStatusCommand(s string) bool {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(fields[0])
	if first != "echo" && first != "write-output" && first != "write-host" {
		return false
	}
	return !strings.ContainsAny(s, ">|")
}

type runCommandInput struct {
	Title   string `json:"title" jsonschema_description:"Short one-line title for the approval card (e.g. 'Clear old log files from /var/log')."`
	Command string `json:"command" jsonschema_description:"The exact shell command to execute if the operator approves."`
	Reason  string `json:"reason" jsonschema_description:"Explanation of why this command was chosen and what issue it addresses."`
	Risk    string `json:"risk" jsonschema_description:"Risk level of running the command: low, medium, or high."`
}

// NewRunApprovedCommandTool returns a tool that presents a proposed shell
// command to the operator for approval, then executes it if approved.
// The command is run via sh -c so pipelines and shell builtins work.
// Nothing is executed unless the operator explicitly approves.
func NewRunApprovedCommandTool(mem *memory.Store, notify *notifier.Notifier) (tool.Tool, error) {
	return tool.New(
		"run_approved_command",
		"Propose a state-modifying shell command to the operator for approval, then execute it only "+
			"if approved. Use this tool ONLY for commands that change system state: installing or "+
			"removing packages, restarting or reconfiguring services, deleting or overwriting files, "+
			"modifying users or permissions, or applying updates. "+
			"NEVER use this for read-only commands. Commands such as du, ls, df, find, ps, cat, "+
			"grep, ss, netstat, journalctl, systemctl status, which, command -v, type, "+
			"tasklist, Get-Process, Get-NetTCPConnection, Get-Service, Get-Counter, "+
			"Get-CimInstance, Get-EventLog, Get-WinEvent, Get-ChildItem, and any "+
			"other listing, querying, or reporting command — Unix or PowerShell alike — "+
			"are ALWAYS run_diagnostic_command "+
			"even when the purpose is to inform a future cleanup or check prerequisites. "+
			"Read-only commands never require operator approval; submitting one here is "+
			"rejected and must be retried via run_diagnostic_command instead. "+
			"The operator sees the full command, reason, and risk level before deciding. "+
			"Returns the command output on approval, or a cancellation message on rejection.",
		func(ctx context.Context, in runCommandInput) (string, error) {
			cmd := strings.TrimSpace(in.Command)
			if cmd == "" {
				return "", fmt.Errorf("run_approved_command: command must not be empty — if no action is needed, write your conclusion in response text instead")
			}
			if isNoOpCommand(cmd) {
				return "", fmt.Errorf("run_approved_command: %q is not an executable command — if no corrective action is needed, write your conclusion in response text or call report_findings instead of submitting a placeholder approval", cmd)
			}
			if isEchoStatusCommand(cmd) {
				return "", fmt.Errorf("run_approved_command: echo is not a state-modifying command — write the message in your response text or call report_findings instead of routing it through an approval request")
			}
			if run_diagnostic_command.IsDiagnosticEligible(cmd) {
				return "", fmt.Errorf("run_approved_command: %q is a read-only diagnostic command and does not require operator approval — call run_diagnostic_command instead, which runs it immediately", cmd)
			}

			host, _ := os.Hostname()
			title := in.Title
			if title == "" {
				title = "Run command on " + host
			}

			decision, err := reqapproval.RequestApproval(
				ctx, mem, notify,
				title,
				fmt.Sprintf("Host: %s\n\nReason: %s", host, in.Reason),
				cmd,
				in.Risk,
			)
			if err != nil {
				return "", fmt.Errorf("approval interrupted: %w", err)
			}

			switch decision {
			case "rejected":
				slog.Info("run_approved_command: operator rejected command", "command", cmd)
				return "Command rejected by operator — not executed.", nil
			case "timed_out":
				slog.Info("run_approved_command: approval timed out", "command", cmd)
				return "Command not executed: approval request timed out.", nil
			}

			// Approved — run it.
			slog.Info("run_approved_command: executing approved command", "command", cmd)
			cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
			defer cancel()

			execCmd := exec.CommandContext(cmdCtx, "sh", "-c", cmd) //nolint:gosec
			out, execErr := execCmd.CombinedOutput()
			output := strings.TrimRight(string(out), "\n")

			if execErr != nil {
				slog.Warn("run_approved_command: command failed", "command", cmd, "error", execErr)
				if output != "" {
					return fmt.Sprintf("Command failed (%v):\n%s", execErr, output), nil
				}
				return fmt.Sprintf("Command failed: %v", execErr), nil
			}

			slog.Info("run_approved_command: command completed successfully", "command", cmd, "output_len", len(output))
			if output == "" {
				return "Command completed with no output.", nil
			}
			return output, nil
		},
	)
}
