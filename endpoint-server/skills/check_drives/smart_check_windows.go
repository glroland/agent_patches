//go:build windows

package check_drives

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// PSPhysicalDisk models the fields returned by Get-PhysicalDisk. Exported so
// the JSON parsing path can be exercised in tests on any platform.
type PSPhysicalDisk struct {
	DeviceId          int    `json:"DeviceId"`
	FriendlyName      string `json:"FriendlyName"`
	MediaType         string `json:"MediaType"`
	HealthStatus      string `json:"HealthStatus"`
	OperationalStatus string `json:"OperationalStatus"`
}

// psGetPhysicalDiskCmd is the PowerShell command that queries one physical
// disk by device ID. The %d placeholder is replaced by the disk number.
const psGetPhysicalDiskCmd = `` +
	`Get-PhysicalDisk | ` +
	`Where-Object {[string]$_.DeviceId -eq "%d"} | ` +
	`Select-Object DeviceId, FriendlyName, MediaType, HealthStatus, OperationalStatus | ` +
	`ConvertTo-Json -Depth 2 -Compress`

// CheckSmart on Windows queries physical disk health via Get-PhysicalDisk.
// device must be in "PhysicalDriveN" form as set by localDisks().
func CheckSmart(ctx context.Context, device string) []SmartReport {
	physical := parentDevices(device)
	if len(physical) == 0 {
		return nil
	}

	numStr := strings.TrimPrefix(physical[0], "PhysicalDrive")
	var diskNum int
	if _, err := fmt.Sscanf(numStr, "%d", &diskNum); err != nil {
		slog.Warn("check_drives: cannot parse PhysicalDrive number", "device", device)
		return nil
	}

	psCmd := fmt.Sprintf(psGetPhysicalDiskCmd, diskNum)
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output() //nolint:gosec
	if err != nil && len(out) == 0 {
		slog.Warn("check_drives: Get-PhysicalDisk failed", "device", device, "error", err)
		return nil
	}

	disks, parseErr := ParsePSPhysicalDisks(string(out))
	if parseErr != nil {
		slog.Warn("check_drives: failed to parse Get-PhysicalDisk output", "device", device, "error", parseErr)
		return nil
	}

	reports := make([]SmartReport, 0, len(disks))
	for _, d := range disks {
		reports = append(reports, PhysicalDiskToReport(d))
	}
	return reports
}

// ParsePSPhysicalDisks parses the JSON output of Get-PhysicalDisk | ConvertTo-Json.
// Exported for testing on any platform. PowerShell emits a bare object when
// exactly one disk matches; this normalises both cases to a slice.
func ParsePSPhysicalDisks(data string) ([]PSPhysicalDisk, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}
	var disks []PSPhysicalDisk
	if err := json.Unmarshal([]byte(trimmed), &disks); err != nil {
		return nil, fmt.Errorf("ParsePSPhysicalDisks: %w", err)
	}
	return disks, nil
}

// PhysicalDiskToReport converts a PSPhysicalDisk to a SmartReport.
// Exported for testing on any platform.
func PhysicalDiskToReport(d PSPhysicalDisk) SmartReport {
	report := SmartReport{
		Device:    fmt.Sprintf("PhysicalDrive%d (%s)", d.DeviceId, d.FriendlyName),
		Available: true,
		Healthy:   d.HealthStatus == "Healthy",
	}

	if d.HealthStatus != "Healthy" {
		report.Findings = append(report.Findings,
			fmt.Sprintf("HealthStatus: %s", d.HealthStatus))
	}
	if d.OperationalStatus != "OK" {
		report.Findings = append(report.Findings,
			fmt.Sprintf("OperationalStatus: %s", d.OperationalStatus))
	}
	// MediaType is informational; omit "Unspecified" (common on VMs).
	if d.MediaType != "" && d.MediaType != "Unspecified" {
		report.Findings = append(report.Findings,
			fmt.Sprintf("MediaType: %s", d.MediaType))
	}

	if len(report.Findings) == 0 {
		report.Findings = append(report.Findings, "No issues detected")
	}
	return report
}
