// Package connmonitor provides a long-running background service that
// periodically samples the host's active TCP/UDP connections and records a
// durable history of connection open/close events in the agent's "Network"
// memory domain.
//
// Connections are stored under the "connection_history" key of the
// "Network" domain, capped at a configurable number of entries. The
// check_network_connections skill reads from this history instead of
// sampling connections on demand, so it can report churn that happened
// between agent invocations — including short-lived connections that would
// be invisible to a point-in-time check.
package connmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	// networkDomain is the memory.Domain name holding all network-related
	// state (connection history, rate baseline), so it surfaces as its own
	// "Network" section rather than in the flat attrs bucket.
	networkDomain       = "Network"
	historyKey          = "connection_history"
	defaultPollInterval = 10 * time.Second
	defaultHistoryLimit = 2000

	// ephemeralPortFloor is the low end of the typical ephemeral port range
	// (Linux's default net.ipv4.ip_local_port_range starts at 32768; Windows
	// and BSD defaults start at 49152). Used as a best-effort heuristic for
	// connection direction: a connection whose local port falls below this
	// floor is assumed to be bound to a fixed/well-known port, i.e. we are
	// the server (inbound); at or above it, we likely initiated the
	// connection from an OS-assigned ephemeral port (outbound). A service
	// unusually bound to a high ephemeral-range port will be misclassified.
	ephemeralPortFloor = 32768
)

// EventType classifies a connection history entry.
type EventType string

const (
	EventExisting EventType = "existing" // connection present at monitor startup
	EventOpen     EventType = "open"     // newly observed since the last poll
	EventClose    EventType = "close"    // no longer observed since the last poll
)

// Direction is a best-effort guess at which side initiated the connection.
type Direction string

const (
	DirectionInbound  Direction = "inbound"  // remote peer connected to us
	DirectionOutbound Direction = "outbound" // we connected to a remote peer
	DirectionUnknown  Direction = "unknown"
)

// Conn is one active connection as reported by the platform-specific gatherer.
type Conn struct {
	Proto      string // "tcp" or "udp"
	LocalAddr  string
	LocalPort  int
	RemoteAddr string
	RemotePort int
	State      string // e.g. "ESTABLISHED", "TIME_WAIT"; "" where not applicable
	PID        string
	Process    string
}

// Key returns the stable identity used to track a connection across polls.
func (c Conn) Key() string {
	return fmt.Sprintf("%s|%s:%d|%s:%d", c.Proto, c.LocalAddr, c.LocalPort, c.RemoteAddr, c.RemotePort)
}

// direction guesses whether this connection is inbound or outbound. See
// ephemeralPortFloor for the heuristic and its limitations.
func (c Conn) direction() Direction {
	if c.LocalPort <= 0 {
		return DirectionUnknown
	}
	if c.LocalPort >= ephemeralPortFloor {
		return DirectionOutbound
	}
	return DirectionInbound
}

// ConnEvent is one record in the connection history.
type ConnEvent struct {
	EventType  EventType `json:"event_type"`
	Direction  Direction `json:"direction"`
	Proto      string    `json:"proto"`
	LocalAddr  string    `json:"local_addr"`
	LocalPort  int       `json:"local_port"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
	RemotePort int       `json:"remote_port,omitempty"`
	State      string    `json:"state,omitempty"`
	PID        string    `json:"pid,omitempty"`
	Process    string    `json:"process,omitempty"`
	Timestamp  time.Time `json:"timestamp"`

	// Unusual and UnusualReason are set by checkAgainstBaseline when this
	// connection deviates from the host's prior connection history (see
	// baseline.go). Reason is one of "new_inbound_port", "new_process",
	// "new_remote_host".
	Unusual       bool   `json:"unusual,omitempty"`
	UnusualReason string `json:"unusual_reason,omitempty"`
}

// connEventKey rebuilds the same identity used by Conn.key() from a history
// entry, so ActiveConnections can match open/close pairs.
func connEventKey(ev ConnEvent) string {
	return fmt.Sprintf("%s|%s:%d|%s:%d", ev.Proto, ev.LocalAddr, ev.LocalPort, ev.RemoteAddr, ev.RemotePort)
}

func newEvent(c Conn, t EventType, now time.Time) ConnEvent {
	return ConnEvent{
		EventType:  t,
		Direction:  c.direction(),
		Proto:      c.Proto,
		LocalAddr:  c.LocalAddr,
		LocalPort:  c.LocalPort,
		RemoteAddr: c.RemoteAddr,
		RemotePort: c.RemotePort,
		State:      c.State,
		PID:        c.PID,
		Process:    c.Process,
		Timestamp:  now,
	}
}

// Monitor periodically samples active connections and records open/close
// history. Call Start to launch the background goroutine, or PollOnce to
// drive it deterministically (e.g. from a test).
type Monitor struct {
	mem       *memory.Store
	notify    *notifier.Notifier
	incidents *incidents.Store
	gather    func(ctx context.Context) ([]Conn, error)

	pollInterval time.Duration
	historyLimit int
	cfg          config.NetworkMonitorSettings

	prev         map[string]Conn
	bootstrapped bool
}

// New creates a Monitor using the platform-specific gatherer.
func New(mem *memory.Store, notify *notifier.Notifier, incidentStore *incidents.Store, cfg config.NetworkMonitorSettings) *Monitor {
	return NewWithGatherer(mem, notify, incidentStore, cfg, snapshot)
}

// NewWithGatherer creates a Monitor with an injected gather function.
// Exported for tests.
func NewWithGatherer(mem *memory.Store, notify *notifier.Notifier, incidentStore *incidents.Store, cfg config.NetworkMonitorSettings, gather func(ctx context.Context) ([]Conn, error)) *Monitor {
	pollInterval := defaultPollInterval
	if d, err := time.ParseDuration(cfg.PollInterval); err == nil && d > 0 {
		pollInterval = d
	}
	historyLimit := cfg.HistoryLimit
	if historyLimit <= 0 {
		historyLimit = defaultHistoryLimit
	}
	return &Monitor{
		mem: mem, notify: notify, incidents: incidentStore, gather: gather,
		pollInterval: pollInterval, historyLimit: historyLimit, cfg: cfg,
		prev: make(map[string]Conn),
	}
}

// ExistingEvents tags a slice of currently-observed connections as
// EventExisting entries at the given time, for callers (such as a live
// fallback report) that want to display a snapshot in the same shape as
// history-derived events without going through the background monitor.
func ExistingEvents(conns []Conn, at time.Time) []ConnEvent {
	events := make([]ConnEvent, 0, len(conns))
	for _, c := range conns {
		events = append(events, newEvent(c, EventExisting, at))
	}
	return events
}

// LiveSnapshot runs the platform-specific gatherer once and returns the
// current active connections directly, for callers that need a reading
// before the background monitor has built up any history.
func LiveSnapshot(ctx context.Context) ([]Conn, error) {
	return snapshot(ctx)
}

// Start launches the background polling goroutine. It returns immediately;
// the goroutine exits when ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	slog.Info("connmonitor: starting", "poll_interval", m.pollInterval, "history_limit", m.historyLimit)
	go m.run(ctx)
}

// run polls on a ticker until ctx is cancelled, logging (but not stopping on)
// individual poll failures.
func (m *Monitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	m.pollAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("connmonitor: stopped")
			return
		case <-ticker.C:
			m.pollAndLog(ctx)
		}
	}
}

func (m *Monitor) pollAndLog(ctx context.Context) {
	events, err := m.PollOnce(ctx)
	if err != nil {
		slog.Warn("connmonitor: poll failed", "error", err)
		return
	}
	slog.Debug("connmonitor: poll complete", "events", len(events), "active", len(m.prev))
}

// PollOnce runs one gather+diff+persist cycle. The first call bootstraps
// (recording every currently active connection as EventExisting); every
// subsequent call diffs against the previous poll and appends the resulting
// EventOpen/EventClose entries to history. Returns the events appended this
// call. Exported so tests can drive the monitor deterministically instead of
// waiting on the internal ticker.
func (m *Monitor) PollOnce(ctx context.Context) ([]ConnEvent, error) {
	conns, err := m.gather(ctx)
	if err != nil {
		return nil, err
	}
	cur := make(map[string]Conn, len(conns))
	for _, c := range conns {
		cur[c.Key()] = c
	}

	now := time.Now().UTC()
	var events []ConnEvent
	if !m.bootstrapped {
		events = make([]ConnEvent, 0, len(cur))
		for _, c := range cur {
			events = append(events, newEvent(c, EventExisting, now))
		}
		m.bootstrapped = true
	} else {
		opened, closed := Diff(m.prev, cur, now)
		if len(opened) > 0 {
			prior, _ := ReadHistory(m.mem)
			for i := range opened {
				m.checkAgainstBaseline(&opened[i], prior)
			}
		}
		events = append(opened, closed...)
	}

	m.appendEvents(events)
	m.prev = cur
	return events, nil
}

// Diff compares two connection snapshots (keyed by Conn.Key()) and returns
// open/close events, each sorted by local port for stable output.
func Diff(prev, cur map[string]Conn, now time.Time) (opened, closed []ConnEvent) {
	for k, c := range cur {
		if _, ok := prev[k]; !ok {
			opened = append(opened, newEvent(c, EventOpen, now))
		}
	}
	for k, c := range prev {
		if _, ok := cur[k]; !ok {
			closed = append(closed, newEvent(c, EventClose, now))
		}
	}
	sort.Slice(opened, func(i, j int) bool { return opened[i].LocalPort < opened[j].LocalPort })
	sort.Slice(closed, func(i, j int) bool { return closed[i].LocalPort < closed[j].LocalPort })
	return opened, closed
}

// appendEvents adds events to the history, capping at m.historyLimit entries.
func (m *Monitor) appendEvents(events []ConnEvent) {
	if len(events) == 0 {
		return
	}
	history, _ := ReadHistory(m.mem)
	history = append(history, events...)
	if len(history) > m.historyLimit {
		history = history[len(history)-m.historyLimit:]
	}
	if err := m.mem.Domain(networkDomain).SetKey(historyKey, history); err != nil {
		slog.Warn("connmonitor: failed to write history", "error", err)
	}
}

// ReadHistory returns the full connection event history from the "Network"
// memory domain. Returns nil, nil if no history has been recorded yet.
func ReadHistory(mem *memory.Store) ([]ConnEvent, error) {
	var history []ConnEvent
	if err := mem.Domain(networkDomain).GetKey(historyKey, &history); err != nil {
		return nil, nil //nolint:nilerr
	}
	return history, nil
}

// splitHostPort splits "host:port" on the last colon, which correctly
// isolates the port for IPv4, bracketed IPv6, and unbracketted IPv6
// addresses alike, since only the port suffix is guaranteed colon-free.
// Returns port 0 if the suffix isn't numeric (e.g. the "*" some platforms
// use for an unset port).
func splitHostPort(s string) (host string, port int) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0
	}
	host = strings.Trim(s[:i], "[]")
	port, _ = strconv.Atoi(s[i+1:])
	return host, port
}

// ActiveConnections derives which connections are currently open from the
// history. A connection is active if its most recent event is "existing" or
// "open" with no subsequent "close" for the same identity.
func ActiveConnections(history []ConnEvent) []ConnEvent {
	type state struct {
		event  ConnEvent
		closed bool
	}
	seen := make(map[string]*state)
	var order []string

	for _, ev := range history {
		k := connEventKey(ev)
		switch ev.EventType {
		case EventOpen, EventExisting:
			if _, ok := seen[k]; !ok {
				order = append(order, k)
			}
			seen[k] = &state{event: ev, closed: false}
		case EventClose:
			if s, ok := seen[k]; ok {
				s.closed = true
			}
		}
	}

	var active []ConnEvent
	for _, k := range order {
		if s := seen[k]; !s.closed {
			active = append(active, s.event)
		}
	}
	return active
}
