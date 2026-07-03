//go:build windows

package check_security_posture

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gatherTimeout bounds each external command used while snapshotting.
const gatherTimeout = 30 * time.Second

// gather captures the host's security posture on Windows: listening ports
// (via netstat, with process names resolved through tasklist), enabled local
// users, and Administrators group membership. Sudoers, authorized_keys, and
// setuid concepts do not apply.
func gather(ctx context.Context) Snapshot {
	var s Snapshot

	ports, err := listeningPorts(ctx)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("listening ports: %v", err))
	}
	s.ListeningPorts = ports

	users, err := localUsers(ctx)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("users: %v", err))
	}
	s.Users = users

	admins, err := administrators(ctx)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("admin users: %v", err))
	}
	s.AdminUsers = admins

	sort.Strings(s.ListeningPorts)
	sort.Strings(s.Users)
	sort.Strings(s.AdminUsers)
	return s
}

// listeningPorts parses `netstat -ano` for TCP LISTENING and UDP sockets,
// resolving PIDs to process names via tasklist when possible.
func listeningPorts(ctx context.Context) ([]string, error) {
	out, err := runCmd(ctx, "netstat", "-ano")
	if err != nil {
		return nil, err
	}
	names := pidNames(ctx)

	seen := map[string]bool{}
	var ports []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		var proto, local, pid string
		switch strings.ToUpper(fields[0]) {
		case "TCP":
			if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") {
				continue
			}
			proto, local, pid = "tcp", fields[1], fields[4]
		case "UDP":
			proto, local, pid = "udp", fields[1], fields[3]
		default:
			continue
		}
		entry := proto + " " + local
		if n, ok := names[pid]; ok {
			entry += " (" + n + ")"
		} else if pid != "" {
			entry += " (pid " + pid + ")"
		}
		if !seen[entry] {
			seen[entry] = true
			ports = append(ports, entry)
		}
	}
	return ports, nil
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

// localUsers returns enabled local accounts via PowerShell Get-LocalUser.
func localUsers(ctx context.Context) ([]string, error) {
	out, err := runCmd(ctx, "powershell", "-NoProfile", "-Command",
		"Get-LocalUser | Where-Object Enabled | Select-Object -ExpandProperty Name")
	if err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			users = append(users, name)
		}
	}
	return users, nil
}

// administrators parses `net localgroup Administrators` membership.
func administrators(ctx context.Context) ([]string, error) {
	out, err := runCmd(ctx, "net", "localgroup", "Administrators")
	if err != nil {
		return nil, err
	}
	var admins []string
	inMembers := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "----"):
			inMembers = true
		case strings.HasPrefix(trimmed, "The command completed"):
			inMembers = false
		case inMembers && trimmed != "":
			admins = append(admins, trimmed)
		}
	}
	return admins, nil
}

// runCmd executes a binary with args under the gather timeout and returns its
// stdout. A non-zero exit is an error.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gatherTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}
