package tests

import (
	"strings"
	"testing"

	"agent_patches/endpoint-server/skills/check_reboot_required"
)

func TestNewCheckRebootRequiredTool_NameAndDescription(t *testing.T) {
	tl, err := check_reboot_required.NewCheckRebootRequiredTool()
	if err != nil {
		t.Fatalf("NewCheckRebootRequiredTool() error: %v", err)
	}
	if got := tl.Name(); got != "check_reboot_required" {
		t.Errorf("Name() = %q, want %q", got, "check_reboot_required")
	}
	if tl.Description() == "" {
		t.Error("Description() returned empty string")
	}
	// Description must mention the key OS detection patterns so the model
	// routes "is a reboot required?" to this tool, not the patch tool.
	for _, keyword := range []string{"reboot", "reboot-required", "needs-restarting"} {
		if !strings.Contains(tl.Description(), keyword) {
			t.Errorf("Description() missing keyword %q: %s", keyword, tl.Description())
		}
	}
}
