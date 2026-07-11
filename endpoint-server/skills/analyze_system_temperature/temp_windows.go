//go:build windows

package analyze_system_temperature

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PSThermalZoneCmd queries the ACPI thermal zone WMI class. Many consumer
// motherboards don't implement it (vendor tools use proprietary embedded
// controller access instead), so an empty/failed result is treated as "no
// sensors available" rather than an error.
const PSThermalZoneCmd = `Get-CimInstance -Namespace "root/wmi" -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue | ` +
	`Select-Object InstanceName, CurrentTemperature | ConvertTo-Json -Compress`

// PSThermalZone models one row returned by Get-CimInstance
// MSAcpi_ThermalZoneTemperature. CurrentTemperature is in tenths of a Kelvin.
// Exported so the JSON parsing path can be exercised in tests.
type PSThermalZone struct {
	InstanceName       string `json:"InstanceName"`
	CurrentTemperature int64  `json:"CurrentTemperature"`
}

func localTemps(ctx context.Context) ([]TempSensor, error) {
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", PSThermalZoneCmd).Output() //nolint:gosec
	if err != nil && len(out) == 0 {
		return nil, nil
	}

	zones, perr := ParsePSThermalZones(string(out))
	if perr != nil {
		return nil, nil
	}

	sensors := make([]TempSensor, 0, len(zones))
	for _, z := range zones {
		name := z.InstanceName
		if name == "" {
			name = "ACPI Thermal Zone"
		}
		sensors = append(sensors, TempSensor{
			Name:     name,
			CelsiusC: float64(z.CurrentTemperature)/10.0 - 273.15,
		})
	}
	return sensors, nil
}

// ParsePSThermalZones parses the JSON output of
// Get-CimInstance MSAcpi_ThermalZoneTemperature | ConvertTo-Json. Exported
// for testing on any platform. PowerShell emits a bare object when exactly
// one zone matches; this normalises both cases to a slice.
func ParsePSThermalZones(data string) ([]PSThermalZone, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}
	var zones []PSThermalZone
	if err := json.Unmarshal([]byte(trimmed), &zones); err != nil {
		return nil, fmt.Errorf("ParsePSThermalZones: %w", err)
	}
	return zones, nil
}
