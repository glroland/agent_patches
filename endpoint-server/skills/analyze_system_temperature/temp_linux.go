//go:build linux

package analyze_system_temperature

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// thermalZoneGlob matches the kernel's thermal sysfs interface, present on
// virtually all Linux systems (bare metal, VMs report what the hypervisor
// exposes, which may be nothing).
const thermalZoneGlob = "/sys/class/thermal/thermal_zone*"

// localTemps reads every thermal zone under /sys/class/thermal. Zones that
// can't be read (permission, transient sensor unavailability) are skipped
// rather than failing the whole survey.
func localTemps(ctx context.Context) ([]TempSensor, error) {
	zones, err := filepath.Glob(thermalZoneGlob)
	if err != nil {
		return nil, fmt.Errorf("analyze_system_temperature: glob thermal zones: %w", err)
	}

	sensors := make([]TempSensor, 0, len(zones))
	for _, zone := range zones {
		select {
		case <-ctx.Done():
			return sensors, ctx.Err()
		default:
		}

		raw, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			continue
		}
		milliC, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			continue
		}

		name := filepath.Base(zone)
		if typ, terr := os.ReadFile(filepath.Join(zone, "type")); terr == nil {
			if t := strings.TrimSpace(string(typ)); t != "" {
				name = t
			}
		}

		sensors = append(sensors, TempSensor{Name: name, CelsiusC: float64(milliC) / 1000.0})
	}

	sort.Slice(sensors, func(i, j int) bool { return sensors[i].Name < sensors[j].Name })
	return sensors, nil
}
