//go:build !windows

package check_drives

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
)

// CollectRawSmartAttrs runs smartctl -A -j once per distinct device and
// extracts the raw counter values for the critical ATA attributes plus NVMe
// wear percentage. Returns nil when smartctl is unavailable.
func CollectRawSmartAttrs(ctx context.Context, disks []DiskStat) []RawSmartAttrs {
	if _, err := exec.LookPath("smartctl"); err != nil {
		slog.Debug("check_drives: smartctl not available for SMART trend collection")
		return nil
	}

	seen := make(map[string]bool)
	var results []RawSmartAttrs

	for _, d := range disks {
		if d.Device == "" || seen[d.Device] {
			continue
		}
		seen[d.Device] = true

		cmdName := "smartctl"
		args := []string{"-A", "-j", d.Device}
		if runtime.GOOS == "linux" && os.Getuid() != 0 {
			args = append([]string{"-n", "smartctl"}, args...)
			cmdName = "sudo"
		}
		slog.Debug("check_drives: collecting SMART attrs", "device", d.Device)
		data, err := exec.CommandContext(ctx, cmdName, args...).Output() //nolint:gosec
		if err != nil && len(data) == 0 {
			slog.Debug("check_drives: smartctl -A failed", "device", d.Device, "error", err)
			continue
		}

		attrs := parseSmartAttrsJSON(data, d.Device)
		if len(attrs.Attrs) > 0 {
			results = append(results, attrs)
		}
	}
	return results
}

// parseSmartAttrsJSON extracts raw SMART attribute values from smartctl -A -j
// output. Exported name is intentionally unexported — use CollectRawSmartAttrs.
func parseSmartAttrsJSON(data []byte, device string) RawSmartAttrs {
	var out smartctlOutput
	if err := json.Unmarshal(data, &out); err != nil {
		slog.Debug("check_drives: failed to parse smartctl -A output", "device", device, "error", err)
		return RawSmartAttrs{Device: device}
	}

	attrs := make(map[string]int64)

	if out.AtaSmartAttributes != nil {
		for _, attr := range out.AtaSmartAttributes.Table {
			name, ok := criticalATAAttrs[attr.ID]
			if !ok {
				continue
			}
			attrs[name] = attr.Raw.Value
		}
	}

	if out.NvmeLog != nil {
		attrs["NVMe_Wear_Pct"] = int64(out.NvmeLog.PercentageUsed)
	}

	return RawSmartAttrs{Device: device, Attrs: attrs}
}
