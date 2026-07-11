package analyze_network_utilization

import (
	"fmt"
	"sort"
	"time"

	"agent_patches/endpoint-server/memory"
)

const (
	baselineAttrsKey    = "network_rate_baseline"
	baselineMaxAge      = 7 * 24 * time.Hour
	baselineMinInterval = 5 * time.Minute

	// baselineMinSamples is how much history must exist before a rate can be
	// judged "normal". Below this the caller should treat every reading as
	// worth investigating, which matches the pre-baseline behaviour of
	// sending every scheduled run to the LLM.
	baselineMinSamples = 4

	// anomalyFactor and anomalyFloorMBps define "unusually high": the current
	// rate must exceed anomalyFactor times the historical median AND the
	// absolute floor, so idle-host noise (a median near zero) can't flag
	// trivial traffic.
	anomalyFactor    = 3.0
	anomalyFloorMBps = 1.0
)

// RateSample is one recorded measurement of network throughput.
type RateSample struct {
	Time     time.Time `json:"time"`
	DownMBps float64   `json:"downMBps"`
	UpMBps   float64   `json:"upMBps"`
}

// RecordSample appends the current rates to the rolling history stored in
// mem, prunes entries older than baselineMaxAge, and returns the updated
// history plus whether the new sample was actually appended. A sample is
// skipped (appended=false) when the newest existing one is less than
// baselineMinInterval old, so frequent runs don't flood the window.
func RecordSample(mem *memory.Store, downMBps, upMBps float64, now time.Time) (samples []RateSample, appended bool, err error) {
	_ = mem.Attrs().Get(baselineAttrsKey, &samples)

	if len(samples) == 0 || now.Sub(samples[len(samples)-1].Time) >= baselineMinInterval {
		samples = append(samples, RateSample{Time: now, DownMBps: downMBps, UpMBps: upMBps})
		appended = true
	}

	cutoff := now.Add(-baselineMaxAge)
	i := 0
	for i < len(samples) && samples[i].Time.Before(cutoff) {
		i++
	}
	samples = samples[i:]

	if err := mem.Attrs().Set(baselineAttrsKey, samples); err != nil {
		return samples, appended, fmt.Errorf("analyze_network_utilization: saving rate baseline: %w", err)
	}
	return samples, appended, nil
}

// AnomalyIssue reports whether the current rates are unusually high compared
// to the recorded history: above anomalyFactor times the historical median
// and above anomalyFloorMBps, in either direction. The returned string
// describes the anomaly; empty when traffic is within the baseline. When
// history holds fewer than baselineMinSamples entries (excluding the current
// reading), every reading is treated as anomalous so a young host is judged
// by the LLM until a baseline exists. Exported for testing.
func AnomalyIssue(samples []RateSample, downMBps, upMBps float64) string {
	if len(samples) < baselineMinSamples {
		return "not enough baseline history yet to judge whether current traffic is normal"
	}

	medDown, medUp := medians(samples)
	if downMBps > anomalyFloorMBps && downMBps > anomalyFactor*medDown {
		return fmt.Sprintf("download rate %.2f MB/s is %.1fx the 7-day median (%.2f MB/s)",
			downMBps, safeRatio(downMBps, medDown), medDown)
	}
	if upMBps > anomalyFloorMBps && upMBps > anomalyFactor*medUp {
		return fmt.Sprintf("upload rate %.2f MB/s is %.1fx the 7-day median (%.2f MB/s)",
			upMBps, safeRatio(upMBps, medUp), medUp)
	}
	return ""
}

// medians returns the median download and upload rates across samples.
func medians(samples []RateSample) (down, up float64) {
	downs := make([]float64, len(samples))
	ups := make([]float64, len(samples))
	for i, s := range samples {
		downs[i] = s.DownMBps
		ups[i] = s.UpMBps
	}
	return median(downs), median(ups)
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}

// safeRatio returns cur/base, capped for display when the base is ~zero.
func safeRatio(cur, base float64) float64 {
	if base <= 0.001 {
		return 999
	}
	return cur / base
}
