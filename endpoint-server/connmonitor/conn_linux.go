//go:build linux

package connmonitor

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// gatherTimeout bounds the ss invocation used on each poll.
const gatherTimeout = 15 * time.Second

// ssProcRe extracts the process name and pid from an ss
// users:(("name",pid=123,fd=4)) column.
var ssProcRe = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`)

// snapshot returns all active TCP/UDP connections (excluding listening
// sockets and unconnected UDP sockets, neither of which is a "connection")
// using ss, falling back to querying without process attribution if the
// agent lacks the privilege to see other users' sockets.
func snapshot(ctx context.Context) ([]Conn, error) {
	out, err := runCmd(ctx, "ss", "-tunapH")
	if err != nil {
		out, err = runCmd(ctx, "ss", "-tunaH")
		if err != nil {
			return nil, fmt.Errorf("connmonitor: ss unavailable: %w", err)
		}
	}
	return parseSS(out), nil
}

// parseSS parses `ss -tunapH` (or `-tunaH`) output. Example line:
//
//	tcp ESTAB 0 0 192.168.1.5:22 192.168.1.10:54321 users:(("sshd",pid=1234,fd=4))
//
// Listening sockets and unconnected UDP sockets report the peer address as
// "*:*" or "<addr>:*" and are skipped, since neither is an active connection.
func parseSS(out string) []Conn {
	var conns []Conn
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		proto := strings.ToLower(fields[0])
		if proto != "tcp" && proto != "udp" {
			continue
		}
		state, local, peer := fields[1], fields[4], fields[5]
		if strings.HasSuffix(peer, ":*") {
			continue
		}
		localAddr, localPort := splitHostPort(local)
		remoteAddr, remotePort := splitHostPort(peer)

		var pid, proc string
		if m := ssProcRe.FindStringSubmatch(line); m != nil {
			proc, pid = m[1], m[2]
		}

		conns = append(conns, Conn{
			Proto:      proto,
			LocalAddr:  localAddr,
			LocalPort:  localPort,
			RemoteAddr: remoteAddr,
			RemotePort: remotePort,
			State:      state,
			PID:        pid,
			Process:    proc,
		})
	}
	return conns
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
