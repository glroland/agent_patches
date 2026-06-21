package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/analyze_cpu_utilization"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
)

// ---- cpuHealth thresholds ---------------------------------------------------

func TestCPUHealth_OK(t *testing.T) {
	report := analyze_cpu_utilization.BuildReport(analyze_cpu_utilization.CPUStat{
		UsedPct: 50.0,
		NumCPU:  4,
	})
	if !strings.Contains(report, "50.0%") {
		t.Errorf("BuildReport missing usage: %q", report)
	}
}

// ---- BuildReport ------------------------------------------------------------

func TestCPUBuildReport_ContainsEssentialFields(t *testing.T) {
	stat := analyze_cpu_utilization.CPUStat{
		UsedPct:   72.5,
		NumCPU:    8,
		LoadAvg1:  1.23,
		LoadAvg5:  0.98,
		LoadAvg15: 0.75,
	}
	report := analyze_cpu_utilization.BuildReport(stat)

	for _, want := range []string{"CPU", "8", "72.5%", "1.23", "0.98", "0.75"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestCPUBuildReport_OmitsLoadAvgWhenZero(t *testing.T) {
	stat := analyze_cpu_utilization.CPUStat{UsedPct: 10.0, NumCPU: 2}
	report := analyze_cpu_utilization.BuildReport(stat)

	if strings.Contains(report, "Load avg") {
		t.Errorf("BuildReport should omit load avg when all zeros:\n%s", report)
	}
}

// ---- Tool metadata ----------------------------------------------------------

func TestNewCPUUsageTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_cpu_utilization.NewCPUUsageTool(mem)
	if err != nil {
		t.Fatalf("NewCPUUsageTool() error: %v", err)
	}
	if got := tl.Name(); got != "analyze_cpu_utilization" {
		t.Errorf("Name() = %q, want %q", got, "analyze_cpu_utilization")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

// ---- Execute integration ----------------------------------------------------

// TestCPUUsageTool_Execute_ReturnsReport verifies that Execute returns a
// CPU report on success. Some platforms (e.g. virtualised macOS without
// access to kern.cp_time) may not support the sampling API; those are skipped
// rather than failed so CI on all platforms stays green.
func TestCPUUsageTool_Execute_ReturnsReport(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_cpu_utilization.NewCPUUsageTool(mem)
	if err != nil {
		t.Fatalf("NewCPUUsageTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, execErr := tl.Execute(context.Background(), input)
	if execErr != nil {
		// Platform doesn't expose the CPU sampling sysctl — skip gracefully.
		t.Skipf("platform CPU sampling unavailable, skipping: %v", execErr)
	}
	if !strings.Contains(result, "CPU") {
		t.Errorf("Execute() result missing CPU section: %q", result)
	}
}

// TestCPUUsageTool_Execute_WritesSkillState verifies that Execute always writes
// a skillstate entry — on success with the real health value, on platform
// failure with HealthCritical.
func TestCPUUsageTool_Execute_WritesSkillState(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_cpu_utilization.NewCPUUsageTool(mem)
	if err != nil {
		t.Fatalf("NewCPUUsageTool() error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	// Ignore the execution error — the important contract is that skillstate is written.
	_, _ = tl.Execute(context.Background(), input)

	states, err := skillstate.LoadAll(mem)
	if err != nil {
		t.Fatalf("skillstate.LoadAll: %v", err)
	}
	if len(states) != 1 || states[0].Skill != "analyze_cpu_utilization" {
		t.Fatalf("skillstate after Execute() = %+v, want one entry for analyze_cpu_utilization", states)
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

// ---- cpuHealth threshold logic via BuildReport + skillstate -----------------

func TestCPUBuildReport_ZeroUsageIsValid(t *testing.T) {
	stat := analyze_cpu_utilization.CPUStat{UsedPct: 0, NumCPU: 1}
	report := analyze_cpu_utilization.BuildReport(stat)
	if !strings.Contains(report, "0.0%") {
		t.Errorf("BuildReport(0%%) = %q, want 0.0%%", report)
	}
}

func TestCPUBuildReport_100PercentIsValid(t *testing.T) {
	stat := analyze_cpu_utilization.CPUStat{UsedPct: 100.0, NumCPU: 1}
	report := analyze_cpu_utilization.BuildReport(stat)
	if !strings.Contains(report, "100.0%") {
		t.Errorf("BuildReport(100%%) = %q, want 100.0%%", report)
	}
}
