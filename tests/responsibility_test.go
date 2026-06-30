package tests

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/loop"
	"agent_patches/endpoint-server/utils/config"
)

func TestNewResponsibility_Frequency(t *testing.T) {
	r, err := loop.NewResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Freq != time.Hour {
		t.Errorf("Freq = %v, want 1h", r.Freq)
	}
	if r.TimeOfDay != "" {
		t.Errorf("TimeOfDay = %q, want empty", r.TimeOfDay)
	}
}

func TestNewResponsibility_Time(t *testing.T) {
	r, err := loop.NewResponsibility(config.ResponsibilitySettings{Name: "daily", Time: "07:00"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeOfDay != "07:00" {
		t.Errorf("TimeOfDay = %q, want 07:00", r.TimeOfDay)
	}
	if r.Freq != 0 {
		t.Errorf("Freq = %v, want 0", r.Freq)
	}
}

func TestNewResponsibility_Errors(t *testing.T) {
	cases := []config.ResponsibilitySettings{
		{Name: "both", Frequency: "1h", Time: "07:00"},
		{Name: "neither"},
		{Name: "bad-freq", Frequency: "not-a-duration"},
		{Name: "zero-freq", Frequency: "0s"},
		{Name: "bad-time", Time: "25:99"},
	}
	for _, c := range cases {
		if _, err := loop.NewResponsibility(c, 0); err == nil {
			t.Errorf("%s: expected error, got nil", c.Name)
		}
	}
}

func TestResponsibility_DueFrequency(t *testing.T) {
	r, err := loop.NewResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	if !r.Due(now) {
		t.Error("expected due immediately on creation")
	}

	r.Schedule(now)
	if r.Due(now) {
		t.Error("expected not due immediately after scheduling")
	}
	if !r.Due(now.Add(time.Hour)) {
		t.Error("expected due one period later")
	}
}

func TestResponsibility_DueTime(t *testing.T) {
	r, err := loop.NewResponsibility(config.ResponsibilitySettings{Name: "daily", Time: "07:00"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	day := time.Date(2026, 6, 13, 7, 0, 0, 0, time.Local)
	if !r.Due(day) {
		t.Error("expected due at configured time")
	}
	if r.Due(day.Add(time.Minute)) {
		t.Error("expected not due a minute later")
	}

	r.Schedule(day)
	if r.Due(day) {
		t.Error("expected not due again the same day after scheduling")
	}
	if !r.Due(day.AddDate(0, 0, 1)) {
		t.Error("expected due again the next day")
	}
}

func TestLoop_CurrentTask(t *testing.T) {
	cfg := &config.Settings{
		Responsibilities: []config.ResponsibilitySettings{
			{Name: "disk-space-check", Frequency: "1h"},
			{Name: "daily-summary", Time: "07:00"},
		},
	}
	l := loop.New(cfg, nil, nil, nil)

	if got := l.CurrentTask(); got != "" {
		t.Errorf("CurrentTask() = %q, want empty when nothing running", got)
	}
}

func TestResponsibility_RunningGate(t *testing.T) {
	r, err := loop.NewResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !r.Running.CompareAndSwap(false, true) {
		t.Fatal("expected to acquire running flag")
	}
	if r.Running.CompareAndSwap(false, true) {
		t.Error("expected second acquire to fail while already running")
	}
	r.Running.Store(false)
	if !r.Running.CompareAndSwap(false, true) {
		t.Error("expected to re-acquire after release")
	}
}
