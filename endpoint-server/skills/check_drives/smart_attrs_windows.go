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

// psReliabilityCounter models the fields returned by Get-StorageReliabilityCounter.
type psReliabilityCounter struct {
	DeviceId         int `json:"DeviceId"`
	Temperature      int `json:"Temperature"`
	Wear             int `json:"Wear"`
	ReadErrorsTotal  int `json:"ReadErrorsTotal"`
	WriteErrorsTotal int `json:"WriteErrorsTotal"`
}

const psReliabilityCmd = `` +
	`Get-PhysicalDisk | Where-Object {[string]$_.DeviceId -eq "%d"} | ` +
	`Get-StorageReliabilityCounter | ` +
	`Select-Object DeviceId, Temperature, Wear, ReadErrorsTotal, WriteErrorsTotal | ` +
	`ConvertTo-Json -Depth 1 -Compress`

// CollectRawSmartAttrs queries Get-StorageReliabilityCounter once per distinct
// PhysicalDriveN device and maps the results to synthetic SMART attribute names.
// Returns nil gracefully when the cmdlet is unavailable (VMs, older controllers).
func CollectRawSmartAttrs(ctx context.Context, disks []DiskStat) []RawSmartAttrs {
	seen := make(map[string]bool)
	var results []RawSmartAttrs

	for _, d := range disks {
		if d.Device == "" || seen[d.Device] {
			continue
		}
		seen[d.Device] = true

		numStr := strings.TrimPrefix(d.Device, "PhysicalDrive")
		var diskNum int
		if _, err := fmt.Sscanf(numStr, "%d", &diskNum); err != nil {
			continue
		}

		psCmd := fmt.Sprintf(psReliabilityCmd, diskNum)
		slog.Debug("check_drives: collecting storage reliability counter", "device", d.Device)
		out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output() //nolint:gosec
		if err != nil && len(out) == 0 {
			slog.Debug("check_drives: Get-StorageReliabilityCounter failed", "device", d.Device, "error", err)
			continue
		}

		attrs, parseErr := ParseReliabilityCounter(string(out))
		if parseErr != nil {
			slog.Debug("check_drives: failed to parse reliability counter", "device", d.Device, "error", parseErr)
			continue
		}
		if len(attrs) > 0 {
			results = append(results, RawSmartAttrs{Device: d.Device, Attrs: attrs})
		}
	}
	return results
}

// ParseReliabilityCounter parses the JSON output of Get-StorageReliabilityCounter.
// Exported so the parsing path can be exercised in tests on any platform.
// Returns nil attrs (not an error) when the output is empty or null.
func ParseReliabilityCounter(data string) (map[string]int64, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	// Normalise single object to array.
	if !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}
	var counters []psReliabilityCounter
	if err := json.Unmarshal([]byte(trimmed), &counters); err != nil {
		return nil, fmt.Errorf("ParseReliabilityCounter: %w", err)
	}
	if len(counters) == 0 {
		return nil, nil
	}
	c := counters[0]
	attrs := map[string]int64{
		"Wear":          int64(c.Wear),
		"Temperature_C": int64(c.Temperature),
		"ReadErrors":    int64(c.ReadErrorsTotal),
		"WriteErrors":   int64(c.WriteErrorsTotal),
	}
	return attrs, nil
}
