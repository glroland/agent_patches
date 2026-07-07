// Package check_network_connections reports the host's currently active
// inbound and outbound network connections and recent connection churn,
// reading from the history maintained by the connmonitor background poller
// so short-lived connections between agent invocations are still visible.
package check_network_connections

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/connmonitor"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

// maxListedConnections caps how many connections are enumerated per
// direction group before the rest are summarized as "… and N more".
const maxListedConnections = 100

// maxRecentEvents caps how many recent open/close/existing events are shown.
const maxRecentEvents = 50

// NewCheckNetworkConnectionsTool returns a tool that reports the host's
// currently active inbound and outbound TCP/UDP connections, plus recent
// open/close activity recorded by the connmonitor background monitor.
func NewCheckNetworkConnectionsTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_network_connections",
		"Reports the host's currently active inbound and outbound network connections "+
			"(TCP/UDP) plus recent connection open/close activity. History is maintained "+
			"by a background monitor that polls active connections every few seconds, so "+
			"short-lived connections that occurred between agent invocations are still "+
			"captured. Falls back to a live snapshot when no history is available yet. "+
			"Direction (inbound/outbound) is a best-effort guess based on whether the local "+
			"port looks like an OS-assigned ephemeral client port or a fixed server port — "+
			"treat it as a strong hint, not a certainty.",
		func(ctx context.Context, _ struct{}) (string, error) {
			slog.Info("check_network_connections: starting")

			history, err := connmonitor.ReadHistory(mem)
			if err != nil {
				slog.Warn("check_network_connections: could not read history, falling back to live snapshot", "error", err)
			}

			if len(history) > 0 {
				return reportFromHistory(mem, history), nil
			}
			return liveReport(ctx, mem), nil
		},
	)
}

// reportFromHistory builds the two-section report (active connections,
// recent activity) from the connmonitor history.
func reportFromHistory(mem *memory.Store, history []connmonitor.ConnEvent) string {
	active := connmonitor.ActiveConnections(history)

	var sb strings.Builder
	writeActiveSection(&sb, active)
	writeActivitySection(&sb, history)

	inbound, outbound := countByDirection(active)
	summary := fmt.Sprintf("%d active connection(s) (%d inbound, %d outbound); %d history event(s)",
		len(active), inbound, outbound, len(history))
	_ = skillstate.Save(mem, "check_network_connections", skillstate.HealthOK, summary)
	slog.Info("check_network_connections: completed from history",
		"active", len(active), "inbound", inbound, "outbound", outbound, "history", len(history))
	return sb.String()
}

// liveReport falls back to a direct one-shot poll when no history exists yet.
func liveReport(ctx context.Context, mem *memory.Store) string {
	conns, err := connmonitor.LiveSnapshot(ctx)
	if err != nil {
		slog.Info("check_network_connections: live snapshot unavailable", "error", err)
		_ = skillstate.Save(mem, "check_network_connections", skillstate.HealthOK,
			fmt.Sprintf("connection snapshot unavailable: %v", err))
		return fmt.Sprintf("Network connection snapshot unavailable: %v\n(Background connection monitor has not recorded any history yet.)", err)
	}

	active := connmonitor.ExistingEvents(conns, time.Now().UTC())

	var sb strings.Builder
	writeActiveSection(&sb, active)

	inbound, outbound := countByDirection(active)
	summary := fmt.Sprintf("%d active connection(s) (%d inbound, %d outbound) [live snapshot]", len(active), inbound, outbound)
	_ = skillstate.Save(mem, "check_network_connections", skillstate.HealthOK, summary)
	slog.Info("check_network_connections: completed (live)", "active", len(active), "inbound", inbound, "outbound", outbound)
	return sb.String() + "\n(Background connection monitor has not recorded any history yet.)"
}

// writeActiveSection renders the current connections, grouped by direction.
func writeActiveSection(sb *strings.Builder, active []connmonitor.ConnEvent) {
	sb.WriteString("=== Active Connections ===\n")
	if len(active) == 0 {
		sb.WriteString("No active connections.\n")
		return
	}
	writeConnGroup(sb, "Inbound", filterDirection(active, connmonitor.DirectionInbound))
	writeConnGroup(sb, "Outbound", filterDirection(active, connmonitor.DirectionOutbound))
	if unknown := filterDirection(active, connmonitor.DirectionUnknown); len(unknown) > 0 {
		writeConnGroup(sb, "Unknown direction", unknown)
	}
}

// writeActivitySection renders the most recent history events, newest first.
func writeActivitySection(sb *strings.Builder, history []connmonitor.ConnEvent) {
	sb.WriteString("\n=== Recent Activity ===\n")
	start := 0
	if len(history) > maxRecentEvents {
		start = len(history) - maxRecentEvents
	}
	recent := history[start:]
	for i := len(recent) - 1; i >= 0; i-- {
		e := recent[i]
		fmt.Fprintf(sb, "[%s] %-8s %-8s %s %s:%d -> %s:%d",
			e.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
			string(e.EventType), string(e.Direction),
			strings.ToUpper(e.Proto), e.LocalAddr, e.LocalPort, e.RemoteAddr, e.RemotePort)
		if proc := processLabel(e); proc != "" {
			fmt.Fprintf(sb, " (%s)", proc)
		}
		sb.WriteString("\n")
	}
}

// filterDirection returns the events matching d, sorted for stable display.
func filterDirection(events []connmonitor.ConnEvent, d connmonitor.Direction) []connmonitor.ConnEvent {
	var out []connmonitor.ConnEvent
	for _, e := range events {
		if e.Direction == d {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LocalPort != out[j].LocalPort {
			return out[i].LocalPort < out[j].LocalPort
		}
		return out[i].RemoteAddr < out[j].RemoteAddr
	})
	return out
}

// writeConnGroup renders one direction group, capped at maxListedConnections.
func writeConnGroup(sb *strings.Builder, label string, events []connmonitor.ConnEvent) {
	fmt.Fprintf(sb, "%s (%d):\n", label, len(events))
	if len(events) == 0 {
		sb.WriteString("  none\n")
		return
	}
	for i, e := range events {
		if i == maxListedConnections {
			fmt.Fprintf(sb, "  … and %d more\n", len(events)-maxListedConnections)
			break
		}
		state := ""
		if e.State != "" {
			state = " [" + e.State + "]"
		}
		proc := processLabel(e)
		if proc != "" {
			proc = " (" + proc + ")"
		}
		fmt.Fprintf(sb, "  %s %s:%d -> %s:%d%s%s\n",
			strings.ToUpper(e.Proto), e.LocalAddr, e.LocalPort, e.RemoteAddr, e.RemotePort, state, proc)
	}
}

// processLabel returns the owning process name, falling back to a bare pid
// when the name couldn't be resolved (e.g. insufficient privilege).
func processLabel(e connmonitor.ConnEvent) string {
	if e.Process != "" {
		return e.Process
	}
	if e.PID != "" {
		return "pid " + e.PID
	}
	return ""
}

// countByDirection tallies active connections by direction.
func countByDirection(events []connmonitor.ConnEvent) (inbound, outbound int) {
	for _, e := range events {
		switch e.Direction {
		case connmonitor.DirectionInbound:
			inbound++
		case connmonitor.DirectionOutbound:
			outbound++
		}
	}
	return inbound, outbound
}
