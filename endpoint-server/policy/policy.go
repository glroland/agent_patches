// Package policy implements standing approval policies: operator-created
// rules that pre-approve narrow classes of state-changing commands so the
// agent can execute them without a fresh HITL round-trip every time.
//
// Policies are created and deleted ONLY by the operator (via the /policies
// HTTP API) — the agent can never grant itself one. The agent's side of the
// loop is advisory: every operator approval of a command is counted, and once
// the same command has been approved promotionThreshold times the tool result
// suggests promoting it to a standing policy.
//
// A policy's pattern is a Go regular expression matched against the ENTIRE
// proposed command (implicitly anchored with \A…\z), so "rm -f /var/log/.*\.gz"
// cannot be satisfied by a command that merely contains that text.
package policy

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"agent_patches/endpoint-server/memory"
)

const (
	// attrsKey is the AttrsStore key holding the policy list.
	attrsKey = "approval_policies"

	// historyKey is the AttrsStore key holding per-command approval counts.
	historyKey = "approval_history"

	// PromotionThreshold is how many operator approvals of the same command
	// trigger a suggestion to create a standing policy.
	PromotionThreshold = 3

	// maxHistoryEntries caps the approval-count map so it cannot grow without
	// bound; the smallest counts are evicted first.
	maxHistoryEntries = 200
)

// Policy is one operator-created standing approval rule.
type Policy struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Pattern     string `json:"pattern"`
	Risk        string `json:"risk"` // advisory: low | medium | high
	CreatedAt   string `json:"createdAt"`
	Enabled     bool   `json:"enabled"`
}

// Store provides serialised access to the policy list and approval history.
type Store struct {
	mem *memory.Store
	mu  sync.Mutex
}

// New returns a Store backed by mem. A nil mem yields a Store whose Match
// never matches and whose mutations fail.
func New(mem *memory.Store) *Store {
	return &Store{mem: mem}
}

// All returns every policy.
func (s *Store) All() ([]Policy, error) {
	if s == nil || s.mem == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Add validates and persists a new enabled policy, returning it.
func (s *Store) Add(description, pattern, risk string) (Policy, error) {
	if s == nil || s.mem == nil {
		return Policy{}, fmt.Errorf("policy: no backing store")
	}
	if strings.TrimSpace(pattern) == "" {
		return Policy{}, fmt.Errorf("policy: pattern must not be empty")
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return Policy{}, fmt.Errorf("policy: invalid pattern: %w", err)
	}
	if strings.TrimSpace(description) == "" {
		return Policy{}, fmt.Errorf("policy: description must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return Policy{}, err
	}
	p := Policy{
		ID:          fmt.Sprintf("policy-%d", time.Now().UnixNano()),
		Description: strings.TrimSpace(description),
		Pattern:     pattern,
		Risk:        strings.ToLower(strings.TrimSpace(risk)),
		CreatedAt:   time.Now().Format(time.RFC3339),
		Enabled:     true,
	}
	list = append(list, p)
	if err := s.mem.Attrs().Set(attrsKey, list); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Delete removes the policy with the given ID. Returns an error if not found.
func (s *Store) Delete(id string) error {
	if s == nil || s.mem == nil {
		return fmt.Errorf("policy: no backing store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return err
	}
	kept := list[:0]
	found := false
	for _, p := range list {
		if p.ID == id {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return fmt.Errorf("policy: no policy with id %q", id)
	}
	return s.mem.Attrs().Set(attrsKey, kept)
}

// Match returns the first enabled policy whose pattern matches the entire
// command, or nil when none matches. Patterns with a compile error are skipped.
func (s *Store) Match(command string) *Policy {
	if s == nil || s.mem == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return nil
	}
	cmd := NormalizeCommand(command)
	for i, p := range list {
		if !p.Enabled {
			continue
		}
		re, err := regexp.Compile(`\A(?:` + p.Pattern + `)\z`)
		if err != nil {
			continue
		}
		if re.MatchString(cmd) {
			return &list[i]
		}
	}
	return nil
}

// RecordApproval increments and returns the count of operator approvals for
// the (whitespace-normalised) command. The count is what drives the
// promote-to-standing-policy suggestion.
func (s *Store) RecordApproval(command string) (int, error) {
	if s == nil || s.mem == nil {
		return 0, fmt.Errorf("policy: no backing store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	counts := make(map[string]int)
	_ = s.mem.Attrs().Get(historyKey, &counts)

	key := NormalizeCommand(command)
	counts[key]++
	n := counts[key]

	// Evict smallest counts when over cap, never the key just updated.
	for len(counts) > maxHistoryEntries {
		minKey, minVal := "", int(^uint(0)>>1)
		for k, v := range counts {
			if k != key && v < minVal {
				minKey, minVal = k, v
			}
		}
		if minKey == "" {
			break
		}
		delete(counts, minKey)
	}

	if err := s.mem.Attrs().Set(historyKey, counts); err != nil {
		return n, err
	}
	return n, nil
}

// load reads the policy list from attrs. A missing key (or missing attrs.json)
// is the normal empty state. Caller must hold s.mu.
func (s *Store) load() ([]Policy, error) {
	var list []Policy
	if err := s.mem.Attrs().Get(attrsKey, &list); err != nil {
		return nil, nil //nolint:nilerr
	}
	return list, nil
}

// NormalizeCommand collapses whitespace so trivially different spellings of
// the same command match the same policy and share one approval count.
func NormalizeCommand(cmd string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(cmd)), " ")
}
