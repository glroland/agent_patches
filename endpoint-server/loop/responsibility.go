package loop

import (
	"fmt"
	"sync/atomic"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

// timeOfDayFormat is the expected layout for ResponsibilitySettings.Time.
const timeOfDayFormat = "15:04"

// Responsibility tracks the schedule and in-flight state of one configured
// ResponsibilitySettings entry.
type Responsibility struct {
	cfg config.ResponsibilitySettings

	// Running is set while an invocation is in flight, used to skip
	// overlapping runs.
	Running atomic.Bool

	// Freq is non-zero for frequency-based responsibilities.
	Freq    time.Duration
	nextRun time.Time

	// TimeOfDay is set ("HH:MM") for once-daily responsibilities.
	TimeOfDay   string
	lastRunDate string
}

// NewResponsibility validates cfg and builds its scheduling state.
// startupDelay is added to the initial nextRun for frequency-based
// responsibilities so agents stagger their first LLM call after restart.
// Time-of-day responsibilities are unaffected (they already have fixed
// wall-clock scheduling).
func NewResponsibility(cfg config.ResponsibilitySettings, startupDelay time.Duration) (*Responsibility, error) {
	if cfg.Frequency != "" && cfg.Time != "" {
		return nil, fmt.Errorf("responsibility %q: specify either frequency or time, not both", cfg.Name)
	}

	r := &Responsibility{cfg: cfg}
	switch {
	case cfg.Frequency != "":
		d, err := time.ParseDuration(cfg.Frequency)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("responsibility %q: invalid frequency %q: %w", cfg.Name, cfg.Frequency, err)
		}
		r.Freq = d
		r.nextRun = time.Now().Add(startupDelay)
	case cfg.Time != "":
		if _, err := time.Parse(timeOfDayFormat, cfg.Time); err != nil {
			return nil, fmt.Errorf("responsibility %q: invalid time %q: %w", cfg.Name, cfg.Time, err)
		}
		r.TimeOfDay = cfg.Time
	default:
		return nil, fmt.Errorf("responsibility %q: must specify either frequency or time", cfg.Name)
	}
	return r, nil
}

// Name returns the responsibility's configured name.
func (r *Responsibility) Name() string { return r.cfg.Name }

// Due reports whether the responsibility should fire at now.
func (r *Responsibility) Due(now time.Time) bool {
	if r.Freq > 0 {
		return !now.Before(r.nextRun)
	}
	return now.Format(timeOfDayFormat) == r.TimeOfDay && r.lastRunDate != now.Format("2006-01-02")
}

// Schedule advances the responsibility's schedule past now. It is called
// once per firing, regardless of whether the run was actually executed or
// skipped due to an overlapping in-flight run.
func (r *Responsibility) Schedule(now time.Time) {
	if r.Freq > 0 {
		r.nextRun = now.Add(r.Freq)
		return
	}
	r.lastRunDate = now.Format("2006-01-02")
}

// NextRunAt returns the wall-clock time this responsibility is next scheduled
// to fire. For frequency-based responsibilities this reads the in-memory
// nextRun field. For time-of-day responsibilities it computes the next
// occurrence from now.
func (r *Responsibility) NextRunAt() *time.Time {
	if r.Freq > 0 {
		t := r.nextRun
		return &t
	}
	now := time.Now()
	candidate, err := time.ParseInLocation(
		"2006-01-02 "+timeOfDayFormat,
		now.Format("2006-01-02")+" "+r.TimeOfDay,
		now.Location(),
	)
	if err != nil {
		return nil
	}
	if candidate.After(now) {
		return &candidate
	}
	next := candidate.Add(24 * time.Hour)
	return &next
}

// ScheduleLabel returns a human-readable schedule description.
func (r *Responsibility) ScheduleLabel() string {
	if r.Freq > 0 {
		return "every " + r.cfg.Frequency
	}
	return "daily at " + r.TimeOfDay
}

// Config returns the underlying responsibility configuration.
func (r *Responsibility) Config() config.ResponsibilitySettings { return r.cfg }
