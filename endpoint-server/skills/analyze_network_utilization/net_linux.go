//go:build linux

package analyze_network_utilization

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// snapshot returns cumulative (bytesIn, bytesOut) summed across all
// non-loopback interfaces by parsing /proc/net/dev.
func snapshot() (uint64, uint64, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, fmt.Errorf("netmon: open /proc/net/dev: %w", err)
	}
	defer f.Close()

	var bytesIn, bytesOut uint64
	scanner := bufio.NewScanner(f)
	// Discard the two header lines.
	scanner.Scan()
	scanner.Scan()

	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if name == "lo" {
			continue
		}
		// Receive fields: bytes packets errs drop fifo frame compressed multicast
		// Transmit fields start at index 8.
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}
		bytesIn += rx
		bytesOut += tx
	}
	if err := scanner.Err(); err != nil {
		return bytesIn, bytesOut, fmt.Errorf("netmon: reading /proc/net/dev: %w", err)
	}
	return bytesIn, bytesOut, nil
}
