//go:build linux

package analyze_memory_utilization

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func localMemory() (MemStat, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemStat{}, fmt.Errorf("memory_monitor: open /proc/meminfo: %w", err)
	}
	defer f.Close()

	vals := make(map[string]uint64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Format: "MemTotal:       16384000 kB"
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		vals[key] = val * 1024 // /proc/meminfo reports in kB
	}
	if err := scanner.Err(); err != nil {
		return MemStat{}, fmt.Errorf("memory_monitor: reading /proc/meminfo: %w", err)
	}

	stat := MemStat{
		Total:     vals["MemTotal"],
		Available: vals["MemAvailable"],
		SwapTotal: vals["SwapTotal"],
		SwapFree:  vals["SwapFree"],
	}
	// MemAvailable was added in kernel 3.14; fall back to MemFree on older kernels.
	if stat.Available == 0 {
		stat.Available = vals["MemFree"]
	}
	return stat, nil
}
