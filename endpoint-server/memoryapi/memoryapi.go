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

// Handler returns the http.HandlerFunc for GET /memory.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dump, err := s.mem.Dump()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dump)
	}
}
