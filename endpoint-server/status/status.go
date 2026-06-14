package status

import (
	"encoding/json"
	"net/http"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/capture_system_info"
)

// currentTasker reports the name of the responsibility currently in flight,
// or "" if none is running. Satisfied by *loop.Loop.
type currentTasker interface {
	CurrentTask() string
}

// Service builds GET /status responses from cached system info,
// memory-backed timeline, and the loop's running state.
type Service struct {
	info capture_system_info.Info
	mem  *memory.Store
	loop currentTasker
}

// New creates a status Service. info is captured once at startup via
// capture_system_info.Gather.
func New(info capture_system_info.Info, mem *memory.Store, l currentTasker) *Service {
	return &Service{info: info, mem: mem, loop: l}
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

	var currentTask *string
	state := "idle"
	if task := s.loop.CurrentTask(); task != "" {
		currentTask = &task
		state = "active"
	} else if hasAttention(timeline) {
		state = "attention"
	}

	return Response{
		Agent: AgentInfo{
			Hostname: s.info.Hostname,
			Platform: s.info.OS,
			OS:       osLabel(s.info),
		},
		Status: StatusBlock{
			State:       state,
			LastPoll:    time.Now().Format(time.RFC3339),
			CurrentTask: currentTask,
		},
		Timeline: timeline,
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
// pending approval request.
func hasAttention(timeline []TimelineEntry) bool {
	for _, e := range timeline {
		if e.Severity == "critical" {
			return true
		}
		if e.Type == "approval" && e.Status != nil && *e.Status == "pending" {
			return true
		}
	}
	return false
}
