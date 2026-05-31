package aisysadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/tool"
)

// readMemoryInput is the typed input for the read_memory tool.
type readMemoryInput struct {
	Domain  string `json:"domain"  jsonschema_description:"Memory domain to read. One of: disk, memory, net_upload, net_download, logins."`
	History bool   `json:"history" jsonschema_description:"If true, return all retained snapshots (up to 60 min of history). If false, return only the most recent snapshot."`
}

// runCommandInput is the typed input for the run_command tool.
type runCommandInput struct {
	Command string `json:"command" jsonschema_description:"Shell command to execute (sh -c on Unix, cmd /c on Windows)."`
	Reason  string `json:"reason"  jsonschema_description:"One-sentence explanation of why this command is being run, for the audit log."`
}

// newReadTools returns tools that read from the memory store. These are safe
// (read-only) and available in both the research and action steps.
func newReadTools(mem *memory.Store, log *slog.Logger) []tool.Tool {
	readMem, _ := tool.New(
		"read_memory",
		"Read the current snapshot or full history from a named memory domain. "+
			"Available domains: disk (disk space per mount), memory (RAM and swap usage), "+
			"net_upload (outbound network rate in MB/s), net_download (inbound network rate in MB/s), "+
			"logins (user login events). "+
			"Set history=true to get all retained snapshots (one per 5-minute bucket over the last 60 minutes).",
		func(_ context.Context, in readMemoryInput) (string, error) {
			d := mem.Domain(in.Domain)
			if in.History {
				log.Debug("ai_sysadmin: tool read_memory history", "domain", in.Domain)
				snaps, err := d.ReadHistory()
				if err != nil {
					return "", fmt.Errorf("read history %q: %w", in.Domain, err)
				}
				if len(snaps) == 0 {
					return fmt.Sprintf(`{"domain":%q,"snapshots":[]}`, in.Domain), nil
				}
				type entry struct {
					Timestamp time.Time       `json:"timestamp"`
					Data      json.RawMessage `json:"data"`
				}
				out := make([]entry, len(snaps))
				for i, s := range snaps {
					out[i] = entry{Timestamp: s.Timestamp, Data: s.Data}
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			}
			log.Debug("ai_sysadmin: tool read_memory current", "domain", in.Domain)
			var raw json.RawMessage
			if err := d.ReadCurrent(&raw); err != nil {
				return fmt.Sprintf(`{"domain":%q,"error":"no snapshot available"}`, in.Domain), nil
			}
			return string(raw), nil
		},
	)
	return []tool.Tool{readMem}
}

// newRunCommandTool returns a tool that executes shell commands. Each
// invocation appends the command string to *commandsRun so the caller can
// detect whether any actions were taken.
func newRunCommandTool(commandsRun *[]string, log *slog.Logger) tool.Tool {
	t, _ := tool.New(
		"run_command",
		"Execute a shell command on the host and return its combined stdout+stderr output. "+
			"Use this for diagnostics (e.g. df -h, free -m, top -bn1, journalctl -n 50) and "+
			"for corrective actions (e.g. restarting a service, clearing a cache). "+
			"Every invocation is logged. Timeout: 30 seconds.",
		func(ctx context.Context, in runCommandInput) (string, error) {
			log.Warn("ai_sysadmin: executing command",
				"command", in.Command,
				"reason", in.Reason,
			)
			*commandsRun = append(*commandsRun, in.Command)

			tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(tctx, "cmd", "/c", in.Command)
			} else {
				cmd = exec.CommandContext(tctx, "sh", "-c", in.Command)
			}

			out, err := cmd.CombinedOutput()
			result := strings.TrimSpace(string(out))

			if err != nil {
				log.Warn("ai_sysadmin: command failed",
					"command", in.Command,
					"error", err,
					"output_preview", truncate(result, 200),
				)
				if result != "" {
					return fmt.Sprintf("exit error: %v\n---\n%s", err, result), nil
				}
				return fmt.Sprintf("exit error: %v", err), nil
			}

			log.Info("ai_sysadmin: command succeeded",
				"command", in.Command,
				"output_lines", strings.Count(result, "\n")+1,
			)
			if result == "" {
				return "(no output)", nil
			}
			return result, nil
		},
	)
	return t
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
