// Package request_manual_run creates a manual-execution task when a command
// cannot run automatically due to sudoers restrictions. The agent pauses and
// waits for the operator to run the command on the target host and paste the
// output back via the dashboard. Unlike request_approval, this task is never
// "approved" or "rejected" — it is "completed" (output provided) or "skipped"
// (operator chose not to run it). The entry also carries a sudoers instruction
// the operator can give to Claude to add native support for the command.
package request_manual_run

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/notifier"
)

const (
	pollInterval  = 5 * time.Second
	manualTimeout = 24 * time.Hour
	maxEntries    = 50

	// attrsRetention bounds how long a decided (non-pending) manual-run
	// attrs record is kept — mirrors request_approval's pruning and
	// incidents.go's resolvedRetention pattern, so attrs.json doesn't grow
	// without bound for the life of the host.
	attrsRetention = 30 * 24 * time.Hour
	// maxAttrsEntries hard-caps the number of decided manual-run records
	// kept, dropping the oldest first.
	maxAttrsEntries = 200
	// sweepInterval is how often the retention sweeper prunes old entries.
	sweepInterval = time.Minute
)

// ManualRunEntry is the durable state stored in AttrsStore under the key
// "manual_run:<id>". The POST /manual-runs/:id/result endpoint updates this
// record when the operator submits output.
type ManualRunEntry struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Command            string     `json:"command"`
	Host               string     `json:"host"`
	Reason             string     `json:"reason"`
	SudoersInstruction string     `json:"sudoers_instruction"`
	Status             string     `json:"status"` // pending | completed | skipped | timed_out | cancelled
	Output             string     `json:"output,omitempty"`
	RequestedAt        time.Time  `json:"requested_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// AttrsKey returns the AttrsStore key for the given manual run ID.
func AttrsKey(id string) string { return "manual_run:" + id }

// RequestManualRun creates a pending manual-run task and blocks until the
// operator submits output or skips. Returns (output, status, error) where
// status is "completed", "skipped", or "timed_out".
func RequestManualRun(ctx context.Context, mem *memory.Store, notify *notifier.Notifier, title, command, host, reason string) (string, string, error) {
	id := newUUID()
	sudoersInstruction := buildSudoersInstruction(host, command)

	now := time.Now()
	attrKey := AttrsKey(id)

	entry := ManualRunEntry{
		ID:                 id,
		Title:              title,
		Command:            command,
		Host:               host,
		Reason:             reason,
		SudoersInstruction: sudoersInstruction,
		Status:             "pending",
		RequestedAt:        now,
	}
	if err := mem.Attrs().Set(attrKey, entry); err != nil {
		return "", "", fmt.Errorf("request_manual_run: write attrs: %w", err)
	}

	if err := writeTimeline(mem, id, title, command, host, reason, sudoersInstruction, now); err != nil {
		slog.Warn("request_manual_run: failed to write timeline entry", "id", id, "error", err)
	}

	notify.Notify(ctx,
		fmt.Sprintf("[Manual Run Required] %s", title),
		fmt.Sprintf(
			"A command could not run automatically due to sudoers restrictions and requires manual execution.\n\nHost: %s\nCommand: %s\n\nReason: %s\n\nPlease run the command on the host and submit the output via the dashboard.",
			host, command, reason,
		),
	)

	slog.Info("request_manual_run: waiting for operator output", "id", id, "title", title, "command", command)

	ticker := time.NewTicker(pollInterval)
	timer := time.NewTimer(manualTimeout)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = patchAttrs(mem, attrKey, "cancelled", "")
			_ = PatchTimeline(mem, id, "cancelled")
			return "", "", ctx.Err()

		case <-timer.C:
			_ = patchAttrs(mem, attrKey, "timed_out", "")
			_ = PatchTimeline(mem, id, "timed_out")
			slog.Warn("request_manual_run: timed out waiting for output", "id", id, "title", title)
			return "", "timed_out", nil

		case <-ticker.C:
			var current ManualRunEntry
			if err := mem.Attrs().Get(attrKey, &current); err != nil {
				slog.Warn("request_manual_run: poll read failed", "id", id, "error", err)
				continue
			}
			if current.Status != "pending" {
				slog.Info("request_manual_run: output received", "id", id, "status", current.Status)
				_ = PatchTimeline(mem, id, current.Status)
				return current.Output, current.Status, nil
			}
		}
	}
}

// buildSudoersInstruction returns a copy-pasteable Claude prompt the operator
// can use to add a native sudoers rule for the command.
func buildSudoersInstruction(host, command string) string {
	return fmt.Sprintf(
		`On %s, add a passwordless sudoers rule for the service account to run: %s`,
		host, command,
	)
}

func writeTimeline(mem *memory.Store, id, title, command, host, reason, sudoersInstruction string, now time.Time) error {
	d := mem.Domain("timeline")
	var entries []status.TimelineEntry
	_ = d.ReadCurrent(&entries)

	pending := "pending"
	entries = append([]status.TimelineEntry{{
		ID:                 id,
		Time:               now.Format(time.RFC3339),
		Type:               "manual_run",
		Title:              title,
		Detail:             fmt.Sprintf("Host: %s\n\nReason: %s", host, reason),
		ProposedAction:     &command,
		Status:             &pending,
		SudoersInstruction: &sudoersInstruction,
	}}, entries...)

	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	return d.Write(entries)
}

// PatchTimeline finds the entry by id in the timeline and updates its status.
// Exported so the manualrunapi result handler can call it directly.
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
// Exported so the manualrunapi handler can call it for stale entries.
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

func patchAttrs(mem *memory.Store, key, newStatus, output string) error {
	var entry ManualRunEntry
	if err := mem.Attrs().Get(key, &entry); err != nil {
		return err
	}
	entry.Status = newStatus
	entry.Output = output
	if newStatus != "pending" {
		now := time.Now()
		entry.CompletedAt = &now
	}
	return mem.Attrs().Set(key, entry)
}

// StartRetentionSweeper launches a background goroutine that prunes decided
// (non-pending) manual-run attrs entries older than attrsRetention, plus
// enforces maxAttrsEntries. Unlike request_approval, there is no
// pending-expiry sweep here: each RequestManualRun call enforces its own
// timeout in-process via manualTimeout. It exits when ctx is cancelled.
func StartRetentionSweeper(ctx context.Context, mem *memory.Store) {
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneAttrs(mem, time.Now())
			}
		}
	}()
}

// pruneAttrs deletes decided (non-pending) manual-run attrs entries older
// than attrsRetention, then enforces maxAttrsEntries by dropping the oldest
// decided entries first. Returns the number of entries deleted.
func pruneAttrs(mem *memory.Store, now time.Time) int {
	attrs, err := mem.Attrs().All()
	if err != nil {
		slog.Warn("request_manual_run: prune sweep read failed", "error", err)
		return 0
	}

	type decided struct {
		key  string
		when time.Time
	}
	var candidates []decided
	cutoff := now.Add(-attrsRetention)

	for key, raw := range attrs {
		if !strings.HasPrefix(key, "manual_run:") {
			continue
		}
		var entry ManualRunEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Status == "pending" {
			continue
		}
		when := entry.RequestedAt
		if entry.CompletedAt != nil {
			when = *entry.CompletedAt
		}
		candidates = append(candidates, decided{key: key, when: when})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].when.Before(candidates[j].when) })

	deleted := 0
	keep := len(candidates)
	for _, c := range candidates {
		expired := c.when.Before(cutoff)
		overCap := keep > maxAttrsEntries
		if !expired && !overCap {
			break
		}
		if err := mem.Attrs().Delete(c.key); err != nil {
			slog.Warn("request_manual_run: prune delete failed", "key", c.key, "error", err)
			continue
		}
		deleted++
		keep--
	}
	if deleted > 0 {
		slog.Info("request_manual_run: pruned decided manual-run records", "count", deleted)
	}
	return deleted
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("manual-run-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
