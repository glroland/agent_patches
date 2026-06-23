package tests

import (
	"math"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_drives"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// --- ComputeSlope ---

func TestComputeSlope_Rising(t *testing.T) {
	// Samples at 0h, 24h, 48h with +5% per day → slope should be ~5%/day.
	base := time.Now()
	samples := []check_drives.DiskSample{
		{Time: base, UsedPct: 50},
		{Time: base.Add(24 * time.Hour), UsedPct: 55},
		{Time: base.Add(48 * time.Hour), UsedPct: 60},
	}
	got := check_drives.ComputeSlope(samples)
	if math.Abs(got-5.0) > 0.01 {
		t.Errorf("ComputeSlope() = %.4f, want ~5.0", got)
	}
}

func TestComputeSlope_Stable(t *testing.T) {
	base := time.Now()
	samples := []check_drives.DiskSample{
		{Time: base, UsedPct: 60},
		{Time: base.Add(24 * time.Hour), UsedPct: 60},
		{Time: base.Add(48 * time.Hour), UsedPct: 60},
	}
	got := check_drives.ComputeSlope(samples)
	if math.Abs(got) > 0.001 {
		t.Errorf("ComputeSlope() = %.4f on flat data, want ~0", got)
	}
}

func TestComputeSlope_InsufficientData(t *testing.T) {
	base := time.Now()
	for _, n := range []int{0, 1, 2} {
		samples := make([]check_drives.DiskSample, n)
		for i := range samples {
			samples[i] = check_drives.DiskSample{Time: base.Add(time.Duration(i) * time.Hour), UsedPct: float64(50 + i)}
		}
		if got := check_drives.ComputeSlope(samples); got != 0 {
			t.Errorf("ComputeSlope(%d samples) = %.4f, want 0", n, got)
		}
	}
}

// --- ForecastDays ---

func TestForecastDays_Computable(t *testing.T) {
	// At 70% with 5%/day → (90-70)/5 = 4 days.
	got := check_drives.ForecastDays(70, 5.0)
	if got != 4 {
		t.Errorf("ForecastDays(70, 5) = %d, want 4", got)
	}
}

func TestForecastDays_FractionalCeiling(t *testing.T) {
	// (90-80)/3 = 3.33 → ceil = 4.
	got := check_drives.ForecastDays(80, 3.0)
	if got != 4 {
		t.Errorf("ForecastDays(80, 3) = %d, want 4", got)
	}
}

func TestForecastDays_AlreadyFull(t *testing.T) {
	for _, pct := range []float64{90, 95, 100} {
		if got := check_drives.ForecastDays(pct, 2.0); got != -1 {
			t.Errorf("ForecastDays(%.0f, 2) = %d, want -1", pct, got)
		}
	}
}

func TestForecastDays_Shrinking(t *testing.T) {
	if got := check_drives.ForecastDays(70, 0); got != -1 {
		t.Errorf("ForecastDays(70, 0) = %d, want -1", got)
	}
	if got := check_drives.ForecastDays(70, -1.0); got != -1 {
		t.Errorf("ForecastDays(70, -1) = %d, want -1", got)
	}
}

// --- RecordSamples ---

func TestRecordSamples_AppendsSample(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	disks := []check_drives.DiskStat{
		{Mount: "/", Total: 100 << 30, Free: 50 << 30},
	}
	now := time.Now()

	trends, err := check_drives.RecordSamples(mem, disks, now)
	if err != nil {
		t.Fatalf("RecordSamples: %v", err)
	}
	entry, ok := trends[""]
	if !ok {
		// "/" sanitizes to "" (leading underscore stripped); try that key.
		// The mount "/" → sanitize → "_" → trim leading _ → ""
		t.Logf("trends keys: %v", keysOf(trends))
		t.Fatal("no entry for '/' in trends")
	}
	if len(entry.Samples) != 1 {
		t.Errorf("expected 1 sample, got %d", len(entry.Samples))
	}
	if math.Abs(entry.Samples[0].UsedPct-50.0) > 0.1 {
		t.Errorf("sample usedPct = %.2f, want ~50", entry.Samples[0].UsedPct)
	}
}

func TestRecordSamples_SkipsWithinInterval(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	disks := []check_drives.DiskStat{
		{Mount: "/data", Total: 100 << 30, Free: 60 << 30},
	}
	now := time.Now()

	if _, err := check_drives.RecordSamples(mem, disks, now); err != nil {
		t.Fatalf("first RecordSamples: %v", err)
	}
	// Second call within 30 minutes — sample should be skipped.
	trends, err := check_drives.RecordSamples(mem, disks, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("second RecordSamples: %v", err)
	}
	for _, entry := range trends {
		if len(entry.Samples) != 1 {
			t.Errorf("expected 1 sample after rapid second call, got %d", len(entry.Samples))
		}
	}
}

func TestRecordSamples_PrunesOldSamples(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	disks := []check_drives.DiskStat{
		{Mount: "/var", Total: 100 << 30, Free: 70 << 30},
	}
	old := time.Now().Add(-8 * 24 * time.Hour) // 8 days ago — outside 7-day window

	// First call with old timestamp (should be written but then pruned on next write).
	if _, err := check_drives.RecordSamples(mem, disks, old); err != nil {
		t.Fatalf("old RecordSamples: %v", err)
	}
	// Second call with current time — old sample should be pruned.
	now := time.Now()
	trends, err := check_drives.RecordSamples(mem, disks, now)
	if err != nil {
		t.Fatalf("new RecordSamples: %v", err)
	}
	for _, entry := range trends {
		for _, s := range entry.Samples {
			if s.Time.Before(now.Add(-7 * 24 * time.Hour)) {
				t.Errorf("found sample older than 7 days: %v", s.Time)
			}
		}
	}
}

// --- TrendHealth ---

func TestTrendHealth_NoData(t *testing.T) {
	h, _ := check_drives.TrendHealth(nil)
	if h != skillstate.HealthOK {
		t.Errorf("TrendHealth(nil) = %q, want ok", h)
	}
	h, _ = check_drives.TrendHealth(map[string]check_drives.DiskTrend{})
	if h != skillstate.HealthOK {
		t.Errorf("TrendHealth(empty) = %q, want ok", h)
	}
}

func TestTrendHealth_OK_SlowGrowth(t *testing.T) {
	// 0.5%/day is below the 1.43%/day threshold.
	trends := map[string]check_drives.DiskTrend{
		"data": {Mount: "/data", SlopePerDay: 0.5, ForecastDays: 60},
	}
	h, _ := check_drives.TrendHealth(trends)
	if h != skillstate.HealthOK {
		t.Errorf("slow growth → %q, want ok", h)
	}
}

func TestTrendHealth_Warning(t *testing.T) {
	// >1.43%/day with forecast 15 days → warning (< 30, >= 7).
	trends := map[string]check_drives.DiskTrend{
		"data": {Mount: "/data", SlopePerDay: 2.0, ForecastDays: 15},
	}
	h, _ := check_drives.TrendHealth(trends)
	if h != skillstate.HealthWarning {
		t.Errorf("forecast 15d → %q, want warning", h)
	}
}

func TestTrendHealth_Critical(t *testing.T) {
	// >1.43%/day with forecast 4 days → critical (< 7).
	trends := map[string]check_drives.DiskTrend{
		"root": {Mount: "/", SlopePerDay: 5.0, ForecastDays: 4},
	}
	h, _ := check_drives.TrendHealth(trends)
	if h != skillstate.HealthCritical {
		t.Errorf("forecast 4d → %q, want critical", h)
	}
}

func TestTrendHealth_IgnoresNegativeForecast(t *testing.T) {
	// High slope but forecastDays = -1 (already >= 90%) should not flag.
	trends := map[string]check_drives.DiskTrend{
		"root": {Mount: "/", SlopePerDay: 10.0, ForecastDays: -1},
	}
	h, _ := check_drives.TrendHealth(trends)
	if h != skillstate.HealthOK {
		t.Errorf("forecastDays=-1 → %q, want ok", h)
	}
}

func keysOf(m map[string]check_drives.DiskTrend) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
