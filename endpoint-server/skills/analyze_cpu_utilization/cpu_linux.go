//go:build linux

package analyze_cpu_utilization

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// cpuTicks holds raw cumulative tick counts from /proc/stat.
type cpuTicks struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (t cpuTicks) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

func (t cpuTicks) idleTotal() uint64 {
	return t.idle + t.iowait
}

func localCPU(ctx context.Context) (CPUStat, error) {
	t1, err := readProcStat()
	if err != nil {
		return CPUStat{}, err
	}

	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
		return CPUStat{}, ctx.Err()
	}

	t2, err := readProcStat()
	if err != nil {
		return CPUStat{}, err
	}

	totalDelta := float64(t2.total() - t1.total())
	idleDelta := float64(t2.idleTotal() - t1.idleTotal())
	var usedPct float64
	if totalDelta > 0 {
		usedPct = (1 - idleDelta/totalDelta) * 100
	}

	load1, load5, load15, _ := readLoadAvg()

	return CPUStat{
		UsedPct:   usedPct,
		NumCPU:    runtime.NumCPU(),
		LoadAvg1:  load1,
		LoadAvg5:  load5,
		LoadAvg15: load15,
	}, nil
}

func readProcStat() (cpuTicks, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTicks{}, fmt.Errorf("cpu_usage: open /proc/stat: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// fields: ["cpu", user, nice, system, idle, iowait, irq, softirq, steal, ...]
		if len(fields) < 5 {
			return cpuTicks{}, fmt.Errorf("cpu_usage: unexpected /proc/stat format")
		}
		parse := func(i int) uint64 {
			if i >= len(fields) {
				return 0
			}
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		return cpuTicks{
			user:    parse(1),
			nice:    parse(2),
			system:  parse(3),
			idle:    parse(4),
			iowait:  parse(5),
			irq:     parse(6),
			softirq: parse(7),
			steal:   parse(8),
		}, nil
	}
	return cpuTicks{}, fmt.Errorf("cpu_usage: cpu line not found in /proc/stat")
}

func readLoadAvg() (load1, load5, load15 float64, err error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected /proc/loadavg format")
	}
	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15, nil
}
