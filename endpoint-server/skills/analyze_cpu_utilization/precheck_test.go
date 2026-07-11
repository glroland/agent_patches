package analyze_cpu_utilization

import "testing"

func TestNeedsInvestigation(t *testing.T) {
	cases := []struct {
		name string
		stat CPUStat
		want bool
	}{
		{"idle", CPUStat{UsedPct: 5, NumCPU: 8, LoadAvg1: 0.5, LoadAvg5: 0.4, LoadAvg15: 0.3}, false},
		{"moderate", CPUStat{UsedPct: 79.9, NumCPU: 8, LoadAvg1: 7.9, LoadAvg5: 6, LoadAvg15: 5}, false},
		{"high usage", CPUStat{UsedPct: 80, NumCPU: 8, LoadAvg1: 1}, true},
		{"load1 over cpus", CPUStat{UsedPct: 40, NumCPU: 4, LoadAvg1: 4.5}, true},
		{"load15 over cpus", CPUStat{UsedPct: 40, NumCPU: 4, LoadAvg15: 4.1}, true},
		{"unknown cpu count ignores load", CPUStat{UsedPct: 40, NumCPU: 0, LoadAvg1: 12}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsInvestigation(tc.stat); got != tc.want {
				t.Fatalf("NeedsInvestigation(%+v) = %v, want %v", tc.stat, got, tc.want)
			}
		})
	}
}
