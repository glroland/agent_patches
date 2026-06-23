package check_drives

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
