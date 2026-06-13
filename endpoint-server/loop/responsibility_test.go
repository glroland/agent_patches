package loop

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

func TestNewResponsibility_Frequency(t *testing.T) {
	r, err := newResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.freq != time.Hour {
		t.Errorf("freq = %v, want 1h", r.freq)
	}
	if r.timeOfDay != "" {
		t.Errorf("timeOfDay = %q, want empty", r.timeOfDay)
	}
}

func TestNewResponsibility_Time(t *testing.T) {
	r, err := newResponsibility(config.ResponsibilitySettings{Name: "daily", Time: "07:00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.timeOfDay != "07:00" {
		t.Errorf("timeOfDay = %q, want 07:00", r.timeOfDay)
	}
	if r.freq != 0 {
		t.Errorf("freq = %v, want 0", r.freq)
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
		if _, err := newResponsibility(c); err == nil {
			t.Errorf("%s: expected error, got nil", c.Name)
		}
	}
}

func TestResponsibility_DueFrequency(t *testing.T) {
	r, err := newResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	if !r.due(now) {
		t.Error("expected due immediately on creation")
	}

	r.schedule(now)
	if r.due(now) {
		t.Error("expected not due immediately after scheduling")
	}
	if !r.due(now.Add(time.Hour)) {
		t.Error("expected due one period later")
	}
}

func TestResponsibility_DueTime(t *testing.T) {
	r, err := newResponsibility(config.ResponsibilitySettings{Name: "daily", Time: "07:00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	day := time.Date(2026, 6, 13, 7, 0, 0, 0, time.Local)
	if !r.due(day) {
		t.Error("expected due at configured time")
	}
	if r.due(day.Add(time.Minute)) {
		t.Error("expected not due a minute later")
	}

	r.schedule(day)
	if r.due(day) {
		t.Error("expected not due again the same day after scheduling")
	}
	if !r.due(day.AddDate(0, 0, 1)) {
		t.Error("expected due again the next day")
	}
}

func TestResponsibility_RunningGate(t *testing.T) {
	r, err := newResponsibility(config.ResponsibilitySettings{Name: "freq", Frequency: "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !r.running.CompareAndSwap(false, true) {
		t.Fatal("expected to acquire running flag")
	}
	if r.running.CompareAndSwap(false, true) {
		t.Error("expected second acquire to fail while already running")
	}
	r.running.Store(false)
	if !r.running.CompareAndSwap(false, true) {
		t.Error("expected to re-acquire after release")
	}
}
