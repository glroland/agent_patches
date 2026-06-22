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
	"agent_patches/endpoint-server/utils/notifier"
)

// commandTimeout bounds how long an approved command may run.
const commandTimeout = 5 * time.Minute

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
			"For read-only investigation (ps, du, ss, journalctl, etc.) use run_diagnostic_command instead. "+
			"The operator sees the full command, reason, and risk level before deciding. "+
			"Returns the command output on approval, or a cancellation message on rejection.",
		func(ctx context.Context, in runCommandInput) (string, error) {
			if strings.TrimSpace(in.Command) == "" {
				return "", fmt.Errorf("run_approved_command: command must not be empty")
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
				in.Command,
				in.Risk,
			)
			if err != nil {
				return "", fmt.Errorf("approval interrupted: %w", err)
			}

			switch decision {
			case "rejected":
				slog.Info("run_approved_command: operator rejected command", "command", in.Command)
				return "Command rejected by operator — not executed.", nil
			case "timed_out":
				slog.Info("run_approved_command: approval timed out", "command", in.Command)
				return "Command not executed: approval request timed out.", nil
			}

			// Approved — run it.
			slog.Info("run_approved_command: executing approved command", "command", in.Command)
			cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", in.Command) //nolint:gosec
			out, execErr := cmd.CombinedOutput()
			output := strings.TrimRight(string(out), "\n")

			if execErr != nil {
				slog.Warn("run_approved_command: command failed", "command", in.Command, "error", execErr)
				if output != "" {
					return fmt.Sprintf("Command failed (%v):\n%s", execErr, output), nil
				}
				return fmt.Sprintf("Command failed: %v", execErr), nil
			}

			slog.Info("run_approved_command: command completed successfully", "command", in.Command, "output_len", len(output))
			if output == "" {
				return "Command completed with no output.", nil
			}
			return output, nil
		},
	)
}
