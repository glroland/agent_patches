// Package request_approval provides a skill that pauses the agent and
// waits for an operator to approve or reject a proposed action.
//
// The approval request is written to the durable file-backed AttrsStore so
// it survives process restarts. The POST /approvals/:id/decision endpoint
// writes the operator's decision to the same attrs key; this function polls
// that key until the status changes from "pending".
//
// High-risk approvals trigger an immediate out-of-band notification so the
// operator does not need to monitor the dashboard. If no decision arrives
// within approvalTimeout the request is permanently cancelled — not retried —
// and the operator is notified that the action was NOT taken.
package request_approval

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	pollInterval    = 5 * time.Second
	approvalTimeout = 24 * time.Hour
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

	// AutoExecute marks an async approval: no goroutine is waiting on the
	// decision; instead the approvalapi decision handler executes
	// ProposedAction when the operator approves. Async approvals survive
	// agent restarts (there is no waiter to cancel them on shutdown).
	AutoExecute bool `json:"auto_execute,omitempty"`
	// ExecReason is the agent's original reason for the command, used in the
	// manual-run escalation message if execution hits a sudoers restriction.
	ExecReason string `json:"exec_reason,omitempty"`
	// Output is the command output recorded after an async execution.
	Output string `json:"output,omitempty"`
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
// until the operator decides via the central dashboard.
//
// For high-risk actions, the notifier fires immediately when the request is
// created so the operator receives an out-of-band alert without needing to
// monitor the dashboard.
//
// If no decision arrives within approvalTimeout, the request is permanently
// cancelled — not retried — and the notifier fires again so the operator knows
// the action was not taken. Returns "approved", "rejected", or "timed_out".
// Returns an error only on context cancellation.
func RequestApproval(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, title, detail, proposedAction, risk string) (string, error) {
	id := newUUID()
	result, err := requestApprovalOnce(ctx, mem, notify, id, title, detail, proposedAction, risk)
	if err != nil {
		return "", err
	}

	if result == "timed_out" {
		notify.Notify(ctx,
			fmt.Sprintf("[Approval Expired] %s", title),
			fmt.Sprintf(
				"The approval request %q expired without a decision.\n\nProposed action: %s\nRisk: %s\n\nThe action was NOT taken. If it is still needed, reissue from the agent dashboard.",
				title, proposedAction, risk,
			),
		)
	}

	return result, nil
}

// createPending writes the pending approval entry, its timeline card, and the
// immediate high-risk notification. Shared by the blocking and async flows.
func createPending(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, id, title, detail, proposedAction, risk string, autoExecute bool, execReason string) error {
	now := time.Now()

	entry := ApprovalEntry{
		ID:             id,
		Title:          title,
		Detail:         detail,
		ProposedAction: proposedAction,
		Risk:           risk,
		Status:         "pending",
		RequestedAt:    now,
		AutoExecute:    autoExecute,
		ExecReason:     execReason,
	}
	if err := mem.Attrs().Set(AttrsKey(id), entry); err != nil {
		return fmt.Errorf("request_approval: write attrs: %w", err)
	}

	if err := writeTimeline(mem, id, title, detail, proposedAction, risk, now); err != nil {
		slog.Warn("request_approval: failed to write timeline entry", "id", id, "error", err)
	}

	// High-risk approvals notify immediately so operators get an out-of-band
	// alert the moment the request is created rather than at timeout.
	if strings.EqualFold(risk, "high") {
		notify.Notify(ctx,
			fmt.Sprintf("[Approval Required] %s", title),
			fmt.Sprintf(
				"A high-risk action requires your approval.\n\nAction: %s\n\nDetail: %s\n\nRisk: %s\n\nApprove or reject via the central dashboard. If no decision is made within 24 hours the action will be automatically cancelled.",
				proposedAction, detail, risk,
			),
		)
	}
	return nil
}

// SubmitApproval writes a pending approval and returns its ID immediately,
// without waiting for the operator. When autoExecute is true, the approvalapi
// decision handler executes proposedAction on approval. Unlike the blocking
// RequestApproval, the submitting agent run finishes right away, so a pending
// approval never parks a responsibility goroutine — and the request survives
// agent restarts. Expiry is enforced by the sweeper (see StartExpirySweeper).
func SubmitApproval(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, title, detail, proposedAction, risk string, autoExecute bool, execReason string) (string, error) {
	id := newUUID()
	if err := createPending(ctx, mem, notify, id, title, detail, proposedAction, risk, autoExecute, execReason); err != nil {
		return "", err
	}
	slog.Info("request_approval: submitted async approval", "id", id, "title", title, "risk", risk, "auto_execute", autoExecute)
	return id, nil
}

// SetOutput records the execution output on an approval entry after an async
// auto-execute run. Output is truncated to keep attrs.json bounded.
func SetOutput(mem *memory.Store, id, output string) error {
	const maxOutputLen = 4000
	var entry ApprovalEntry
	key := AttrsKey(id)
	if err := mem.Attrs().Get(key, &entry); err != nil {
		return err
	}
	if len(output) > maxOutputLen {
		output = output[:maxOutputLen] + "\n… (truncated)"
	}
	entry.Output = output
	return mem.Attrs().Set(key, entry)
}

// requestApprovalOnce runs a single approval wait cycle bounded by approvalTimeout.
// Returns "approved", "rejected", or "timed_out"; returns a non-nil error only
// on context cancellation.
func requestApprovalOnce(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, id, title, detail, proposedAction, risk string) (string, error) {
	attrKey := AttrsKey(id)
	if err := createPending(ctx, mem, notify, id, title, detail, proposedAction, risk, false, ""); err != nil {
		return "", err
	}

	slog.Info("request_approval: waiting for operator decision", "id", id, "title", title, "risk", risk)

	ticker := time.NewTicker(pollInterval)
	timer := time.NewTimer(approvalTimeout)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// The agent process is shutting down (e.g. a redeploy) while this
			// approval is still in flight. Patch both stores — not just
			// attrs — so the timeline (what the UI renders the Approve/Reject
			// card from) stops showing this as actionable too. Without this,
			// the card looks pending forever after restart, but any decision
			// on it 409s because attrs already says "cancelled".
			_ = patchAttrs(mem, attrKey, "cancelled", "", nil)
			_ = PatchTimeline(mem, id, "cancelled")
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

// NewRequestApprovalTool wraps RequestApproval as an agent tool the LLM can
// invoke directly from the tool-use loop.
func NewRequestApprovalTool(mem *memory.Store, notify *notifier.Notifier) (tool.Tool, error) {
	return tool.New(
		"request_approval",
		"Pause and request operator approval before taking a potentially impactful action. "+
			"Writes the request to durable agent memory and blocks until the operator approves "+
			"or rejects via the central dashboard. High-risk requests trigger an immediate "+
			"out-of-band notification. If no decision arrives within 24 hours the request is "+
			"permanently cancelled and the operator is notified — it is NOT retried and the "+
			"action is NOT taken. Returns \"approved\", \"rejected\", or \"timed_out\". "+
			"Always use this before actions that modify system state, remove data, restart "+
			"services, or carry medium-to-high risk.",
		func(ctx context.Context, in requestApprovalInput) (string, error) {
			return RequestApproval(ctx, mem, notify, in.Title, in.Detail, in.ProposedAction, in.Risk)
		},
	)
}

// writeTimeline prepends an approval entry to the timeline domain.
func writeTimeline(mem *memory.Store, id, title, detail, proposedAction, risk string, now time.Time) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)

	pending := "pending"
	entries = append([]status.TimelineEntry{{
		ID:             id,
		Time:           now.Format(time.RFC3339),
		Type:           "approval",
		Title:          title,
		Detail:         detail,
		Risk:           risk,
		ProposedAction: &proposedAction,
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

// RemoveFromTimeline removes the entry with the given id from the timeline.
// It is a no-op if no matching entry exists. Exported so approvalapi can call
// it when purging a stale approval on a 409 Conflict response.
func RemoveFromTimeline(mem *memory.Store, id string) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	if err := d.ReadCurrent(&entries); err != nil {
		return err
	}
	filtered := entries[:0]
	for _, e := range entries {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) == len(entries) {
		return nil
	}
	return d.Write(filtered)
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

// sweepInterval is how often the expiry sweeper scans for stale approvals.
const sweepInterval = time.Minute

// StartExpirySweeper launches a background goroutine that expires pending
// approvals older than the approval timeout. The blocking RequestApproval
// flow enforces its own timeout in-process; the sweeper exists for async
// (AutoExecute) approvals, which have no waiting goroutine, and for orphaned
// pendings left behind by a crash. It exits when ctx is cancelled.
func StartExpirySweeper(ctx context.Context, mem *memory.Store, notify *notifier.Notifier) {
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ExpirePending(ctx, mem, notify, time.Now())
			}
		}
	}()
}

// ExpirePending marks every pending approval older than the approval timeout
// as timed_out, patching attrs and the timeline. Async approvals additionally
// notify the operator that the action was NOT taken (the blocking flow sends
// its own expiry notification via its in-process waiter). Returns the number
// of approvals expired. Exported for the sweeper and for tests.
func ExpirePending(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, now time.Time) int {
	attrs, err := mem.Attrs().All()
	if err != nil {
		slog.Warn("request_approval: expiry sweep read failed", "error", err)
		return 0
	}

	expired := 0
	for key, raw := range attrs {
		if !strings.HasPrefix(key, "approval:") {
			continue
		}
		var entry ApprovalEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Status != "pending" || now.Sub(entry.RequestedAt) <= approvalTimeout {
			continue
		}

		_ = patchAttrs(mem, key, "timed_out", "no operator response within timeout window", nil)
		_ = PatchTimeline(mem, entry.ID, "timed_out")
		expired++
		slog.Warn("request_approval: expired stale pending approval", "id", entry.ID, "title", entry.Title, "requested_at", entry.RequestedAt)

		if entry.AutoExecute {
			notify.Notify(ctx,
				fmt.Sprintf("[Approval Expired] %s", entry.Title),
				fmt.Sprintf(
					"The approval request %q expired without a decision.\n\nProposed action: %s\nRisk: %s\n\nThe action was NOT taken. If it is still needed, reissue from the agent dashboard.",
					entry.Title, entry.ProposedAction, entry.Risk,
				),
			)
		}
	}
	return expired
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
