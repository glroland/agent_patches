package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"agent_patches/endpoint-server/tasks/patch/patching"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// UpdateChecker is satisfied by *patching.Patcher; injected in tests.
type UpdateChecker interface {
	UpdatesAvailable(ctx context.Context) (bool, string, error)
	OS() patching.OSType
}

// Scheduler runs background maintenance tasks once per day at a configured
// wall-clock time. The goroutine always starts; each task is individually
// gated by config. To add a new recurring task, implement it as a method
// and call it from run.
type Scheduler struct {
	cfg          *config.DailyTasksSettings
	notifier     *notifier.Notifier
	NewPatcher   func() (UpdateChecker, error)
	NextWakeFunc func() (time.Duration, error) // overridable for tests
}

// New creates a Scheduler.
func New(cfg *config.DailyTasksSettings, n *notifier.Notifier) *Scheduler {
	s := &Scheduler{
		cfg:        cfg,
		notifier:   n,
		NewPatcher: defaultPatcherFactory,
	}
	s.NextWakeFunc = func() (time.Duration, error) {
		return NextWake(cfg.WakeTime)
	}
	return s
}

// Start launches the background loop in a new goroutine and returns
// immediately. All enabled tasks run once at startup, then again each day at
// the configured wake_time. The loop exits when ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("daily_tasks: starting",
		"wake_time", s.cfg.WakeTime,
		"patch_check", s.cfg.PatchCheck.Enabled,
	)
	go s.loop(ctx)
}

func (s *Scheduler) loop(ctx context.Context) {
	s.run(ctx)

	for {
		d, err := s.NextWakeFunc()
		if err != nil {
			slog.Error("daily_tasks: invalid wake_time, loop exiting", "error", err)
			return
		}
		slog.Debug("daily_tasks: sleeping until next wake", "in", d.Round(time.Second))

		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Info("daily_tasks: stopped")
			return
		case <-timer.C:
			s.run(ctx)
		}
	}
}

// run calls each scheduled task in sequence.
// Add new task calls here as they are introduced.
func (s *Scheduler) run(ctx context.Context) {
	slog.Debug("daily_tasks: running tasks")
	s.CheckPatches(ctx)
}

// CheckPatches queries the OS package manager for pending updates and notifies
// when any are found. Skipped entirely when PatchCheck.Enabled is false.
func (s *Scheduler) CheckPatches(ctx context.Context) {
	if !s.cfg.PatchCheck.Enabled {
		slog.Debug("daily_tasks: patch check disabled")
		return
	}

	slog.Info("daily_tasks: checking for available patches")

	p, err := s.NewPatcher()
	if err != nil {
		slog.Warn("daily_tasks: patch check: patcher init failed", "error", err)
		return
	}

	available, details, err := p.UpdatesAvailable(ctx)
	if err != nil {
		slog.Warn("daily_tasks: patch check failed", "error", err, "output", details)
		return
	}

	if !available {
		slog.Info("daily_tasks: patch check complete, no updates available")
		return
	}

	host, _ := os.Hostname()
	slog.Info("daily_tasks: updates available, sending notification", "host", host, "os", p.OS())

	s.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Updates Available", host),
		fmt.Sprintf("System updates are available on host %q (OS: %s).\n\nDetails:\n%s",
			host, p.OS(), details),
	)
}

// NextWake returns the duration until the next occurrence of wakeTime (HH:MM)
// in the local timezone. If wakeTime is empty it defaults to "00:00".
// The returned duration is always positive: if the target time has already
// passed today, the next occurrence is tomorrow.
func NextWake(wakeTime string) (time.Duration, error) {
	if wakeTime == "" {
		wakeTime = "00:00"
	}
	parsed, err := time.Parse("15:04", wakeTime)
	if err != nil {
		return 0, fmt.Errorf("daily_tasks: invalid wake_time %q: must be HH:MM (got: %w)", wakeTime, err)
	}

	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0, now.Location())

	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return time.Until(next), nil
}

// defaultPatcherFactory is the production factory; tests substitute their own.
func defaultPatcherFactory() (UpdateChecker, error) {
	return patching.New()
}
