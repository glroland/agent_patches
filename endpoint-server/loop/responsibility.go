package loop

import (
	"fmt"
	"sync/atomic"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

// timeOfDayFormat is the expected layout for ResponsibilitySettings.Time.
const timeOfDayFormat = "15:04"

// responsibility tracks the schedule and in-flight state of one configured
// ResponsibilitySettings entry.
type responsibility struct {
	cfg config.ResponsibilitySettings

	// running is set while an invocation is in flight, used to skip
	// overlapping runs.
	running atomic.Bool

	// freq is non-zero for frequency-based responsibilities.
	freq    time.Duration
	nextRun time.Time

	// timeOfDay is set ("HH:MM") for once-daily responsibilities.
	timeOfDay   string
	lastRunDate string
}

// newResponsibility validates cfg and builds its scheduling state.
func newResponsibility(cfg config.ResponsibilitySettings) (*responsibility, error) {
	if cfg.Frequency != "" && cfg.Time != "" {
		return nil, fmt.Errorf("responsibility %q: specify either frequency or time, not both", cfg.Name)
	}

	r := &responsibility{cfg: cfg}
	switch {
	case cfg.Frequency != "":
		d, err := time.ParseDuration(cfg.Frequency)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("responsibility %q: invalid frequency %q: %w", cfg.Name, cfg.Frequency, err)
		}
		r.freq = d
		r.nextRun = time.Now()
	case cfg.Time != "":
		if _, err := time.Parse(timeOfDayFormat, cfg.Time); err != nil {
			return nil, fmt.Errorf("responsibility %q: invalid time %q: %w", cfg.Name, cfg.Time, err)
		}
		r.timeOfDay = cfg.Time
	default:
		return nil, fmt.Errorf("responsibility %q: must specify either frequency or time", cfg.Name)
	}
	return r, nil
}

// due reports whether the responsibility should fire at now.
func (r *responsibility) due(now time.Time) bool {
	if r.freq > 0 {
		return !now.Before(r.nextRun)
	}
	return now.Format(timeOfDayFormat) == r.timeOfDay && r.lastRunDate != now.Format("2006-01-02")
}

// schedule advances the responsibility's schedule past now. It is called
// once per firing, regardless of whether the run was actually executed or
// skipped due to an overlapping in-flight run.
func (r *responsibility) schedule(now time.Time) {
	if r.freq > 0 {
		r.nextRun = now.Add(r.freq)
		return
	}
	r.lastRunDate = now.Format("2006-01-02")
}
