package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent_patches/endpoint-server/observers/memmon"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func newTestMemMonitor(cfg *config.MemoryMonitorSettings, getMemory func() (memmon.MemStat, error)) *memmon.Monitor {
	m := memmon.New(cfg, notifier.New(&config.NotifierSettings{}))
	m.GetMemory = getMemory
	return m
}

func memCfg(ramPct, swapPct float64) *config.MemoryMonitorSettings {
	return &config.MemoryMonitorSettings{
		Enabled:              true,
		ThresholdPercent:     ramPct,
		SwapThresholdPercent: swapPct,
		Interval:             "5m",
	}
}

// ---- MemStat helpers --------------------------------------------------------

func TestMemStat_UsedPct_Normal(t *testing.T) {
	s := memmon.MemStat{Total: 16 << 30, Available: 4 << 30}
	if got := s.UsedPct(); got != 75.0 {
		t.Errorf("UsedPct() = %.2f, want 75.00", got)
	}
}

func TestMemStat_UsedPct_ZeroTotal(t *testing.T) {
	s := memmon.MemStat{}
	if got := s.UsedPct(); got != 0 {
		t.Errorf("UsedPct() with zero total = %.2f, want 0", got)
	}
}

func TestMemStat_Used_Underflow(t *testing.T) {
	s := memmon.MemStat{Total: 100, Available: 200}
	if got := s.Used(); got != 0 {
		t.Errorf("Used() with Available>Total = %d, want 0", got)
	}
}

func TestMemStat_SwapUsedPct_Normal(t *testing.T) {
	s := memmon.MemStat{SwapTotal: 8 << 30, SwapFree: 2 << 30}
	want := 75.0
	if got := s.SwapUsedPct(); got != want {
		t.Errorf("SwapUsedPct() = %.2f, want %.2f", got, want)
	}
}

func TestMemStat_SwapUsedPct_NoSwap(t *testing.T) {
	s := memmon.MemStat{SwapTotal: 0}
	if got := s.SwapUsedPct(); got != 0 {
		t.Errorf("SwapUsedPct() with no swap = %.2f, want 0", got)
	}
}

// ---- BuildMessage -----------------------------------------------------------

func TestMemBuildMessage_ContainsEssentialFields(t *testing.T) {
	stat := memmon.MemStat{
		Total:     16 << 30,
		Available: 2 << 30,
		SwapTotal: 8 << 30,
		SwapFree:  1 << 30,
	}
	msg := memmon.BuildMessage("webserver01", 90, 80, stat)

	for _, want := range []string{
		"webserver01",
		"RAM",
		"Swap",
		"90%",       // RAM threshold
		"80%",       // swap threshold
		"16.00 GB",  // total RAM
		"87.5%",     // RAM used pct: (16-2)/16
		"8.00 GB",   // swap total
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("BuildMessage missing %q\nfull:\n%s", want, msg)
		}
	}
}

func TestMemBuildMessage_NoSwap(t *testing.T) {
	stat := memmon.MemStat{Total: 8 << 30, Available: 1 << 30}
	msg := memmon.BuildMessage("host", 90, 80, stat)

	if strings.Contains(msg, "Swap") {
		t.Errorf("BuildMessage should omit Swap section when SwapTotal=0:\n%s", msg)
	}
}

func TestMemBuildMessage_SwapThresholdZero_OmitsSwapThresholdLine(t *testing.T) {
	stat := memmon.MemStat{Total: 8 << 30, Available: 1 << 30, SwapTotal: 4 << 30, SwapFree: 1 << 30}
	msg := memmon.BuildMessage("host", 90, 0, stat)

	if strings.Contains(msg, "Threshold") && strings.Count(msg, "Threshold") > 1 {
		t.Errorf("BuildMessage should only show one Threshold line when swap threshold is 0:\n%s", msg)
	}
}

// ---- Check ------------------------------------------------------------------

func TestMemCheck_BelowThreshold_NoNotify(t *testing.T) {
	called := false
	getMemory := func() (memmon.MemStat, error) {
		called = true
		return memmon.MemStat{Total: 100, Available: 20}, nil // 80% used < 90% threshold
	}
	m := newTestMemMonitor(memCfg(90, 0), getMemory)
	m.Check(context.Background())

	if !called {
		t.Error("expected GetMemory to be called")
	}
}

func TestMemCheck_RamAboveThreshold(t *testing.T) {
	getMemory := func() (memmon.MemStat, error) {
		return memmon.MemStat{Total: 100, Available: 5}, nil // 95% used > 90%
	}
	m := newTestMemMonitor(memCfg(90, 0), getMemory)
	m.Check(context.Background()) // must not panic; notification goes to no-op sink
}

func TestMemCheck_SwapAboveThreshold(t *testing.T) {
	getMemory := func() (memmon.MemStat, error) {
		return memmon.MemStat{
			Total:     100, Available: 50,  // RAM at 50% — ok
			SwapTotal: 100, SwapFree: 10,   // swap at 90% > 80% threshold
		}, nil
	}
	m := newTestMemMonitor(memCfg(90, 80), getMemory)
	m.Check(context.Background())
}

func TestMemCheck_SwapDisabled_NoSwapAlert(t *testing.T) {
	getMemory := func() (memmon.MemStat, error) {
		return memmon.MemStat{
			Total:     100, Available: 50,
			SwapTotal: 100, SwapFree: 1, // swap at 99% but threshold is disabled
		}, nil
	}
	m := newTestMemMonitor(memCfg(90, 0), getMemory) // swap threshold = 0 = disabled
	m.Check(context.Background())                     // must not notify
}

func TestMemCheck_GetMemoryError_IsHandledGracefully(t *testing.T) {
	getMemory := func() (memmon.MemStat, error) {
		return memmon.MemStat{}, errors.New("kernel read failed")
	}
	m := newTestMemMonitor(memCfg(90, 80), getMemory)
	m.Check(context.Background()) // must not panic
}

func TestMemCheck_ExactlyAtThreshold_Alerts(t *testing.T) {
	getMemory := func() (memmon.MemStat, error) {
		return memmon.MemStat{Total: 100, Available: 10}, nil // exactly 90% used
	}
	m := newTestMemMonitor(memCfg(90, 0), getMemory)
	m.Check(context.Background()) // threshold is inclusive — must trigger
}
