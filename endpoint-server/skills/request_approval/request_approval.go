// Package request_approval provides a skill that pauses the agent and
// waits for an operator to approve or reject a proposed action.
//
// The approval request is written to the durable file-backed AttrsStore so
// it survives process restarts. The POST /approvals/:id/decision endpoint
// writes the operator's decision to the same attrs key; this function polls
// that key until the status changes from "pending".
//
// If no decision arrives within approvalTimeout, the request is requeued
// automatically up to maxRetries times. After all retries are exhausted a
// critical escalation entry is written to the timeline and the notifier is
// called so the operator is alerted via every configured sink.
package request_approval

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	pollInterval    = 5 * time.Second
	approvalTimeout = 24 * time.Hour
	maxRetries      = 3
	maxEntries      = 50
)

// ApprovalEntry is the durable state stored in AttrsStore under the key
// "approval:<id>". The POST /approvals/:id/decision endpoint updates this
// record when the operator decides.
type ApprovalEntry struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Detail         string     `json:"detail"`
	ProposedAction string     `json:"proposed_action"`
	Risk           string     `json:"risk"`
	Status         string     `json:"status"` // pending | approved | rejected | timed_out | cancelled
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	RetryCount     int        `json:"retry_count,omitempty"`
	ParentID       string     `json:"parent_id,omitempty"` // ID of the first attempt; empty on attempt 0
}

// AttrsKey returns the AttrsStore key for the given approval ID.
func AttrsKey(id string) string { return "approval:" + id }

type requestApprovalInput struct {
	Title          string `json:"title" jsonschema_description:"Short one-line description of what requires approval."`
	Detail         string `json:"detail" jsonschema_description:"Full explanation of why operator approval is needed."`
	ProposedAction string `json:"proposed_action" jsonschema_description:"The exact action that will be executed if approved."`
	Risk           string `json:"risk" jsonschema_description:"Risk level of the proposed action: low, medium, or high."`
}

// RequestApproval writes a pending approval to durable agent memory and blocks
// until the operator decides via the central dashboard. If no decision arrives
// within approvalTimeout, it retries automatically up to maxRetries times.
// After all retries are exhausted it escalates via the notifier and returns
// "timed_out". Returns an error only on context cancellation.
func RequestApproval(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, title, detail, proposedAction, risk string) (string, error) {
	var parentID string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		id := newUUID()
		if attempt == 0 {
			parentID = id
		}

		result, err := requestApprovalOnce(ctx, mem, id, parentID, title, detail, proposedAction, risk, attempt)
		if err != nil {
			return "", err
		}

		switch result {
		case "approved", "rejected":
			return result, nil
		case "timed_out":
			if attempt < maxRetries {
				slog.Warn("request_approval: timed out, retrying",
					"id", id, "attempt", attempt+1, "max_retries", maxRetries)
				continue
			}
			// All retries exhausted.
			escalate(ctx, mem, notify, title, proposedAction, risk, parentID)
			return "timed_out", nil
		}
	}

	return "timed_out", nil
}

// requestApprovalOnce runs a single approval wait cycle bounded by approvalTimeout.
// Returns "approved", "rejected", or "timed_out"; returns a non-nil error only
// on context cancellation.
func requestApprovalOnce(ctx context.Context, mem *memory.Store, id, parentID, title, detail, proposedAction, risk string, retryCount int) (string, error) {
	now := time.Now()
	attrKey := AttrsKey(id)

	parentIDField := ""
	if id != parentID {
		parentIDField = parentID
	}

	entry := ApprovalEntry{
		ID:             id,
		Title:          title,
		Detail:         detail,
		ProposedAction: proposedAction,
		Risk:           risk,
		Status:         "pending",
		RequestedAt:    now,
		RetryCount:     retryCount,
		ParentID:       parentIDField,
	}
	if err := mem.Attrs().Set(attrKey, entry); err != nil {
		return "", fmt.Errorf("request_approval: write attrs: %w", err)
	}

	if err := writeTimeline(mem, id, parentIDField, title, detail, proposedAction, risk, now, retryCount); err != nil {
		slog.Warn("request_approval: failed to write timeline entry", "id", id, "error", err)
	}

	if retryCount == 0 {
		slog.Info("request_approval: waiting for operator decision", "id", id, "title", title)
	} else {
		slog.Info("request_approval: retrying approval request",
			"id", id, "title", title, "attempt", retryCount+1, "of", maxRetries+1)
	}

	ticker := time.NewTicker(pollInterval)
	timer := time.NewTimer(approvalTimeout)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = patchAttrs(mem, attrKey, "cancelled", "", nil)
			return "", ctx.Err()

		case <-timer.C:
			_ = patchAttrs(mem, attrKey, "timed_out", "no operator response within timeout window", nil)
			_ = PatchTimeline(mem, id, "timed_out")
			slog.Warn("request_approval: timed out waiting for decision", "id", id, "title", title)
			return "timed_out", nil

		case <-ticker.C:
			var current ApprovalEntry
			if err := mem.Attrs().Get(attrKey, &current); err != nil {
				slog.Warn("request_approval: poll read failed", "id", id, "error", err)
				continue
			}
			if current.Status != "pending" {
				slog.Info("request_approval: decision received", "id", id, "decision", current.Status)
				_ = PatchTimeline(mem, id, current.Status)
				return current.Status, nil
			}
		}
	}
}

// escalate writes a critical escalation timeline entry and notifies the operator
// after all retry attempts have been exhausted without an operator decision.
func escalate(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, title, proposedAction, risk, parentID string) {
	slog.Error("request_approval: all retries exhausted, escalating",
		"title", title, "parent_id", parentID, "attempts", maxRetries+1)

	now := time.Now()
	severity := "critical"
	escalationTitle := fmt.Sprintf("ESCALATION: \"%s\" has not been acknowledged after %d attempts", title, maxRetries+1)
	detail := fmt.Sprintf(
		"Proposed action: %s\nRisk: %s\nOriginal approval ID: %s\n\nThis approval has timed out %d times with no operator response. Immediate attention required.",
		proposedAction, risk, parentID, maxRetries+1,
	)

	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)
	entries = append([]status.TimelineEntry{{
		ID:       newUUID(),
		Time:     now.Format(time.RFC3339),
		Type:     "escalation",
		Title:    escalationTitle,
		Detail:   detail,
		Severity: severity,
		Risk:     risk,
		ParentID: parentID,
	}}, entries...)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	_ = d.Write(entries)

	notify.Notify(ctx,
		fmt.Sprintf("[ESCALATION] Approval not acknowledged: %s", title),
		fmt.Sprintf(
			"The approval request %q (action: %s, risk: %s) has timed out %d times with no operator response.\n\nOriginal approval ID: %s\n\nImmediate operator action required.",
			title, proposedAction, risk, maxRetries+1, parentID,
		),
	)
}

// NewRequestApprovalTool wraps RequestApproval as an agent tool the LLM can
// invoke directly from the tool-use loop.
func NewRequestApprovalTool(mem *memory.Store, notify *notifier.Notifier) (tool.Tool, error) {
	return tool.New(
		"request_approval",
		"Pause and request operator approval before taking a potentially impactful action. "+
			"Writes the request to durable agent memory and blocks until the operator approves "+
			"or rejects via the central dashboard. If no decision arrives within 24 hours the "+
			"request is automatically requeued up to 3 times; after that the operator is "+
			"escalated via the notification system. Returns \"approved\", \"rejected\", or "+
			"\"timed_out\". Always use this before actions that modify system state, remove "+
			"data, restart services, or carry medium-to-high risk.",
		func(ctx context.Context, in requestApprovalInput) (string, error) {
			return RequestApproval(ctx, mem, notify, in.Title, in.Detail, in.ProposedAction, in.Risk)
		},
	)
}

// writeTimeline prepends an approval entry to the timeline domain.
func writeTimeline(mem *memory.Store, id, parentID, title, detail, proposedAction, risk string, now time.Time, retryCount int) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)

	displayTitle := title
	if retryCount > 0 {
		displayTitle = fmt.Sprintf("%s (retry %d of %d)", title, retryCount, maxRetries)
	}

	pending := "pending"
	entries = append([]status.TimelineEntry{{
		ID:             id,
		Time:           now.Format(time.RFC3339),
		Type:           "approval",
		Title:          displayTitle,
		Detail:         detail,
		Risk:           risk,
		ProposedAction: &proposedAction,
		Status:         &pending,
		RetryCount:     retryCount,
		ParentID:       parentID,
	}}, entries...)

	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	return d.Write(entries)
}

// PatchTimeline finds the entry by id in the timeline and updates its status.
// Exported so the approvalapi decision handler can call it directly.
func PatchTimeline(mem *memory.Store, id, newStatus string) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	if err := d.ReadCurrent(&entries); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].ID == id {
			s := newStatus
			entries[i].Status = &s
			return d.Write(entries)
		}
	}
	return nil
}

// patchAttrs updates the status fields on an existing approval attrs entry.
func patchAttrs(mem *memory.Store, key, newStatus, reason string, decidedAt *time.Time) error {
	var entry ApprovalEntry
	if err := mem.Attrs().Get(key, &entry); err != nil {
		return err
	}
	entry.Status = newStatus
	entry.Reason = reason
	entry.DecidedAt = decidedAt
	return mem.Attrs().Set(key, entry)
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("approval-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
