//go:build !windows

package check_drives

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
)

// CheckSmart resolves device to its underlying physical disk(s) (following
// device-mapper/LVM mappings where applicable) and returns a S.M.A.R.T.
// health summary for each. If device cannot be resolved to any physical
// disk, or smartctl is unavailable, the result is empty.
func CheckSmart(ctx context.Context, device string) []SmartReport {
	physical := parentDevices(device)
	if len(physical) == 0 {
		slog.Debug("check_drives: cannot resolve physical device for SMART check", "device", device)
		return nil
	}
	reports := make([]SmartReport, 0, len(physical))
	for _, dev := range physical {
		reports = append(reports, checkSmartDevice(ctx, dev))
	}
	return reports
}

// checkSmartDevice runs smartctl against a physical disk device and
// summarizes the result.
func checkSmartDevice(ctx context.Context, device string) SmartReport {
	report := SmartReport{Device: device}

	if _, err := exec.LookPath("smartctl"); err != nil {
		slog.Debug("check_drives: smartctl not available", "error", err)
		return report
	}

	// smartctl typically requires root; use sudo -n on Linux when running as
	// a non-root user so the /etc/sudoers.d/agent_patches allowlist applies.
	cmdName := "smartctl"
	args := []string{"-H", "-A", "-j", device}
	if runtime.GOOS == "linux" && os.Getuid() != 0 {
		args = append([]string{"-n", "smartctl"}, args...)
		cmdName = "sudo"
	}
	slog.Info("check_drives: running command", "command", cmdName, "args", args)
	data, err := exec.CommandContext(ctx, cmdName, args...).Output() //nolint:gosec
	if err != nil && len(data) == 0 {
		slog.Info("check_drives: command failed", "command", cmdName, "device", device, "error", err)
		return report
	}
	// smartctl's exit code encodes warning bits even when the JSON payload is
	// valid and useful, so a non-nil err with output is not fatal.
	slog.Info("check_drives: command finished", "command", cmdName, "device", device,
		"output_len", len(data), "exit_error", err)

	var out smartctlOutput
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		slog.Debug("check_drives: failed to parse smartctl output", "device", device, "error", jerr)
		return report
	}

	report.Available = true
	report.Healthy = true

	if out.SmartStatus != nil {
		report.Healthy = out.SmartStatus.Passed
		if !out.SmartStatus.Passed {
			report.Findings = append(report.Findings, "Overall SMART health check: FAILED")
		}
	}

	if out.AtaSmartAttributes != nil {
		for _, attr := range out.AtaSmartAttributes.Table {
			name, ok := criticalATAAttrs[attr.ID]
			if !ok || attr.Raw.Value <= 0 {
				continue
			}
			report.Healthy = false
			report.Findings = append(report.Findings,
				fmt.Sprintf("%s is %d (nonzero indicates degrading sectors)", name, attr.Raw.Value))
		}
	}

	if out.NvmeLog != nil {
		n := out.NvmeLog
		if n.CriticalWarning != 0 {
			report.Healthy = false
			report.Findings = append(report.Findings, fmt.Sprintf("NVMe critical warning flags: 0x%x", n.CriticalWarning))
		}
		if n.MediaErrors > 0 {
			report.Healthy = false
			report.Findings = append(report.Findings, fmt.Sprintf("NVMe media errors: %d", n.MediaErrors))
		}
		if n.PercentageUsed >= 90 {
			report.Findings = append(report.Findings, fmt.Sprintf("NVMe endurance used: %d%%", n.PercentageUsed))
		}
	}

	if out.Temperature != nil && out.Temperature.Current > 0 {
		report.Findings = append(report.Findings, fmt.Sprintf("Temperature: %d°C", out.Temperature.Current))
	}

	if len(report.Findings) == 0 {
		report.Findings = append(report.Findings, "No issues detected")
	}
	return report
}
