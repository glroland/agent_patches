package analyze_system_temperature

import (
	"fmt"
	"strings"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
)

const (
	trendAttrsKey       = "temperature_trends"
	trendMaxAge         = 24 * time.Hour
	trendMinInterval    = 2 * time.Minute
	sustainedWindow     = 15 * time.Minute
	sustainedMinSamples = 3
)

// TempSample is one recorded measurement of a sensor's temperature.
type TempSample struct {
	Time     time.Time `json:"time"`
	CelsiusC float64   `json:"celsiusC"`
}

// TempTrend holds the rolling sample history for one sensor and its average
// over the most recent sustainedWindow.
type TempTrend struct {
	Sensor     string       `json:"sensor"`
	Samples    []TempSample `json:"samples"`
	RecentAvgC float64      `json:"recentAvgC"`
}

// RecordSamples appends the current sensor readings as new samples to the
// rolling history stored in mem, prunes entries older than trendMaxAge,
// recomputes the recent-window average for each sensor, and returns the
// updated trend map. Sensors whose most recent sample is less than
// trendMinInterval old are skipped to avoid skewing the average when this
// check runs more frequently than the intended cadence.
func RecordSamples(mem *memory.Store, sensors []TempSensor, now time.Time) (map[string]TempTrend, error) {
	trends := make(map[string]TempTrend)
	_ = mem.Attrs().Get(trendAttrsKey, &trends)

	for _, s := range sensors {
		key := sanitizeSensor(s.Name)
		entry := trends[key]
		entry.Sensor = s.Name

		// Skip if last sample is too recent.
		if len(entry.Samples) > 0 {
			last := entry.Samples[len(entry.Samples)-1]
			if now.Sub(last.Time) < trendMinInterval {
				continue
			}
		}

		// Append new sample.
		entry.Samples = append(entry.Samples, TempSample{Time: now, CelsiusC: s.CelsiusC})

		// Prune old samples.
		cutoff := now.Add(-trendMaxAge)
		i := 0
		for i < len(entry.Samples) && entry.Samples[i].Time.Before(cutoff) {
			i++
		}
		entry.Samples = entry.Samples[i:]

		entry.RecentAvgC = recentAverage(entry.Samples, now)

		trends[key] = entry
	}

	if err := mem.Attrs().Set(trendAttrsKey, trends); err != nil {
		return trends, fmt.Errorf("analyze_system_temperature: saving temperature trends: %w", err)
	}
	return trends, nil
}

// recentAverage averages the samples within sustainedWindow of now. Returns 0
// if there are none.
func recentAverage(samples []TempSample, now time.Time) float64 {
	cutoff := now.Add(-sustainedWindow)
	var sum float64
	var n int
	for _, s := range samples {
		if s.Time.Before(cutoff) {
			continue
		}
		sum += s.CelsiusC
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// recentCount returns how many samples fall within sustainedWindow of now.
func recentCount(samples []TempSample, now time.Time) int {
	cutoff := now.Add(-sustainedWindow)
	n := 0
	for _, s := range samples {
		if !s.Time.Before(cutoff) {
			n++
		}
	}
	return n
}

// SustainedHealth derives a skillstate health/summary from the current trend
// map: a sensor is flagged only once it has at least sustainedMinSamples
// readings within the last sustainedWindow, so a single transient spike
// (e.g. a brief burst of CPU load) doesn't trigger an alert — only a
// temperature that stays elevated does. The worst severity across all
// sensors is returned.
func SustainedHealth(trends map[string]TempTrend) (skillstate.Health, string) {
	var criticals, warnings []string
	now := time.Now()

	for _, t := range trends {
		if recentCount(t.Samples, now) < sustainedMinSamples {
			continue
		}
		label := fmt.Sprintf("%s sustained avg %.1f°C over last %.0f min", t.Sensor, t.RecentAvgC, sustainedWindow.Minutes())
		switch {
		case t.RecentAvgC >= tempCriticalC:
			criticals = append(criticals, label)
		case t.RecentAvgC >= tempWarningC:
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

// sanitizeSensor converts a sensor name into a safe map key.
func sanitizeSensor(name string) string {
	key := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_").Replace(name)
	return strings.TrimPrefix(key, "_")
}
