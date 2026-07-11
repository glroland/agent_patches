package loop

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

// TestNewResponsibility_TimeOfDayJitter verifies that the per-process startup
// jitter shifts a time-of-day responsibility's daily run, so a fleet of hosts
// configured with the same wall-clock time doesn't hit the LLM gateway in the
// same minute.
func TestNewResponsibility_TimeOfDayJitter(t *testing.T) {
	cases := []struct {
		name  string
		time  string
		delay time.Duration
		want  string
	}{
		{"no jitter", "03:00", 0, "03:00"},
		{"shifted", "03:00", 17 * time.Minute, "03:17"},
		{"midnight wrap", "23:50", 30 * time.Minute, "00:20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewResponsibility(config.ResponsibilitySettings{
				Name: "test", Time: tc.time,
			}, tc.delay)
			if err != nil {
				t.Fatalf("NewResponsibility: %v", err)
			}
			if r.TimeOfDay != tc.want {
				t.Fatalf("TimeOfDay = %q, want %q", r.TimeOfDay, tc.want)
			}
		})
	}
}

// TestNewResponsibility_FrequencyUnaffectedByTimeJitter guards that the
// frequency path still applies the startup delay to the first run only.
func TestNewResponsibility_FrequencyStartupDelay(t *testing.T) {
	before := time.Now()
	r, err := NewResponsibility(config.ResponsibilitySettings{
		Name: "test", Frequency: "1h",
	}, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewResponsibility: %v", err)
	}
	if r.Due(before.Add(5 * time.Minute)) {
		t.Fatal("responsibility due before the startup delay elapsed")
	}
	if !r.Due(before.Add(11 * time.Minute)) {
		t.Fatal("responsibility not due after the startup delay elapsed")
	}
}
