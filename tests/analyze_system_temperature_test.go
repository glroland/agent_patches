package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/analyze_system_temperature"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// ---- BuildReport ------------------------------------------------------------

func TestTempBuildReport_ContainsSensorAndValue(t *testing.T) {
	report := analyze_system_temperature.BuildReport([]analyze_system_temperature.TempSensor{
		{Name: "x86_pkg_temp", CelsiusC: 55.2},
	})
	for _, want := range []string{"Temperature", "x86_pkg_temp", "55.2"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestTempBuildReport_MarksWarningAndCritical(t *testing.T) {
	report := analyze_system_temperature.BuildReport([]analyze_system_temperature.TempSensor{
		{Name: "core0", CelsiusC: 82.0},
		{Name: "core1", CelsiusC: 95.0},
		{Name: "core2", CelsiusC: 40.0},
	})
	if !strings.Contains(report, "core0") || !strings.Contains(report, "(WARNING)") {
		t.Errorf("BuildReport should flag 82.0 as warning:\n%s", report)
	}
	if !strings.Contains(report, "core1") || !strings.Contains(report, "(CRITICAL)") {
		t.Errorf("BuildReport should flag 95.0 as critical:\n%s", report)
	}
	if !strings.Contains(report, "core2:") || !strings.Contains(report, "40.0") {
		t.Errorf("BuildReport missing core2:\n%s", report)
	}
}

// ---- Tool metadata ----------------------------------------------------------

func TestNewTemperatureTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_system_temperature.NewTemperatureTool(mem)
	if err != nil {
		t.Fatalf("NewTemperatureTool() error: %v", err)
	}
	if got := tl.Name(); got != "analyze_system_temperature" {
		t.Errorf("Name() = %q, want %q", got, "analyze_system_temperature")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

// ---- Execute integration ----------------------------------------------------

// TestTemperatureTool_Execute_ReturnsReport verifies that Execute succeeds and
// returns a report on every platform. When no sensors are exposed (VMs,
// sandboxed hosts, missing privileges) the tool degrades to a "no sensors
// detected" report rather than an error.
func TestTemperatureTool_Execute_ReturnsReport(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_system_temperature.NewTemperatureTool(mem)
	if err != nil {
		t.Fatalf("NewTemperatureTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, execErr := tl.Execute(context.Background(), input)
	if execErr != nil {
		t.Fatalf("Execute() error: %v", execErr)
	}
	if result == "" {
		t.Error("Execute() returned empty result")
	}
}

// TestTemperatureTool_Execute_WritesSkillState verifies that Execute always
// writes a skillstate entry.
func TestTemperatureTool_Execute_WritesSkillState(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_system_temperature.NewTemperatureTool(mem)
	if err != nil {
		t.Fatalf("NewTemperatureTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	_, _ = tl.Execute(context.Background(), input)

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("skillstate.LoadAll: %v", err)
	}
	if len(states) != 1 || states[0].Skill != "analyze_system_temperature" {
		t.Fatalf("skillstate after Execute() = %+v, want one entry for analyze_system_temperature", states)
	}
	switch states[0].Health {
	case skillstate.HealthOK, skillstate.HealthWarning, skillstate.HealthCritical:
	default:
		t.Errorf("skillstate health = %q, want ok/warning/critical", states[0].Health)
	}
	if states[0].Summary == "" {
		t.Error("skillstate summary is empty")
	}
}

// ---- RecordSamples ------------------------------------------------------------

func TestTempRecordSamples_AppendsSample(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	sensors := []analyze_system_temperature.TempSensor{{Name: "x86_pkg_temp", CelsiusC: 65.0}}
	now := time.Now()

	trends, err := analyze_system_temperature.RecordSamples(mem, sensors, now)
	if err != nil {
		t.Fatalf("RecordSamples: %v", err)
	}
	entry, ok := trends["x86_pkg_temp"]
	if !ok {
		t.Fatalf("no entry for x86_pkg_temp in trends: %+v", trends)
	}
	if len(entry.Samples) != 1 {
		t.Errorf("expected 1 sample, got %d", len(entry.Samples))
	}
	if entry.RecentAvgC != 65.0 {
		t.Errorf("RecentAvgC = %.2f, want 65.0", entry.RecentAvgC)
	}
}

func TestTempRecordSamples_SkipsWithinInterval(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	sensors := []analyze_system_temperature.TempSensor{{Name: "cpu", CelsiusC: 60.0}}
	now := time.Now()

	if _, err := analyze_system_temperature.RecordSamples(mem, sensors, now); err != nil {
		t.Fatalf("first RecordSamples: %v", err)
	}
	// Second call within 2 minutes — sample should be skipped.
	trends, err := analyze_system_temperature.RecordSamples(mem, sensors, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second RecordSamples: %v", err)
	}
	for _, entry := range trends {
		if len(entry.Samples) != 1 {
			t.Errorf("expected 1 sample after rapid second call, got %d", len(entry.Samples))
		}
	}
}

func TestTempRecordSamples_PrunesOldSamples(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	sensors := []analyze_system_temperature.TempSensor{{Name: "cpu", CelsiusC: 70.0}}
	old := time.Now().Add(-25 * time.Hour) // outside the 24h window

	if _, err := analyze_system_temperature.RecordSamples(mem, sensors, old); err != nil {
		t.Fatalf("old RecordSamples: %v", err)
	}
	now := time.Now()
	trends, err := analyze_system_temperature.RecordSamples(mem, sensors, now)
	if err != nil {
		t.Fatalf("new RecordSamples: %v", err)
	}
	for _, entry := range trends {
		for _, s := range entry.Samples {
			if s.Time.Before(now.Add(-24 * time.Hour)) {
				t.Errorf("found sample older than 24h: %v", s.Time)
			}
		}
	}
}

// ---- SustainedHealth ----------------------------------------------------------

func TestSustainedHealth_NoData(t *testing.T) {
	h, _ := analyze_system_temperature.SustainedHealth(nil)
	if h != skillstate.HealthOK {
		t.Errorf("SustainedHealth(nil) = %q, want ok", h)
	}
	h, _ = analyze_system_temperature.SustainedHealth(map[string]analyze_system_temperature.TempTrend{})
	if h != skillstate.HealthOK {
		t.Errorf("SustainedHealth(empty) = %q, want ok", h)
	}
}

func TestSustainedHealth_RequiresMinSamples(t *testing.T) {
	now := time.Now()
	trends := map[string]analyze_system_temperature.TempTrend{
		"cpu": {
			Sensor: "cpu",
			Samples: []analyze_system_temperature.TempSample{
				{Time: now.Add(-1 * time.Minute), CelsiusC: 95.0},
			},
			RecentAvgC: 95.0,
		},
	}
	// Only 1 sample within the sustained window — below sustainedMinSamples (3).
	h, _ := analyze_system_temperature.SustainedHealth(trends)
	if h != skillstate.HealthOK {
		t.Errorf("SustainedHealth() with 1 sample = %q, want ok (needs sustained samples)", h)
	}
}

func TestSustainedHealth_WarningWhenSustained(t *testing.T) {
	now := time.Now()
	trends := map[string]analyze_system_temperature.TempTrend{
		"cpu": {
			Sensor: "cpu",
			Samples: []analyze_system_temperature.TempSample{
				{Time: now.Add(-10 * time.Minute), CelsiusC: 82.0},
				{Time: now.Add(-6 * time.Minute), CelsiusC: 83.0},
				{Time: now.Add(-2 * time.Minute), CelsiusC: 81.0},
			},
			RecentAvgC: 82.0,
		},
	}
	h, summary := analyze_system_temperature.SustainedHealth(trends)
	if h != skillstate.HealthWarning {
		t.Errorf("SustainedHealth() = %q, want warning", h)
	}
	if !strings.Contains(summary, "cpu") {
		t.Errorf("summary missing sensor name: %q", summary)
	}
}

func TestSustainedHealth_CriticalWhenSustained(t *testing.T) {
	now := time.Now()
	trends := map[string]analyze_system_temperature.TempTrend{
		"cpu": {
			Sensor: "cpu",
			Samples: []analyze_system_temperature.TempSample{
				{Time: now.Add(-10 * time.Minute), CelsiusC: 92.0},
				{Time: now.Add(-6 * time.Minute), CelsiusC: 93.0},
				{Time: now.Add(-2 * time.Minute), CelsiusC: 94.0},
			},
			RecentAvgC: 93.0,
		},
	}
	h, _ := analyze_system_temperature.SustainedHealth(trends)
	if h != skillstate.HealthCritical {
		t.Errorf("SustainedHealth() = %q, want critical", h)
	}
}

func TestSustainedHealth_OKWhenBelowThreshold(t *testing.T) {
	now := time.Now()
	trends := map[string]analyze_system_temperature.TempTrend{
		"cpu": {
			Sensor: "cpu",
			Samples: []analyze_system_temperature.TempSample{
				{Time: now.Add(-10 * time.Minute), CelsiusC: 45.0},
				{Time: now.Add(-6 * time.Minute), CelsiusC: 46.0},
				{Time: now.Add(-2 * time.Minute), CelsiusC: 44.0},
			},
			RecentAvgC: 45.0,
		},
	}
	h, _ := analyze_system_temperature.SustainedHealth(trends)
	if h != skillstate.HealthOK {
		t.Errorf("SustainedHealth() = %q, want ok", h)
	}
}
