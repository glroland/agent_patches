package tests

import (
	"strings"
	"testing"

	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
)

func newIncidentStore(t *testing.T) *incidents.Store {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return incidents.New(mem)
}

func TestIncidents_ReportOpensNewIncident(t *testing.T) {
	s := newIncidentStore(t)

	inc, isNew, err := s.Report("disk-full-var", "/var above 90%", "growing 2%/day", "warning")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !isNew {
		t.Error("Report: want isNew=true for first report")
	}
	if inc.Status != "open" || inc.TimesSeen != 1 {
		t.Errorf("incident = %+v, want status=open timesSeen=1", inc)
	}

	open, err := s.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(open) != 1 || open[0].Fingerprint != "disk-full-var" {
		t.Errorf("Open() = %+v, want one incident disk-full-var", open)
	}
}

func TestIncidents_ReportDedupesByFingerprint(t *testing.T) {
	s := newIncidentStore(t)

	if _, _, err := s.Report("high-cpu-chrome", "chrome at 300% CPU", "", "warning"); err != nil {
		t.Fatalf("Report 1: %v", err)
	}
	// Same problem, different formatting of the fingerprint — must dedupe.
	inc, isNew, err := s.Report("  High CPU Chrome ", "chrome still hot", "", "critical")
	if err != nil {
		t.Fatalf("Report 2: %v", err)
	}
	if isNew {
		t.Error("Report: want isNew=false for recurrence")
	}
	if inc.TimesSeen != 2 {
		t.Errorf("TimesSeen = %d, want 2", inc.TimesSeen)
	}
	if inc.Severity != "critical" {
		t.Errorf("Severity = %q, want refreshed to critical", inc.Severity)
	}

	all, _ := s.All()
	if len(all) != 1 {
		t.Errorf("ledger has %d incidents, want 1 (deduped)", len(all))
	}
}

func TestIncidents_LogActionAndResolve(t *testing.T) {
	s := newIncidentStore(t)

	if _, _, err := s.Report("disk-full-var", "/var above 90%", "", "warning"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	inc, err := s.LogAction("disk-full-var", "proposed clearing /var/log/*.gz — awaiting approval")
	if err != nil {
		t.Fatalf("LogAction: %v", err)
	}
	if len(inc.Actions) != 1 {
		t.Fatalf("Actions = %d, want 1", len(inc.Actions))
	}

	inc, err = s.Resolve("disk-full-var", "cleared 4GB of rotated logs; /var now at 71%")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if inc.Status != "resolved" || inc.ResolvedAt == "" {
		t.Errorf("incident after resolve = %+v, want status=resolved with resolvedAt", inc)
	}

	open, _ := s.Open()
	if len(open) != 0 {
		t.Errorf("Open() = %d incidents after resolve, want 0", len(open))
	}
}

func TestIncidents_ReportReopensResolvedIncident(t *testing.T) {
	s := newIncidentStore(t)

	if _, _, err := s.Report("nfs-stale-backup", "stale NFS mount", "", "warning"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := s.Resolve("nfs-stale-backup", "remounted"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	inc, isNew, err := s.Report("nfs-stale-backup", "stale NFS mount", "", "warning")
	if err != nil {
		t.Fatalf("Report after resolve: %v", err)
	}
	if isNew {
		t.Error("want isNew=false when reopening")
	}
	if inc.Status != "open" || inc.ResolvedAt != "" {
		t.Errorf("reopened incident = %+v, want status=open with cleared resolvedAt", inc)
	}
	if inc.TimesSeen != 2 {
		t.Errorf("TimesSeen = %d, want 2", inc.TimesSeen)
	}
}

func TestIncidents_LogActionUnknownFingerprint_ReturnsError(t *testing.T) {
	s := newIncidentStore(t)
	if _, err := s.LogAction("nope", "note"); err == nil {
		t.Error("LogAction on unknown fingerprint: want error, got nil")
	}
	if _, err := s.Resolve("nope", "note"); err == nil {
		t.Error("Resolve on unknown fingerprint: want error, got nil")
	}
}

func TestIncidents_OpenSummary(t *testing.T) {
	s := newIncidentStore(t)

	if s.OpenSummary() != "" {
		t.Error("OpenSummary on empty ledger: want empty string")
	}

	if _, _, err := s.Report("disk-full-var", "/var above 90%", "", "warning"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := s.LogAction("disk-full-var", "cleanup proposed"); err != nil {
		t.Fatalf("LogAction: %v", err)
	}

	sum := s.OpenSummary()
	for _, want := range []string{"disk-full-var", "/var above 90%", "cleanup proposed", "seen 1 times"} {
		if !strings.Contains(sum, want) {
			t.Errorf("OpenSummary = %q, want it to contain %q", sum, want)
		}
	}
}

func TestIncidents_NilBackingStore_IsSafe(t *testing.T) {
	s := incidents.New(nil)
	if got := s.OpenSummary(); got != "" {
		t.Errorf("OpenSummary on nil-backed store = %q, want empty", got)
	}
	if all, err := s.All(); err != nil || all != nil {
		t.Errorf("All on nil-backed store = (%v, %v), want (nil, nil)", all, err)
	}
	if _, _, err := s.Report("x", "y", "", ""); err == nil {
		t.Error("Report on nil-backed store: want error")
	}
}
