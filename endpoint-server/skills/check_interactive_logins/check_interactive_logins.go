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

// NewLoginSessionsTool returns a tool that reports active login sessions,
// recent login/logout history, and recent failed login attempts recorded by
// the background monitors. Falls back to a live D-Bus query when no history
// is available yet.
func NewLoginSessionsTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_interactive_logins",
		"Reports currently active login sessions, recent login/logout history, and "+
			"recent failed login attempts. History is maintained by background monitors "+
			"that watch systemd-logind D-Bus signals and the system journal (sshd). "+
			"Falls back to a live snapshot when no history is available. "+
			"Requires systemd-logind; unavailable on macOS and Windows.",
		func(_ context.Context, _ struct{}) (string, error) {
			slog.Info("check_interactive_logins: starting")

			history, err := loginmonitor.ReadHistory(mem)
			if err != nil {
				slog.Warn("check_interactive_logins: could not read history, falling back to live query", "error", err)
			}

			if len(history) > 0 {
				failedHistory, _ := loginmonitor.ReadFailedHistory(mem)
				return reportFromHistory(mem, history, failedHistory), nil
			}

			return liveReport(mem), nil
		},
	)
}

// reportFromHistory builds a three-section report: active sessions, recent
// login/logout activity, and recent failed login attempts.
func reportFromHistory(mem *memory.Store, history []loginmonitor.LoginEvent, failedHistory []loginmonitor.FailedLoginEvent) string {
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
					fmt.Fprintf(&sb, "Remote host:  %s\n", ev.RemoteHost)
				}
				if ev.SourceIP != "" {
					fmt.Fprintf(&sb, "Source IP:    %s\n", ev.SourceIP)
				}
				if ev.ResolvedHostname != "" && ev.ResolvedHostname != ev.RemoteHost {
					fmt.Fprintf(&sb, "Hostname:     %s\n", ev.ResolvedHostname)
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

	// Section 2: recent login/logout activity (last 50 events, newest first).
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
			parts := []string{"remote"}
			if ev.SourceIP != "" {
				parts = append(parts, ev.SourceIP)
			} else if ev.RemoteHost != "" {
				parts = append(parts, ev.RemoteHost)
			}
			if ev.ResolvedHostname != "" && ev.ResolvedHostname != ev.RemoteHost && ev.ResolvedHostname != ev.SourceIP {
				parts = append(parts, ev.ResolvedHostname)
			}
			origin = strings.Join(parts, ":")
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

	// Section 3: recent failed login attempts (last 20, newest first).
	sb.WriteString("\n=== Recent Failed Login Attempts ===\n")
	if len(failedHistory) == 0 {
		sb.WriteString("No failed login attempts recorded.\n")
	} else {
		fstart := 0
		if len(failedHistory) > 20 {
			fstart = len(failedHistory) - 20
		}
		recentFailed := failedHistory[fstart:]
		for i := len(recentFailed) - 1; i >= 0; i-- {
			ev := recentFailed[i]
			src := ev.SourceIP
			if src == "" {
				src = ev.RemoteHost
			}
			hostname := ev.ResolvedHostname
			if hostname != "" && hostname != ev.RemoteHost && hostname != ev.SourceIP {
				src = fmt.Sprintf("%s (%s)", src, hostname)
			}
			fmt.Fprintf(&sb, "[%s] %-16s %-20s %s\n",
				ev.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
				ev.Username,
				src,
				ev.Reason,
			)
		}
	}

	state := fmt.Sprintf("%d active session(s); %d history events; %d failed attempts",
		len(active), len(history), len(failedHistory))
	_ = skillstate.Save(mem, "check_interactive_logins", skillstate.HealthOK, state)
	slog.Info("check_interactive_logins: completed from history",
		"active", len(active), "history", len(history), "failed", len(failedHistory))
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
