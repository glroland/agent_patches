package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_drives"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

func newSmartTrendMem(t *testing.T) *memory.Store {
	t.Helper()
	dir := t.TempDir()
	return memory.New(&config.MemorySettings{Root: filepath.Join(dir, "mem")})
}

// --- RecordSmartSamples ---

func TestRecordSmartSamples_AppendsSample(t *testing.T) {
	mem := newSmartTrendMem(t)
	now := time.Now()
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 0}},
	}
	trends, err := check_drives.RecordSmartSamples(mem, raw, now)
	if err != nil {
		t.Fatalf("RecordSmartSamples error: %v", err)
	}
	key := "dev_sda"
	dev, ok := trends[key]
	if !ok {
		t.Fatalf("expected key %q in trends, got keys: %v", key, trendKeys(trends))
	}
	attr, ok := dev.Attrs["Reallocated_Sector_Ct"]
	if !ok {
		t.Fatal("expected Reallocated_Sector_Ct in attrs")
	}
	if len(attr.Samples) != 1 {
		t.Errorf("want 1 sample, got %d", len(attr.Samples))
	}
	if attr.Samples[0].Value != 0 {
		t.Errorf("want value 0, got %d", attr.Samples[0].Value)
	}
}

func TestRecordSmartSamples_SkipsWithinInterval(t *testing.T) {
	mem := newSmartTrendMem(t)
	base := time.Now()
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 1}},
	}
	if _, err := check_drives.RecordSmartSamples(mem, raw, base); err != nil {
		t.Fatal(err)
	}
	// Second call within the min interval — should be skipped.
	raw[0].Attrs["Reallocated_Sector_Ct"] = 2
	trends, err := check_drives.RecordSmartSamples(mem, raw, base.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	attr := trends["dev_sda"].Attrs["Reallocated_Sector_Ct"]
	if len(attr.Samples) != 1 {
		t.Errorf("want 1 sample (skipped), got %d", len(attr.Samples))
	}
}

func TestRecordSmartSamples_PrunesOldSamples(t *testing.T) {
	mem := newSmartTrendMem(t)
	base := time.Now()

	// Record an old sample directly by writing it through two calls well apart.
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 5}},
	}
	// Inject a sample from 31 days ago (simulated) then a recent one.
	// We can't time-travel the store, but we can record two samples 1h+ apart
	// and verify pruning removes samples outside the window.
	old := base.Add(-31 * 24 * time.Hour)
	if _, err := check_drives.RecordSmartSamples(mem, raw, old); err != nil {
		t.Fatal(err)
	}
	raw[0].Attrs["Reallocated_Sector_Ct"] = 6
	trends, err := check_drives.RecordSmartSamples(mem, raw, base)
	if err != nil {
		t.Fatal(err)
	}
	attr := trends["dev_sda"].Attrs["Reallocated_Sector_Ct"]
	// The old sample should have been pruned; only the recent one remains.
	if len(attr.Samples) != 1 {
		t.Errorf("want 1 sample after pruning, got %d", len(attr.Samples))
	}
	if attr.Samples[0].Value != 6 {
		t.Errorf("want value 6 (recent), got %d", attr.Samples[0].Value)
	}
}

// --- SmartTrendHealth ---

func TestSmartTrendHealth_NoData(t *testing.T) {
	h, _ := check_drives.SmartTrendHealth(nil)
	if h != skillstate.HealthOK {
		t.Errorf("want OK for nil trends, got %v", h)
	}
}

func TestSmartTrendHealth_AllZero(t *testing.T) {
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_sda": {
			Device: "/dev/sda",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"Reallocated_Sector_Ct": {
					Name:   "Reallocated_Sector_Ct",
					Delta:  0,
					Samples: twoSamples(0, 0),
				},
			},
		},
	}
	h, _ := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthOK {
		t.Errorf("want OK for all-zero counters, got %v", h)
	}
}

func TestSmartTrendHealth_ReallocatedDelta_Warning(t *testing.T) {
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_sda": {
			Device: "/dev/sda",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"Reallocated_Sector_Ct": {
					Name:    "Reallocated_Sector_Ct",
					Delta:   3,
					Samples: twoSamples(0, 3),
				},
			},
		},
	}
	h, msg := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthWarning {
		t.Errorf("want Warning for Reallocated_Sector_Ct delta>0, got %v", h)
	}
	if msg == "" {
		t.Error("want non-empty message")
	}
}

func TestSmartTrendHealth_OfflineUncorrectableDelta_Critical(t *testing.T) {
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_sda": {
			Device: "/dev/sda",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"Offline_Uncorrectable": {
					Name:    "Offline_Uncorrectable",
					Delta:   1,
					Samples: twoSamples(0, 1),
				},
			},
		},
	}
	h, _ := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthCritical {
		t.Errorf("want Critical for Offline_Uncorrectable delta>0, got %v", h)
	}
}

func TestSmartTrendHealth_RapidSlope_Escalates(t *testing.T) {
	// Reallocated_Sector_Ct is normally a warning attr, but slope>1/day escalates it.
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_sda": {
			Device: "/dev/sda",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"Reallocated_Sector_Ct": {
					Name:        "Reallocated_Sector_Ct",
					Delta:       5,
					SlopePerDay: 2.5,
					Samples:     twoSamples(0, 5),
				},
			},
		},
	}
	h, _ := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthCritical {
		t.Errorf("want Critical when slope>1/day, got %v", h)
	}
}

func TestSmartTrendHealth_NVMeWear_Warning(t *testing.T) {
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_nvme0": {
			Device: "/dev/nvme0",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"NVMe_Wear_Pct": {
					Name:    "NVMe_Wear_Pct",
					Samples: twoSamples(70, 75),
				},
			},
		},
	}
	h, _ := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthWarning {
		t.Errorf("want Warning for NVMe wear >=70%%, got %v", h)
	}
}

func TestSmartTrendHealth_NVMeWear_Critical(t *testing.T) {
	trends := map[string]check_drives.SmartDeviceTrend{
		"dev_nvme0": {
			Device: "/dev/nvme0",
			Attrs: map[string]check_drives.SmartAttrTrend{
				"NVMe_Wear_Pct": {
					Name:    "NVMe_Wear_Pct",
					Samples: twoSamples(88, 92),
				},
			},
		},
	}
	h, _ := check_drives.SmartTrendHealth(trends)
	if h != skillstate.HealthCritical {
		t.Errorf("want Critical for NVMe wear >=90%%, got %v", h)
	}
}

// --- computeSmartSlope (via RecordSmartSamples) ---

func TestComputeSmartSlope_Rising(t *testing.T) {
	mem := newSmartTrendMem(t)
	base := time.Now().Add(-4 * time.Hour)
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 0}},
	}
	if _, err := check_drives.RecordSmartSamples(mem, raw, base); err != nil {
		t.Fatal(err)
	}
	raw[0].Attrs["Reallocated_Sector_Ct"] = 12
	if _, err := check_drives.RecordSmartSamples(mem, raw, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	raw[0].Attrs["Reallocated_Sector_Ct"] = 24
	trends, err := check_drives.RecordSmartSamples(mem, raw, base.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	slope := trends["dev_sda"].Attrs["Reallocated_Sector_Ct"].SlopePerDay
	if slope <= 0 {
		t.Errorf("want positive slope for rising counter, got %v", slope)
	}
}

func TestComputeSmartSlope_Flat(t *testing.T) {
	mem := newSmartTrendMem(t)
	base := time.Now().Add(-4 * time.Hour)
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 0}},
	}
	for _, offset := range []time.Duration{0, 2 * time.Hour, 4 * time.Hour} {
		if _, err := check_drives.RecordSmartSamples(mem, raw, base.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	trends, _ := check_drives.RecordSmartSamples(mem, raw, base.Add(5*time.Hour))
	slope := trends["dev_sda"].Attrs["Reallocated_Sector_Ct"].SlopePerDay
	if slope != 0 {
		t.Errorf("want slope=0 for flat counter, got %v", slope)
	}
}

func TestComputeSmartSlope_InsufficientSamples(t *testing.T) {
	mem := newSmartTrendMem(t)
	base := time.Now()
	raw := []check_drives.RawSmartAttrs{
		{Device: "/dev/sda", Attrs: map[string]int64{"Reallocated_Sector_Ct": 5}},
	}
	if _, err := check_drives.RecordSmartSamples(mem, raw, base); err != nil {
		t.Fatal(err)
	}
	// Only 1 sample — slope should be 0.
	raw[0].Attrs["Reallocated_Sector_Ct"] = 10
	trends, err := check_drives.RecordSmartSamples(mem, raw, base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	slope := trends["dev_sda"].Attrs["Reallocated_Sector_Ct"].SlopePerDay
	// 2 samples < smartTrendMinSamples (3), slope must be 0.
	if slope != 0 {
		t.Errorf("want slope=0 with <3 samples, got %v", slope)
	}
}

// --- helpers ---

func twoSamples(first, last int64) []check_drives.SmartAttrSample {
	base := time.Now().Add(-2 * time.Hour)
	return []check_drives.SmartAttrSample{
		{Time: base, Value: first},
		{Time: base.Add(2 * time.Hour), Value: last},
	}
}

func trendKeys(m map[string]check_drives.SmartDeviceTrend) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Ensure the memory package is used (avoids "imported and not used" for the os import).
var _ = os.DevNull
