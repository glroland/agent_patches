// Package loginmonitor provides a long-running background service that
// subscribes to systemd-logind D-Bus signals and records a durable history
// of login and logout events in the agent's AttrsStore.
//
// Events are stored under the "login_history" key in attrs.json, capped at
// maxHistory entries. Because AttrsStore is file-backed, history survives
// server restarts. The check_interactive_logins skill reads from this history
// instead of querying D-Bus on demand.
package loginmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/logind"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	historyKey = "login_history"
	maxHistory = 500
	retryDelay = 10 * time.Second

	enrichTimeout = 2 * time.Second
)

// EventType classifies a login history entry.
type EventType string

const (
	EventExisting EventType = "existing" // session present at monitor startup
	EventLogin    EventType = "login"    // SessionNew signal received
	EventLogout   EventType = "logout"   // SessionRemoved signal received
)

// LoginEvent is one record in the login history.
type LoginEvent struct {
	EventType        EventType `json:"event_type"`
	SessionID        string    `json:"session_id"`
	Username         string    `json:"username"`
	Class            string    `json:"class,omitempty"`
	SessionType      string    `json:"session_type,omitempty"`
	Remote           bool      `json:"remote"`
	RemoteHost       string    `json:"remote_host,omitempty"`       // raw value from logind (hostname or IP)
	SourceIP         string    `json:"source_ip,omitempty"`         // resolved IPv4/v6 address
	ResolvedHostname string    `json:"resolved_hostname,omitempty"` // PTR lookup or forward hostname
	TTY              string    `json:"tty,omitempty"`
	Timestamp        time.Time `json:"timestamp"` // UTC wall time of the event

	// Unusual and UnusualReason are set by checkAgainstBaseline when this
	// login deviates from the user's prior history (see baseline.go). Reason
	// is one of "new_user", "new_source", "unusual_time".
	Unusual       bool   `json:"unusual,omitempty"`
	UnusualReason string `json:"unusual_reason,omitempty"`
}

// Monitor watches logind D-Bus signals and records login/logout history.
type Monitor struct {
	mem       *memory.Store
	notify    *notifier.Notifier
	incidents *incidents.Store
	cfg       config.LoginMonitorSettings
}

// New creates a Monitor. Call Start to launch the background goroutine.
func New(mem *memory.Store, notify *notifier.Notifier, incidentStore *incidents.Store, cfg config.LoginMonitorSettings) *Monitor {
	return &Monitor{mem: mem, notify: notify, incidents: incidentStore, cfg: cfg}
}

// Start launches the background goroutine. It returns immediately; the
// goroutine exits when ctx is cancelled. If D-Bus is unavailable (macOS,
// Windows, no systemd), Start logs once and returns — no retry in that case.
func (m *Monitor) Start(ctx context.Context) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Info("loginmonitor: system D-Bus unavailable, login history disabled", "error", err)
		return
	}
	conn.Close()

	slog.Info("loginmonitor: starting")
	go m.runWithRetry(ctx)
}

// runWithRetry restarts the watch loop after transient D-Bus failures.
func (m *Monitor) runWithRetry(ctx context.Context) {
	for {
		if err := m.run(ctx); err != nil {
			slog.Warn("loginmonitor: watch loop exited, retrying", "error", err, "delay", retryDelay)
		}
		select {
		case <-ctx.Done():
			slog.Info("loginmonitor: stopped")
			return
		case <-time.After(retryDelay):
		}
	}
}

// run connects to D-Bus, bootstraps history from current sessions, subscribes
// to signals, and processes events until ctx is cancelled or the connection
// drops.
func (m *Monitor) run(ctx context.Context) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	// Snapshot sessions that already exist so history is useful from first read.
	existing, err := logind.ListSessionsConn(conn)
	if err != nil {
		slog.Warn("loginmonitor: could not enumerate existing sessions", "error", err)
	}
	for _, s := range existing {
		ts := s.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		ip, hostname := enrichSourceInfo(s.RemoteHost)
		m.appendEvent(LoginEvent{
			EventType:        EventExisting,
			SessionID:        s.ID,
			Username:         s.Username,
			Class:            s.Class,
			SessionType:      s.SessionType,
			Remote:           s.Remote,
			RemoteHost:       s.RemoteHost,
			SourceIP:         ip,
			ResolvedHostname: hostname,
			TTY:              s.TTY,
			Timestamp:        ts,
		})
	}
	slog.Info("loginmonitor: bootstrapped existing sessions", "count", len(existing))

	// Subscribe to SessionNew and SessionRemoved signals.
	matchRules := []string{
		fmt.Sprintf("type='signal',interface='%s',member='SessionNew'", logind.LogindIface),
		fmt.Sprintf("type='signal',interface='%s',member='SessionRemoved'", logind.LogindIface),
	}
	for _, rule := range matchRules {
		if err := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
			return fmt.Errorf("AddMatch %q: %w", rule, err)
		}
	}

	signals := make(chan *dbus.Signal, 32)
	conn.Signal(signals)
	slog.Info("loginmonitor: subscribed to logind signals")

	for {
		select {
		case <-ctx.Done():
			return nil

		case sig, ok := <-signals:
			if !ok {
				return fmt.Errorf("D-Bus signal channel closed")
			}
			m.handleSignal(conn, sig)
		}
	}
}

// handleSignal dispatches a received D-Bus signal.
func (m *Monitor) handleSignal(conn *dbus.Conn, sig *dbus.Signal) {
	member := sig.Name[strings.LastIndex(sig.Name, ".")+1:]

	switch member {
	case "SessionNew":
		if len(sig.Body) < 2 {
			return
		}
		id, _ := sig.Body[0].(string)
		path, _ := sig.Body[1].(dbus.ObjectPath)

		info, err := logind.FetchSessionInfo(conn, path)
		if err != nil {
			slog.Warn("loginmonitor: could not fetch new session info", "id", id, "error", err)
			m.appendEvent(LoginEvent{
				EventType: EventLogin,
				SessionID: id,
				Timestamp: time.Now().UTC(),
			})
			return
		}
		info.ID = id

		ip, hostname := enrichSourceInfo(info.RemoteHost)
		ev := LoginEvent{
			EventType:        EventLogin,
			SessionID:        id,
			Username:         info.Username,
			Class:            info.Class,
			SessionType:      info.SessionType,
			Remote:           info.Remote,
			RemoteHost:       info.RemoteHost,
			SourceIP:         ip,
			ResolvedHostname: hostname,
			TTY:              info.TTY,
			Timestamp:        time.Now().UTC(),
		}
		slog.Info("loginmonitor: session opened", "user", info.Username, "type", info.SessionType, "remote", info.Remote, "source_ip", ip)
		prior, _ := ReadHistory(m.mem)
		m.checkAgainstBaseline(&ev, prior)
		m.appendEvent(ev)
		m.checkUnusualSource(ev)

	case "SessionRemoved":
		if len(sig.Body) < 1 {
			return
		}
		id, _ := sig.Body[0].(string)

		// Session is gone by the time we get here; recover username from history.
		username := m.lastUsernameForSession(id)
		slog.Info("loginmonitor: session closed", "id", id, "user", username)
		m.appendEvent(LoginEvent{
			EventType: EventLogout,
			SessionID: id,
			Username:  username,
			Timestamp: time.Now().UTC(),
		})
	}
}

// checkUnusualSource fires a critical alert when a remote login originates
// from an address not in cfg.AllowedSources. No-op when AllowedSources is
// empty or the login is local.
func (m *Monitor) checkUnusualSource(ev LoginEvent) {
	if !ev.Remote || len(m.cfg.AllowedSources) == 0 {
		return
	}

	candidate := ev.SourceIP
	if candidate == "" {
		candidate = ev.RemoteHost
	}
	if candidate == "" {
		return
	}

	parsedIP := net.ParseIP(candidate)
	for _, src := range m.cfg.AllowedSources {
		if _, cidr, err := net.ParseCIDR(src); err == nil {
			if parsedIP != nil && cidr.Contains(parsedIP) {
				return
			}
			continue
		}
		if allowedIP := net.ParseIP(src); allowedIP != nil {
			if parsedIP != nil && parsedIP.Equal(allowedIP) {
				return
			}
			continue
		}
		// Treat as hostname — match against RemoteHost or ResolvedHostname.
		if src == ev.RemoteHost || src == ev.ResolvedHostname {
			return
		}
	}

	subject := fmt.Sprintf("[CRITICAL] Unusual login source: %s from %s", ev.Username, candidate)
	body := fmt.Sprintf(
		"A login was detected from an unexpected source.\n\nUser:     %s\nSource IP: %s\nHostname:  %s\nSession:  %s\nTime:     %s",
		ev.Username, ev.SourceIP, ev.ResolvedHostname, ev.SessionID, ev.Timestamp.Format(time.RFC1123),
	)
	m.notify.Notify(context.Background(), subject, body)
	_ = skillstate.Save(m.mem, "check_interactive_logins", skillstate.HealthCritical,
		fmt.Sprintf("unusual login source: %s from %s", ev.Username, candidate))
	slog.Warn("loginmonitor: unusual login source", "user", ev.Username, "source", candidate)
}

// enrichSourceInfo resolves a raw host value (IP or hostname from logind) into
// a canonical source IP and a resolved hostname. Both fields may be empty on
// lookup failure. Lookups are capped at enrichTimeout.
func enrichSourceInfo(host string) (sourceIP, resolvedHostname string) {
	if host == "" {
		return "", ""
	}

	if net.ParseIP(host) != nil {
		// Already an IP — PTR lookup for hostname.
		sourceIP = host
		ctx, cancel := context.WithTimeout(context.Background(), enrichTimeout)
		defer cancel()
		names, err := net.DefaultResolver.LookupAddr(ctx, host)
		if err == nil && len(names) > 0 {
			resolvedHostname = strings.TrimSuffix(names[0], ".")
		}
		return
	}

	// Hostname — forward lookup for IP.
	resolvedHostname = host
	ctx, cancel := context.WithTimeout(context.Background(), enrichTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err == nil && len(addrs) > 0 {
		sourceIP = addrs[0]
	}
	return
}

// lastUsernameForSession scans history in reverse to find the most recent
// event for the given session ID and returns its Username.
func (m *Monitor) lastUsernameForSession(id string) string {
	history, err := ReadHistory(m.mem)
	if err != nil {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].SessionID == id {
			return history[i].Username
		}
	}
	return ""
}

// appendEvent adds ev to the history, capping at maxHistory entries.
func (m *Monitor) appendEvent(ev LoginEvent) {
	history, _ := ReadHistory(m.mem)
	history = append(history, ev)
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	if err := m.mem.Attrs().Set(historyKey, history); err != nil {
		slog.Warn("loginmonitor: failed to write history", "error", err)
	}
}

// ReadHistory returns the full login event history from the AttrsStore.
// Returns nil, nil if no history has been recorded yet.
func ReadHistory(mem *memory.Store) ([]LoginEvent, error) {
	var history []LoginEvent
	raw, err := mem.Attrs().All()
	if err != nil || raw == nil {
		return nil, err
	}
	val, ok := raw[historyKey]
	if !ok {
		return nil, nil
	}
	if err := json.Unmarshal(val, &history); err != nil {
		return nil, fmt.Errorf("loginmonitor: parse history: %w", err)
	}
	return history, nil
}

// ActiveSessions derives which sessions are currently open from the history.
// A session is active if it has a "login" or "existing" event with no
// subsequent "logout" event for the same SessionID.
func ActiveSessions(history []LoginEvent) []LoginEvent {
	type state struct {
		event  LoginEvent
		closed bool
	}
	seen := make(map[string]*state)
	var order []string

	for _, ev := range history {
		switch ev.EventType {
		case EventLogin, EventExisting:
			if _, ok := seen[ev.SessionID]; !ok {
				order = append(order, ev.SessionID)
			}
			seen[ev.SessionID] = &state{event: ev, closed: false}
		case EventLogout:
			if s, ok := seen[ev.SessionID]; ok {
				s.closed = true
			}
		}
	}

	var active []LoginEvent
	for _, id := range order {
		if s := seen[id]; !s.closed {
			active = append(active, s.event)
		}
	}
	return active
}
