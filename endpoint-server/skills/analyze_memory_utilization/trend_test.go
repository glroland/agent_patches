package analyze_memory_utilization

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
)

func TestGrowthIssue(t *testing.T) {
	now := time.Now()
	old := now.Add(-24 * time.Hour)
	young := now.Add(-2 * time.Hour)

	cases := []struct {
		name     string
		samples  []UsageSample
		usedPct  float64
		wantFlag bool
	}{
		{"no history", nil, 60, false},
		{"stable", []UsageSample{{Time: old, UsedPct: 55}}, 60, false},
		{"leak over a day", []UsageSample{{Time: old, UsedPct: 35}}, 60, true},
		{"big jump but history too young", []UsageSample{{Time: young, UsedPct: 35}}, 60, false},
		{"exactly at threshold", []UsageSample{{Time: old, UsedPct: 40}}, 60, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GrowthIssue(tc.samples, tc.usedPct, now)
			if (got != "") != tc.wantFlag {
				t.Fatalf("GrowthIssue = %q, want flagged=%v", got, tc.wantFlag)
			}
		})
	}
}

func TestRecordSample_PrunesAndThrottles(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	now := time.Now()

	// Ancient sample must be pruned; a sample recorded moments ago must
	// suppress a new append.
	if _, err := RecordSample(mem, 30, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("RecordSample: %v", err)
	}
	samples, err := RecordSample(mem, 40, now)
	if err != nil {
		t.Fatalf("RecordSample: %v", err)
	}
	if len(samples) != 1 || samples[0].UsedPct != 40 {
		t.Fatalf("expected only the fresh sample, got %+v", samples)
	}

	samples, err = RecordSample(mem, 50, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordSample: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("sample within min interval was not throttled: %+v", samples)
	}
}
