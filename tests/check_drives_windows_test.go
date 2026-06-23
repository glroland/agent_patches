//go:build windows

package tests

import (
	"testing"

	"agent_patches/endpoint-server/skills/check_drives"
)

const samplePhysicalDiskSingle = `{"DeviceId":0,"FriendlyName":"Samsung SSD 870","MediaType":"SSD","HealthStatus":"Healthy","OperationalStatus":"OK"}`

const samplePhysicalDiskArray = `[
  {"DeviceId":0,"FriendlyName":"Samsung SSD 870","MediaType":"SSD","HealthStatus":"Healthy","OperationalStatus":"OK"},
  {"DeviceId":1,"FriendlyName":"WD Blue HDD","MediaType":"HDD","HealthStatus":"Warning","OperationalStatus":"Degraded"}
]`

// --- ParsePSPhysicalDisks ---

func TestParsePSPhysicalDisks_SingleObject(t *testing.T) {
	disks, err := check_drives.ParsePSPhysicalDisks(samplePhysicalDiskSingle)
	if err != nil {
		t.Fatalf("ParsePSPhysicalDisks error: %v", err)
	}
	if len(disks) != 1 {
		t.Fatalf("want 1 disk, got %d", len(disks))
	}
	if disks[0].FriendlyName != "Samsung SSD 870" {
		t.Errorf("FriendlyName = %q, want Samsung SSD 870", disks[0].FriendlyName)
	}
}

func TestParsePSPhysicalDisks_Array(t *testing.T) {
	disks, err := check_drives.ParsePSPhysicalDisks(samplePhysicalDiskArray)
	if err != nil {
		t.Fatalf("ParsePSPhysicalDisks error: %v", err)
	}
	if len(disks) != 2 {
		t.Fatalf("want 2 disks, got %d", len(disks))
	}
}

func TestParsePSPhysicalDisks_Empty(t *testing.T) {
	disks, err := check_drives.ParsePSPhysicalDisks("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(disks) != 0 {
		t.Errorf("want 0 disks for empty input, got %d", len(disks))
	}
}

func TestParsePSPhysicalDisks_Null(t *testing.T) {
	disks, err := check_drives.ParsePSPhysicalDisks("null")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(disks) != 0 {
		t.Errorf("want 0 disks for null input, got %d", len(disks))
	}
}

// --- PhysicalDiskToReport ---

func TestPhysicalDiskToReport_Healthy(t *testing.T) {
	d := check_drives.PSPhysicalDisk{
		DeviceId: 0, FriendlyName: "Samsung SSD 870",
		MediaType: "SSD", HealthStatus: "Healthy", OperationalStatus: "OK",
	}
	r := check_drives.PhysicalDiskToReport(d)
	if !r.Healthy {
		t.Error("expected Healthy=true")
	}
	if !r.Available {
		t.Error("expected Available=true")
	}
	if len(r.Findings) != 1 || r.Findings[0] != "No issues detected" {
		t.Errorf("Findings = %v, want [No issues detected]", r.Findings)
	}
}

func TestPhysicalDiskToReport_HealthStatusWarning(t *testing.T) {
	d := check_drives.PSPhysicalDisk{
		DeviceId: 1, FriendlyName: "WD Blue HDD",
		MediaType: "HDD", HealthStatus: "Warning", OperationalStatus: "OK",
	}
	r := check_drives.PhysicalDiskToReport(d)
	if r.Healthy {
		t.Error("expected Healthy=false for Warning health status")
	}
	found := false
	for _, f := range r.Findings {
		if f == "HealthStatus: Warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("Findings %v missing 'HealthStatus: Warning'", r.Findings)
	}
}

func TestPhysicalDiskToReport_Unhealthy_OperationalStatus(t *testing.T) {
	d := check_drives.PSPhysicalDisk{
		DeviceId: 2, FriendlyName: "Failing Disk",
		HealthStatus: "Healthy", OperationalStatus: "Degraded",
	}
	r := check_drives.PhysicalDiskToReport(d)
	found := false
	for _, f := range r.Findings {
		if f == "OperationalStatus: Degraded" {
			found = true
		}
	}
	if !found {
		t.Errorf("Findings %v missing 'OperationalStatus: Degraded'", r.Findings)
	}
}

func TestPhysicalDiskToReport_MediaTypeIncluded(t *testing.T) {
	d := check_drives.PSPhysicalDisk{
		DeviceId: 0, FriendlyName: "Samsung 990 Pro",
		MediaType: "NVMe", HealthStatus: "Healthy", OperationalStatus: "OK",
	}
	r := check_drives.PhysicalDiskToReport(d)
	found := false
	for _, f := range r.Findings {
		if f == "MediaType: NVMe" {
			found = true
		}
	}
	if !found {
		t.Errorf("Findings %v missing 'MediaType: NVMe'", r.Findings)
	}
}

func TestPhysicalDiskToReport_UnspecifiedMediaTypeOmitted(t *testing.T) {
	d := check_drives.PSPhysicalDisk{
		DeviceId: 0, FriendlyName: "VM Disk",
		MediaType: "Unspecified", HealthStatus: "Healthy", OperationalStatus: "OK",
	}
	r := check_drives.PhysicalDiskToReport(d)
	for _, f := range r.Findings {
		if f == "MediaType: Unspecified" {
			t.Error("'Unspecified' MediaType should be suppressed in findings")
		}
	}
}

func TestPhysicalDiskToReport_DeviceName(t *testing.T) {
	d := check_drives.PSPhysicalDisk{DeviceId: 3, FriendlyName: "Test Drive"}
	r := check_drives.PhysicalDiskToReport(d)
	want := "PhysicalDrive3 (Test Drive)"
	if r.Device != want {
		t.Errorf("Device = %q, want %q", r.Device, want)
	}
}
