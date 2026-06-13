package patching

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OSType identifies the OS family.
type OSType string

const (
	OSDebian  OSType = "debian"
	OSFedora  OSType = "fedora"
	OSDarwin  OSType = "darwin"
	OSWindows OSType = "windows"
	OSUnknown OSType = "unknown"
)

// Commander abstracts process execution so the Patcher can be tested without
// running real system commands.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) (output string, err error)
}

// ExitCodeError is returned by Commander.Run when a process exits with a
// non-zero status. It carries the exit code so callers can distinguish
// meaningful non-zero exits (e.g. needs-restarting -r exits 1 when a reboot
// is required) from genuine errors.
type ExitCodeError struct {
	Code   int
	Stderr string
}

func (e *ExitCodeError) Error() string  { return fmt.Sprintf("exit status %d", e.Code) }
func (e *ExitCodeError) ExitCode() int  { return e.Code }

// Patcher detects the OS and applies system patches.
type Patcher struct {
	os        OSType
	commander Commander
	// HTTPClient is used for vendor and NVD API calls in ListUpdates.
	// When nil, updateinfo.go uses defaultHTTPClient. Tests may inject a mock client.
	HTTPClient *http.Client
	// darwinRebootRequired is set by patchDarwin when the softwareupdate output
	// indicates a restart is needed; read back by needsReboot.
	darwinRebootRequired bool
}

// New creates a Patcher that auto-detects the running OS.
func New() (*Patcher, error) {
	osType, err := detectOS()
	if err != nil {
		return nil, err
	}
	return &Patcher{os: osType, commander: &realCommander{}}, nil
}

// NewWithCommander creates a Patcher with the given OS type and Commander.
// Intended for testing.
func NewWithCommander(osType OSType, c Commander) *Patcher {
	return &Patcher{os: osType, commander: c}
}

// OS returns the detected OS family.
func (p *Patcher) OS() OSType { return p.os }

// Run executes the full patch cycle:
//  1. Apply OS updates.
//  2. Check whether a reboot is required.
//  3. Reboot the system if necessary.
//
// It returns a human-readable log of all actions taken.
func (p *Patcher) Run(ctx context.Context) (string, error) {
	var sb strings.Builder
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		sb.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			sb.WriteByte('\n')
		}
	}

	logf("Detected OS: %s", p.os)
	logf("")
	slog.Info("patching: starting", "os", p.os)

	out, err := p.applyUpdates(ctx)
	logf("=== Package Update ===")
	logf("%s", strings.TrimRight(out, "\n"))
	logf("")
	if err != nil {
		return sb.String(), fmt.Errorf("update failed: %w", err)
	}
	slog.Info("patching: update complete")

	needs, err := p.needsReboot(ctx)
	if err != nil {
		logf("Warning: reboot check failed: %v", err)
		slog.Warn("patching: reboot check failed", "error", err)
		return sb.String(), nil
	}

	if !needs {
		logf("No reboot required.")
		slog.Info("patching: no reboot required")
		return sb.String(), nil
	}

	logf("Reboot required. Initiating reboot...")
	slog.Info("patching: reboot required — rebooting now")

	if out, err := p.reboot(ctx); err != nil {
		logf("%s", out)
		return sb.String(), fmt.Errorf("reboot failed: %w", err)
	}

	logf("Reboot command issued successfully.")
	return sb.String(), nil
}

// UpdatesAvailable checks whether the package manager reports pending updates
// without applying them. It returns true when at least one update is pending,
// along with a human-readable summary of what was found.
func (p *Patcher) UpdatesAvailable(ctx context.Context) (bool, string, error) {
	switch p.os {
	case OSDebian:
		return p.checkDebianUpdates(ctx)
	case OSFedora:
		return p.checkFedoraUpdates(ctx)
	case OSDarwin:
		return p.checkDarwinUpdates(ctx)
	case OSWindows:
		return p.checkWindowsUpdates(ctx)
	default:
		return false, "", fmt.Errorf("unsupported OS: %s", p.os)
	}
}

// checkDebianUpdates refreshes package lists then dry-runs the upgrade to
// count pending packages. The summary line "0 upgraded, 0 newly installed,
// 0 to remove" signals nothing to do.
func (p *Patcher) checkDebianUpdates(ctx context.Context) (bool, string, error) {
	if out, err := p.commander.Run(ctx, "apt-get", "update", "-q"); err != nil {
		return false, out, fmt.Errorf("apt-get update: %w", err)
	}
	out, err := p.commander.Run(ctx, "apt-get", "upgrade", "--dry-run")
	if err != nil {
		return false, out, fmt.Errorf("apt-get upgrade --dry-run: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "0 upgraded, 0 newly installed, 0 to remove") {
			return false, out, nil
		}
	}
	return true, out, nil
}

// checkFedoraUpdates uses dnf check-update, which exits 100 when updates are
// available and 0 when none. Falls back to yum if dnf is not installed.
func (p *Patcher) checkFedoraUpdates(ctx context.Context) (bool, string, error) {
	out, err := p.commander.Run(ctx, "dnf", "check-update")
	if err == nil {
		return false, out, nil // exit 0 = nothing to update
	}
	var ec *ExitCodeError
	if errors.As(err, &ec) && ec.ExitCode() == 100 {
		return true, out, nil // exit 100 = updates are available
	}
	slog.Warn("patching: dnf check-update failed, trying yum", "error", err)

	out2, err2 := p.commander.Run(ctx, "yum", "check-update")
	if err2 == nil {
		return false, out2, nil
	}
	if errors.As(err2, &ec) && ec.ExitCode() == 100 {
		return true, out2, nil
	}
	return false, out + out2, fmt.Errorf("dnf check-update: %w; yum check-update: %v", err, err2)
}

// checkWindowsUpdates runs a PowerShell search without downloading or
// installing anything, then checks whether the result count is non-zero.
func (p *Patcher) checkWindowsUpdates(ctx context.Context) (bool, string, error) {
	script := strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`$session  = New-Object -ComObject Microsoft.Update.Session`,
		`$searcher = $session.CreateUpdateSearcher()`,
		`$results  = $searcher.Search("IsInstalled=0 and Type='Software'")`,
		`Write-Output "$($results.Updates.Count) update(s) available"`,
	}, "; ")
	out, err := p.commander.Run(ctx, "powershell.exe", "-ExecutionPolicy", "Bypass", "-NoProfile", "-Command", script)
	if err != nil {
		return false, out, fmt.Errorf("windows update check: %w", err)
	}
	return !strings.Contains(out, "0 update(s) available"), out, nil
}

// applyUpdates dispatches to the OS-specific update method.
func (p *Patcher) applyUpdates(ctx context.Context) (string, error) {
	switch p.os {
	case OSDebian:
		return p.patchDebian(ctx)
	case OSFedora:
		return p.patchFedora(ctx)
	case OSDarwin:
		return p.patchDarwin(ctx)
	case OSWindows:
		return p.patchWindows(ctx)
	default:
		return "", fmt.Errorf("unsupported OS: %s", p.os)
	}
}

func (p *Patcher) patchDebian(ctx context.Context) (string, error) {
	var sb strings.Builder

	out, err := p.commander.Run(ctx, "apt-get", "update", "-q")
	sb.WriteString(out)
	if err != nil {
		return sb.String(), fmt.Errorf("apt-get update: %w", err)
	}

	out, err = p.commander.Run(ctx, "apt-get", "upgrade", "-y")
	sb.WriteString(out)
	if err != nil {
		return sb.String(), fmt.Errorf("apt-get upgrade: %w", err)
	}

	return sb.String(), nil
}

func (p *Patcher) patchFedora(ctx context.Context) (string, error) {
	out, err := p.commander.Run(ctx, "dnf", "update", "-y")
	if err == nil {
		return out, nil
	}
	slog.Warn("patching: dnf failed, falling back to yum", "error", err)

	out2, err2 := p.commander.Run(ctx, "yum", "update", "-y")
	if err2 != nil {
		return out + out2, fmt.Errorf("dnf: %w; yum: %v", err, err2)
	}
	return out2, nil
}

func (p *Patcher) patchWindows(ctx context.Context) (string, error) {
	// Uses the built-in Windows Update COM API via PowerShell.
	// PSWindowsUpdate is imported only if available.
	script := strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`Import-Module PSWindowsUpdate -ErrorAction SilentlyContinue`,
		`$session   = New-Object -ComObject Microsoft.Update.Session`,
		`$searcher  = $session.CreateUpdateSearcher()`,
		`$results   = $searcher.Search("IsInstalled=0 and Type='Software'")`,
		`if ($results.Updates.Count -eq 0) { Write-Output 'No updates available.'; exit 0 }`,
		`$dl = $session.CreateUpdateDownloader(); $dl.Updates = $results.Updates; $dl.Download()`,
		`$in = $session.CreateUpdateInstaller(); $in.Updates = $results.Updates`,
		`$r  = $in.Install()`,
		`Write-Output "Result: $($r.ResultCode), RebootRequired: $($r.RebootRequired)"`,
	}, "; ")
	return p.commander.Run(ctx, "powershell.exe", "-ExecutionPolicy", "Bypass", "-NoProfile", "-Command", script)
}

// checkDarwinUpdates runs softwareupdate -l and reports whether any updates are
// listed. The output contains "No new software available." when the system is
// fully up to date.
func (p *Patcher) checkDarwinUpdates(ctx context.Context) (bool, string, error) {
	out, err := p.commander.Run(ctx, "softwareupdate", "-l")
	if err != nil {
		return false, out, fmt.Errorf("softwareupdate -l: %w", err)
	}
	if strings.Contains(out, "No new software available") {
		return false, out, nil
	}
	return true, out, nil
}

// patchDarwin installs all available macOS software updates via softwareupdate.
// It parses the command output to detect whether a restart will be required and
// stores the result in darwinRebootRequired for needsReboot to consume.
func (p *Patcher) patchDarwin(ctx context.Context) (string, error) {
	out, err := p.commander.Run(ctx, "softwareupdate", "--install", "--all")
	if err != nil {
		return out, fmt.Errorf("softwareupdate --install --all: %w", err)
	}
	lower := strings.ToLower(out)
	p.darwinRebootRequired = strings.Contains(lower, "restart") || strings.Contains(lower, "reboot")
	return out, nil
}

// needsReboot returns true when the OS signals that a reboot is required.
func (p *Patcher) needsReboot(ctx context.Context) (bool, error) {
	switch p.os {
	case OSDebian:
		// Debian/Ubuntu write this sentinel file when a reboot is needed.
		_, err := os.Stat("/var/run/reboot-required")
		if err == nil {
			return true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err

	case OSFedora:
		// needs-restarting exits 0 → no reboot, exits 1 → reboot required.
		_, err := p.commander.Run(ctx, "needs-restarting", "-r")
		if err == nil {
			return false, nil
		}
		var ec *ExitCodeError
		if errors.As(err, &ec) {
			if ec.ExitCode() == 1 {
				return true, nil
			}
			// Any other non-zero code means the tool failed or isn't installed.
			slog.Warn("patching: needs-restarting returned unexpected code, skipping reboot check",
				"code", ec.ExitCode())
			return false, nil
		}
		// Tool not found or other execution error — skip check gracefully.
		slog.Warn("patching: needs-restarting unavailable, skipping reboot check", "error", err)
		return false, nil

	case OSDarwin:
		return p.darwinRebootRequired, nil

	case OSWindows:
		// Pending reboot is indicated by the presence of this registry key.
		const key = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`
		out, err := p.commander.Run(ctx, "reg", "query", key, "/v", "RebootRequired")
		if err != nil {
			return false, nil // key absent = no reboot needed
		}
		return strings.Contains(out, "RebootRequired"), nil
	}

	return false, nil
}

// reboot issues the OS-specific shutdown/reboot command.
func (p *Patcher) reboot(ctx context.Context) (string, error) {
	switch p.os {
	case OSDebian, OSFedora, OSDarwin:
		return p.commander.Run(ctx, "shutdown", "-r", "now")
	case OSWindows:
		return p.commander.Run(ctx, "shutdown", "/r", "/t", "0")
	}
	return "", fmt.Errorf("reboot not supported for OS: %s", p.os)
}

// detectOS determines the OS family using runtime.GOOS and, on Linux,
// the content of /etc/os-release.
func detectOS() (OSType, error) {
	switch runtime.GOOS {
	case "darwin":
		return OSDarwin, nil
	case "windows":
		return OSWindows, nil
	case "linux":
		data, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return OSUnknown, fmt.Errorf("patching: reading /etc/os-release: %w", err)
		}
		osType := ParseOSRelease(string(data))
		if osType == OSUnknown {
			return OSUnknown, fmt.Errorf("patching: unrecognised Linux distribution")
		}
		return osType, nil
	default:
		return OSUnknown, fmt.Errorf("patching: unsupported platform: %s", runtime.GOOS)
	}
}

// ParseOSRelease parses the content of an /etc/os-release file and returns the
// OS family. Exported so it can be exercised directly in tests.
func ParseOSRelease(content string) OSType {
	var id, idLike string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "ID":
			id = strings.ToLower(strings.TrimSpace(v))
		case "ID_LIKE":
			idLike = strings.ToLower(strings.TrimSpace(v))
		}
	}

	for _, token := range tokenise(id, idLike) {
		switch token {
		case "debian", "ubuntu", "mint", "kali", "pop", "elementary",
			"zorin", "raspbian", "linuxmint", "mxlinux":
			return OSDebian
		case "fedora", "rhel", "centos", "rocky", "almalinux",
			"ol", "oracle", "scientific":
			return OSFedora
		}
	}

	return OSUnknown
}

// tokenise splits space-separated distro identifiers from ID and ID_LIKE.
func tokenise(values ...string) []string {
	var tokens []string
	for _, v := range values {
		for _, t := range strings.Fields(v) {
			if t != "" {
				tokens = append(tokens, t)
			}
		}
	}
	return tokens
}

// realCommander is the production Commander backed by os/exec.
type realCommander struct{}

func (r *realCommander) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return output, &ExitCodeError{Code: exitErr.ExitCode(), Stderr: output}
		}
		return output, err
	}
	return output, nil
}
