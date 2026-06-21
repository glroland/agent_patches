package check_for_pending_system_patches

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/notifier"
)

type patchInput struct{}

// NewPatchTool creates a task tool that patches the current system.
// It auto-detects whether the OS is Windows, Debian-based, or Fedora-based,
// analyzes pending updates including CVE severity, requests operator approval,
// then runs the appropriate package manager and reboots the system if required.
// On Debian/Ubuntu it also checks for a distribution upgrade and includes that
// information in every response, but never offers to perform the dist-upgrade.
// n may be nil, in which case notifications are silently skipped.
func NewPatchTool(n *notifier.Notifier, mem *memory.Store) (tool.Tool, error) {
	return tool.New(
		"check_for_pending_system_patches",
		"Checks for pending system patches, analyses their CVE severity, requests "+
			"operator approval via the central dashboard, and — once approved — applies "+
			"the updates and reboots if required. Detects the OS automatically "+
			"(Windows, Debian-based Linux, or Fedora-based Linux). On Ubuntu/Debian "+
			"also reports whether a distribution upgrade is available (informational only).",
		func(ctx context.Context, _ patchInput) (string, error) {
			host, _ := os.Hostname()
			slog.Info("check_for_pending_system_patches: starting", "host", host)

			p, err := patching.New()
			if err != nil {
				msg := fmt.Sprintf("OS detection failed: %v", err)
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "os_detection_failed")
				n.Notify(ctx, fmt.Sprintf("[%s] Patch Failed", host), msg)
				_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthWarning, msg)
				return msg, nil
			}
			slog.Debug("check_for_pending_system_patches: detected OS", "os", p.OS())

			// --- Phase 1: check without applying ---

			available, checkOut, err := p.UpdatesAvailable(ctx)
			if err != nil {
				slog.Warn("patch: update check failed, proceeding anyway", "error", err)
			}

			// Check for a distribution upgrade (Debian/Ubuntu only).
			// Pure file read — no command is executed. Always informational;
			// never included in the proposed action or applied automatically.
			distUpgrade := p.CheckDistUpgrade()
			if distUpgrade != "" {
				slog.Info("check_for_pending_system_patches: dist-upgrade check", "result", distUpgrade)
			}

			if !available {
				result := "System packages are up to date.\n\n" + checkOut
				if distUpgrade != "" {
					result += "\n\nDistribution upgrade: " + distUpgrade
				}
				stateMsg := "system is up to date"
				if distUpgrade != "" && strings.Contains(distUpgrade, "New release") {
					stateMsg = "packages current; " + distUpgrade
				}
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "up_to_date")
				_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthOK, stateMsg)
				return result, nil
			}
			slog.Debug("check_for_pending_system_patches: updates available, gathering details")

			// Fetch per-package CVE details within a bounded timeout so a slow API
			// does not delay the approval indefinitely.
			updateReport := checkOut
			var updates []patching.PackageUpdate
			listCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			updates, err = p.ListUpdates(listCtx)
			if err != nil {
				slog.Warn("patch: CVE detail lookup failed", "error", err)
			} else {
				logPatchUpdates(updates)
				updateReport = patching.FormatUpdateReport(updates)
			}

			// --- Phase 2: request operator approval ---

			// Build the approval detail shown in the dashboard — concise summary
			// with grouped packages and any HIGH/CRITICAL CVEs called out.
			// The full verbose report (updateReport) is reserved for email notifications.
			approvalDetail := patching.FormatUpdateSummary(host, p.OS(), updates)
			if distUpgrade != "" {
				approvalDetail += "\nDistribution upgrade available: " + distUpgrade +
					" (informational only — not applied automatically)"
			}

			risk := riskFromUpdates(updates)
			proposedAction := proposedActionFor(p.OS(), updates)

			slog.Info("check_for_pending_system_patches: requesting operator approval",
				"host", host, "packages", len(updates), "risk", risk)

			decision, err := reqapproval.RequestApproval(
				ctx, mem, n,
				fmt.Sprintf("Apply system patches to %s", host),
				approvalDetail,
				proposedAction,
				risk,
			)
			if err != nil {
				// Only on context cancellation (e.g. SIGTERM).
				return "", fmt.Errorf("approval interrupted: %w", err)
			}

			switch decision {
			case "rejected":
				msg := "Patching cancelled: operator rejected the update request."
				if distUpgrade != "" {
					msg += "\n\nDistribution upgrade: " + distUpgrade
				}
				slog.Info("check_for_pending_system_patches: operator rejected patching", "host", host)
				_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthWarning,
					"patching declined by operator")
				return msg, nil

			case "timed_out":
				msg := "Patching skipped: no operator response within the approval window."
				if distUpgrade != "" {
					msg += "\n\nDistribution upgrade: " + distUpgrade
				}
				slog.Info("check_for_pending_system_patches: approval timed out", "host", host)
				_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthWarning,
					"patching approval timed out")
				return msg, nil
			}

			// --- Phase 3: apply patches ---

			slog.Info("check_for_pending_system_patches: approval granted, applying patches", "host", host)
			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Approved — Starting", host),
				fmt.Sprintf("Operator approved updates on %q (OS: %s). Applying now.\n\nUpdate details:\n%s",
					host, p.OS(), updateReport),
			)

			log, err := p.Run(ctx)
			if err != nil {
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "error", "error", err)
				n.Notify(ctx,
					fmt.Sprintf("[%s] Patch Failed", host),
					fmt.Sprintf("Patch run on host %q encountered an error.\n\nError: %v\n\nOutput:\n%s", host, err, log),
				)
				_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthCritical,
					fmt.Sprintf("patch run failed: %v", err))
				return fmt.Sprintf("%serror: %v", log, err), nil
			}

			result := log
			if distUpgrade != "" {
				result += "\n\nDistribution upgrade: " + distUpgrade
			}
			slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "patched", "output_len", len(log))
			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Complete", host),
				fmt.Sprintf("System patch completed successfully on host %q.\n\nOutput:\n%s", host, log),
			)
			_ = skillstate.Save(mem, "check_for_pending_system_patches", skillstate.HealthOK, "updates applied successfully")
			return result, nil
		},
	)
}

// riskFromUpdates returns "high" when any CVE is critical, otherwise "medium".
// Patching always carries some risk (service restarts, potential reboot) so
// the floor is "medium" rather than "low".
func riskFromUpdates(updates []patching.PackageUpdate) string {
	for _, u := range updates {
		for _, c := range u.CVEs {
			if strings.EqualFold(c.Severity, "critical") {
				return "high"
			}
		}
	}
	return "medium"
}

// proposedActionFor returns a human-readable string describing what will run.
func proposedActionFor(os patching.OSType, updates []patching.PackageUpdate) string {
	var cmd string
	switch os {
	case patching.OSDebian:
		cmd = "apt-get upgrade -y"
	case patching.OSFedora:
		cmd = "dnf update -y"
	case patching.OSDarwin:
		cmd = "softwareupdate --install --all"
	case patching.OSWindows:
		cmd = "Install-WindowsUpdate (PowerShell COM API)"
	default:
		cmd = "package manager upgrade"
	}
	n := len(updates)
	if n > 0 {
		return fmt.Sprintf("Apply %d pending package update(s) via: %s", n, cmd)
	}
	return fmt.Sprintf("Apply pending package updates via: %s", cmd)
}

func logPatchUpdates(updates []patching.PackageUpdate) {
	for _, u := range updates {
		slog.Info("patch: update",
			"package", u.Name,
			"version", u.NewVersion,
			"description", u.Description,
			"cve_count", len(u.CVEs),
		)
		for _, c := range u.CVEs {
			slog.Info("patch: cve",
				"cve", c.ID,
				"severity", c.Severity,
				"cvss_score", c.CVSSScore,
				"url", c.URL,
			)
		}
	}
}
