package tests

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
)

// failTransport is an http.RoundTripper that always returns an error,
// used to disable network calls during unit tests.
type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("test: HTTP disabled")
}

var noHTTP = &http.Client{Transport: failTransport{}}

// warnFailHandler is an slog.Handler that fails the test on any Warn-or-above
// log record, surfacing swallowed errors that would otherwise pass silently.
type warnFailHandler struct {
	t *testing.T
}

func (h warnFailHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h warnFailHandler) Handle(_ context.Context, r slog.Record) error {
	h.t.Errorf("unexpected %s log: %s", r.Level, r.Message)
	return nil
}

func (h warnFailHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h warnFailHandler) WithGroup(_ string) slog.Handler      { return h }

// failOnWarnLogs installs a handler that fails t if any Warn-or-above log is
// emitted during the test, and returns a func that restores the prior default.
func failOnWarnLogs(t *testing.T) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(warnFailHandler{t: t}))
	return func() { slog.SetDefault(prev) }
}

// ---- ParseDebianInstLines ---------------------------------------------------

func TestParseDebianInstLines_Basic(t *testing.T) {
	output := `Reading package lists... Done
Inst curl [7.81.0-1ubuntu1.15] (8.5.0-2ubuntu10.6 Ubuntu:24.04/noble-updates [amd64])
Inst libssl3:amd64 [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.16 Ubuntu:24.04/noble-updates [amd64])
Conf curl (8.5.0-2ubuntu10.6 Ubuntu:24.04/noble-updates [amd64])
2 upgraded, 0 newly installed, 0 to remove`

	pkgs := patching.ParseDebianInstLines(output)
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0].Name != "curl" {
		t.Errorf("pkgs[0].Name = %q, want %q", pkgs[0].Name, "curl")
	}
	if pkgs[0].OldVersion != "7.81.0-1ubuntu1.15" {
		t.Errorf("pkgs[0].OldVersion = %q", pkgs[0].OldVersion)
	}
	if pkgs[0].NewVersion != "8.5.0-2ubuntu10.6" {
		t.Errorf("pkgs[0].NewVersion = %q", pkgs[0].NewVersion)
	}
	if pkgs[1].Name != "libssl3" {
		t.Errorf("pkgs[1].Name = %q, want %q", pkgs[1].Name, "libssl3")
	}
}

func TestParseDebianInstLines_NoOldVersion(t *testing.T) {
	output := "Inst newpkg (1.2.3 repo [amd64])\n"
	pkgs := patching.ParseDebianInstLines(output)
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].OldVersion != "" {
		t.Errorf("OldVersion should be empty for new install, got %q", pkgs[0].OldVersion)
	}
}

func TestParseDebianInstLines_Empty(t *testing.T) {
	pkgs := patching.ParseDebianInstLines("0 upgraded, 0 newly installed")
	if len(pkgs) != 0 {
		t.Errorf("expected 0 packages from no-update output, got %d", len(pkgs))
	}
}

// ---- ParseFedoraCheckUpdate -------------------------------------------------

func TestParseFedoraCheckUpdate_Basic(t *testing.T) {
	output := `Last metadata expiration check: 1:00:00 ago.

curl.x86_64                        8.0.1-1.fc40        updates
libcurl.x86_64                     8.0.1-1.fc40        updates
python3.x86_64                     3.12.3-1.fc40       updates

Obsoleting Packages
oldfoo.x86_64                      1.0-1.fc40          updates
`
	pkgs := patching.ParseFedoraCheckUpdate(output)
	if len(pkgs) != 3 {
		t.Fatalf("expected 3 packages, got %d: %v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "curl" {
		t.Errorf("pkgs[0].Name = %q, want %q", pkgs[0].Name, "curl")
	}
	if pkgs[0].NewVersion != "8.0.1-1.fc40" {
		t.Errorf("pkgs[0].NewVersion = %q", pkgs[0].NewVersion)
	}
	// Verify the Obsoleting section is skipped.
	for _, p := range pkgs {
		if p.Name == "oldfoo" {
			t.Error("Obsoleting Packages section should be skipped")
		}
	}
}

func TestParseFedoraCheckUpdate_Empty(t *testing.T) {
	pkgs := patching.ParseFedoraCheckUpdate("Last metadata expiration check: 0:00:01 ago.\n")
	if len(pkgs) != 0 {
		t.Errorf("expected 0 packages, got %d", len(pkgs))
	}
}

// ---- ParseDarwinUpdateList --------------------------------------------------

func TestParseDarwinUpdateList_Basic(t *testing.T) {
	output := `Software Update Tool

Finding available software

Software Update found the following new or updated software:
* Label: macOS Sequoia 15.4.1-24E263
	Title: macOS Sequoia 15.4.1, Version: 15.4.1, Size: 3455256KiB, Recommended: YES, Action: restart,
* Label: Safari 18.4.1-20621.1.15.11.10
	Title: Safari 18.4.1, Version: 18.4.1, Size: 81768KiB, Recommended: YES, Action: restart,
`
	pkgs := patching.ParseDarwinUpdateList(output)
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0].Name != "macOS Sequoia 15.4.1-24E263" {
		t.Errorf("pkgs[0].Name = %q", pkgs[0].Name)
	}
	if pkgs[0].Description != "macOS Sequoia 15.4.1" {
		t.Errorf("pkgs[0].Description = %q", pkgs[0].Description)
	}
	if pkgs[1].Name != "Safari 18.4.1-20621.1.15.11.10" {
		t.Errorf("pkgs[1].Name = %q", pkgs[1].Name)
	}
}

// ---- StripBuildNumber -------------------------------------------------------

func TestStripBuildNumber(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"macOS Sequoia 15.4.1-24E263", "macOS Sequoia 15.4.1"},
		{"Safari 18.4.1-20621.1.15.11.10", "Safari 18.4.1"},
		{"macOS Sequoia 15.4.1", "macOS Sequoia 15.4.1"},
		{"Xcode 16.3", "Xcode 16.3"},
	}
	for _, tc := range cases {
		got := patching.StripBuildNumber(tc.in)
		if got != tc.out {
			t.Errorf("StripBuildNumber(%q) = %q, want %q", tc.in, got, tc.out)
		}
	}
}

// ---- ExtractCVEIDs ----------------------------------------------------------

func TestExtractCVEIDs(t *testing.T) {
	s := "CVE-2024-1234 and CVE-2024-56789 appear here. CVE-2024-1234 again."
	ids := patching.ExtractCVEIDs(s)
	if len(ids) != 2 {
		t.Errorf("expected 2 unique CVE IDs, got %d: %v", len(ids), ids)
	}
	if ids[0] != "CVE-2024-1234" || ids[1] != "CVE-2024-56789" {
		t.Errorf("unexpected ids: %v", ids)
	}
}

func TestExtractCVEIDs_None(t *testing.T) {
	ids := patching.ExtractCVEIDs("no CVE references here")
	if len(ids) != 0 {
		t.Errorf("expected 0 CVE IDs, got %v", ids)
	}
}

// ---- FormatUpdateReport -----------------------------------------------------

func TestFormatUpdateReport_Empty(t *testing.T) {
	out := patching.FormatUpdateReport(nil)
	if !strings.Contains(out, "No updates") {
		t.Errorf("FormatUpdateReport(nil) = %q, want 'No updates'", out)
	}
}

func TestFormatUpdateReport_WithCVEs(t *testing.T) {
	updates := []patching.PackageUpdate{
		{
			Name:        "curl",
			OldVersion:  "7.81.0",
			NewVersion:  "8.5.0",
			Description: "command line tool for transferring data",
			CVEs: []patching.CVEInfo{
				{ID: "CVE-2024-1234", Severity: "HIGH", CVSSScore: 7.5, URL: "https://ubuntu.com/security/CVE-2024-1234"},
				{ID: "CVE-2024-5678", Severity: "MEDIUM", CVSSScore: 5.0},
			},
		},
		{
			Name:       "openssl",
			NewVersion: "3.0.15",
			CVEs:       nil,
		},
	}
	out := patching.FormatUpdateReport(updates)

	for _, want := range []string{"Package: curl", "7.81.0", "8.5.0", "CVE-2024-1234", "HIGH", "7.5", "CVE-2024-5678", "Package: openssl", "none identified"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatUpdateReport output missing %q:\n%s", want, out)
		}
	}
}

// ---- ListUpdates — Debian (mock commander, no HTTP) -------------------------

func TestListUpdates_Debian_PackageParsing(t *testing.T) {
	nvdResp := `{"vulnerabilities":[{"cve":{"metrics":{"cvssMetricV31":[{"cvssData":{"baseScore":7.5,"baseSeverity":"HIGH"}}]}}}]}`

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(r.URL.Path, "/security/api/v1/cves") && strings.Contains(r.URL.RawQuery, "package=curl"):
			body = `{"results":[{"id":"CVE-2024-1234","priority":"high"}]}`
		case strings.Contains(r.URL.Path, "/security/api/v1/cves") && strings.Contains(r.URL.RawQuery, "package=libssl3"):
			body = `{"results":[]}`
		case strings.Contains(r.URL.Host, "nvd"):
			body = nvdResp
		default:
			return nil, fmt.Errorf("unexpected request to %s", r.URL)
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get": {output: `Reading package lists... Done
Inst curl [7.81.0-1ubuntu1.15] (8.5.0-2ubuntu10.6 Ubuntu [amd64])
Inst libssl3:amd64 [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.16 Ubuntu [amd64])
2 upgraded, 0 newly installed`},
			"apt-cache": {output: "Description-en: command line tool for transferring data with URL syntax\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	p.HTTPClient = &http.Client{Transport: transport}

	defer failOnWarnLogs(t)()

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	if updates[0].Name != "curl" {
		t.Errorf("updates[0].Name = %q, want %q", updates[0].Name, "curl")
	}
	if updates[0].Description != "command line tool for transferring data with URL syntax" {
		t.Errorf("unexpected description: %q", updates[0].Description)
	}
	if len(updates[0].CVEs) != 1 || updates[0].CVEs[0].ID != "CVE-2024-1234" {
		t.Errorf("expected CVE-2024-1234 for curl, got %v", updates[0].CVEs)
	}
	if len(updates[1].CVEs) != 0 {
		t.Errorf("expected no CVEs for libssl3, got %v", updates[1].CVEs)
	}
}

// ---- ListUpdates — Fedora (mock commander, no HTTP) -------------------------

func TestListUpdates_Fedora_PackageParsing(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf": {
				output:   "curl.x86_64   8.0.1-1.fc40   updates\nlibcurl.x86_64   8.0.1-1.fc40   updates\n",
				exitCode: 100,
			},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	p.HTTPClient = noHTTP

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d: %v", len(updates), updates)
	}
	if updates[0].Name != "curl" {
		t.Errorf("updates[0].Name = %q, want %q", updates[0].Name, "curl")
	}
}

// ---- ListUpdates — Darwin (mock commander, no HTTP) -------------------------

func TestListUpdates_Darwin_PackageParsing(t *testing.T) {
	releaseIndexHTML := `<a href="/en-us/100001">About the security content of macOS Sequoia 15.4.1</a>`
	releasePageHTML := `<p>Fixes CVE-2024-1234.</p>`
	nvdResp := `{"vulnerabilities":[{"cve":{"metrics":{"cvssMetricV31":[{"cvssData":{"baseScore":7.5,"baseSeverity":"HIGH"}}]}}}]}`

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case r.URL.Host == "support.apple.com" && r.URL.Path == "/en-us/100100":
			body = releaseIndexHTML
		case r.URL.Host == "support.apple.com" && r.URL.Path == "/en-us/100001":
			body = releasePageHTML
		case strings.Contains(r.URL.Host, "nvd"):
			body = nvdResp
		default:
			return nil, fmt.Errorf("unexpected request to %s", r.URL)
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"softwareupdate": {output: `Software Update Tool

* Label: macOS Sequoia 15.4.1-24E263
	Title: macOS Sequoia 15.4.1, Version: 15.4.1, Size: 1234KiB, Recommended: YES, Action: restart,
`},
		},
	}
	p := patching.NewWithCommander(patching.OSDarwin, cmdr)
	p.HTTPClient = &http.Client{Transport: transport}

	defer failOnWarnLogs(t)()

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Name != "macOS Sequoia 15.4.1-24E263" {
		t.Errorf("updates[0].Name = %q", updates[0].Name)
	}
	if len(updates[0].CVEs) != 1 || updates[0].CVEs[0].ID != "CVE-2024-1234" {
		t.Errorf("expected CVE-2024-1234, got %v", updates[0].CVEs)
	}
}

// ---- ListUpdates — Windows (mock commander) ---------------------------------

func TestListUpdates_Windows_ParsesTitle(t *testing.T) {
	// The PowerShell is mocked; simulate tab-separated output that our parser expects.
	// Windows update info uses ||| delimiter.
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "UPDATE|||2024-06 Cumulative Update for Windows 10 (KB5034765)|||KB5034765|||Security update|||https://support.microsoft.com/kb/5034765\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	p.HTTPClient = noHTTP

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if !strings.Contains(updates[0].Name, "KB5034765") {
		t.Errorf("expected KB5034765 in name, got %q", updates[0].Name)
	}
}

// ---- ListUpdates — mock HTTP server with Canonical API response -------------

func TestListUpdates_Debian_WithCanonicalAPI(t *testing.T) {
	// Set up a mock HTTP server that returns a Canonical-format JSON response.
	canonicalResp := `{"results":[{"id":"CVE-2024-1234","priority":"high"},{"id":"CVE-2024-5678","priority":"critical"}]}`
	nvdResp := `{"vulnerabilities":[{"cve":{"metrics":{"cvssMetricV31":[{"cvssData":{"baseScore":7.5,"baseSeverity":"HIGH"}}]}}}]}`

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := canonicalResp
		if strings.Contains(r.URL.Host, "nvd") || strings.Contains(r.URL.Path, "nvd") ||
			strings.Contains(r.URL.String(), "nvd.nist.gov") {
			body = nvdResp
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get":   {output: "Inst curl [7.81.0] (8.5.0 Ubuntu [amd64])\n"},
			"apt-cache": {output: "Description-en: transfer tool\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	p.HTTPClient = &http.Client{Transport: transport}

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if len(updates[0].CVEs) != 2 {
		t.Errorf("expected 2 CVEs, got %d: %v", len(updates[0].CVEs), updates[0].CVEs)
	}
	if updates[0].CVEs[0].ID != "CVE-2024-1234" {
		t.Errorf("CVE[0].ID = %q", updates[0].CVEs[0].ID)
	}
	if updates[0].CVEs[0].Severity != "HIGH" {
		t.Errorf("CVE[0].Severity = %q, want HIGH", updates[0].CVEs[0].Severity)
	}
}

// ---- ListUpdates — Debian changelog delta scoping ----------------------------

func TestListUpdates_Debian_ChangelogDelta(t *testing.T) {
	// The Ubuntu API reports both a current CVE and a stale one fixed long ago.
	// The changelog delta between installed and candidate versions names only
	// the current CVE — the approval must not resurrect the stale one.
	canonicalResp := `{"results":[{"id":"CVE-2025-1111","priority":"medium"},{"id":"CVE-2018-0000","priority":"high"}]}`

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/security/api/v1/cves") {
			return nil, fmt.Errorf("unexpected request to %s", r.URL)
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(canonicalResp)),
			Header:     make(http.Header),
		}, nil
	})

	changelog := `curl (8.5.0-2ubuntu10.6) noble-security; urgency=medium

  * SECURITY UPDATE: cookie handling
    - CVE-2025-1111

 -- Ubuntu Security <security@ubuntu.com>  Mon, 02 Jun 2025 10:00:00 +0000

curl (8.5.0-2ubuntu10.5) noble; urgency=medium

  * Old entry that once fixed CVE-2018-0000

 -- Ubuntu Developers <devel@ubuntu.com>  Mon, 05 May 2025 10:00:00 +0000
`

	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get":           {output: "Inst curl [8.5.0-2ubuntu10.5] (8.5.0-2ubuntu10.6 Ubuntu [amd64])\n"},
			"apt-get changelog": {output: changelog},
			"apt-cache":         {output: "Description-en: transfer tool\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	p.HTTPClient = &http.Client{Transport: transport}

	defer failOnWarnLogs(t)()

	updates, err := p.ListUpdates(context.Background())
	if err != nil {
		t.Fatalf("ListUpdates() error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].CVELookupFailed {
		t.Error("CVELookupFailed should be false")
	}
	if len(updates[0].CVEs) != 1 || updates[0].CVEs[0].ID != "CVE-2025-1111" {
		t.Fatalf("expected only the delta CVE-2025-1111, got %v", updates[0].CVEs)
	}
	if updates[0].CVEs[0].Severity != "MEDIUM" {
		t.Errorf("Severity = %q, want MEDIUM (from Ubuntu priority)", updates[0].CVEs[0].Severity)
	}
}

// ---- DebianChangelogCVEsSince -------------------------------------------------

func TestDebianChangelogCVEsSince_Boundary(t *testing.T) {
	changelog := `pkg (2.0-1) noble; urgency=high

  * SECURITY UPDATE
    - CVE-2025-1111
    - CVE-2025-2222

 -- someone  Mon, 02 Jun 2025 10:00:00 +0000

pkg (1.9-1) noble; urgency=medium

  * older fix for CVE-2020-9999

 -- someone  Mon, 05 May 2025 10:00:00 +0000
`
	ids, ok := patching.DebianChangelogCVEsSince(changelog, "1.9-1")
	if !ok {
		t.Fatal("expected boundary to be found")
	}
	if len(ids) != 2 || ids[0] != "CVE-2025-1111" || ids[1] != "CVE-2025-2222" {
		t.Errorf("ids = %v, want the two delta CVEs only", ids)
	}
}

func TestDebianChangelogCVEsSince_NoBoundary(t *testing.T) {
	changelog := "pkg (2.0-1) noble; urgency=high\n\n  * fixes CVE-2025-1111\n"
	ids, ok := patching.DebianChangelogCVEsSince(changelog, "1.0-1")
	if ok {
		t.Error("boundary should not be found for a version absent from the changelog")
	}
	if ids != nil {
		t.Errorf("ids should be nil when the delta cannot be established, got %v", ids)
	}
}

// ---- ImportanceAssessment -----------------------------------------------------

func TestImportanceAssessment(t *testing.T) {
	cases := []struct {
		name           string
		updates        []patching.PackageUpdate
		wantImportance string
		wantIn         string // substring expected in the rationale
	}{
		{
			name: "critical CVE is high importance",
			updates: []patching.PackageUpdate{
				{Name: "openssl", CVEs: []patching.CVEInfo{{ID: "CVE-1", Severity: "CRITICAL"}}},
			},
			wantImportance: "high",
			wantIn:         "CRITICAL",
		},
		{
			name: "medium CVEs are medium importance with rationale",
			updates: []patching.PackageUpdate{
				{Name: "curl", CVEs: []patching.CVEInfo{{ID: "CVE-1", Severity: "MEDIUM"}, {ID: "CVE-2", Severity: "LOW"}}},
			},
			wantImportance: "medium",
			wantIn:         "none rated CRITICAL",
		},
		{
			name:           "no CVEs is low importance",
			updates:        []patching.PackageUpdate{{Name: "tzdata"}},
			wantImportance: "low",
			wantIn:         "routine",
		},
		{
			name: "mostly failed lookups cannot be low importance",
			updates: []patching.PackageUpdate{
				{Name: "a", CVELookupFailed: true},
				{Name: "b", CVELookupFailed: true},
				{Name: "c"},
			},
			wantImportance: "medium",
			wantIn:         "unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			importance, rationale := patching.ImportanceAssessment(tc.updates)
			if importance != tc.wantImportance {
				t.Errorf("importance = %q, want %q", importance, tc.wantImportance)
			}
			if !strings.Contains(rationale, tc.wantIn) {
				t.Errorf("rationale %q missing %q", rationale, tc.wantIn)
			}
		})
	}
}

// ---- RiskAssessment -------------------------------------------------------------
//
// Risk is the operational-disruption dimension: independent of CVE severity,
// it depends on reboot requirements, blast radius, and whether core server
// packages are touched.

func TestRiskAssessment(t *testing.T) {
	cases := []struct {
		name     string
		updates  []patching.PackageUpdate
		wantRisk string
		wantIn   string
	}{
		{
			name: "critical CVE on a small, non-core update is still low risk",
			updates: []patching.PackageUpdate{
				{Name: "acme-widget-lib", CVEs: []patching.CVEInfo{{ID: "CVE-1", Severity: "CRITICAL"}}},
			},
			wantRisk: "low",
			wantIn:   "no reboot",
		},
		{
			name:     "routine update with no CVEs is low risk",
			updates:  []patching.PackageUpdate{{Name: "tzdata"}},
			wantRisk: "low",
			wantIn:   "no reboot",
		},
		{
			name:     "core server package with no CVEs is still medium risk",
			updates:  []patching.PackageUpdate{{Name: "postgresql"}},
			wantRisk: "medium",
			wantIn:   "core server package",
		},
		{
			name:     "kernel update requires a reboot — medium risk",
			updates:  []patching.PackageUpdate{{Name: "kernel-core"}},
			wantRisk: "medium",
			wantIn:   "reboot",
		},
		{
			name: "reboot plus a large, core-touching batch is high risk",
			updates: func() []patching.PackageUpdate {
				updates := []patching.PackageUpdate{{Name: "kernel-core"}, {Name: "postgresql"}}
				for i := 0; i < 25; i++ {
					updates = append(updates, patching.PackageUpdate{Name: fmt.Sprintf("pkg%d", i)})
				}
				return updates
			}(),
			wantRisk: "high",
			wantIn:   "reboot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			risk, rationale := patching.RiskAssessment(tc.updates)
			if risk != tc.wantRisk {
				t.Errorf("risk = %q, want %q (%s)", risk, tc.wantRisk, rationale)
			}
			if !strings.Contains(rationale, tc.wantIn) {
				t.Errorf("rationale %q missing %q", rationale, tc.wantIn)
			}
		})
	}
}

// ---- FormatUpdateSummary — new content ----------------------------------------

func TestFormatUpdateSummary_MediumCVEsVisible(t *testing.T) {
	updates := []patching.PackageUpdate{
		{
			Name:       "curl",
			NewVersion: "8.5.0",
			CVEs: []patching.CVEInfo{
				{ID: "CVE-2024-1111", Severity: "MEDIUM"},
				{ID: "CVE-2024-2222", Severity: "LOW"},
			},
		},
		{Name: "tzdata", NewVersion: "2024a"},
	}
	out := patching.FormatUpdateSummary("host01", patching.OSDebian, updates)

	for _, want := range []string{"Risk: MEDIUM", "CVE-2024-1111 (MEDIUM)", "CVE-2024-2222 (LOW)", "curl → 8.5.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatUpdateSummary missing %q:\n%s", want, out)
		}
	}
}

func TestFormatUpdateSummary_LookupFailuresCalledOut(t *testing.T) {
	updates := []patching.PackageUpdate{
		{Name: "curl", NewVersion: "8.5.0", CVELookupFailed: true},
		{Name: "tzdata", NewVersion: "2024a"},
	}
	out := patching.FormatUpdateSummary("host01", patching.OSDebian, updates)

	if !strings.Contains(out, "CVE data unavailable for 1 package(s): curl") {
		t.Errorf("summary should call out failed CVE lookups:\n%s", out)
	}
	if !strings.Contains(out, "Risk: MEDIUM") {
		t.Errorf("mostly-failed lookups should not present as low risk:\n%s", out)
	}
}

func TestFormatUpdateSummary_RiskHeaderLow(t *testing.T) {
	updates := []patching.PackageUpdate{{Name: "tzdata", NewVersion: "2024a"}}
	out := patching.FormatUpdateSummary("host01", patching.OSDebian, updates)
	if !strings.Contains(out, "Risk: LOW") || !strings.Contains(out, "routine") {
		t.Errorf("expected a low-risk rationale header:\n%s", out)
	}
}

// ---- FormatFallbackSummary ------------------------------------------------------

func TestFormatFallbackSummary(t *testing.T) {
	raw := "Inst curl [8.5.0] (8.6.0 Ubuntu [amd64])\n1 upgraded, 0 newly installed"
	out := patching.FormatFallbackSummary(raw, fmt.Errorf("network unreachable"))

	for _, want := range []string{"Risk: MEDIUM", "network unreachable", "Inst curl", "could not be assessed"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatFallbackSummary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "up to date") {
		t.Errorf("fallback summary must never claim the system is up to date:\n%s", out)
	}
}

// roundTripFunc is an http.RoundTripper backed by a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
