// Package findingsapi implements POST /findings/:id/resolve, letting an
// operator dismiss a specific finding recorded by report_findings (an
// observation or recommendation with a severity) from the timeline. This is
// the "resolved" counterpart to approvalapi's decision endpoint, but for
// findings rather than approval requests: resolving marks the timeline entry
// so it stops counting as an open concern on the Issues page, without
// removing its history.
package findingsapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/status"
)

// Service handles operator resolve requests for timeline findings.
type Service struct {
	mem *memory.Store
}

// New returns a Service backed by mem.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
}

// Handler returns an http.Handler for:
//
//	POST /findings/{id}/resolve
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Expect path: /findings/{id}/resolve
		path := strings.TrimPrefix(r.URL.Path, "/findings/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "resolve" || parts[0] == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id := parts[0]

		found, notAFinding, err := s.resolve(id)
		if err != nil {
			slog.Error("findingsapi: failed to resolve finding", "id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, "finding not found", http.StatusNotFound)
			return
		}
		if notAFinding {
			http.Error(w, "entry is not a resolvable finding (no severity)", http.StatusBadRequest)
			return
		}

		slog.Info("findingsapi: resolved finding", "id", id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "resolved"})
	})
}

// resolve marks the timeline entry with the given id as resolved. found is
// false when no entry with that id exists; notAFinding is true when the
// entry exists but has no severity (e.g. an approval or manual_run entry,
// which are resolved through their own endpoints instead).
func (s *Service) resolve(id string) (found, notAFinding bool, err error) {
	d := s.mem.Domain("timeline")
	var entries []status.TimelineEntry
	// An error here just means no timeline snapshot has ever been written
	// (e.g. report_findings hasn't run yet) — treat that the same as an
	// empty timeline rather than a failure, mirroring report_findings.go.
	_ = d.ReadCurrent(&entries)
	for i := range entries {
		if entries[i].ID != id {
			continue
		}
		if entries[i].Severity == "" {
			return true, true, nil
		}
		resolved := "resolved"
		entries[i].Status = &resolved
		return true, false, d.Write(entries)
	}
	return false, false, nil
}
