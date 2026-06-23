//go:build !windows

package run_diagnostic_command

import (
	"context"
	"os/exec"
	"time"
)

func runShell(ctx context.Context, cmd string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput() //nolint:gosec
}
