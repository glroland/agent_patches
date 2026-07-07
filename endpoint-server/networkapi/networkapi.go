// Package networkapi implements the GET /network-connections endpoint, which
// exposes the connmonitor background monitor's connection history — currently
// active connections plus recent open/close activity — for display in
// central-ui.
package networkapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"agent_patches/endpoint-server/connmonitor"
	"agent_patches/endpoint-server/memory"
)

// liveSnapshotTimeout bounds the one-shot fallback poll used when no history
// has been recorded yet.
const liveSnapshotTimeout = 10 * time.Second

// maxRecentActivity caps how many history events are returned, newest first.
const maxRecentActivity = 50

// Service serves GET /network-connections responses from connmonitor history.
type Service struct {
	mem *memory.Store
}

// New creates a network Service.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
}

// Response is the GET /network-connections payload.
type Response struct {
	Active         []ConnItem `json:"active"`
	RecentActivity []ConnItem `json:"recentActivity"`
	HistoryCount   int        `json:"historyCount"`
	// Live is true when no background history exists yet and this response
	// was built from a one-shot live poll instead.
	Live bool   `json:"live"`
	Note string `json:"note,omitempty"`
}

// ConnItem is one connection entry (active or historical) in the response.
type ConnItem struct {
	EventType  string `json:"eventType"`
	Direction  string `json:"direction"`
	Proto      string `json:"proto"`
	LocalAddr  string `json:"localAddr"`
	LocalPort  int    `json:"localPort"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	RemotePort int    `json:"remotePort,omitempty"`
	State      string `json:"state,omitempty"`
	PID        string `json:"pid,omitempty"`
	Process    string `json:"process,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// Handler returns the http.HandlerFunc for GET /network-connections.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		history, err := connmonitor.ReadHistory(s.mem)
		if err != nil {
			slog.Warn("networkapi: could not read history, falling back to live snapshot", "error", err)
		}
		if len(history) > 0 {
			_ = json.NewEncoder(w).Encode(buildFromHistory(history))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), liveSnapshotTimeout)
		defer cancel()
		conns, err := connmonitor.LiveSnapshot(ctx)
		if err != nil {
			_ = json.NewEncoder(w).Encode(buildLive(nil, "connection snapshot unavailable: "+err.Error()))
			return
		}
		_ = json.NewEncoder(w).Encode(buildLive(conns, "background connection monitor has not recorded any history yet"))
	}
}

// buildFromHistory derives the active-connection set and a capped, newest-first
// activity feed from the full connmonitor history.
func buildFromHistory(history []connmonitor.ConnEvent) Response {
	active := connmonitor.ActiveConnections(history)

	recent := history
	if len(recent) > maxRecentActivity {
		recent = recent[len(recent)-maxRecentActivity:]
	}

	return Response{
		Active:         toItems(active),
		RecentActivity: toItems(reverseEvents(recent)),
		HistoryCount:   len(history),
	}
}

// buildLive builds a response from a one-shot poll, used when the background
// monitor has not accumulated any history yet.
func buildLive(conns []connmonitor.Conn, note string) Response {
	events := connmonitor.ExistingEvents(conns, time.Now().UTC())
	return Response{
		Active:         toItems(events),
		RecentActivity: []ConnItem{},
		Live:           true,
		Note:           note,
	}
}

func toItems(events []connmonitor.ConnEvent) []ConnItem {
	items := make([]ConnItem, 0, len(events))
	for _, e := range events {
		items = append(items, ConnItem{
			EventType:  string(e.EventType),
			Direction:  string(e.Direction),
			Proto:      e.Proto,
			LocalAddr:  e.LocalAddr,
			LocalPort:  e.LocalPort,
			RemoteAddr: e.RemoteAddr,
			RemotePort: e.RemotePort,
			State:      e.State,
			PID:        e.PID,
			Process:    e.Process,
			Timestamp:  e.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	return items
}

// reverseEvents returns events newest-first, for display.
func reverseEvents(events []connmonitor.ConnEvent) []connmonitor.ConnEvent {
	out := make([]connmonitor.ConnEvent, len(events))
	for i, e := range events {
		out[len(events)-1-i] = e
	}
	return out
}
