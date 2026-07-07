// Package loginapi implements the GET /interactive-logins endpoint, which
// exposes the loginmonitor background monitors' session history — currently
// active login sessions, recent login/logout activity, and recent failed
// login attempts — for display in central-ui.
package loginapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"agent_patches/endpoint-server/logind"
	"agent_patches/endpoint-server/loginmonitor"
	"agent_patches/endpoint-server/memory"
)

// maxRecentActivity caps how many login/logout events are returned, newest first.
const maxRecentActivity = 50

// maxRecentFailed caps how many failed login attempts are returned, newest first.
const maxRecentFailed = 20

// Service serves GET /interactive-logins responses from loginmonitor history.
type Service struct {
	mem *memory.Store
}

// New creates a login Service.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
}

// Response is the GET /interactive-logins payload.
type Response struct {
	Active               []SessionItem `json:"active"`
	RecentActivity       []SessionItem `json:"recentActivity"`
	RecentFailedAttempts []FailedItem  `json:"recentFailedAttempts"`
	HistoryCount         int           `json:"historyCount"`
	FailedCount          int           `json:"failedCount"`
	// Live is true when no background history exists yet and this response
	// was built from a one-shot live D-Bus query instead.
	Live bool   `json:"live"`
	Note string `json:"note,omitempty"`
}

// SessionItem is one login session entry (active or historical) in the response.
type SessionItem struct {
	EventType        string `json:"eventType"`
	SessionID        string `json:"sessionId"`
	Username         string `json:"username"`
	Class            string `json:"class,omitempty"`
	SessionType      string `json:"sessionType,omitempty"`
	Remote           bool   `json:"remote"`
	RemoteHost       string `json:"remoteHost,omitempty"`
	SourceIP         string `json:"sourceIp,omitempty"`
	ResolvedHostname string `json:"resolvedHostname,omitempty"`
	TTY              string `json:"tty,omitempty"`
	Timestamp        string `json:"timestamp"`
}

// FailedItem is one failed login attempt in the response.
type FailedItem struct {
	Username         string `json:"username"`
	SourceIP         string `json:"sourceIp,omitempty"`
	RemoteHost       string `json:"remoteHost,omitempty"`
	ResolvedHostname string `json:"resolvedHostname,omitempty"`
	Timestamp        string `json:"timestamp"`
	Service          string `json:"service,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// Handler returns the http.HandlerFunc for GET /interactive-logins.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		history, err := loginmonitor.ReadHistory(s.mem)
		if err != nil {
			slog.Warn("loginapi: could not read history, falling back to live query", "error", err)
		}
		if len(history) > 0 {
			failedHistory, _ := loginmonitor.ReadFailedHistory(s.mem)
			_ = json.NewEncoder(w).Encode(buildFromHistory(history, failedHistory))
			return
		}

		sessions, err := logind.ListSessions()
		if err != nil {
			_ = json.NewEncoder(w).Encode(buildLive(nil, "session enumeration unavailable: "+err.Error()))
			return
		}
		_ = json.NewEncoder(w).Encode(buildLive(sessions, "background login monitor has not recorded any history yet"))
	}
}

// buildFromHistory derives the active-session set and capped, newest-first
// activity/failed-attempt feeds from the full loginmonitor history.
func buildFromHistory(history []loginmonitor.LoginEvent, failedHistory []loginmonitor.FailedLoginEvent) Response {
	active := loginmonitor.ActiveSessions(history)

	recent := history
	if len(recent) > maxRecentActivity {
		recent = recent[len(recent)-maxRecentActivity:]
	}

	recentFailed := failedHistory
	if len(recentFailed) > maxRecentFailed {
		recentFailed = recentFailed[len(recentFailed)-maxRecentFailed:]
	}

	return Response{
		Active:               toSessionItems(active),
		RecentActivity:       toSessionItems(reverseSessions(recent)),
		RecentFailedAttempts: toFailedItems(reverseFailed(recentFailed)),
		HistoryCount:         len(history),
		FailedCount:          len(failedHistory),
	}
}

// buildLive builds a response from a direct D-Bus query, used when the
// background monitor has not accumulated any history yet.
func buildLive(sessions []logind.SessionInfo, note string) Response {
	items := make([]SessionItem, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, SessionItem{
			EventType:   "existing",
			SessionID:   s.ID,
			Username:    s.Username,
			Class:       s.Class,
			SessionType: s.SessionType,
			Remote:      s.Remote,
			RemoteHost:  s.RemoteHost,
			TTY:         s.TTY,
			Timestamp:   formatTime(s.Timestamp),
		})
	}
	return Response{
		Active:               items,
		RecentActivity:       []SessionItem{},
		RecentFailedAttempts: []FailedItem{},
		Live:                 true,
		Note:                 note,
	}
}

func toSessionItems(events []loginmonitor.LoginEvent) []SessionItem {
	items := make([]SessionItem, 0, len(events))
	for _, e := range events {
		items = append(items, SessionItem{
			EventType:        string(e.EventType),
			SessionID:        e.SessionID,
			Username:         e.Username,
			Class:            e.Class,
			SessionType:      e.SessionType,
			Remote:           e.Remote,
			RemoteHost:       e.RemoteHost,
			SourceIP:         e.SourceIP,
			ResolvedHostname: e.ResolvedHostname,
			TTY:              e.TTY,
			Timestamp:        formatTime(e.Timestamp),
		})
	}
	return items
}

func toFailedItems(events []loginmonitor.FailedLoginEvent) []FailedItem {
	items := make([]FailedItem, 0, len(events))
	for _, e := range events {
		items = append(items, FailedItem{
			Username:         e.Username,
			SourceIP:         e.SourceIP,
			RemoteHost:       e.RemoteHost,
			ResolvedHostname: e.ResolvedHostname,
			Timestamp:        formatTime(e.Timestamp),
			Service:          e.Service,
			Reason:           e.Reason,
		})
	}
	return items
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// reverseSessions returns events newest-first, for display.
func reverseSessions(events []loginmonitor.LoginEvent) []loginmonitor.LoginEvent {
	out := make([]loginmonitor.LoginEvent, len(events))
	for i, e := range events {
		out[len(events)-1-i] = e
	}
	return out
}

// reverseFailed returns events newest-first, for display.
func reverseFailed(events []loginmonitor.FailedLoginEvent) []loginmonitor.FailedLoginEvent {
	out := make([]loginmonitor.FailedLoginEvent, len(events))
	for i, e := range events {
		out[len(events)-1-i] = e
	}
	return out
}
