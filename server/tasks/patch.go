package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"agent_patches/server/notifier"
	"agent_patches/server/patching"
	"agent_patches/server/tool"
)

type patchInput struct{}

// NewPatchTool creates a task tool that patches the current system.
// It auto-detects whether the OS is Windows, Debian-based, or Fedora-based,
// runs the appropriate package manager, and reboots the system if required.
// n may be nil, in which case notifications are silently skipped.
func NewPatchTool(n *notifier.Notifier) (tool.Tool, error) {
	return tool.New(
		"patch",
		"Patches the current system. Detects the OS (Windows, Debian-based Linux, "+
			"or Fedora-based Linux), runs the appropriate update commands, checks "+
			"whether a reboot is required, and reboots the system if so.",
		func(ctx context.Context, _ patchInput) (string, error) {
			host, _ := os.Hostname()

			p, err := patching.New()
			if err != nil {
				msg := fmt.Sprintf("OS detection failed: %v", err)
				n.Notify(ctx, fmt.Sprintf("[%s] Patch Failed", host), msg)
				return msg, nil
			}

			available, checkOut, err := p.UpdatesAvailable(ctx)
			if err != nil {
				// Log the check failure but do not abort — let the update run
				// and surface any real error itself.
				slog.Warn("patch: update check failed, proceeding anyway", "error", err)
			} else if !available {
				return "No updates available. System is up to date.\n\n" + checkOut, nil
			}

			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Starting", host),
				fmt.Sprintf("Updates available on host %q (OS: %s). Beginning system patch.\n\nUpdate check:\n%s", host, p.OS(), checkOut),
			)

			log, err := p.Run(ctx)
			if err != nil {
				n.Notify(ctx,
					fmt.Sprintf("[%s] Patch Failed", host),
					fmt.Sprintf("Patch run on host %q encountered an error.\n\nError: %v\n\nOutput:\n%s", host, err, log),
				)
				return fmt.Sprintf("%serror: %v", log, err), nil
			}

			n.Notify(ctx,
				fmt.Sprintf("[%s] Patch Complete", host),
				fmt.Sprintf("System patch completed successfully on host %q.\n\nOutput:\n%s", host, log),
			)
			return log, nil
		},
	)
}
