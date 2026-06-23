package status

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/capture_system_info"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// currentTasker reports which responsibilities are currently in flight.
// Satisfied by *loop.Loop.
type currentTasker interface {
	RunningTasks() []string
}

// Service builds GET /status responses from cached system info,
// memory-backed timeline, and the loop's running state.
type Service struct {
	info       capture_system_info.Info
	mem        *memory.Store
	loop       currentTasker
	summarizer *summarizer
	patcher    *patching.Patcher
}

// New creates a status Service. info is captured once at startup via
// capture_system_info.Gather.
func New(info capture_system_info.Info, mem *memory.Store, l currentTasker, cfg *config.Settings) *Service {
	p, err := patching.New()
	if err != nil {
		slog.Warn("status: OS detection failed — last-updated fallback disabled", "error", err)
	}
	return &Service{info: info, mem: mem, loop: l, summarizer: newSummarizer(cfg), patcher: p}
}

// Handler returns the http.HandlerFunc for GET /status.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.build())
	}
}

func (s *Service) build() Response {
	var timeline []TimelineEntry
	_ = s.mem.Domain("timeline").ReadCurrent(&timeline)
	if timeline == nil {
		timeline = []TimelineEntry{}
	}

	// Merge in the last known state of every check/analyze skill, so a
	// problem (e.g. a near-full or failing disk) is reflected here even if
	// the agent's tool-use loop never calls report_findings.
	if states, _ := skillstate.LoadAll(s.mem); len(states) > 0 {
		for _, st := range states {
			if st.Health == skillstate.HealthOK {
				continue
			}
			timeline = append([]TimelineEntry{{
				ID:       "skillstate:" + st.Skill,
				Time:     st.Time,
				Type:     "observation",
				Title:    fmt.Sprintf("%s: %s", st.Skill, st.Summary),
				Detail:   st.Summary,
				Severity: string(st.Health),
			}}, timeline...)
		}
	}

	runningTasks := s.loop.RunningTasks()
	var currentTask *string
	state := "idle"
	if len(runningTasks) > 0 {
		currentTask = &runningTasks[0]
		state = "active"
	} else if hasAttention(timeline) {
		state = "attention"
	}

	var lastPatchedAt *string
	var patchTime string
	if err := s.mem.Attrs().Get("last_patched_at", &patchTime); err == nil && patchTime != "" {
		// Application-managed patch record takes precedence.
		lastPatchedAt = &patchTime
	} else if s.patcher != nil {
		// Fall back to OS-native package database mtime so the dashboard shows
		// accurate dates even before this application has ever applied updates.
		if t, err := s.patcher.LastUpdated(context.Background()); err == nil {
			ts := t.UTC().Format(time.RFC3339)
			lastPatchedAt = &ts
		}
	}

	var statusDescription string
	if state == "attention" {
		statusDescription = s.summarizer.get(timeline)
	}

	var diskTrends json.RawMessage
	if err := s.mem.Attrs().Get("disk_trends", &diskTrends); err != nil || string(diskTrends) == "null" {
		diskTrends = nil
	}

	return Response{
		Agent: AgentInfo{
			Hostname: s.info.Hostname,
			Platform: s.info.OS,
			OS:       osLabel(s.info),
		},
		Status: StatusBlock{
			State:        state,
			LastPoll:     time.Now().Format(time.RFC3339),
			CurrentTask:  currentTask,
			CurrentTasks: runningTasks,
		},
		Timeline:          timeline,
		LastPatchedAt:     lastPatchedAt,
		StatusDescription: statusDescription,
		DiskTrends:        diskTrends,
	}
}

// osLabel combines distribution and version into a single human-readable OS
// string, falling back to the bare GOOS value when neither is set.
func osLabel(info capture_system_info.Info) string {
	switch {
	case info.Distribution != "" && info.Version != "":
		return info.Distribution + " " + info.Version
	case info.Distribution != "":
		return info.Distribution
	default:
		return info.OS
	}
}

// hasAttention reports whether the timeline contains a critical entry or a
// pending approval that carries meaningful risk (medium or high). Routine
// low-risk patch approvals do not count — available updates alone do not
// make a server unhealthy.
func hasAttention(timeline []TimelineEntry) bool {
	for _, e := range timeline {
		if e.Severity == "critical" {
			return true
		}
		if e.Type == "approval" && e.Status != nil && *e.Status == "pending" &&
			(e.Risk == "high" || e.Risk == "medium") {
			return true
		}
	}
	return false
}
