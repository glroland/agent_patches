// Package incidents implements the agent's incident ledger: a durable record
// of ongoing problems that spans responsibility runs. Each responsibility run
// is a fresh LLM conversation, so without the ledger the agent rediscovers
// the same problem every cycle and files duplicate findings. The ledger gives
// each problem a stable fingerprint with first-seen/last-seen times, an
// occurrence count, and a log of actions taken, and lets the agent close the
// loop by resolving incidents that have cleared.
//
// Incidents are stored in the "Incidents" memory domain so they survive
// restarts and appear in the /memory dump under their own section. All
// mutations go through Store, whose mutex serialises the read-modify-write
// cycle across concurrently running responsibilities.
package incidents

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agent_patches/endpoint-server/memory"
)

const (
	// incidentsDomain is the memory.Domain name holding the full incident
	// list, so it surfaces as its own "Incidents" section rather than in
	// the flat attrs bucket.
	incidentsDomain = "Incidents"

	// maxIncidents caps the ledger size; the oldest resolved incidents are
	// dropped first when the cap is exceeded.
	maxIncidents = 100

	// resolvedRetention is how long resolved incidents are kept before being
	// pruned on the next write.
	resolvedRetention = 30 * 24 * time.Hour
)

// Action is one remediation step or notable update logged against an incident.
type Action struct {
	Time string `json:"time"`
	Note string `json:"note"`
}

// Incident is one entry in the ledger.
type Incident struct {
	Fingerprint string   `json:"fingerprint"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Severity    string   `json:"severity"` // info | warning | critical
	Status      string   `json:"status"`   // open | resolved
	FirstSeen   string   `json:"firstSeen"`
	LastSeen    string   `json:"lastSeen"`
	TimesSeen   int      `json:"timesSeen"`
	Actions     []Action `json:"actions,omitempty"`
	ResolvedAt  string   `json:"resolvedAt,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
}

// Store provides serialised access to the incident ledger.
type Store struct {
	mem *memory.Store
	mu  sync.Mutex
}

// New returns a Store backed by mem. A nil mem yields a Store whose methods
// are safe no-ops (reads return empty).
func New(mem *memory.Store) *Store {
	return &Store{mem: mem}
}

// All returns every incident in the ledger, open first, then newest LastSeen first.
func (s *Store) All() ([]Incident, error) {
	if s == nil || s.mem == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Open returns only the open incidents, newest LastSeen first.
func (s *Store) Open() ([]Incident, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var open []Incident
	for _, in := range all {
		if in.Status == "open" {
			open = append(open, in)
		}
	}
	return open, nil
}

// Report upserts an incident by fingerprint. A new fingerprint opens a fresh
// incident; an existing one has its LastSeen touched and TimesSeen bumped
// (and is reopened if it had been resolved). Non-empty title/detail/severity
// refresh the stored values. Returns the stored incident and whether it was new.
func (s *Store) Report(fingerprint, title, detail, severity string) (Incident, bool, error) {
	if s == nil || s.mem == nil {
		return Incident{}, false, fmt.Errorf("incidents: no backing store")
	}
	fingerprint = normalizeFingerprint(fingerprint)
	if fingerprint == "" {
		return Incident{}, false, fmt.Errorf("incidents: fingerprint must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return Incident{}, false, err
	}

	now := time.Now().Format(time.RFC3339)
	for i := range list {
		if list[i].Fingerprint != fingerprint {
			continue
		}
		reopened := list[i].Status == "resolved"
		list[i].Status = "open"
		list[i].LastSeen = now
		list[i].TimesSeen++
		if reopened {
			list[i].ResolvedAt = ""
			list[i].Resolution = ""
			list[i].Actions = append(list[i].Actions, Action{Time: now, Note: "recurred after being resolved — reopened"})
		}
		if title != "" {
			list[i].Title = title
		}
		if detail != "" {
			list[i].Detail = detail
		}
		if severity != "" {
			list[i].Severity = severity
		}
		if err := s.save(list); err != nil {
			return Incident{}, false, err
		}
		return list[i], false, nil
	}

	in := Incident{
		Fingerprint: fingerprint,
		Title:       title,
		Detail:      detail,
		Severity:    severity,
		Status:      "open",
		FirstSeen:   now,
		LastSeen:    now,
		TimesSeen:   1,
	}
	list = append(list, in)
	if err := s.save(list); err != nil {
		return Incident{}, false, err
	}
	return in, true, nil
}

// LogAction appends a note (an action taken or a notable update) to an open
// incident and touches its LastSeen.
func (s *Store) LogAction(fingerprint, note string) (Incident, error) {
	if s == nil || s.mem == nil {
		return Incident{}, fmt.Errorf("incidents: no backing store")
	}
	fingerprint = normalizeFingerprint(fingerprint)

	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return Incident{}, err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range list {
		if list[i].Fingerprint != fingerprint {
			continue
		}
		list[i].Actions = append(list[i].Actions, Action{Time: now, Note: note})
		list[i].LastSeen = now
		if err := s.save(list); err != nil {
			return Incident{}, err
		}
		return list[i], nil
	}
	return Incident{}, fmt.Errorf("incidents: no incident with fingerprint %q", fingerprint)
}

// Resolve marks an incident resolved with the given resolution note.
func (s *Store) Resolve(fingerprint, resolution string) (Incident, error) {
	if s == nil || s.mem == nil {
		return Incident{}, fmt.Errorf("incidents: no backing store")
	}
	fingerprint = normalizeFingerprint(fingerprint)

	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load()
	if err != nil {
		return Incident{}, err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range list {
		if list[i].Fingerprint != fingerprint {
			continue
		}
		if list[i].Status == "resolved" {
			return list[i], nil
		}
		list[i].Status = "resolved"
		list[i].ResolvedAt = now
		list[i].Resolution = resolution
		if err := s.save(list); err != nil {
			return Incident{}, err
		}
		return list[i], nil
	}
	return Incident{}, fmt.Errorf("incidents: no incident with fingerprint %q", fingerprint)
}

// OpenSummary renders the open incidents as a compact text block for
// injection into a responsibility prompt. Returns "" when there are none.
func (s *Store) OpenSummary() string {
	open, err := s.Open()
	if err != nil || len(open) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, in := range open {
		fmt.Fprintf(&sb, "- [%s] %s (severity: %s; first seen %s; last seen %s; seen %d times)",
			in.Fingerprint, in.Title, in.Severity, in.FirstSeen, in.LastSeen, in.TimesSeen)
		if n := len(in.Actions); n > 0 {
			fmt.Fprintf(&sb, " — last action: %s", in.Actions[n-1].Note)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// load reads the ledger from attrs, sorted open-first then newest LastSeen
// first. Caller must hold s.mu.
func (s *Store) load() ([]Incident, error) {
	var list []Incident
	if err := s.mem.Domain(incidentsDomain).ReadCurrent(&list); err != nil {
		// No snapshot existing yet is the normal empty state.
		return nil, nil //nolint:nilerr
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Status != list[j].Status {
			return list[i].Status == "open"
		}
		return list[i].LastSeen > list[j].LastSeen
	})
	return list, nil
}

// save prunes and persists the ledger. Caller must hold s.mu.
func (s *Store) save(list []Incident) error {
	cutoff := time.Now().Add(-resolvedRetention).Format(time.RFC3339)
	kept := list[:0]
	for _, in := range list {
		if in.Status == "resolved" && in.ResolvedAt != "" && in.ResolvedAt < cutoff {
			continue
		}
		kept = append(kept, in)
	}

	// Enforce the cap by dropping resolved incidents oldest-first, then — only
	// if still over — the oldest open ones.
	if len(kept) > maxIncidents {
		sort.SliceStable(kept, func(i, j int) bool {
			if kept[i].Status != kept[j].Status {
				return kept[i].Status == "open"
			}
			return kept[i].LastSeen > kept[j].LastSeen
		})
		kept = kept[:maxIncidents]
	}

	return s.mem.Domain(incidentsDomain).Write(kept)
}

// normalizeFingerprint lower-cases and kebab-cases a fingerprint so the same
// problem reported with minor formatting differences still dedupes.
func normalizeFingerprint(fp string) string {
	fp = strings.ToLower(strings.TrimSpace(fp))
	fp = strings.Join(strings.Fields(fp), "-")
	return fp
}
