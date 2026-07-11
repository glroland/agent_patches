//go:build darwin

package analyze_system_temperature

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
)

// powermetricsTempRe matches the die-temperature lines from
// `powermetrics --samplers smc`, e.g. "CPU die temperature: 52.34 C".
var powermetricsTempRe = regexp.MustCompile(`(?m)^(CPU|GPU) die temperature:\s*([\d.]+)\s*C`)

// localTemps reads SMC die temperatures via powermetrics. macOS exposes no
// sysfs-style sensor interface; powermetrics is the standard tool but
// requires root. When it's unavailable or unauthorized (no sudoers NOPASSWD
// entry configured for it — this repo doesn't ship one, unlike smartctl on
// Linux) this returns an empty result rather than an error, matching the
// "no sensors available" degradation used on Windows.
func localTemps(ctx context.Context) ([]TempSensor, error) {
	out, err := runPowermetrics(ctx)
	if err != nil || len(out) == 0 {
		return nil, nil
	}

	matches := powermetricsTempRe.FindAllStringSubmatch(string(out), -1)
	sensors := make([]TempSensor, 0, len(matches))
	for _, m := range matches {
		c, perr := strconv.ParseFloat(m[2], 64)
		if perr != nil {
			continue
		}
		sensors = append(sensors, TempSensor{Name: m[1] + " die", CelsiusC: c})
	}
	return sensors, nil
}

func runPowermetrics(ctx context.Context) ([]byte, error) {
	if _, err := exec.LookPath("powermetrics"); err != nil {
		return nil, err
	}
	if os.Getuid() == 0 {
		return exec.CommandContext(ctx, "powermetrics", "--samplers", "smc", "-i1", "-n1").Output() //nolint:gosec
	}
	// Non-root: rely on a sudoers NOPASSWD allowlist entry for powermetrics,
	// following the same "sudo -n" pattern used for smartctl in check_drives.
	return exec.CommandContext(ctx, "sudo", "-n", "powermetrics", "--samplers", "smc", "-i1", "-n1").Output() //nolint:gosec
}
