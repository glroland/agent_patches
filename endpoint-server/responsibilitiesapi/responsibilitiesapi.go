// Package responsibilitiesapi exposes GET /responsibilities, returning the
// configured responsibility list augmented with live scheduling state and the
// last-run outcome persisted in agent memory.
package responsibilitiesapi

import (
	"encoding/json"
	"net/http"
	"time"

	"agent_patches/endpoint-server/loop"
	"agent_patches/endpoint-server/memory"
)

// Service serves the /responsibilities endpoint.
type Service struct {
	lp  *loop.Loop
	mem *memory.Store
}

// New creates a Service.
func New(lp *loop.Loop, mem *memory.Store) *Service {
	return &Service{lp: lp, mem: mem}
}

// Handler returns an http.Handler for GET /responsibilities.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.handle)
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	attrs, _ := s.mem.Attrs().All()

	items := make([]ResponsibilityItem, 0, len(s.lp.Responsibilities()))
	for _, resp := range s.lp.Responsibilities() {
		items = append(items, buildItem(resp, attrs))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// ResponsibilityItem is one entry in the GET /responsibilities response.
type ResponsibilityItem struct {
	Name        string   `json:"name"`
	Schedule    string   `json:"schedule"`
	Tools       []string `json:"tools"`
	Instruction string   `json:"instruction"`
	LastRunAt   *string  `json:"lastRunAt,omitempty"`
	NextRunAt   *string  `json:"nextRunAt,omitempty"`
	Status      string   `json:"status"` // "never", "running", "ok", "error"
	Summary     string   `json:"summary,omitempty"`
}

func buildItem(r *loop.Responsibility, attrs map[string]json.RawMessage) ResponsibilityItem {
	cfg := r.Config()
	item := ResponsibilityItem{
		Name:        r.Name(),
		Schedule:    r.ScheduleLabel(),
		Tools:       cfg.Tools,
		Instruction: cfg.Instruction,
		Status:      "never",
	}

	// Overlay last-run state from persisted attrs.
	if raw, ok := attrs[loop.AttrRunPrefix+r.Name()]; ok {
		var state loop.RunState
		if err := json.Unmarshal(raw, &state); err == nil {
			item.LastRunAt = &state.LastRunAt
			item.Status = state.Status
			item.Summary = state.Summary
		}
	}

	// Running state is ephemeral (in-memory only) and overrides persisted status.
	if r.Running.Load() {
		item.Status = "running"
	}

	if next := r.NextRunAt(); next != nil {
		s := next.UTC().Format(time.RFC3339)
		item.NextRunAt = &s
	}

	return item
}
