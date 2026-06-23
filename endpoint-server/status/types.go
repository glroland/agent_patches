// Package status implements the GET /status endpoint, which reports a
// snapshot of this host's identity, current activity, and recent timeline
// entries for consumption by central-backend.
package status

import "encoding/json"

// AgentInfo describes the identity of this agent's host. Operator-assigned
// metadata (role, tags) lives in central-backend's inventory, not here.
type AgentInfo struct {
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
	OS       string `json:"os"`
}

// StatusBlock describes the agent's current activity state.
type StatusBlock struct {
	State        string   `json:"state"`
	LastPoll     string   `json:"lastPoll"`
	CurrentTask  *string  `json:"currentTask"`  // first running task; nil when idle
	CurrentTasks []string `json:"currentTasks"` // all running tasks; empty when idle
}

// TimelineEntry is one entry in the agent's activity timeline, recorded via
// the report_findings tool.
type TimelineEntry struct {
	ID             string  `json:"id"`
	Time           string  `json:"time"`
	Type           string  `json:"type"`
	Title          string  `json:"title"`
	Detail         string  `json:"detail"`
	Severity       string  `json:"severity,omitempty"`
	Risk           string  `json:"risk,omitempty"`
	ProposedAction *string `json:"proposedAction,omitempty"`
	Status         *string `json:"status,omitempty"`
	RetryCount     int     `json:"retryCount,omitempty"`
	ParentID       string  `json:"parentId,omitempty"`
}

// Response is the full GET /status response body.
type Response struct {
	Agent         AgentInfo       `json:"agent"`
	Status        StatusBlock     `json:"status"`
	Timeline      []TimelineEntry `json:"timeline"`
	LastPatchedAt *string         `json:"lastPatchedAt,omitempty"`
	// StatusDescription is an AI-generated one-sentence summary of what
	// needs operator attention. Only present when state is "attention".
	StatusDescription string `json:"statusDescription,omitempty"`
	// DiskTrends holds the 7-day rolling usage history and computed growth
	// slope for each mount point. Omitted when no trend data has been recorded
	// yet (requires at least one check_drives run).
	DiskTrends json.RawMessage `json:"diskTrends,omitempty"`
}
