//go:build windows

package tests

import (
	"testing"

	"agent_patches/endpoint-server/skills/check_drives"
)

const sampleReliabilityCounter = `{"DeviceId":0,"Temperature":35,"Wear":12,"ReadErrorsTotal":0,"WriteErrorsTotal":0}`

const sampleReliabilityCounterHighWear = `{"DeviceId":0,"Temperature":42,"Wear":85,"ReadErrorsTotal":3,"WriteErrorsTotal":1}`

func TestParseReliabilityCounter_Healthy(t *testing.T) {
	attrs, err := check_drives.ParseReliabilityCounter(sampleReliabilityCounter)
	if err != nil {
		t.Fatalf("ParseReliabilityCounter error: %v", err)
	}
	if attrs == nil {
		t.Fatal("expected non-nil attrs")
	}
	if attrs["Wear"] != 12 {
		t.Errorf("Wear = %d, want 12", attrs["Wear"])
	}
	if attrs["Temperature_C"] != 35 {
		t.Errorf("Temperature_C = %d, want 35", attrs["Temperature_C"])
	}
	if attrs["ReadErrors"] != 0 {
		t.Errorf("ReadErrors = %d, want 0", attrs["ReadErrors"])
	}
}

func TestParseReliabilityCounter_HighWear(t *testing.T) {
	attrs, err := check_drives.ParseReliabilityCounter(sampleReliabilityCounterHighWear)
	if err != nil {
		t.Fatalf("ParseReliabilityCounter error: %v", err)
	}
	if attrs["Wear"] != 85 {
		t.Errorf("Wear = %d, want 85", attrs["Wear"])
	}
	if attrs["WriteErrors"] != 1 {
		t.Errorf("WriteErrors = %d, want 1", attrs["WriteErrors"])
	}
}

func TestParseReliabilityCounter_Empty(t *testing.T) {
	attrs, err := check_drives.ParseReliabilityCounter("")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if attrs != nil {
		t.Errorf("want nil attrs for empty input, got %v", attrs)
	}
}
