// baseline.go compares a newly-opened connection against the host's own
// accumulated connection_history and flags it when it deviates from that
// history — a local port accepting inbound traffic for the first time, a
// process making a network connection for the first time, or an outbound
// connection to a remote host never seen before. Needs no configuration:
// the baseline is learned entirely from this host's own history, the same
// approach loginmonitor uses for unusual logins.
package connmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/skillstate"
)

// checkAgainstBaseline compares ev against prior — the connection history for
// this host, which must NOT include ev itself — and, on a deviation, sets
// ev.Unusual/ev.UnusualReason and raises the matching escalations (skillstate,
// incident ledger, and — for the critical tier — email). At most one reason
// fires per connection: new_inbound_port takes priority over new_process,
// which takes priority over new_remote_host. Only ever called for newly
// opened connections, never for existing/closed ones.
func (m *Monitor) checkAgainstBaseline(ev *ConnEvent, prior []ConnEvent) {
	if m.cfg.DisableUnusualConnectionBaseline {
		return
	}

	knownInboundPorts := make(map[string]bool)
	knownProcesses := make(map[string]bool)
	knownRemoteHosts := make(map[string]bool)
	for _, h := range prior {
		if h.EventType != EventOpen && h.EventType != EventExisting {
			continue
		}
		if h.Direction == DirectionInbound {
			knownInboundPorts[fmt.Sprintf("%s:%d", h.Proto, h.LocalPort)] = true
		}
		if h.Direction == DirectionOutbound && h.RemoteAddr != "" {
			knownRemoteHosts[h.RemoteAddr] = true
		}
		if h.Process != "" {
			knownProcesses[h.Process] = true
		}
	}

	var reason, fingerprint, title, detail string
	var severity skillstate.Health
	switch {
	case ev.Direction == DirectionInbound && ev.LocalPort == m.cfg.OwnPort:
		// The agent's own configured listening port. It never appears in
		// history as an established connection until the first request
		// arrives (listening sockets aren't recorded, see snapshot()), so
		// without this exclusion the first ever inbound request would
		// always look like a brand-new port.
		return

	case ev.Direction == DirectionInbound && !knownInboundPorts[fmt.Sprintf("%s:%d", ev.Proto, ev.LocalPort)]:
		reason = "new_inbound_port"
		fingerprint = fmt.Sprintf("unusual-conn-newport-%s-%d", ev.Proto, ev.LocalPort)
		title = fmt.Sprintf("new inbound port: %s/%d", ev.Proto, ev.LocalPort)
		detail = fmt.Sprintf("A connection arrived on %s port %d, which has never accepted inbound traffic on this host before (from %s%s).",
			ev.Proto, ev.LocalPort, ev.RemoteAddr, processSuffix(*ev))
		severity = skillstate.HealthCritical

	case ev.Process != "" && !knownProcesses[ev.Process]:
		reason = "new_process"
		fingerprint = fmt.Sprintf("unusual-conn-newprocess-%s", ev.Process)
		title = fmt.Sprintf("new process on the network: %q", ev.Process)
		detail = fmt.Sprintf("Process %q made a network connection for the first time in recorded history (%s %s:%d -> %s:%d).",
			ev.Process, ev.Proto, ev.LocalAddr, ev.LocalPort, ev.RemoteAddr, ev.RemotePort)
		severity = skillstate.HealthCritical

	case ev.Direction == DirectionOutbound && ev.RemoteAddr != "" && !knownRemoteHosts[ev.RemoteAddr]:
		reason = "new_remote_host"
		fingerprint = fmt.Sprintf("unusual-conn-newremote-%s", ev.RemoteAddr)
		title = fmt.Sprintf("outbound connection to a new remote host: %s", ev.RemoteAddr)
		detail = fmt.Sprintf("An outbound connection was opened to %s:%d, which has never been seen in this host's connection history before%s.",
			ev.RemoteAddr, ev.RemotePort, processSuffix(*ev))
		severity = skillstate.HealthWarning

	default:
		return
	}

	ev.Unusual = true
	ev.UnusualReason = reason

	_ = skillstate.Save(m.mem, "check_network_connections", severity, fmt.Sprintf("%s: %s", reason, detail))
	if _, _, err := m.incidents.Report(fingerprint, title, detail, string(severity)); err != nil {
		slog.Warn("connmonitor: failed to report unusual-connection incident", "fingerprint", fingerprint, "error", err)
	}
	slog.Warn("connmonitor: unusual connection detected", "reason", reason, "severity", severity,
		"proto", ev.Proto, "local", fmt.Sprintf("%s:%d", ev.LocalAddr, ev.LocalPort), "remote", fmt.Sprintf("%s:%d", ev.RemoteAddr, ev.RemotePort))

	if severity == skillstate.HealthCritical {
		subject := fmt.Sprintf("[CRITICAL] %s", title)
		body := fmt.Sprintf("%s\n\nProtocol: %s\nLocal:    %s:%d\nRemote:   %s:%d\nProcess:  %s\nTime:     %s",
			detail, ev.Proto, ev.LocalAddr, ev.LocalPort, ev.RemoteAddr, ev.RemotePort, processLabel(*ev), ev.Timestamp.Format(time.RFC1123))
		m.notify.Notify(context.Background(), subject, body)
	}
}

// processSuffix renders " (process)" for alert text when a process name is known.
func processSuffix(ev ConnEvent) string {
	if ev.Process == "" {
		return ""
	}
	return fmt.Sprintf(" (%s)", ev.Process)
}

// processLabel returns the owning process name, falling back to a bare pid
// or "unknown" when neither could be resolved.
func processLabel(ev ConnEvent) string {
	if ev.Process != "" {
		return ev.Process
	}
	if ev.PID != "" {
		return "pid " + ev.PID
	}
	return "unknown"
}
