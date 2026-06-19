// Package memoryapi implements the GET /memory endpoint, which exposes the
// same file-backed memory store used by the read_agent_memory tool — the
// most recent snapshot of every domain plus all attrs — for display in
// central-ui.
package memoryapi

import (
	"encoding/json"
	"net/http"

	"agent_patches/endpoint-server/memory"
)

// Service builds GET /memory responses from the agent's memory store.
type Service struct {
	mem *memory.Store
}

// New creates a memory Service.
func New(mem *memory.Store) *Service {
	return &Service{mem: mem}
}

// Handler returns the http.HandlerFunc for /memory.
// GET returns a dump of all memory domains and attrs.
// DELETE clears all memory and returns {"cleared":true}.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			dump, err := s.mem.Dump()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(dump)
		case http.MethodDelete:
			if err := s.mem.Clear(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"cleared": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
