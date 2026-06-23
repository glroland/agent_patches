package check_drives

import (
	"fmt"
	"math"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

const (
	trendAttrsKey     = "disk_trends"
	trendMaxAge       = 7 * 24 * time.Hour
	trendMinInterval  = 30 * time.Minute
	trendMinSamples   = 3
	trendGrowthPerDay = 10.0 / 7 // ~1.43% / day ≈ 10% / week
	trendWarningDays  = 30
	trendCriticalDays = 7
	trendFullPct      = 90.0
)

// DiskSample is one recorded measurement of a mount's usage.
type DiskSample struct {
	Time       time.Time `json:"time"`
	UsedPct    float64   `json:"usedPct"`
	UsedBytes  uint64    `json:"usedBytes"`
	TotalBytes uint64    `json:"totalBytes"`
}

// DiskTrend holds the rolling sample history and computed slope for one mount.
type DiskTrend struct {
	Mount        string       `json:"mount"`
	Samples      []DiskSample `json:"samples"`
	SlopePerDay  float64      `json:"slopePerDay"`  // % growth per day; positive = growing
	ForecastDays int          `json:"forecastDays"` // days to trendFullPct; -1 if not computable
}

// RecordSamples appends the current disk stats as new samples to the rolling
// history stored in attrs, prunes entries older than trendMaxAge, recomputes
// slope and forecast for each mount, and returns the updated trend map.
// Mounts whose most recent sample is less than trendMinInterval old are
// skipped to avoid skewing trends when check_drives runs more frequently
// than the intended hourly cadence.
func RecordSamples(mem *memory.Store, disks []DiskStat, now time.Time) (map[string]DiskTrend, error) {
	trends := make(map[string]DiskTrend)
	_ = mem.Attrs().Get(trendAttrsKey, &trends)

	for _, d := range disks {
		key := sanitizeMount(d.Mount)
		entry := trends[key]
		entry.Mount = d.Mount

		// Skip if last sample is too recent.
		if len(entry.Samples) > 0 {
			last := entry.Samples[len(entry.Samples)-1]
			if now.Sub(last.Time) < trendMinInterval {
				continue
			}
		}

		// Append new sample.
		entry.Samples = append(entry.Samples, DiskSample{
			Time:       now,
			UsedPct:    d.UsedPct(),
			UsedBytes:  d.Used(),
			TotalBytes: d.Total,
		})

		// Prune old samples.
		cutoff := now.Add(-trendMaxAge)
		i := 0
		for i < len(entry.Samples) && entry.Samples[i].Time.Before(cutoff) {
			i++
		}
		entry.Samples = entry.Samples[i:]

		// Recompute slope and forecast.
		entry.SlopePerDay = ComputeSlope(entry.Samples)
		var currentPct float64
		if len(entry.Samples) > 0 {
			currentPct = entry.Samples[len(entry.Samples)-1].UsedPct
		}
		entry.ForecastDays = ForecastDays(currentPct, entry.SlopePerDay)

		trends[key] = entry
	}

	if err := mem.Attrs().Set(trendAttrsKey, trends); err != nil {
		return trends, fmt.Errorf("check_drives: saving disk trends: %w", err)
	}
	return trends, nil
}

// ComputeSlope performs ordinary least-squares linear regression on the
// sample timestamps (hours from first sample) and usedPct values, returning
// the slope in % per day. Returns 0 if fewer than trendMinSamples exist.
func ComputeSlope(samples []DiskSample) float64 {
	if len(samples) < trendMinSamples {
		return 0
	}

	origin := samples[0].Time
	n := float64(len(samples))
	var sumX, sumY, sumXY, sumX2 float64
	for _, s := range samples {
		x := s.Time.Sub(origin).Hours()
		y := s.UsedPct
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denom := n*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-9 {
		return 0
	}
	slopePerHour := (n*sumXY - sumX*sumY) / denom
	return slopePerHour * 24 // convert to % per day
}

// ForecastDays returns how many days until the mount reaches trendFullPct
// given the current usage percentage and daily growth slope. Returns -1
// when the forecast is not computable (slope ≤ 0 or already at/above 90%).
func ForecastDays(currentPct, slopePerDay float64) int {
	if slopePerDay <= 0 || currentPct >= trendFullPct {
		return -1
	}
	days := (trendFullPct - currentPct) / slopePerDay
	return int(math.Ceil(days))
}

// TrendHealth derives a skillstate health/summary from the current trend map.
// A mount is flagged when its growth exceeds trendGrowthPerDay AND the
// forecast is within the warning or critical window. The worst severity
// across all mounts is returned.
func TrendHealth(trends map[string]DiskTrend) (skillstate.Health, string) {
	var criticals, warnings []string
	for _, t := range trends {
		if t.SlopePerDay <= trendGrowthPerDay {
			continue
		}
		fd := t.ForecastDays
		if fd < 0 {
			continue
		}
		label := fmt.Sprintf("%s growing %.1f%%/day (fills in ~%d days)", t.Mount, t.SlopePerDay, fd)
		switch {
		case fd < trendCriticalDays:
			criticals = append(criticals, label)
		case fd < trendWarningDays:
			warnings = append(warnings, label)
		}
	}

	switch {
	case len(criticals) > 0:
		return skillstate.HealthCritical, strings.Join(criticals, "; ")
	case len(warnings) > 0:
		return skillstate.HealthWarning, strings.Join(warnings, "; ")
	default:
		return skillstate.HealthOK, ""
	}
}

// sanitizeMount converts a mount path into a safe map key (replaces slashes
// and backslashes with underscores, trims leading underscore).
func sanitizeMount(mount string) string {
	key := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(mount)
	return strings.TrimPrefix(key, "_")
}
