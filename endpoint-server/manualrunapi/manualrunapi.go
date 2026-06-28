// Package manualrunapi implements POST /manual-runs/:id/result.
//
// When run_approved_command fails with a sudoers restriction, it calls
// request_manual_run which writes a pending entry to AttrsStore and polls it.
// This endpoint writes the operator's output (or a skip decision) to that same
// key, which the polling loop detects within one poll interval (≤5 s).
package manualrunapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	reqmanualrun "agent_patches/endpoint-server/skills/request_manual_run"
)

// Service handles operator manual-run result submissions.
type Service struct {
	mem *memory.Store
}

// New returns a Service backed by mem.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
}

// Handler returns an http.Handler for:
//
//	POST /manual-runs/{id}/result
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Expect path: /manual-runs/{id}/result
		path := strings.TrimPrefix(r.URL.Path, "/manual-runs/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "result" || parts[0] == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id := parts[0]

		var req struct {
			Output string `json:"output"`
			Status string `json:"status"` // "completed" or "skipped"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Status != "completed" && req.Status != "skipped" {
			http.Error(w, `status must be "completed" or "skipped"`, http.StatusBadRequest)
			return
		}

		attrKey := reqmanualrun.AttrsKey(id)

		var entry reqmanualrun.ManualRunEntry
		if err := s.mem.Attrs().Get(attrKey, &entry); err != nil {
			if removeErr := reqmanualrun.RemoveFromTimeline(s.mem, id); removeErr != nil {
				slog.Warn("manualrunapi: failed to remove stale timeline entry", "id", id, "error", removeErr)
			} else {
				slog.Info("manualrunapi: removed stale timeline entry for already-processed request", "id", id)
			}
			http.Error(w, "manual run request not found", http.StatusNotFound)
			return
		}
		if entry.Status != "pending" {
			http.Error(w, "manual run request already resolved: "+entry.Status, http.StatusConflict)
			return
		}

		now := time.Now()
		entry.Status = req.Status
		entry.Output = req.Output
		entry.CompletedAt = &now
		if err := s.mem.Attrs().Set(attrKey, entry); err != nil {
			slog.Error("manualrunapi: failed to persist result", "id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if err := reqmanualrun.PatchTimeline(s.mem, id, req.Status); err != nil {
			slog.Warn("manualrunapi: failed to update timeline status", "id", id, "error", err)
		}

		slog.Info("manualrunapi: result recorded", "id", id, "status", req.Status)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": req.Status})
	})
}
