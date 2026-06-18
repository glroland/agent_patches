// Package request_approval provides a skill that pauses the agent and
// waits for an operator to approve or reject a proposed action.
//
// The approval request is written to the durable file-backed AttrsStore so
// it survives process restarts. The POST /approvals/:id/decision endpoint
// writes the operator's decision to the same attrs key; this function polls
// that key until the status changes from "pending".
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
)

const (
	pollInterval   = 5 * time.Second
	defaultTimeout = 24 * time.Hour
	maxEntries     = 50
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
}

// AttrsKey returns the AttrsStore key for the given approval ID.
func AttrsKey(id string) string { return "approval:" + id }

type requestApprovalInput struct {
	Title          string `json:"title" jsonschema_description:"Short one-line description of what requires approval."`
	Detail         string `json:"detail" jsonschema_description:"Full explanation of why operator approval is needed."`
	ProposedAction string `json:"proposed_action" jsonschema_description:"The exact action that will be executed if approved."`
	Risk           string `json:"risk" jsonschema_description:"Risk level of the proposed action: low, medium, or high."`
}

// NewRequestApprovalTool returns a tool that blocks until an operator approves
// or rejects via the central dashboard. It returns "approved", "rejected", or
// "timed_out". Use it before any action that modifies system state or carries
// medium-to-high risk.
func NewRequestApprovalTool(mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"request_approval",
		"Pause and request operator approval before taking a potentially impactful action. "+
			"Writes the request to durable agent memory and blocks until the operator approves "+
			"or rejects via the central dashboard. Returns \"approved\", \"rejected\", or "+
			"\"timed_out\". Always use this before actions that modify system state, remove "+
			"data, restart services, or carry medium-to-high risk.",
		func(ctx context.Context, in requestApprovalInput) (string, error) {
			id := newUUID()
			attrKey := AttrsKey(id)
			now := time.Now()

			entry := ApprovalEntry{
				ID:             id,
				Title:          in.Title,
				Detail:         in.Detail,
				ProposedAction: in.ProposedAction,
				Risk:           in.Risk,
				Status:         "pending",
				RequestedAt:    now,
			}

			// Durable write — this is what the decision endpoint updates and what
			// this loop polls. Survives restarts on both sides.
			if err := mem.Attrs().Set(attrKey, entry); err != nil {
				return "", fmt.Errorf("request_approval: write attrs: %w", err)
			}

			// Timeline entry lets the dashboard display the pending request.
			// The entry ID matches the approval ID so the decision handler can
			// update the timeline status when a decision arrives.
			if err := writeTimeline(mem, id, in, now); err != nil {
				slog.Warn("request_approval: failed to write timeline entry", "id", id, "error", err)
			}

			slog.Info("request_approval: waiting for operator decision", "id", id, "title", in.Title)

			ticker := time.NewTicker(pollInterval)
			defer ticker.Stop()
			timeout := time.NewTimer(defaultTimeout)
			defer timeout.Stop()

			for {
				select {
				case <-ctx.Done():
					_ = patchAttrs(mem, attrKey, "cancelled", "", nil)
					return "", ctx.Err()

				case <-timeout.C:
					t := time.Now()
					_ = patchAttrs(mem, attrKey, "timed_out", "", &t)
					_ = PatchTimeline(mem, id, "timed_out")
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
		},
	)
}

// writeTimeline prepends an approval entry to the timeline domain.
func writeTimeline(mem *memory.Store, id string, in requestApprovalInput, now time.Time) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)

	proposed := in.ProposedAction
	pending := "pending"
	entries = append([]status.TimelineEntry{{
		ID:             id,
		Time:           now.Format(time.RFC3339),
		Type:           "approval",
		Title:          in.Title,
		Detail:         in.Detail,
		Risk:           in.Risk,
		ProposedAction: &proposed,
		Status:         &pending,
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
