package analyze_network_utilization

import (
	"testing"
	"time"
)

func history(rates ...[2]float64) []RateSample {
	out := make([]RateSample, len(rates))
	base := time.Now().Add(-24 * time.Hour)
	for i, r := range rates {
		out[i] = RateSample{Time: base.Add(time.Duration(i) * time.Hour), DownMBps: r[0], UpMBps: r[1]}
	}
	return out
}

func TestAnomalyIssue(t *testing.T) {
	normal := history([2]float64{2, 1}, [2]float64{3, 1}, [2]float64{2, 2}, [2]float64{3, 1})

	cases := []struct {
		name     string
		samples  []RateSample
		down, up float64
		wantFlag bool
	}{
		{"young host always escalates", history([2]float64{1, 1}), 0.1, 0.1, true},
		{"within baseline", normal, 3, 1, false},
		{"download spike", normal, 20, 1, true},
		{"upload spike", normal, 2, 10, true},
		{"idle host trivial traffic under floor", history([2]float64{0.01, 0.01}, [2]float64{0.01, 0.01}, [2]float64{0.02, 0.01}, [2]float64{0.01, 0.02}), 0.5, 0.5, false},
		{"idle host real spike over floor", history([2]float64{0.01, 0.01}, [2]float64{0.01, 0.01}, [2]float64{0.02, 0.01}, [2]float64{0.01, 0.02}), 5, 0.1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnomalyIssue(tc.samples, tc.down, tc.up)
			if (got != "") != tc.wantFlag {
				t.Fatalf("AnomalyIssue = %q, want flagged=%v", got, tc.wantFlag)
			}
		})
	}
}
