// Package approvalapi implements POST /approvals/:id/decision.
//
// The request_approval skill writes a pending entry to AttrsStore and polls
// it. This endpoint writes the operator's decision to that same key, which
// the polling loop detects within one poll interval (≤5 s).
package approvalapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
)

// Service handles operator approval decision requests.
type Service struct {
	mem *memory.Store
}

// New returns a Service backed by mem.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
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

		slog.Info("approvalapi: decision recorded", "id", id, "decision", req.Decision)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": req.Decision})
	})
}
