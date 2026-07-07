//go:build windows

package connmonitor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gatherTimeout bounds each external command used on each poll.
const gatherTimeout = 15 * time.Second

// snapshot returns active TCP connections using netstat, resolving PIDs to
// process names via tasklist when possible. UDP is skipped entirely: netstat
// never reports a foreign address for UDP sockets on Windows, so there is no
// peer to record as a "connection".
func snapshot(ctx context.Context) ([]Conn, error) {
	out, err := runCmd(ctx, "netstat", "-ano")
	if err != nil {
		return nil, fmt.Errorf("connmonitor: netstat: %w", err)
	}
	return parseNetstatConns(out, pidNames(ctx)), nil
}

// parseNetstatConns parses `netstat -ano` output. Example lines:
//
//	TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1044
//	TCP    192.168.1.5:445        192.168.1.20:54321     ESTABLISHED     4
//
// Only established (non-LISTENING) TCP rows are kept.
func parseNetstatConns(out string, names map[string]string) []Conn {
	var conns []Conn
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		state := fields[3]
		if strings.EqualFold(state, "LISTENING") {
			continue
		}
		localAddr, localPort := splitHostPort(fields[1])
		remoteAddr, remotePort := splitHostPort(fields[2])
		pid := fields[4]

		conns = append(conns, Conn{
			Proto:      "tcp",
			LocalAddr:  localAddr,
			LocalPort:  localPort,
			RemoteAddr: remoteAddr,
			RemotePort: remotePort,
			State:      state,
			PID:        pid,
			Process:    names[pid],
		})
	}
	return conns
}

// pidNames maps PID -> image name using `tasklist /fo csv /nh`. Best-effort:
// returns an empty map on any failure.
func pidNames(ctx context.Context) map[string]string {
	out, err := runCmd(ctx, "tasklist", "/fo", "csv", "/nh")
	if err != nil {
		return map[string]string{}
	}
	names := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		// "Image Name","PID","Session Name","Session#","Mem Usage"
		parts := strings.Split(line, "\",\"")
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimPrefix(parts[0], "\"")
		if _, err := strconv.Atoi(parts[1]); err == nil {
			names[parts[1]] = name
		}
	}
	return names
}

// runCmd executes a binary with args under the gather timeout and returns
// its stdout. A non-zero exit is an error.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gatherTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}
