// Package status implements the GET /status endpoint, which reports a
// snapshot of this host's identity, current activity, and recent timeline
// entries for consumption by central-backend.
package status

// AgentInfo describes the identity of this agent's host. Operator-assigned
// metadata (role, tags) lives in central-backend's inventory, not here.
type AgentInfo struct {
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
	OS       string `json:"os"`
}

// StatusBlock describes the agent's current activity state.
type StatusBlock struct {
	State       string  `json:"state"`
	LastPoll    string  `json:"lastPoll"`
	CurrentTask *string `json:"currentTask"`
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
}
