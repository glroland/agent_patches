package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/skills/memoryusage"
)

func TestMemStat_UsedPct_Normal(t *testing.T) {
	s := memoryusage.MemStat{Total: 16 << 30, Available: 4 << 30}
	if got := s.UsedPct(); got != 75.0 {
		t.Errorf("UsedPct() = %.2f, want 75.00", got)
	}
}

func TestMemStat_UsedPct_ZeroTotal(t *testing.T) {
	s := memoryusage.MemStat{}
	if got := s.UsedPct(); got != 0 {
		t.Errorf("UsedPct() with zero total = %.2f, want 0", got)
	}
}

func TestMemStat_Used_Underflow(t *testing.T) {
	s := memoryusage.MemStat{Total: 100, Available: 200}
	if got := s.Used(); got != 0 {
		t.Errorf("Used() with Available>Total = %d, want 0", got)
	}
}

func TestMemStat_SwapUsedPct_Normal(t *testing.T) {
	s := memoryusage.MemStat{SwapTotal: 8 << 30, SwapFree: 2 << 30}
	if got := s.SwapUsedPct(); got != 75.0 {
		t.Errorf("SwapUsedPct() = %.2f, want 75.00", got)
	}
}

func TestMemStat_SwapUsedPct_NoSwap(t *testing.T) {
	s := memoryusage.MemStat{SwapTotal: 0}
	if got := s.SwapUsedPct(); got != 0 {
		t.Errorf("SwapUsedPct() with no swap = %.2f, want 0", got)
	}
}

func TestMemoryUsage_BuildReport_ContainsEssentialFields(t *testing.T) {
	stat := memoryusage.MemStat{
		Total:     16 << 30,
		Available: 2 << 30,
		SwapTotal: 8 << 30,
		SwapFree:  1 << 30,
	}
	report := memoryusage.BuildReport(stat)

	for _, want := range []string{"RAM", "Swap", "16.00 GB", "87.5%", "8.00 GB"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestMemoryUsage_BuildReport_NoSwap(t *testing.T) {
	stat := memoryusage.MemStat{Total: 8 << 30, Available: 1 << 30}
	report := memoryusage.BuildReport(stat)

	if strings.Contains(report, "Swap") {
		t.Errorf("BuildReport should omit Swap section when SwapTotal=0:\n%s", report)
	}
}

func TestNewMemoryUsageTool_NameAndDescription(t *testing.T) {
	tl, err := memoryusage.NewMemoryUsageTool()
	if err != nil {
		t.Fatalf("NewMemoryUsageTool() unexpected error: %v", err)
	}
	if got := tl.Name(); got != "memory_usage" {
		t.Errorf("Name() = %q, want %q", got, "memory_usage")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestMemoryUsageTool_Execute_ReturnsReport(t *testing.T) {
	tl, err := memoryusage.NewMemoryUsageTool()
	if err != nil {
		t.Fatalf("NewMemoryUsageTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if !strings.Contains(result, "RAM") {
		t.Errorf("Execute() result missing RAM section: %q", result)
	}
}
