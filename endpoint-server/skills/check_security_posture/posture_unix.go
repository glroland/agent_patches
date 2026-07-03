//go:build !windows

package check_security_posture

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// gatherTimeout bounds each external command used while snapshotting.
const gatherTimeout = 30 * time.Second

// setuidSearchDirs are the standard binary directories scanned for
// setuid/setgid executables. Kept to system paths so the walk stays fast.
var setuidSearchDirs = []string{
	"/usr/bin", "/usr/sbin", "/bin", "/sbin", "/usr/local/bin", "/usr/local/sbin",
	"/usr/libexec", "/usr/lib/openssh", "/opt/bin",
}

// nonLoginShells mark accounts that cannot start an interactive session.
var nonLoginShells = map[string]bool{
	"nologin": true, "false": true, "sync": true, "shutdown": true, "halt": true,
}

// adminGroups are group names whose members count as admin users.
var adminGroups = map[string]bool{"sudo": true, "wheel": true, "admin": true}

// ssProcessRe extracts the process name from an ss -p users:(("name",pid=…))
// column.
var ssProcessRe = regexp.MustCompile(`users:\(\("([^"]+)"`)

// gather captures the host's security posture on Linux/macOS.
func gather(ctx context.Context) Snapshot {
	var s Snapshot

	ports, err := listeningPorts(ctx)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("listening ports: %v", err))
	}
	s.ListeningPorts = ports

	users, uidZero, homes, err := passwdUsers()
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("users: %v", err))
	}
	s.Users = users

	admins, err := adminUsers(uidZero)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("admin users: %v", err))
	}
	s.AdminUsers = admins

	s.SudoersHash = sudoersFingerprint()
	s.AuthorizedKeys = authorizedKeysFingerprints(homes)
	s.SetuidBinaries = setuidBinaries()

	sort.Strings(s.ListeningPorts)
	sort.Strings(s.Users)
	sort.Strings(s.AdminUsers)
	sort.Strings(s.SetuidBinaries)
	return s
}

// listeningPorts returns one formatted entry per listening socket, preferring
// ss (Linux) and falling back to lsof (macOS and hosts without iproute2).
func listeningPorts(ctx context.Context) ([]string, error) {
	if out, err := runCmd(ctx, "ss", "-tulnpH"); err == nil {
		return parseSS(out), nil
	}
	out, err := runCmd(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN")
	if err != nil {
		return nil, fmt.Errorf("neither ss nor lsof usable: %w", err)
	}
	return parseLsof(out), nil
}

// parseSS parses `ss -tulnpH` output. Example line:
//
//	tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=812,fd=3))
//
// The process column is absent when the agent lacks the privilege to see it.
func parseSS(out string) []string {
	seen := map[string]bool{}
	var ports []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto, local := fields[0], fields[4]
		entry := proto + " " + local
		if m := ssProcessRe.FindStringSubmatch(line); m != nil {
			entry += " (" + m[1] + ")"
		}
		if !seen[entry] {
			seen[entry] = true
			ports = append(ports, entry)
		}
	}
	return ports
}

// parseLsof parses `lsof -nP -iTCP -sTCP:LISTEN` output. Example line:
//
//	sshd 812 root 3u IPv4 0x0 0t0 TCP *:22 (LISTEN)
func parseLsof(out string) []string {
	seen := map[string]bool{}
	var ports []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || fields[0] == "COMMAND" {
			continue
		}
		entry := "tcp " + fields[8] + " (" + fields[0] + ")"
		if !seen[entry] {
			seen[entry] = true
			ports = append(ports, entry)
		}
	}
	return ports
}

// passwdUsers reads /etc/passwd and returns the login-capable users
// ("name (uid N)"), the uid-0 account names, and a user->home map for
// authorized_keys fingerprinting.
func passwdUsers() (users []string, uidZero []string, homes map[string]string, err error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, nil, nil, err
	}
	homes = make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 7 || strings.HasPrefix(parts[0], "#") {
			continue
		}
		name, uid, home, shell := parts[0], parts[2], parts[5], parts[6]
		if uid == "0" {
			uidZero = append(uidZero, name)
		}
		if nonLoginShells[filepath.Base(shell)] || shell == "" {
			continue
		}
		users = append(users, fmt.Sprintf("%s (uid %s)", name, uid))
		if home != "" && home != "/" {
			homes[name] = home
		}
	}
	return users, uidZero, homes, nil
}

// adminUsers returns uid-0 accounts plus members of the admin groups from
// /etc/group.
func adminUsers(uidZero []string) ([]string, error) {
	set := map[string]bool{}
	for _, u := range uidZero {
		set[u] = true
	}

	data, err := os.ReadFile("/etc/group")
	if err != nil {
		return setToList(set), err
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 4 || !adminGroups[parts[0]] {
			continue
		}
		for _, m := range strings.Split(parts[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				set[m] = true
			}
		}
	}
	return setToList(set), nil
}

func setToList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sudoersFingerprint fingerprints /etc/sudoers and every file under
// /etc/sudoers.d, combined into one hash so any change anywhere shows up.
func sudoersFingerprint() string {
	paths := []string{"/etc/sudoers"}
	if entries, err := os.ReadDir("/etc/sudoers.d"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				paths = append(paths, filepath.Join("/etc/sudoers.d", e.Name()))
			}
		}
	}

	h := sha256.New()
	any := false
	for _, p := range paths {
		fp := fingerprintFile(p)
		if fp == "" {
			continue
		}
		any = true
		fmt.Fprintf(h, "%s=%s\n", p, fp)
	}
	if !any {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// fingerprintFile returns a stable fingerprint for path: a content sha256
// when readable, otherwise an mtime+size fingerprint (sudoers and other
// users' files are typically unreadable to the agent user, but stat still
// reveals modification). Returns "" if the file does not exist.
func fingerprintFile(path string) string {
	if data, err := os.ReadFile(path); err == nil {
		return fmt.Sprintf("sha256:%x", sha256.Sum256(data))[:23]
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("meta:%d:%d", fi.ModTime().Unix(), fi.Size())
}

// authorizedKeysFingerprints fingerprints each user's ~/.ssh/authorized_keys.
func authorizedKeysFingerprints(homes map[string]string) map[string]string {
	out := map[string]string{}
	for user, home := range homes {
		fp := fingerprintFile(filepath.Join(home, ".ssh", "authorized_keys"))
		if fp != "" {
			out[user] = fp
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setuidBinaries walks the standard binary directories and returns regular
// files with the setuid or setgid bit set. Directories are deduplicated by
// resolved path (e.g. /bin is usually a symlink to /usr/bin).
func setuidBinaries() []string {
	seenDirs := map[string]bool{}
	seenFiles := map[string]bool{}
	var out []string

	for _, dir := range setuidSearchDirs {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || seenDirs[resolved] {
			continue
		}
		seenDirs[resolved] = true

		_ = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // skip unreadable entries, keep walking
			}
			fi, err := d.Info()
			if err != nil {
				return nil //nolint:nilerr
			}
			if fi.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 && !seenFiles[path] {
				seenFiles[path] = true
				out = append(out, path)
			}
			return nil
		})
	}
	return out
}

// runCmd executes a binary with args under the gather timeout and returns its
// stdout. Stderr is discarded; a non-zero exit is an error.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gatherTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}
