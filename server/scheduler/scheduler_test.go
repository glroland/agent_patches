package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"agent_patches/server/config"
	"agent_patches/server/notifier"
	"agent_patches/server/patching"
)

// stubChecker is a test double for updateChecker.
type stubChecker struct {
	available bool
	details   string
	err       error
	calls     atomic.Int32
}

func (s *stubChecker) UpdatesAvailable(_ context.Context) (bool, string, error) {
	s.calls.Add(1)
	return s.available, s.details, s.err
}
func (s *stubChecker) OS() patching.OSType { return patching.OSDebian }

func newTestScheduler(cfg *config.DailyTasksSettings, checker *stubChecker) *Scheduler {
	s := New(cfg, notifier.New(&config.NotifierSettings{}))
	s.newPatcher = func() (updateChecker, error) { return checker, nil }
	return s
}

// ---- nextWake ---------------------------------------------------------------

func TestNextWake_ValidTimes(t *testing.T) {
	for _, wt := range []string{"00:00", "06:30", "12:00", "23:59"} {
		d, err := nextWake(wt)
		if err != nil {
			t.Fatalf("nextWake(%q) error: %v", wt, err)
		}
		if d <= 0 || d > 24*time.Hour {
			t.Errorf("nextWake(%q) = %v, want in (0, 24h]", wt, d)
		}
	}
}

func TestNextWake_EmptyDefaultsMidnight(t *testing.T) {
	d, err := nextWake("")
	if err != nil {
		t.Fatalf("nextWake(\"\") error: %v", err)
	}
	if d <= 0 || d > 24*time.Hour {
		t.Errorf("nextWake(\"\") = %v, want in (0, 24h]", d)
	}
}

func TestNextWake_InvalidFormat(t *testing.T) {
	for _, bad := range []string{"25:00", "12:60", "noon", "1200"} {
		if _, err := nextWake(bad); err == nil {
			t.Errorf("nextWake(%q) expected error, got nil", bad)
		}
	}
}

func TestNextWake_AlwaysFuture(t *testing.T) {
	// Whatever the current time is, nextWake must return a positive duration.
	for _, wt := range []string{"00:00", "12:00", "23:59"} {
		d, err := nextWake(wt)
		if err != nil {
			t.Fatalf("nextWake(%q) error: %v", wt, err)
		}
		if d <= 0 {
			t.Errorf("nextWake(%q) = %v, must be positive", wt, d)
		}
	}
}

// ---- patch check disabled ---------------------------------------------------

func TestCheckPatches_Disabled_SkipsChecker(t *testing.T) {
	checker := &stubChecker{available: true}
	cfg := &config.DailyTasksSettings{
		WakeTime:   "00:00",
		PatchCheck: config.PatchCheckSettings{Enabled: false},
	}
	s := newTestScheduler(cfg, checker)
	s.checkPatches(context.Background())

	if checker.calls.Load() != 0 {
		t.Errorf("checker called %d times, want 0 when disabled", checker.calls.Load())
	}
}

// ---- patch check enabled, no updates ----------------------------------------

func TestCheckPatches_Enabled_NoUpdates(t *testing.T) {
	checker := &stubChecker{available: false}
	cfg := &config.DailyTasksSettings{
		WakeTime:   "00:00",
		PatchCheck: config.PatchCheckSettings{Enabled: true},
	}
	s := newTestScheduler(cfg, checker)
	s.checkPatches(context.Background())

	if checker.calls.Load() != 1 {
		t.Errorf("checker called %d times, want 1", checker.calls.Load())
	}
}

// ---- patch check enabled, updates available ---------------------------------

func TestCheckPatches_Enabled_UpdatesAvailable(t *testing.T) {
	checker := &stubChecker{available: true, details: "curl 8.0-1"}
	cfg := &config.DailyTasksSettings{
		WakeTime:   "00:00",
		PatchCheck: config.PatchCheckSettings{Enabled: true},
	}
	s := newTestScheduler(cfg, checker)
	s.checkPatches(context.Background())

	if checker.calls.Load() != 1 {
		t.Errorf("checker called %d times, want 1", checker.calls.Load())
	}
}

// ---- goroutine lifecycle ----------------------------------------------------

func TestScheduler_StartsAndStops(t *testing.T) {
	checker := &stubChecker{available: false}
	cfg := &config.DailyTasksSettings{
		WakeTime:   "00:00",
		PatchCheck: config.PatchCheckSettings{Enabled: true},
	}
	s := newTestScheduler(cfg, checker)
	// Replace the real wall-clock schedule with a 50 ms tick so the test
	// can observe multiple fires without waiting until midnight.
	s.nextWakeFunc = func() (time.Duration, error) { return 50 * time.Millisecond, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	s.Start(ctx)
	<-ctx.Done()

	// Startup run + at least one 50 ms tick within 200 ms window = ≥2 calls.
	if checker.calls.Load() < 2 {
		t.Errorf("checker called %d times, want ≥2 (startup + at least one tick)", checker.calls.Load())
	}
}

func TestScheduler_StopsOnContextCancel(t *testing.T) {
	checker := &stubChecker{available: false}
	cfg := &config.DailyTasksSettings{
		WakeTime:   "00:00",
		PatchCheck: config.PatchCheckSettings{Enabled: true},
	}
	s := newTestScheduler(cfg, checker)
	// Long next-wake so no tick fires during the test.
	s.nextWakeFunc = func() (time.Duration, error) { return 10 * time.Second, nil }

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)

	// Allow the startup run to complete.
	time.Sleep(20 * time.Millisecond)
	beforeCancel := checker.calls.Load()

	cancel()
	time.Sleep(20 * time.Millisecond)

	if checker.calls.Load() != beforeCancel {
		t.Errorf("checker ran after context cancel: calls went from %d to %d",
			beforeCancel, checker.calls.Load())
	}
}
