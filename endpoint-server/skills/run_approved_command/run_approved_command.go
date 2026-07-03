// Package run_approved_command provides a skill that proposes a shell command
// to the operator via the HITL approval flow.
//
// The flow is asynchronous: the tool files the approval and returns
// immediately, so a pending approval never parks the agent run (or its
// responsibility slot) waiting on the operator. When the operator approves
// via the dashboard, the approvalapi decision handler calls ExecuteOnApproval
// to run the command and record the result on the timeline. Pending requests
// survive agent restarts and are expired by the request_approval sweeper
// after 24 hours.
//
// The operator sees the full command, the reason it was chosen, and the
// assessed risk level before deciding. Nothing is executed unless they
// approve — with one exception: commands matching an operator-created
// standing approval policy (see the policy package) execute immediately,
// because the operator has already approved that command class in advance.
package run_approved_command

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/policy"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	reqmanualrun "agent_patches/endpoint-server/skills/request_manual_run"
	"agent_patches/endpoint-server/skills/run_diagnostic_command"
	"agent_patches/endpoint-server/status"
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

// noOpShellBuiltins are shell idioms that intentionally do nothing and
// always succeed — used as a placeholder "command" when the model has
// nothing to propose but the tool still requires one.
var noOpShellBuiltins = map[string]bool{
	"true": true, ":": true,
}

// isNoOpShellBuiltin reports whether s, taken as a whole, is one of
// noOpShellBuiltins. Commands that merely contain "true" as part of a larger
// expression (e.g. "systemctl restart foo || true") are not affected, since
// the comparison is against the full trimmed command, not a substring.
func isNoOpShellBuiltin(s string) bool {
	return noOpShellBuiltins[strings.ToLower(strings.TrimSpace(s))]
}

// IsNoOpShellBuiltinForTest exposes isNoOpShellBuiltin for package-external tests.
func IsNoOpShellBuiltinForTest(s string) bool { return isNoOpShellBuiltin(s) }

// isSudoersError reports whether output from a failed sudo -n invocation
// indicates that the command is blocked by the sudoers policy. Any "sudo:"
// prefixed error line is a reliable signal; sudo exits non-zero in all such
// cases (password required, command not allowed, user not in sudoers file).
func isSudoersError(output string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(output)), "sudo:")
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
// Nothing is executed unless the operator explicitly approves, except
// commands matching a standing approval policy in policies (nil disables
// policy matching), which the operator has pre-approved.
func NewRunApprovedCommandTool(mem *memory.Store, notify *notifier.Notifier, policies *policy.Store) (tool.Tool, error) {
	return tool.New(
		"run_approved_command",
		"Propose a state-modifying shell command for operator approval. The request is filed "+
			"asynchronously: this tool returns immediately with a pending-approval confirmation, and "+
			"the command executes automatically once the operator approves (within 24 hours), with the "+
			"result recorded on the host timeline. Do not wait for or poll the decision. "+
			"Use this tool ONLY for commands that change system state: installing or "+
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
			"Commands matching an operator-created standing approval policy execute "+
			"immediately without a fresh approval and return their output directly; "+
			"the result says so when that happens.",
		func(ctx context.Context, in runCommandInput) (string, error) {
			cmd := strings.TrimSpace(in.Command)
			if cmd == "" {
				return "", fmt.Errorf("run_approved_command: command must not be empty — if no action is needed, write your conclusion in response text instead")
			}
			if isNoOpCommand(cmd) {
				return "", fmt.Errorf("run_approved_command: %q is not an executable command — if no corrective action is needed, write your conclusion in response text or call report_findings instead of submitting a placeholder approval", cmd)
			}
			if isNoOpShellBuiltin(cmd) {
				return "", fmt.Errorf("run_approved_command: %q is a no-op placeholder, not a real action — if no corrective action is needed, write your conclusion in response text or call report_findings instead of submitting a placeholder approval", cmd)
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

			// Standing approval policy: the operator has pre-approved this
			// command class, so execute immediately and record it on the
			// timeline so the action is still visible in the dashboard.
			if p := policies.Match(cmd); p != nil {
				slog.Info("run_approved_command: executing under standing policy",
					"command", cmd, "policy_id", p.ID, "policy", p.Description)
				policyNote := fmt.Sprintf("Executed immediately under standing approval policy %s (%q).", p.ID, p.Description)
				if err := status.AppendTimeline(mem, status.TimelineEntry{
					Type:     "action",
					Title:    title,
					Detail:   fmt.Sprintf("Command: %s\n\nReason: %s\n\n%s", cmd, in.Reason, policyNote),
					Severity: "info",
				}); err != nil {
					slog.Warn("run_approved_command: failed to record policy execution on timeline", "error", err)
				}
				output, err := executeApproved(ctx, mem, notify, cmd, host, in.Reason)
				if err != nil {
					return "", err
				}
				return policyNote + "\n\n" + output, nil
			}

			// Async approval: file the request and return immediately so the
			// agent run (and its responsibility slot) is never parked waiting
			// on the operator. The approvalapi decision handler executes the
			// command when the operator approves; the expiry sweeper cancels
			// it after 24 hours without a decision.
			id, err := reqapproval.SubmitApproval(
				ctx, mem, notify,
				title,
				fmt.Sprintf("Host: %s\n\nReason: %s", host, in.Reason),
				cmd,
				in.Risk,
				true,
				in.Reason,
			)
			if err != nil {
				return "", fmt.Errorf("approval request failed: %w", err)
			}

			slog.Info("run_approved_command: approval submitted, not waiting", "id", id, "command", cmd, "risk", in.Risk)
			return fmt.Sprintf(
				"Approval requested (id %s, risk %s) — the command has NOT been executed yet. "+
					"It will run automatically if the operator approves within 24 hours, and the "+
					"result will be recorded on the host timeline. Do not wait for the decision and "+
					"do not resubmit this command; state in your report that the remediation is "+
					"pending operator approval.",
				id, in.Risk,
			), nil
		},
	)
}

// ExecuteOnApproval runs an auto-execute command whose approval the operator
// just granted. Called by the approvalapi decision handler in a detached
// goroutine (the HTTP response does not wait for the command). It records the
// output on the approval entry and the timeline, notifies the operator of the
// result, and tracks the approval for standing-policy promotion suggestions.
func ExecuteOnApproval(mem *memory.Store, notify *notifier.Notifier, policies *policy.Store, entry reqapproval.ApprovalEntry) {
	cmd := strings.TrimSpace(entry.ProposedAction)
	host, _ := os.Hostname()

	slog.Info("run_approved_command: executing operator-approved command", "id", entry.ID, "command", cmd)

	// Detached from the HTTP request: the decision response has already been
	// sent, so the command gets its own lifetime (bounded by commandTimeout
	// inside executeApproved; a manual-run escalation may wait longer).
	ctx := context.Background()
	output, err := executeApproved(ctx, mem, notify, cmd, host, entry.ExecReason)
	if err != nil {
		output = fmt.Sprintf("Execution error: %v", err)
	}

	if err := reqapproval.SetOutput(mem, entry.ID, output); err != nil {
		slog.Warn("run_approved_command: failed to record output on approval entry", "id", entry.ID, "error", err)
	}

	failed := strings.HasPrefix(output, "Command failed") || strings.HasPrefix(output, "Execution error")
	severity := "info"
	if failed {
		severity = "warning"
	}
	detail := fmt.Sprintf("Command: %s\n\nResult:\n%s", cmd, truncate(output, 1500))
	if err := status.AppendTimeline(mem, status.TimelineEntry{
		Type:     "action",
		Title:    fmt.Sprintf("Executed approved command: %s", entry.Title),
		Detail:   detail,
		Severity: severity,
	}); err != nil {
		slog.Warn("run_approved_command: failed to record execution on timeline", "id", entry.ID, "error", err)
	}

	subject := fmt.Sprintf("[Approved Command Executed] %s", entry.Title)
	if failed {
		subject = fmt.Sprintf("[Approved Command FAILED] %s", entry.Title)
	}
	notify.Notify(ctx, subject, fmt.Sprintf("Host: %s\n\n%s", host, detail))

	// Count this approval and, once the operator has approved the same
	// command enough times, recommend promoting it to a standing policy so
	// future runs skip the HITL round-trip.
	if n, recErr := policies.RecordApproval(cmd); recErr == nil && n >= policy.PromotionThreshold {
		if err := status.AppendTimeline(mem, status.TimelineEntry{
			Type:     "recommendation",
			Title:    fmt.Sprintf("Candidate for standing approval policy (approved %dx)", n),
			Detail:   fmt.Sprintf("Command: %s\n\nThis command has been operator-approved %d times. Creating a standing approval policy (POST /policies) would let the agent run it without a fresh approval each time.", policy.NormalizeCommand(cmd), n),
			Severity: "info",
		}); err != nil {
			slog.Warn("run_approved_command: failed to record policy recommendation", "error", err)
		}
	}
}

// truncate shortens s to at most n bytes, appending an ellipsis marker.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… (truncated)"
}

// executeApproved runs a command that has passed the approval gate (HITL or
// standing policy). On Linux under a non-root user it escalates via sudo -n;
// a sudoers restriction escalates further to a manual-run request so the
// operator can execute the command themselves.
func executeApproved(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, cmd, host, reason string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	// Use sudo -n on Linux when running as a non-root user so approved
	// commands can perform privileged operations (e.g. systemctl restart,
	// snap remove). The approval gate ensures the operator has already seen
	// and approved the exact command text (or its policy) before this point.
	usingSudo := runtime.GOOS == "linux" && os.Getuid() != 0
	var execCmd *exec.Cmd
	if usingSudo {
		execCmd = exec.CommandContext(cmdCtx, "sudo", "-n", "sh", "-c", cmd) //nolint:gosec
	} else {
		execCmd = exec.CommandContext(cmdCtx, "sh", "-c", cmd) //nolint:gosec
	}
	out, execErr := execCmd.CombinedOutput()
	output := strings.TrimRight(string(out), "\n")

	if execErr != nil {
		// If sudo -n failed because the command is not in the sudoers
		// policy, escalate to a manual-run task rather than absorbing
		// the error. The operator runs the command themselves and pastes
		// the output back via the dashboard.
		if usingSudo && isSudoersError(output) {
			slog.Warn("run_approved_command: sudoers restriction — escalating to manual run", "command", cmd)
			manualTitle := fmt.Sprintf("Run Command Manually on %s", host)
			manualOutput, manualStatus, manualErr := reqmanualrun.RequestManualRun(ctx, mem, notify, manualTitle, cmd, host, reason)
			if manualErr != nil {
				return "", fmt.Errorf("manual run interrupted: %w", manualErr)
			}
			switch manualStatus {
			case "completed":
				return manualOutput, nil
			case "skipped":
				return "Operator chose to skip manual execution. No output available.", nil
			default:
				return "Manual run request timed out without a response.", nil
			}
		}

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
}
