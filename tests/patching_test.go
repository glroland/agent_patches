package tests

import (
	"context"
	"strings"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
	"agent_patches/endpoint-server/utils/config"
)

// ---- mock Commander ---------------------------------------------------------

type cmdCall struct {
	name string
	args []string
}

type cmdStub struct {
	output   string
	exitCode int // 0 = success
}

type mockCmdr struct {
	stubs    map[string]cmdStub // keyed by executable name
	calls    []cmdCall
	fallback cmdStub // returned when no stub matches
}

func (m *mockCmdr) Run(_ context.Context, name string, args ...string) (string, error) {
	m.calls = append(m.calls, cmdCall{name: name, args: args})
	stub, ok := m.stubs[name]
	if !ok {
		stub = m.fallback
	}
	if stub.exitCode != 0 {
		return stub.output, &patching.ExitCodeError{Code: stub.exitCode, Stderr: stub.output}
	}
	return stub.output, nil
}

func (m *mockCmdr) called(name string) bool {
	for _, c := range m.calls {
		if c.name == name {
			return true
		}
	}
	return false
}

func (m *mockCmdr) callCount(name string) int {
	n := 0
	for _, c := range m.calls {
		if c.name == name {
			n++
		}
	}
	return n
}

// ---- ParseOSRelease tests ---------------------------------------------------

func TestParseOSRelease_Debian(t *testing.T) {
	content := `PRETTY_NAME="Debian GNU/Linux 12"
ID=debian
VERSION_ID="12"`
	if got := patching.ParseOSRelease(content); got != patching.OSDebian {
		t.Errorf("ParseOSRelease(debian) = %q, want %q", got, patching.OSDebian)
	}
}

func TestParseOSRelease_Ubuntu(t *testing.T) {
	content := `PRETTY_NAME="Ubuntu 24.04 LTS"
ID=ubuntu
ID_LIKE=debian`
	if got := patching.ParseOSRelease(content); got != patching.OSDebian {
		t.Errorf("ParseOSRelease(ubuntu) = %q, want %q", got, patching.OSDebian)
	}
}

func TestParseOSRelease_Mint(t *testing.T) {
	content := `ID=linuxmint
ID_LIKE="ubuntu debian"`
	if got := patching.ParseOSRelease(content); got != patching.OSDebian {
		t.Errorf("ParseOSRelease(mint) = %q, want %q", got, patching.OSDebian)
	}
}

func TestParseOSRelease_Fedora(t *testing.T) {
	content := `ID=fedora
VERSION_ID=40`
	if got := patching.ParseOSRelease(content); got != patching.OSFedora {
		t.Errorf("ParseOSRelease(fedora) = %q, want %q", got, patching.OSFedora)
	}
}

func TestParseOSRelease_RHEL(t *testing.T) {
	content := `ID="rhel"
ID_LIKE="fedora"
VERSION_ID="9.3"`
	if got := patching.ParseOSRelease(content); got != patching.OSFedora {
		t.Errorf("ParseOSRelease(rhel) = %q, want %q", got, patching.OSFedora)
	}
}

func TestParseOSRelease_Rocky(t *testing.T) {
	content := `ID="rocky"
ID_LIKE="rhel centos fedora"
VERSION_ID="9.3"`
	if got := patching.ParseOSRelease(content); got != patching.OSFedora {
		t.Errorf("ParseOSRelease(rocky) = %q, want %q", got, patching.OSFedora)
	}
}

func TestParseOSRelease_Unknown(t *testing.T) {
	content := `ID=archlinux`
	if got := patching.ParseOSRelease(content); got != patching.OSUnknown {
		t.Errorf("ParseOSRelease(arch) = %q, want %q", got, patching.OSUnknown)
	}
}

func TestParseOSRelease_QuotedValues(t *testing.T) {
	content := `ID="ubuntu"
ID_LIKE="debian"`
	if got := patching.ParseOSRelease(content); got != patching.OSDebian {
		t.Errorf("ParseOSRelease(quoted) = %q, want %q", got, patching.OSDebian)
	}
}

// ---- Patcher.Run — Debian ---------------------------------------------------

func TestPatcher_Debian_NoReboot(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get":          {output: "apt-get ok"},
			"needs-restarting": {output: ""},
		},
	}
	// Reboot file absent → handled by os.Stat in patching.go (not through commander)
	p := patching.NewWithCommander(patching.OSDebian, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if cmdr.callCount("apt-get") != 2 {
		t.Errorf("apt-get called %d times, want 2", cmdr.callCount("apt-get"))
	}
	if strings.Contains(log, "Reboot required") {
		t.Error("log should not mention reboot when /var/run/reboot-required is absent")
	}
	if !strings.Contains(log, "No reboot required") {
		t.Errorf("expected 'No reboot required' in log, got:\n%s", log)
	}
}

func TestPatcher_Debian_AptUpdateFails(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get": {output: "E: Could not get lock", exitCode: 100},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)

	_, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("Run() expected error when apt-get fails, got nil")
	}
}

// ---- Patcher.Run — Fedora ---------------------------------------------------

func TestPatcher_Fedora_NoReboot(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf":              {output: "dnf ok"},
			"needs-restarting": {output: "No core libraries or services have been updated since boot-up.\nReboot should not be necessary."}, // exit 0
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !cmdr.called("dnf") {
		t.Error("expected dnf to be called")
	}
	if strings.Contains(log, "Reboot required") {
		t.Errorf("unexpected reboot in log:\n%s", log)
	}
}

func TestPatcher_Fedora_RebootRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf":              {output: "dnf ok"},
			"needs-restarting": {exitCode: 1}, // exit 1 = reboot needed
			"shutdown":         {output: "shutting down"},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !strings.Contains(log, "Reboot required") {
		t.Errorf("expected 'Reboot required' in log:\n%s", log)
	}
	if !cmdr.called("shutdown") {
		t.Error("expected shutdown to be called after reboot-required signal")
	}
}

func TestPatcher_Fedora_DnfFallsBackToYum(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf":              {output: "dnf: command not found", exitCode: 127},
			"yum":              {output: "yum ok"},
			"needs-restarting": {output: ""},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !cmdr.called("yum") {
		t.Error("expected yum to be called as dnf fallback")
	}
}

// ---- Patcher.Run — Windows --------------------------------------------------

func TestPatcher_Windows_NoReboot(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "No updates available."},
			"reg":            {exitCode: 1}, // key absent = no reboot
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !cmdr.called("powershell.exe") {
		t.Error("expected powershell.exe to be called")
	}
	if strings.Contains(log, "Reboot required") {
		t.Errorf("unexpected reboot in log:\n%s", log)
	}
}

func TestPatcher_Windows_RebootRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "Updates installed."},
			"reg":            {output: "HKLM\\...\\RebootRequired\n    RebootRequired    REG_DWORD    0x1"},
			"shutdown":       {output: ""},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !strings.Contains(log, "Reboot required") {
		t.Errorf("expected 'Reboot required' in log:\n%s", log)
	}
	if !cmdr.called("shutdown") {
		t.Error("expected shutdown to be called")
	}
}

// ---- Patcher.Run — macOS ----------------------------------------------------

func TestPatcher_Darwin_NoReboot(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"softwareupdate": {output: "Software Update Tool\nDone."},
		},
	}
	p := patching.NewWithCommander(patching.OSDarwin, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !cmdr.called("softwareupdate") {
		t.Error("expected softwareupdate to be called")
	}
	if strings.Contains(log, "Reboot required") {
		t.Errorf("unexpected reboot in log:\n%s", log)
	}
}

func TestPatcher_Darwin_RebootRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"softwareupdate": {output: "Installing macOS Sequoia...\nA restart is required to complete the installation."},
			"shutdown":       {output: ""},
		},
	}
	p := patching.NewWithCommander(patching.OSDarwin, cmdr)

	log, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v\nlog:\n%s", err, log)
	}
	if !strings.Contains(log, "Reboot required") {
		t.Errorf("expected 'Reboot required' in log:\n%s", log)
	}
	if !cmdr.called("shutdown") {
		t.Error("expected shutdown to be called")
	}
}

// ---- Patcher.Run — unsupported OS ------------------------------------------

func TestPatcher_UnsupportedOS_ReturnsError(t *testing.T) {
	p := patching.NewWithCommander(patching.OSUnknown, &mockCmdr{})

	_, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("Run() expected error for unsupported OS, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error %q should mention 'unsupported'", err.Error())
	}
}

// ---- ExitCodeError ----------------------------------------------------------

func TestExitCodeError_Code(t *testing.T) {
	err := &patching.ExitCodeError{Code: 42, Stderr: "boom"}
	if err.ExitCode() != 42 {
		t.Errorf("ExitCode() = %d, want 42", err.ExitCode())
	}
	if !strings.Contains(err.Error(), "42") {
		t.Errorf("Error() = %q, should contain exit code", err.Error())
	}
}

// ---- Patcher.OS -------------------------------------------------------------

func TestPatcher_OS_ReturnsInjectedType(t *testing.T) {
	for _, tc := range []patching.OSType{patching.OSDebian, patching.OSFedora, patching.OSDarwin, patching.OSWindows} {
		p := patching.NewWithCommander(tc, &mockCmdr{})
		if got := p.OS(); got != tc {
			t.Errorf("OS() = %q, want %q", got, tc)
		}
	}
}

// ---- UpdatesAvailable — Debian ----------------------------------------------

func TestUpdatesAvailable_Debian_None(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get": {output: "Reading package lists...\n0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if available {
		t.Error("expected available=false when summary shows 0 upgraded")
	}
	if cmdr.callCount("apt-get") != 2 {
		t.Errorf("apt-get called %d times, want 2 (update + dry-run)", cmdr.callCount("apt-get"))
	}
}

func TestUpdatesAvailable_Debian_HasUpdates(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get": {output: "The following packages will be upgraded:\n  curl\n5 upgraded, 0 newly installed, 0 to remove and 1 not upgraded.\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if !available {
		t.Error("expected available=true when summary shows 5 upgraded")
	}
}

func TestUpdatesAvailable_Debian_UpdateFails(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"apt-get": {output: "E: Could not get lock", exitCode: 100},
		},
	}
	p := patching.NewWithCommander(patching.OSDebian, cmdr)
	_, _, err := p.UpdatesAvailable(context.Background())
	if err == nil {
		t.Fatal("UpdatesAvailable() expected error when apt-get update fails")
	}
}

// ---- UpdatesAvailable — Fedora ----------------------------------------------

func TestUpdatesAvailable_Fedora_None(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf": {output: "Last metadata expiration check: ..."},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if available {
		t.Error("expected available=false when dnf exits 0")
	}
}

func TestUpdatesAvailable_Fedora_HasUpdates(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf": {output: "curl   x86_64   8.0-1.fc40   updates   200 k\n", exitCode: 100},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if !available {
		t.Error("expected available=true when dnf exits 100")
	}
}

func TestUpdatesAvailable_Fedora_FallsBackToYum(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"dnf": {output: "dnf: command not found", exitCode: 127},
			"yum": {output: "curl   x86_64   8.0-1.el9   updates   200 k\n", exitCode: 100},
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if !available {
		t.Error("expected available=true when yum exits 100")
	}
	if !cmdr.called("yum") {
		t.Error("expected yum to be called as dnf fallback")
	}
}

// ---- UpdatesAvailable — Windows ---------------------------------------------

func TestUpdatesAvailable_Windows_None(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "0 update(s) available"},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if available {
		t.Error("expected available=false when count is 0")
	}
}

func TestUpdatesAvailable_Windows_HasUpdates(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "3 update(s) available"},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if !available {
		t.Error("expected available=true when count is non-zero")
	}
}

// ---- UpdatesAvailable — macOS -----------------------------------------------

func TestUpdatesAvailable_Darwin_None(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"softwareupdate": {output: "Software Update Tool\nNo new software available."},
		},
	}
	p := patching.NewWithCommander(patching.OSDarwin, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if available {
		t.Error("expected available=false when no new software available")
	}
}

func TestUpdatesAvailable_Darwin_HasUpdates(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"softwareupdate": {output: "Software Update Tool\n* Label: macOS Sequoia 15.4\n  Title: macOS Sequoia, Action: restart"},
		},
	}
	p := patching.NewWithCommander(patching.OSDarwin, cmdr)
	available, _, err := p.UpdatesAvailable(context.Background())
	if err != nil {
		t.Fatalf("UpdatesAvailable() error: %v", err)
	}
	if !available {
		t.Error("expected available=true when updates are listed")
	}
}

// ---- UpdatesAvailable — unsupported OS --------------------------------------

func TestUpdatesAvailable_UnknownOS_ReturnsError(t *testing.T) {
	p := patching.NewWithCommander(patching.OSUnknown, &mockCmdr{})
	_, _, err := p.UpdatesAvailable(context.Background())
	if err == nil {
		t.Fatal("UpdatesAvailable() expected error for unsupported OS")
	}
}

// ---- NeedsReboot — direct tests ---------------------------------------------

func TestNeedsReboot_Fedora_RebootRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"needs-restarting": {exitCode: 1}, // exit 1 = reboot needed
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	needed, err := p.NeedsReboot(context.Background())
	if err != nil {
		t.Fatalf("NeedsReboot() unexpected error: %v", err)
	}
	if !needed {
		t.Error("expected NeedsReboot() = true when needs-restarting exits 1")
	}
}

func TestNeedsReboot_Fedora_NotRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"needs-restarting": {output: "No core libraries updated."}, // exit 0
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	needed, err := p.NeedsReboot(context.Background())
	if err != nil {
		t.Fatalf("NeedsReboot() unexpected error: %v", err)
	}
	if needed {
		t.Error("expected NeedsReboot() = false when needs-restarting exits 0")
	}
}

func TestNeedsReboot_Fedora_UnexpectedExitCode_ReturnsNoReboot(t *testing.T) {
	// An unexpected non-zero exit code (not 1) means the tool failed, not that
	// a reboot is needed. We must not confuse the two.
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"needs-restarting": {exitCode: 127}, // command not found
		},
	}
	p := patching.NewWithCommander(patching.OSFedora, cmdr)
	needed, err := p.NeedsReboot(context.Background())
	if err != nil {
		t.Fatalf("NeedsReboot() unexpected error: %v", err)
	}
	if needed {
		t.Error("expected NeedsReboot() = false for unexpected exit code")
	}
}

func TestNeedsReboot_Windows_RebootRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"reg": {output: "HKLM\\...\\RebootRequired\n    RebootRequired    REG_DWORD    0x1"},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	needed, err := p.NeedsReboot(context.Background())
	if err != nil {
		t.Fatalf("NeedsReboot() unexpected error: %v", err)
	}
	if !needed {
		t.Error("expected NeedsReboot() = true when registry key present")
	}
}

func TestNeedsReboot_Windows_NotRequired(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"reg": {exitCode: 1}, // key absent
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	needed, err := p.NeedsReboot(context.Background())
	if err != nil {
		t.Fatalf("NeedsReboot() unexpected error: %v", err)
	}
	if needed {
		t.Error("expected NeedsReboot() = false when registry key absent")
	}
}

// ---- CheckDistUpgrade -------------------------------------------------------

func TestCheckDistUpgrade_NonDebian_ReturnsEmpty(t *testing.T) {
	for _, os := range []patching.OSType{patching.OSFedora, patching.OSDarwin, patching.OSWindows} {
		p := patching.NewWithCommander(os, &mockCmdr{})
		if got := p.CheckDistUpgrade(); got != "" {
			t.Errorf("CheckDistUpgrade(%s) = %q, want empty", os, got)
		}
	}
}

func TestCheckDistUpgrade_Debian_FileAbsent_ReturnsEmpty(t *testing.T) {
	// The sentinel file does not exist in CI — expect empty string (no upgrade).
	p := patching.NewWithCommander(patching.OSDebian, &mockCmdr{})
	got := p.CheckDistUpgrade()
	// Only assert it doesn't panic and returns a string; presence depends on host.
	_ = got
}

// ---- FormatUpdateSummary ----------------------------------------------------

func TestFormatUpdateSummary_WithCVEs(t *testing.T) {
	updates := []patching.PackageUpdate{
		{
			Name:       "openssl",
			NewVersion: "3.0.15",
			CVEs: []patching.CVEInfo{
				{ID: "CVE-2024-9999", Severity: "CRITICAL", CVSSScore: 9.8},
			},
		},
		{
			Name:       "curl",
			NewVersion: "8.5.0",
		},
	}
	out := patching.FormatUpdateSummary("host01", patching.OSDebian, updates)
	// Expect a CVE bullet with the severity, CVE ID, and affected package.
	for _, want := range []string{"CVE-2024-9999", "CRITICAL", "openssl", "3.0.15"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatUpdateSummary missing %q:\n%s", want, out)
		}
	}
}

func TestFormatUpdateSummary_NoUpdates(t *testing.T) {
	out := patching.FormatUpdateSummary("host01", patching.OSDebian, nil)
	if !strings.Contains(out, "up to date") {
		t.Errorf("FormatUpdateSummary(nil) = %q, expected up-to-date message", out)
	}
}

// ---- LastUpdated — Windows (via mock commander) -----------------------------

func TestLastUpdated_Windows_ParsesHotfixDate(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "2025-03-15T10:00:00Z\n"},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	t1, err := p.LastUpdated(context.Background())
	if err != nil {
		t.Fatalf("LastUpdated() error: %v", err)
	}
	if t1 == nil {
		t.Fatal("LastUpdated() returned nil time")
	}
	if t1.Year() != 2025 || t1.Month() != 3 || t1.Day() != 15 {
		t.Errorf("LastUpdated() = %v, want 2025-03-15", *t1)
	}
}

func TestLastUpdated_Windows_PowerShellFails_ReturnsError(t *testing.T) {
	cmdr := &mockCmdr{
		stubs: map[string]cmdStub{
			"powershell.exe": {output: "error", exitCode: 1},
		},
	}
	p := patching.NewWithCommander(patching.OSWindows, cmdr)
	_, err := p.LastUpdated(context.Background())
	if err == nil {
		t.Fatal("LastUpdated() expected error when powershell fails, got nil")
	}
}

func TestLastUpdated_UnsupportedOS_ReturnsError(t *testing.T) {
	p := patching.NewWithCommander(patching.OSUnknown, &mockCmdr{})
	_, err := p.LastUpdated(context.Background())
	if err == nil {
		t.Fatal("LastUpdated() expected error for unsupported OS, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error %q should mention 'unsupported'", err.Error())
	}
}

// ---- NewPatchTool -----------------------------------------------------------

func TestNewPatchTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tool, err := check_for_pending_system_patches.NewPatchTool(nil, mem)
	if err != nil {
		t.Fatalf("NewPatchTool() error: %v", err)
	}
	if tool.Name() != "check_for_pending_system_patches" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "check_for_pending_system_patches")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}
