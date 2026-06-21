// Package check_reboot_required provides a skill that checks whether the OS
// has flagged a pending reboot without applying any updates or making changes.
//
// On Debian/Ubuntu it checks for /var/run/reboot-required.
// On Fedora/RHEL it runs needs-restarting -r.
// On Windows it queries the WindowsUpdate registry key.
package check_reboot_required

import (
	"context"
	"fmt"
	"os"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches/patching"
)

type checkRebootInput struct{}

func NewCheckRebootRequiredTool() (tool.Tool, error) {
	return tool.New(
		"check_reboot_required",
		"Checks whether the OS has flagged a pending reboot after a previous update, "+
			"without applying any patches or making any changes. "+
			"On Debian/Ubuntu checks /var/run/reboot-required. "+
			"On Fedora/RHEL runs needs-restarting -r. "+
			"On Windows queries the WindowsUpdate registry key. "+
			"Use this to answer questions like 'is a reboot required?' or "+
			"'does this system need to be restarted after the last update?'",
		func(ctx context.Context, _ checkRebootInput) (string, error) {
			host, _ := os.Hostname()

			p, err := patching.New()
			if err != nil {
				return fmt.Sprintf("OS detection failed: %v", err), nil
			}

			needed, err := p.NeedsReboot(ctx)
			if err != nil {
				return fmt.Sprintf("Reboot check failed on %s (%s): %v", host, p.OS(), err), nil
			}

			if needed {
				return fmt.Sprintf("Yes — %s (%s) has a pending reboot flagged by the OS. A restart is required to complete a previous update.", host, p.OS()), nil
			}
			return fmt.Sprintf("No — %s (%s) does not have a pending reboot. No restart is required.", host, p.OS()), nil
		},
	)
}
