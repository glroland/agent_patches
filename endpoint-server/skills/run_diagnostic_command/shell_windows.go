//go:build windows

package run_diagnostic_command

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// runShell executes cmd on Windows.
//   - "powershell ..." / "powershell.exe ..." → strip the binary and pass the
//     remainder as -Command so quoting is not doubled.
//   - Bare PowerShell Verb-Noun cmdlets (e.g. "Get-Process | Sort-Object CPU")
//     → wrap in powershell.exe -Command automatically.
//   - Everything else → cmd /c.
func runShell(ctx context.Context, cmd string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	first := strings.ToLower(strings.Fields(cmd)[0])

	switch {
	case first == "powershell" || first == "powershell.exe":
		args := strings.SplitN(cmd, " ", 2)
		if len(args) == 1 {
			return exec.CommandContext(ctx, args[0]).CombinedOutput() //nolint:gosec
		}
		remainder := strings.TrimSpace(args[1])
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", remainder).CombinedOutput() //nolint:gosec

	case isPowerShellReadOnlyCmdlet(first):
		// Bare "Get-Process | ..." style — pass the whole string as -Command.
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd).CombinedOutput() //nolint:gosec

	default:
		return exec.CommandContext(ctx, "cmd", "/c", cmd).CombinedOutput() //nolint:gosec
	}
}
