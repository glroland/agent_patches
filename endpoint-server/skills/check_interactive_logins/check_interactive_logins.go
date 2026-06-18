package check_interactive_logins

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/logind"
	"agent_patches/endpoint-server/loginmonitor"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

// NewLoginSessionsTool returns a tool that reports active login sessions and
// recent login/logout history recorded by the loginmonitor background service.
// When no history is available yet (monitor not running, or first start), it
// falls back to a live D-Bus query so the tool is always useful.
func NewLoginSessionsTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_interactive_logins",
		"Reports currently active login sessions and recent login/logout history. "+
			"History is maintained by a background monitor that watches systemd-logind "+
			"D-Bus signals, so it captures events between skill invocations. "+
			"Falls back to a live snapshot when no history is available. "+
			"Requires systemd-logind; unavailable on macOS and Windows.",
		func(_ context.Context, _ struct{}) (string, error) {
			slog.Info("check_interactive_logins: starting")

			history, err := loginmonitor.ReadHistory(mem)
			if err != nil {
				slog.Warn("check_interactive_logins: could not read history, falling back to live query", "error", err)
			}

			if len(history) > 0 {
				return reportFromHistory(mem, history), nil
			}

			// No history yet — live fallback.
			return liveReport(mem), nil
		},
	)
}

// reportFromHistory builds a two-section report from recorded history:
// active sessions and recent activity.
func reportFromHistory(mem *memory.Store, history []loginmonitor.LoginEvent) string {
	active := loginmonitor.ActiveSessions(history)

	var sb strings.Builder

	// Section 1: active sessions.
	sb.WriteString("=== Active Sessions ===\n")
	if len(active) == 0 {
		sb.WriteString("No active login sessions.\n")
	} else {
		for i, ev := range active {
			fmt.Fprintf(&sb, "Session ID:   %s\n", ev.SessionID)
			fmt.Fprintf(&sb, "User:         %s\n", ev.Username)
			if ev.Class != "" {
				fmt.Fprintf(&sb, "Class:        %s\n", ev.Class)
			}
			if ev.SessionType != "" {
				fmt.Fprintf(&sb, "Session type: %s\n", ev.SessionType)
			}
			if ev.TTY != "" {
				fmt.Fprintf(&sb, "TTY:          %s\n", ev.TTY)
			}
			if ev.Remote {
				fmt.Fprintf(&sb, "Origin:       remote\n")
				if ev.RemoteHost != "" {
					fmt.Fprintf(&sb, "From host:    %s\n", ev.RemoteHost)
				}
			} else {
				fmt.Fprintf(&sb, "Origin:       local console\n")
			}
			fmt.Fprintf(&sb, "Since:        %s\n", ev.Timestamp.Format(time.RFC1123))
			if i < len(active)-1 {
				sb.WriteString("\n")
			}
		}
	}

	// Section 2: recent activity (last 50 events, newest first).
	sb.WriteString("\n=== Recent Activity ===\n")
	start := 0
	if len(history) > 50 {
		start = len(history) - 50
	}
	recent := history[start:]
	for i := len(recent) - 1; i >= 0; i-- {
		ev := recent[i]
		origin := "local"
		if ev.Remote {
			origin = "remote"
			if ev.RemoteHost != "" {
				origin = "remote:" + ev.RemoteHost
			}
		}
		tty := ev.TTY
		if tty == "" {
			tty = ev.SessionType
		}
		fmt.Fprintf(&sb, "[%s] %-8s %-16s %-12s (%s)\n",
			ev.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
			string(ev.EventType),
			ev.Username,
			tty,
			origin,
		)
	}

	state := fmt.Sprintf("%d active session(s); %d history events", len(active), len(history))
	_ = skillstate.Save(mem, "check_interactive_logins", skillstate.HealthOK, state)
	slog.Info("check_interactive_logins: completed from history", "active", len(active), "history", len(history))
	return sb.String()
}

// liveReport falls back to a direct D-Bus query when no history exists.
func liveReport(mem *memory.Store) string {
	sessions, err := logind.ListSessions()
	if err != nil {
		slog.Info("check_interactive_logins: live query unavailable", "error", err)
		_ = skillstate.Save(mem, "check_interactive_logins", skillstate.HealthOK,
			fmt.Sprintf("session enumeration unavailable: %v", err))
		return fmt.Sprintf("Login session enumeration unavailable: %v\n(Background login monitor has not recorded any history yet.)", err)
	}
	if len(sessions) == 0 {
		slog.Info("check_interactive_logins: completed (live), no sessions")
		_ = skillstate.Save(mem, "check_interactive_logins", skillstate.HealthOK, "no active login sessions")
		return "No active login sessions.\n(Background login monitor has not recorded any history yet.)"
	}
	report := logind.BuildReport(sessions)
	slog.Info("check_interactive_logins: completed (live)", "sessions", len(sessions))
	_ = skillstate.Save(mem, "check_interactive_logins", skillstate.HealthOK,
		fmt.Sprintf("%d active login session(s)", len(sessions)))
	return report + "\n(Background login monitor has not recorded any history yet.)"
}
