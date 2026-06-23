//go:build windows

package check_drives

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// psPartition models the fields we need from Get-Partition output.
type psPartition struct {
	DriveLetter string `json:"DriveLetter"`
	DiskNumber  int    `json:"DiskNumber"`
}

// driveLetterToDiskNumber runs Get-Partition to build a map from uppercase
// drive letter (e.g. "C") to physical disk number (e.g. 0). Returns nil on
// failure so callers degrade gracefully without SMART data.
func driveLetterToDiskNumber() map[string]int {
	psCmd := `Get-Partition | ` +
		`Where-Object {$_.DriveLetter} | ` +
		`Select-Object DriveLetter, DiskNumber | ` +
		`ConvertTo-Json -Depth 1 -Compress`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output() //nolint:gosec
	if err != nil {
		slog.Debug("check_drives: Get-Partition failed", "error", err)
		return nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}

	var parts []psPartition
	if jerr := json.Unmarshal([]byte(trimmed), &parts); jerr != nil {
		slog.Debug("check_drives: failed to parse Get-Partition output", "error", jerr)
		return nil
	}

	m := make(map[string]int, len(parts))
	for _, p := range parts {
		if p.DriveLetter != "" {
			m[strings.ToUpper(p.DriveLetter)] = p.DiskNumber
		}
	}
	return m
}

func localDisks() ([]DiskStat, error) {
	diskMap := driveLetterToDiskNumber()

	var disks []DiskStat
	for _, letter := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		root := string(letter) + ":\\"
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(rootPtr) != windows.DRIVE_FIXED {
			continue
		}

		var freeBytesAvailable, totalBytes, totalFreeBytes uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
			continue
		}

		var device string
		if diskMap != nil {
			if diskNum, ok := diskMap[string(letter)]; ok {
				device = fmt.Sprintf("PhysicalDrive%d", diskNum)
			}
		}

		disks = append(disks, DiskStat{
			Mount:  root,
			Device: device,
			Total:  totalBytes,
			Free:   totalFreeBytes,
		})
	}
	return disks, nil
}
