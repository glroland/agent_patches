package check_for_pending_system_patches

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
	"agent_patches/endpoint-server/utils/notifier"
)

type patchInput struct{}

// NewPatchTool creates a task tool that patches the current system.
// It auto-detects whether the OS is Windows, Debian-based, or Fedora-based,
// runs the appropriate package manager, and reboots the system if required.
// n may be nil, in which case notifications are silently skipped.
func NewPatchTool(n *notifier.Notifier) (tool.Tool, error) {
	return tool.New(
		"check_for_pending_system_patches",
		"Patches the current system. Detects the OS (Windows, Debian-based Linux, "+
			"or Fedora-based Linux), runs the appropriate update commands, checks "+
			"whether a reboot is required, and reboots the system if so.",
		func(ctx context.Context, _ patchInput) (string, error) {
			host, _ := os.Hostname()
			slog.Info("check_for_pending_system_patches: starting", "host", host)

			p, err := patching.New()
			if err != nil {
				msg := fmt.Sprintf("OS detection failed: %v", err)
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "os_detection_failed")
				n.Notify(ctx, fmt.Sprintf("[%s] Patch Failed", host), msg)
				return msg, nil
			}
			slog.Debug("check_for_pending_system_patches: detected OS", "os", p.OS())

			available, checkOut, err := p.UpdatesAvailable(ctx)
			if err != nil {
				slog.Warn("patch: update check failed, proceeding anyway", "error", err)
			} else if !available {
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "up_to_date")
				return "No updates available. System is up to date.\n\n" + checkOut, nil
			}
			slog.Debug("check_for_pending_system_patches: updates available, gathering details")

			// Fetch per-package CVE details. Use a bounded timeout so a slow API
			// does not delay the actual patching indefinitely.
			updateReport := checkOut
			listCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			updates, err := p.ListUpdates(listCtx)
			if err != nil {
				slog.Warn("patch: CVE detail lookup failed", "error", err)
			} else {
				logPatchUpdates(updates)
				updateReport = patching.FormatUpdateReport(updates)
			}

			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Starting", host),
				fmt.Sprintf("Updates available on host %q (OS: %s). Beginning system patch.\n\nUpdate details:\n%s",
					host, p.OS(), updateReport),
			)

			log, err := p.Run(ctx)
			if err != nil {
				slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "error", "error", err)
				n.Notify(ctx,
					fmt.Sprintf("[%s] Patch Failed", host),
					fmt.Sprintf("Patch run on host %q encountered an error.\n\nError: %v\n\nOutput:\n%s", host, err, log),
				)
				return fmt.Sprintf("%serror: %v", log, err), nil
			}

			slog.Info("check_for_pending_system_patches: completed", "host", host, "result", "patched", "output_len", len(log))
			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Complete", host),
				fmt.Sprintf("System patch completed successfully on host %q.\n\nOutput:\n%s", host, log),
			)
			return log, nil
		},
	)
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
