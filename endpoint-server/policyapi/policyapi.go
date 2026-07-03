// Package policyapi implements the operator-facing standing approval policy
// endpoints:
//
//	GET    /policies       — list all policies
//	POST   /policies       — create a policy {description, pattern, risk}
//	DELETE /policies/{id}  — remove a policy
//
// Policies pre-approve narrow classes of state-changing commands so
// run_approved_command can execute a matching command without a fresh HITL
// round-trip. Only these endpoints can create or remove policies — the agent
// itself has no tool for that, by design.
package policyapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"agent_patches/endpoint-server/policy"
)

// Service handles standing approval policy management requests.
type Service struct {
	policies *policy.Store
}

// New returns a Service backed by the given policy store.
func New(policies *policy.Store) *Service {
	return &Service{policies: policies}
}

// Handler returns an http.Handler covering /policies and /policies/{id}.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/policies"), "/")

		switch {
		case r.Method == http.MethodGet && id == "":
			s.list(w)
		case r.Method == http.MethodPost && id == "":
			s.create(w, r)
		case r.Method == http.MethodDelete && id != "":
			s.delete(w, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *Service) list(w http.ResponseWriter) {
	list, err := s.policies.All()
	if err != nil {
		slog.Error("policyapi: list failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []policy.Policy{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"policies": list})
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		Risk        string `json:"risk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	p, err := s.policies.Add(req.Description, req.Pattern, req.Risk)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("policyapi: policy created", "id", p.ID, "pattern", p.Pattern)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (s *Service) delete(w http.ResponseWriter, id string) {
	if err := s.policies.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Info("policyapi: policy deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}
