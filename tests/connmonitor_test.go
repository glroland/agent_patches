package tests

import (
	"context"
	"testing"
	"time"

	"agent_patches/endpoint-server/connmonitor"
	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func inboundConn() connmonitor.Conn {
	return connmonitor.Conn{
		Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 22,
		RemoteAddr: "192.168.1.10", RemotePort: 54321,
		State: "ESTABLISHED", PID: "1234", Process: "sshd",
	}
}

func outboundConn() connmonitor.Conn {
	return connmonitor.Conn{
		Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000,
		RemoteAddr: "93.184.216.34", RemotePort: 443,
		State: "ESTABLISHED", PID: "5678", Process: "curl",
	}
}

func TestDiff_DetectsOpenAndClose(t *testing.T) {
	inbound, outbound := inboundConn(), outboundConn()

	prev := map[string]connmonitor.Conn{inbound.Key(): inbound}
	cur := map[string]connmonitor.Conn{outbound.Key(): outbound}

	opened, closed := connmonitor.Diff(prev, cur, time.Now())

	if len(opened) != 1 || opened[0].LocalPort != outbound.LocalPort {
		t.Fatalf("opened = %+v, want one event for the outbound connection", opened)
	}
	if opened[0].Direction != connmonitor.DirectionOutbound {
		t.Errorf("opened[0].Direction = %q, want outbound (local port %d is in the ephemeral range)", opened[0].Direction, outbound.LocalPort)
	}
	if len(closed) != 1 || closed[0].LocalPort != inbound.LocalPort {
		t.Fatalf("closed = %+v, want one event for the inbound connection", closed)
	}
	if closed[0].Direction != connmonitor.DirectionInbound {
		t.Errorf("closed[0].Direction = %q, want inbound (local port %d is below the ephemeral range)", closed[0].Direction, inbound.LocalPort)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	c := inboundConn()
	m := map[string]connmonitor.Conn{c.Key(): c}

	opened, closed := connmonitor.Diff(m, m, time.Now())
	if len(opened) != 0 || len(closed) != 0 {
		t.Errorf("Diff on identical snapshots = opened %v, closed %v, want none", opened, closed)
	}
}

func TestActiveConnections_ExistingStaysActiveUntilClosed(t *testing.T) {
	now := time.Now().UTC()
	history := []connmonitor.ConnEvent{
		{EventType: connmonitor.EventExisting, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 22, RemoteAddr: "192.168.1.10", RemotePort: 54321, Timestamp: now},
	}

	active := connmonitor.ActiveConnections(history)
	if len(active) != 1 {
		t.Fatalf("ActiveConnections = %+v, want 1 active entry", active)
	}
}

func TestActiveConnections_ClosedConnectionDropsOut(t *testing.T) {
	now := time.Now().UTC()
	history := []connmonitor.ConnEvent{
		{EventType: connmonitor.EventOpen, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, Timestamp: now},
		{EventType: connmonitor.EventClose, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, Timestamp: now.Add(time.Second)},
	}

	active := connmonitor.ActiveConnections(history)
	if len(active) != 0 {
		t.Errorf("ActiveConnections = %+v, want none (connection was closed)", active)
	}
}

func TestActiveConnections_ReopenedAfterClose(t *testing.T) {
	now := time.Now().UTC()
	history := []connmonitor.ConnEvent{
		{EventType: connmonitor.EventOpen, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, Timestamp: now},
		{EventType: connmonitor.EventClose, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, Timestamp: now.Add(time.Second)},
		{EventType: connmonitor.EventOpen, Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, Timestamp: now.Add(2 * time.Second)},
	}

	active := connmonitor.ActiveConnections(history)
	if len(active) != 1 {
		t.Errorf("ActiveConnections = %+v, want 1 (reopened after close)", active)
	}
}

func TestExistingEvents_TagsEveryConnAsExisting(t *testing.T) {
	conns := []connmonitor.Conn{inboundConn(), outboundConn()}
	events := connmonitor.ExistingEvents(conns, time.Now())

	if len(events) != 2 {
		t.Fatalf("ExistingEvents returned %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.EventType != connmonitor.EventExisting {
			t.Errorf("event type = %q, want existing", e.EventType)
		}
	}
}

func TestMonitor_PollOnce_BootstrapsThenDiffs(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	polls := [][]connmonitor.Conn{
		{inboundConn()},                 // poll 1: bootstrap
		{inboundConn(), outboundConn()}, // poll 2: outbound opens
		{outboundConn()},                // poll 3: inbound closes
	}
	call := 0
	gather := func(context.Context) ([]connmonitor.Conn, error) {
		conns := polls[call]
		call++
		return conns, nil
	}

	mon := connmonitor.NewWithGatherer(mem, notifier.New(mem), incidents.New(mem), config.NetworkMonitorSettings{}, gather)
	ctx := context.Background()

	events, err := mon.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce (bootstrap): %v", err)
	}
	if len(events) != 1 || events[0].EventType != connmonitor.EventExisting {
		t.Fatalf("bootstrap events = %+v, want one EventExisting", events)
	}

	events, err = mon.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce (2nd): %v", err)
	}
	if len(events) != 1 || events[0].EventType != connmonitor.EventOpen {
		t.Fatalf("2nd poll events = %+v, want one EventOpen (outbound)", events)
	}

	events, err = mon.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce (3rd): %v", err)
	}
	if len(events) != 1 || events[0].EventType != connmonitor.EventClose {
		t.Fatalf("3rd poll events = %+v, want one EventClose (inbound)", events)
	}

	history, err := connmonitor.ReadHistory(mem)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %+v, want 3 accumulated events", history)
	}

	active := connmonitor.ActiveConnections(history)
	if len(active) != 1 || active[0].Direction != connmonitor.DirectionOutbound {
		t.Errorf("active = %+v, want only the outbound connection still active", active)
	}
}

func TestMonitor_HistoryLimit_Caps(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	call := 0
	gather := func(context.Context) ([]connmonitor.Conn, error) {
		// A distinct connection on every poll so every poll after the first
		// produces exactly one EventOpen (the previous poll's connection
		// simultaneously closes, but we only assert on the cap here).
		c := connmonitor.Conn{
			Proto: "tcp", LocalAddr: "127.0.0.1", LocalPort: 22,
			RemoteAddr: "127.0.0.1", RemotePort: 10000 + call,
			State: "ESTABLISHED",
		}
		call++
		return []connmonitor.Conn{c}, nil
	}

	mon := connmonitor.NewWithGatherer(mem, notifier.New(mem), incidents.New(mem), config.NetworkMonitorSettings{HistoryLimit: 3}, gather)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := mon.PollOnce(ctx); err != nil {
			t.Fatalf("PollOnce #%d: %v", i, err)
		}
	}

	history, err := connmonitor.ReadHistory(mem)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(history) > 3 {
		t.Errorf("history has %d entries, want capped at 3", len(history))
	}
}
