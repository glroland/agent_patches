package analyze_memory_utilization

import (
	"fmt"
	"time"

	"agent_patches/endpoint-server/memory"
)

const (
	trendAttrsKey    = "memory_usage_trend"
	trendMaxAge      = 25 * time.Hour
	trendMinInterval = 5 * time.Minute

	// growthEscalationPts is how many percentage points RAM usage must rise
	// over ~24h to be flagged as a possible leak even while still below the
	// warning threshold.
	growthEscalationPts = 20.0
	// growthLookback is how far back the growth comparison reaches. A sample
	// must be at least this old to serve as the comparison point, so a young
	// history can't fake a day-long trend.
	growthLookback = 20 * time.Hour
)

// UsageSample is one recorded measurement of RAM usage.
type UsageSample struct {
	Time    time.Time `json:"time"`
	UsedPct float64   `json:"usedPct"`
}

// RecordSample appends the current RAM usage to the rolling history stored in
// mem, prunes entries older than trendMaxAge, and returns the updated history.
// A sample is skipped when the newest existing one is less than
// trendMinInterval old, so frequent runs don't flood the window.
func RecordSample(mem *memory.Store, usedPct float64, now time.Time) ([]UsageSample, error) {
	var samples []UsageSample
	_ = mem.Attrs().Get(trendAttrsKey, &samples)

	if len(samples) == 0 || now.Sub(samples[len(samples)-1].Time) >= trendMinInterval {
		samples = append(samples, UsageSample{Time: now, UsedPct: usedPct})
	}

	cutoff := now.Add(-trendMaxAge)
	i := 0
	for i < len(samples) && samples[i].Time.Before(cutoff) {
		i++
	}
	samples = samples[i:]

	if err := mem.Attrs().Set(trendAttrsKey, samples); err != nil {
		return samples, fmt.Errorf("analyze_memory_utilization: saving usage trend: %w", err)
	}
	return samples, nil
}

// GrowthIssue reports whether RAM usage has risen by growthEscalationPts or
// more compared to the oldest sample at least growthLookback old, suggesting
// a leak even while usage is still below the warning threshold. The returned
// string describes the growth; empty when there is nothing to flag (including
// when history is too young to judge). Exported for testing.
func GrowthIssue(samples []UsageSample, usedPct float64, now time.Time) string {
	cutoff := now.Add(-growthLookback)
	for _, s := range samples {
		if s.Time.After(cutoff) {
			break
		}
		if delta := usedPct - s.UsedPct; delta >= growthEscalationPts {
			return fmt.Sprintf("RAM usage grew %.1f points over the last %.0fh (%.1f%% -> %.1f%%), possible leak",
				delta, now.Sub(s.Time).Hours(), s.UsedPct, usedPct)
		}
	}
	return ""
}
