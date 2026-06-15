// Package skillstate lets check_* and analyze_* skills record their last
// known health state, so it can be surfaced via report_findings and the
// GET /status endpoint even if the agent's tool-use loop never explicitly
// reports a problem.
package skillstate

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
)

// Health describes the severity of a skill's last known state. The values
// double as status.TimelineEntry severities ("warning"/"critical").
type Health string

const (
	HealthOK       Health = "ok"
	HealthWarning  Health = "warning"
	HealthCritical Health = "critical"
)

// keyPrefix namespaces skill-state entries within the shared attrs store.
const keyPrefix = "skill_state:"

// State is the last known state recorded by a check/analyze skill.
type State struct {
	Skill   string `json:"skill"`
	Health  Health `json:"health"`
	Summary string `json:"summary"`
	Time    string `json:"time"`
}

// Save persists the skill's last known state, keyed by skill name. A nil
// *memory.Store is safe to call (no-op).
func Save(mem *memory.Store, skill string, health Health, summary string) error {
	if mem == nil {
		return nil
	}
	return mem.Attrs().Set(keyPrefix+skill, State{
		Skill:   skill,
		Health:  health,
		Summary: summary,
		Time:    time.Now().Format(time.RFC3339),
	})
}

// LoadAll returns the last known state for every skill that has recorded
// one, sorted by skill name. A nil *memory.Store returns nil, nil.
func LoadAll(mem *memory.Store) ([]State, error) {
	if mem == nil {
		return nil, nil
	}

	attrs, err := mem.Attrs().All()
	if err != nil {
		return nil, err
	}

	var states []State
	for k, raw := range attrs {
		if !strings.HasPrefix(k, keyPrefix) {
			continue
		}
		var s State
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		states = append(states, s)
	}

	sort.Slice(states, func(i, j int) bool { return states[i].Skill < states[j].Skill })
	return states, nil
}
