//go:build darwin

package analyze_cpu_utilization

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// cpuTicks holds raw cumulative tick counts from kern.cp_time.
// Order matches the kernel CP_* constants: user, nice, sys, intr, idle.
type cpuTicks struct {
	user, nice, sys, intr, idle uint64
}

func (t cpuTicks) total() uint64 {
	return t.user + t.nice + t.sys + t.intr + t.idle
}

func localCPU(ctx context.Context) (CPUStat, error) {
	t1, err := readCPTime()
	if err != nil {
		return CPUStat{}, err
	}

	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
		return CPUStat{}, ctx.Err()
	}

	t2, err := readCPTime()
	if err != nil {
		return CPUStat{}, err
	}

	totalDelta := float64(t2.total() - t1.total())
	idleDelta := float64(t2.idle - t1.idle)
	var usedPct float64
	if totalDelta > 0 {
		usedPct = (1 - idleDelta/totalDelta) * 100
	}

	load1, load5, load15 := readLoadAvg()

	return CPUStat{
		UsedPct:   usedPct,
		NumCPU:    runtime.NumCPU(),
		LoadAvg1:  load1,
		LoadAvg5:  load5,
		LoadAvg15: load15,
	}, nil
}

// readLoadAvg reads vm.loadavg which returns struct loadavg:
// ldavg[3] (3×uint32 fixed-point values) + fscale (int64 on 64-bit macOS).
func readLoadAvg() (load1, load5, load15 float64) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 20 {
		return 0, 0, 0
	}
	fscale := float64(binary.LittleEndian.Uint64(raw[12:20]))
	if fscale == 0 {
		return 0, 0, 0
	}
	load1 = float64(binary.LittleEndian.Uint32(raw[0:4])) / fscale
	load5 = float64(binary.LittleEndian.Uint32(raw[4:8])) / fscale
	load15 = float64(binary.LittleEndian.Uint32(raw[8:12])) / fscale
	return load1, load5, load15
}

// readCPTime reads kern.cp_time which returns 5 × int32 CPU tick counters:
// [user, nice, sys, intr, idle].
func readCPTime() (cpuTicks, error) {
	raw, err := unix.SysctlRaw("kern.cp_time")
	if err != nil {
		return cpuTicks{}, fmt.Errorf("cpu_usage: kern.cp_time: %w", err)
	}
	// Each field is a 4-byte little-endian int32 (uint32 for our purposes).
	if len(raw) < 20 {
		return cpuTicks{}, fmt.Errorf("cpu_usage: kern.cp_time: short read (%d bytes)", len(raw))
	}
	field := func(i int) uint64 {
		return uint64(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return cpuTicks{
		user: field(0),
		nice: field(1),
		sys:  field(2),
		intr: field(3),
		idle: field(4),
	}, nil
}
