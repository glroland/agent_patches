// Package approvalapi implements POST /approvals/:id/decision.
//
// Two kinds of approvals land here:
//   - Blocking (request_approval skill): a goroutine polls the attrs entry;
//     this endpoint writes the decision, which the poll detects within ≤5 s.
//   - Async (run_approved_command, AutoExecute=true): no goroutine is
//     waiting. On approval, this endpoint launches the stored command in a
//     detached goroutine via run_approved_command.ExecuteOnApproval, which
//     records the result on the approval entry and the timeline.
package approvalapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/policy"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/skills/run_approved_command"
	"agent_patches/endpoint-server/utils/notifier"
)

// Service handles operator approval decision requests.
type Service struct {
	mem      *memory.Store
	notify   *notifier.Notifier
	policies *policy.Store
}

// New returns a Service backed by mem. notify and policies are used when an
// approved entry carries an auto-execute command.
func New(mem *memory.Store, notify *notifier.Notifier, policies *policy.Store) *Service {
	return &Service{mem: mem, notify: notify, policies: policies}
}

// Handler returns an http.Handler for:
//
//	POST /approvals/{id}/decision
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Expect path: /approvals/{id}/decision
		path := strings.TrimPrefix(r.URL.Path, "/approvals/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "decision" || parts[0] == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id := parts[0]

		var req struct {
			Decision string `json:"decision"` // "approved" or "rejected"
			Reason   string `json:"reason,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Decision != "approved" && req.Decision != "rejected" {
			http.Error(w, `decision must be "approved" or "rejected"`, http.StatusBadRequest)
			return
		}

		attrKey := reqapproval.AttrsKey(id)

		var entry reqapproval.ApprovalEntry
		if err := s.mem.Attrs().Get(attrKey, &entry); err != nil {
			// Attrs entry is gone — the agent already processed this approval.
			// Remove the stale timeline entry so the next poll clears it from the UI.
			if removeErr := reqapproval.RemoveFromTimeline(s.mem, id); removeErr != nil {
				slog.Warn("approvalapi: failed to remove stale timeline entry", "id", id, "error", removeErr)
			} else {
				slog.Info("approvalapi: removed stale timeline entry for already-processed approval", "id", id)
			}
			http.Error(w, "approval not found", http.StatusNotFound)
			return
		}
		if entry.Status != "pending" {
			msg := "approval already decided: " + entry.Status
			switch entry.Status {
			case "cancelled":
				msg = "approval cancelled: the agent restarted before a decision was made"
			case "timed_out":
				msg = "approval timed out waiting for a decision and was requeued or escalated"
			}
			if err := s.mem.Attrs().Delete(attrKey); err != nil {
				slog.Warn("approvalapi: failed to delete stale approval attrs", "id", id, "error", err)
			}
			if err := reqapproval.RemoveFromTimeline(s.mem, id); err != nil {
				slog.Warn("approvalapi: failed to remove stale approval from timeline", "id", id, "error", err)
			}
			slog.Info("approvalapi: purged stale approval on conflict", "id", id, "status", entry.Status)
			http.Error(w, msg, http.StatusConflict)
			return
		}

		now := time.Now()
		entry.Status = req.Decision
		entry.Reason = req.Reason
		entry.DecidedAt = &now
		if err := s.mem.Attrs().Set(attrKey, entry); err != nil {
			slog.Error("approvalapi: failed to persist decision", "id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Update the timeline entry status so the dashboard stops showing the
		// approval as pending without waiting for the next poll cycle.
		if err := reqapproval.PatchTimeline(s.mem, id, req.Decision); err != nil {
			slog.Warn("approvalapi: failed to update timeline status", "id", id, "error", err)
		}

		// Async approvals have no waiting goroutine — execute the stored
		// command now, detached from this request (the response returns
		// immediately; the result lands on the timeline and approval entry).
		if req.Decision == "approved" && entry.AutoExecute {
			slog.Info("approvalapi: launching auto-execute command", "id", id)
			go run_approved_command.ExecuteOnApproval(s.mem, s.notify, s.policies, entry)
		}

		slog.Info("approvalapi: decision recorded", "id", id, "decision", req.Decision)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": req.Decision})
	})
}
