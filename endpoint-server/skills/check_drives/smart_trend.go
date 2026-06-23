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
	smartTrendAttrsKey    = "smart_trends"
	smartTrendMaxAge      = 30 * 24 * time.Hour
	smartTrendMinInterval = 1 * time.Hour
	smartTrendMinSamples  = 3
)

// RawSmartAttrs is the result of one platform-specific SMART attribute
// collection for one device. Attrs maps attribute name to raw counter value.
type RawSmartAttrs struct {
	Device string
	Attrs  map[string]int64
}

// SmartAttrSample is one recorded measurement of a single SMART attribute.
type SmartAttrSample struct {
	Time  time.Time `json:"time"`
	Value int64     `json:"value"`
}

// SmartAttrTrend holds the rolling sample history and computed trend for one
// SMART attribute on one device.
type SmartAttrTrend struct {
	Name        string             `json:"name"`
	Samples     []SmartAttrSample  `json:"samples"`
	Delta       int64              `json:"delta"`       // current − first sample (baseline)
	SlopePerDay float64            `json:"slopePerDay"` // OLS regression, units/day
}

// SmartDeviceTrend holds attribute trends for one physical device.
type SmartDeviceTrend struct {
	Device string                    `json:"device"`
	Attrs  map[string]SmartAttrTrend `json:"attrs"` // keyed by attribute name
}

// RecordSmartSamples appends the current raw SMART attribute values to the
// rolling history in mem, prunes entries older than smartTrendMaxAge,
// recomputes delta and slope for each attribute, and returns the updated map.
// Devices whose most recent sample is less than smartTrendMinInterval old are
// skipped to avoid skewing trends during rapid re-runs.
func RecordSmartSamples(mem *memory.Store, raw []RawSmartAttrs, now time.Time) (map[string]SmartDeviceTrend, error) {
	trends := make(map[string]SmartDeviceTrend)
	_ = mem.Attrs().Get(smartTrendAttrsKey, &trends)

	for _, r := range raw {
		if r.Device == "" || len(r.Attrs) == 0 {
			continue
		}
		key := sanitizeMount(r.Device)
		entry := trends[key]
		entry.Device = r.Device
		if entry.Attrs == nil {
			entry.Attrs = make(map[string]SmartAttrTrend)
		}

		for attrName, value := range r.Attrs {
			attrTrend := entry.Attrs[attrName]
			attrTrend.Name = attrName

			// Skip if last sample is too recent.
			if len(attrTrend.Samples) > 0 {
				last := attrTrend.Samples[len(attrTrend.Samples)-1]
				if now.Sub(last.Time) < smartTrendMinInterval {
					continue
				}
			}

			// Append new sample.
			attrTrend.Samples = append(attrTrend.Samples, SmartAttrSample{
				Time:  now,
				Value: value,
			})

			// Prune old samples.
			cutoff := now.Add(-smartTrendMaxAge)
			i := 0
			for i < len(attrTrend.Samples) && attrTrend.Samples[i].Time.Before(cutoff) {
				i++
			}
			attrTrend.Samples = attrTrend.Samples[i:]

			// Recompute delta and slope.
			if len(attrTrend.Samples) >= 2 {
				attrTrend.Delta = attrTrend.Samples[len(attrTrend.Samples)-1].Value - attrTrend.Samples[0].Value
			} else {
				attrTrend.Delta = 0
			}
			attrTrend.SlopePerDay = computeSmartSlope(attrTrend.Samples)

			entry.Attrs[attrName] = attrTrend
		}

		trends[key] = entry
	}

	if err := mem.Attrs().Set(smartTrendAttrsKey, trends); err != nil {
		return trends, fmt.Errorf("check_drives: saving smart trends: %w", err)
	}
	return trends, nil
}

// criticalSmartAttrs defines the alert behaviour for known SMART attributes.
// true = critical severity on any positive delta; false = warning.
var criticalSmartAttrs = map[string]bool{
	"Reallocated_Sector_Ct":  false, // warning
	"Current_Pending_Sector": false, // warning
	"Offline_Uncorrectable":  true,  // critical
	"Reported_Uncorrect":     true,  // critical
}

// SmartTrendHealth derives a skillstate health/summary from the current smart
// trend map using the delta-from-baseline alert model:
//
//   - Reallocated_Sector_Ct / Current_Pending_Sector: delta > 0 → warning
//   - Offline_Uncorrectable / Reported_Uncorrect: delta > 0 → critical
//   - Any critical attr with slopePerDay > 1.0 escalates to critical
//   - NVMe_Wear_Pct / Windows Wear ≥ 70 → warning; ≥ 90 → critical
func SmartTrendHealth(trends map[string]SmartDeviceTrend) (skillstate.Health, string) {
	var criticals, warnings []string

	for _, dev := range trends {
		for attrName, attr := range dev.Attrs {
			if len(attr.Samples) < 2 {
				continue
			}

			// Wear percentage thresholds (NVMe / Windows).
			if attrName == "NVMe_Wear_Pct" || attrName == "Wear" {
				current := attr.Samples[len(attr.Samples)-1].Value
				switch {
				case current >= 90:
					criticals = append(criticals, fmt.Sprintf("%s %s wear: %d%%", dev.Device, attrName, current))
				case current >= 70:
					warnings = append(warnings, fmt.Sprintf("%s %s wear: %d%%", dev.Device, attrName, current))
				}
				continue
			}

			// Delta-from-baseline for critical ATA attributes.
			isCriticalAttr, tracked := criticalSmartAttrs[attrName]
			if !tracked || attr.Delta <= 0 {
				continue
			}

			label := fmt.Sprintf("%s %s +%d from baseline", dev.Device, attrName, attr.Delta)
			if isCriticalAttr || attr.SlopePerDay > 1.0 {
				criticals = append(criticals, label)
			} else {
				warnings = append(warnings, label)
			}
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

// computeSmartSlope performs ordinary least-squares linear regression on the
// sample timestamps (hours from first sample) and raw values, returning the
// slope in units per day. Returns 0 if fewer than smartTrendMinSamples exist.
func computeSmartSlope(samples []SmartAttrSample) float64 {
	if len(samples) < smartTrendMinSamples {
		return 0
	}

	origin := samples[0].Time
	n := float64(len(samples))
	var sumX, sumY, sumXY, sumX2 float64
	for _, s := range samples {
		x := s.Time.Sub(origin).Hours()
		y := float64(s.Value)
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
	return slopePerHour * 24
}
