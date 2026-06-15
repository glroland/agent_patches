package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/analyze_network_utilization"
	"agent_patches/endpoint-server/utils/config"
)

func TestNetworkUsage_BuildReport_ContainsEssentialFields(t *testing.T) {
	report := analyze_network_utilization.BuildReport(200.0, 150.5)
	for _, want := range []string{"Download:", "Upload:", "200.00 MB/s", "150.50 MB/s"} {
		if !strings.Contains(report, want) {
			t.Errorf("BuildReport missing %q\nfull:\n%s", want, report)
		}
	}
}

func TestNetworkUsage_BuildReport_GbpsFormatting(t *testing.T) {
	report := analyze_network_utilization.BuildReport(1200, 500)
	if !strings.Contains(report, "GB/s") {
		t.Errorf("expected GB/s for 1200 MB/s, got:\n%s", report)
	}
}

func TestNetworkUsage_BuildReport_KbpsFormatting(t *testing.T) {
	report := analyze_network_utilization.BuildReport(0.5, 1)
	if !strings.Contains(report, "KB/s") {
		t.Errorf("expected KB/s for 0.5 MB/s, got:\n%s", report)
	}
}

func TestNewNetworkUsageTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_network_utilization.NewNetworkUsageTool(mem)
	if err != nil {
		t.Fatalf("NewNetworkUsageTool() unexpected error: %v", err)
	}
	if got := tl.Name(); got != "analyze_network_utilization" {
		t.Errorf("Name() = %q, want %q", got, "analyze_network_utilization")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestNetworkUsageTool_Execute_ReturnsReport(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := analyze_network_utilization.NewNetworkUsageTool(mem)
	if err != nil {
		t.Fatalf("NewNetworkUsageTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(map[string]float64{"duration_seconds": 0.1})
	result, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if !strings.Contains(result, "Download:") || !strings.Contains(result, "Upload:") {
		t.Errorf("Execute() result missing rate lines: %q", result)
	}
}
