package loginmonitor

import (
	"encoding/json"
	"fmt"
	"time"

	"agent_patches/endpoint-server/memory"
)

const (
	failedHistoryKey = "failed_login_history"
	maxFailedHistory = 500
)

// FailedLoginEvent records one failed authentication attempt.
type FailedLoginEvent struct {
	Username         string    `json:"username"`
	SourceIP         string    `json:"source_ip,omitempty"`
	RemoteHost       string    `json:"remote_host,omitempty"`
	ResolvedHostname string    `json:"resolved_hostname,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
	Service          string    `json:"service,omitempty"` // e.g. "sshd"
	Reason           string    `json:"reason,omitempty"`  // e.g. "invalid password", "invalid user"
}

// ReadFailedHistory returns the full failed login event history from the AttrsStore.
// Returns nil, nil if no history has been recorded yet.
func ReadFailedHistory(mem *memory.Store) ([]FailedLoginEvent, error) {
	var history []FailedLoginEvent
	raw, err := mem.Attrs().All()
	if err != nil || raw == nil {
		return nil, err
	}
	val, ok := raw[failedHistoryKey]
	if !ok {
		return nil, nil
	}
	if err := json.Unmarshal(val, &history); err != nil {
		return nil, fmt.Errorf("loginmonitor: parse failed history: %w", err)
	}
	return history, nil
}
