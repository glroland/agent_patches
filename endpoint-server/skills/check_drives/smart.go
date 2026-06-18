package check_drives

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
)

// SmartReport summarizes S.M.A.R.T. health for one physical disk device.
type SmartReport struct {
	Device    string
	Available bool // true if smartctl ran and returned usable data
	Healthy   bool // overall health assessment
	Findings  []string
}

// smartctlOutput models the subset of `smartctl -H -A -j` JSON output used
// to assess drive health, covering both ATA/SATA and NVMe devices.
type smartctlOutput struct {
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	AtaSmartAttributes *struct {
		Table []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Raw  struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NvmeLog *struct {
		CriticalWarning int   `json:"critical_warning"`
		PercentageUsed  int   `json:"percentage_used"`
		MediaErrors     int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
}

// criticalATAAttrs maps SMART attribute IDs to a human-readable name for
// attributes whose raw value indicates drive degradation when nonzero.
var criticalATAAttrs = map[int]string{
	5:   "Reallocated_Sector_Ct",
	197: "Current_Pending_Sector",
	198: "Offline_Uncorrectable",
	187: "Reported_Uncorrect",
}

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

	args := []string{"-H", "-A", "-j", device}
	slog.Info("check_drives: running command", "command", "smartctl", "args", args)
	data, err := exec.CommandContext(ctx, "smartctl", args...).Output() //nolint:gosec
	if err != nil && len(data) == 0 {
		slog.Info("check_drives: command failed", "command", "smartctl", "device", device, "error", err)
		return report
	}
	// smartctl's exit code encodes warning bits even when the JSON payload is
	// valid and useful, so a non-nil err with output is not fatal.
	slog.Info("check_drives: command finished", "command", "smartctl", "device", device,
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
