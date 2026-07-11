//go:build windows

package tests

import (
	"testing"

	"agent_patches/endpoint-server/skills/analyze_system_temperature"
)

const sampleThermalZoneSingle = `{"InstanceName":"ACPI\\ThermalZone\\TZ00_0","CurrentTemperature":3232}`

const sampleThermalZoneArray = `[
  {"InstanceName":"ACPI\\ThermalZone\\TZ00_0","CurrentTemperature":3232},
  {"InstanceName":"ACPI\\ThermalZone\\TZ01_0","CurrentTemperature":3372}
]`

// --- ParsePSThermalZones ---

func TestParsePSThermalZones_SingleObject(t *testing.T) {
	zones, err := analyze_system_temperature.ParsePSThermalZones(sampleThermalZoneSingle)
	if err != nil {
		t.Fatalf("ParsePSThermalZones error: %v", err)
	}
	if len(zones) != 1 {
		t.Fatalf("want 1 zone, got %d", len(zones))
	}
	if zones[0].CurrentTemperature != 3232 {
		t.Errorf("CurrentTemperature = %d, want 3232", zones[0].CurrentTemperature)
	}
}

func TestParsePSThermalZones_Array(t *testing.T) {
	zones, err := analyze_system_temperature.ParsePSThermalZones(sampleThermalZoneArray)
	if err != nil {
		t.Fatalf("ParsePSThermalZones error: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("want 2 zones, got %d", len(zones))
	}
}

func TestParsePSThermalZones_Empty(t *testing.T) {
	zones, err := analyze_system_temperature.ParsePSThermalZones("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(zones) != 0 {
		t.Errorf("want 0 zones for empty input, got %d", len(zones))
	}
}

func TestParsePSThermalZones_Null(t *testing.T) {
	zones, err := analyze_system_temperature.ParsePSThermalZones("null")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(zones) != 0 {
		t.Errorf("want 0 zones for null input, got %d", len(zones))
	}
}

func TestParsePSThermalZones_Invalid(t *testing.T) {
	if _, err := analyze_system_temperature.ParsePSThermalZones("{not json"); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
