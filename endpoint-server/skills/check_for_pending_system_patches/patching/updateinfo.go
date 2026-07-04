package patching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PackageUpdate describes a single pending package update.
type PackageUpdate struct {
	Name        string    // package name
	OldVersion  string    // currently installed version (empty when unknown)
	NewVersion  string    // version being upgraded to (empty when unknown)
	Description string    // brief description of the package's purpose
	CVEs        []CVEInfo // security advisories fixed by this update

	// CVELookupFailed is set when CVE enrichment for this package produced no
	// usable data because of an error (network failure, command failure,
	// context expiry). It lets the approval summary distinguish "no CVEs" from
	// "no CVE data" instead of silently presenting the update as routine.
	CVELookupFailed bool
}

// CVEInfo holds severity details for one CVE identifier.
type CVEInfo struct {
	ID        string  // "CVE-YYYY-NNNNN"
	Severity  string  // "CRITICAL", "HIGH", "MEDIUM", "LOW", or "UNKNOWN"
	CVSSScore float64 // CVSS v3 base score; 0 when unavailable
	URL       string  // canonical link to the advisory or NVD entry
}

// defaultHTTPClient is used for vendor and NVD API calls.
var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

// maxNVDLookupsPerPackage caps how many rate-limited NVD queries a single
// package may spend. NVD allows one call per ~7 s, so unbounded lookups are
// what used to blow the enrichment time budget and leave later packages with
// no CVE data at all. CVEs beyond the cap keep severity "UNKNOWN".
const maxNVDLookupsPerPackage = 8

// enrichConcurrency bounds the parallel HTTP CVE lookups during enrichment.
// Vendor APIs (Ubuntu, Red Hat) are not rate-limited the way NVD is, so a
// small worker pool keeps large update sets within the enrichment deadline.
const enrichConcurrency = 4

// nvdState rate-limits and caches NVD API lookups.
// NVD allows 5 requests per 30 s without an API key; we use 1 per 7 s (safe margin).
var nvdState = struct {
	mu       sync.Mutex
	lastCall time.Time
	cache    map[string]nvdEntry
}{cache: make(map[string]nvdEntry)}

type nvdEntry struct {
	score    float64
	severity string
	err      error
	expiry   time.Time
}

// cveIDRe matches CVE identifiers in arbitrary text.
var cveIDRe = regexp.MustCompile(`CVE-\d{4}-\d{4,}`)

// ListUpdates returns structured information about each pending package update,
// including the package description and any addressed CVEs with their severity.
//
// Network failures during CVE enrichment are logged but do not abort the list;
// partial results (packages without CVE data) are returned on failure.
func (p *Patcher) ListUpdates(ctx context.Context) ([]PackageUpdate, error) {
	slog.Debug("updateinfo: listing updates", "os", p.os)
	switch p.os {
	case OSDebian:
		return p.listDebianUpdates(ctx)
	case OSFedora:
		return p.listFedoraUpdates(ctx)
	case OSDarwin:
		return p.listDarwinUpdates(ctx)
	case OSWindows:
		return p.listWindowsUpdates(ctx)
	default:
		return nil, fmt.Errorf("unsupported OS: %s", p.os)
	}
}

// FormatUpdateReport renders a []PackageUpdate as a verbose human-readable
// report suitable for an email body or log output.
func FormatUpdateReport(updates []PackageUpdate) string {
	if len(updates) == 0 {
		return "No updates found.\n"
	}
	var sb strings.Builder
	for _, u := range updates {
		fmt.Fprintf(&sb, "Package: %s\n", u.Name)
		if u.OldVersion != "" || u.NewVersion != "" {
			fmt.Fprintf(&sb, "  Version: %s → %s\n", u.OldVersion, u.NewVersion)
		}
		if u.Description != "" {
			fmt.Fprintf(&sb, "  Description: %s\n", u.Description)
		}
		if len(u.CVEs) == 0 {
			sb.WriteString("  CVEs: none identified\n")
		} else {
			sb.WriteString("  CVEs:\n")
			for _, c := range u.CVEs {
				line := fmt.Sprintf("    - %s  [%s", c.ID, c.Severity)
				if c.CVSSScore > 0 {
					line += fmt.Sprintf(" %.1f", c.CVSSScore)
				}
				line += "]"
				if c.URL != "" {
					line += "  " + c.URL
				}
				sb.WriteString(line + "\n")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// rebootPackagePrefixes lists package name prefixes that typically require
// a full system reboot to take effect after installation.
var rebootPackagePrefixes = []string{
	"kernel", "linux-image", "linux-headers",
	"glibc", "libc6",
	"systemd",
	"udev",
	"dracut",
	"grub",
	"shim",
}

// notablePackagePrefixes lists package name prefixes that are broadly
// significant on server systems regardless of their specific workload.
var notablePackagePrefixes = []string{
	// Security / cryptography
	"openssl", "libssl", "ca-cert", "nss", "gnupg", "gpg",
	// Network tools
	"curl", "libcurl", "wget", "rsync", "openssh", "libssh",
	// Container runtime
	"podman", "docker", "containerd", "cri-o", "runc", "buildah",
	// Language runtimes
	"python3", "python2", "nodejs", "java", "openjdk", "ruby", "perl", "php", "golang",
	// Web / app servers
	"nginx", "httpd", "apache",
	// Databases
	"postgresql", "mysql", "mariadb", "redis", "mongodb",
	// Auth / identity
	"sudo", "pam", "sssd", "krb5", "libpam",
	// Package managers
	"dnf", "yum", "apt", "rpm",
}

// rebootLikely returns true when updating this package typically requires
// a full system reboot.
func rebootLikely(name string) bool {
	n := strings.ToLower(name)
	for _, prefix := range rebootPackagePrefixes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// isNotable returns true when a package is broadly significant for server
// operations (security libraries, container runtime, language runtimes, etc.).
func isNotable(name string) bool {
	n := strings.ToLower(name)
	for _, prefix := range notablePackagePrefixes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// shortestName returns the shortest name from a slice (the "root" package in a family).
func shortestName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	root := names[0]
	for _, n := range names[1:] {
		if len(n) < len(root) {
			root = n
		}
	}
	return root
}

// RiskAssessment maps pending updates to an approval risk level ("low",
// "medium", or "high") and a one-line rationale tying that level to evidence,
// so the operator sees *why* a request needs review, not just what it lists.
//
// Critical CVEs make the request high risk; any other CVE makes it medium.
// A run where most per-package CVE lookups failed is also medium, because
// "no CVEs found" cannot be distinguished from "no CVE data" — only verified
// routine updates are low risk.
func RiskAssessment(updates []PackageUpdate) (risk, rationale string) {
	var critical, high, other, failed int
	seen := make(map[string]bool)
	for _, u := range updates {
		if u.CVELookupFailed {
			failed++
		}
		for _, c := range u.CVEs {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			switch strings.ToUpper(c.Severity) {
			case "CRITICAL":
				critical++
			case "HIGH":
				high++
			default:
				other++
			}
		}
	}
	total := critical + high + other

	switch {
	case critical > 0:
		r := fmt.Sprintf("fixes %d CRITICAL", critical)
		if high > 0 {
			r += fmt.Sprintf(" and %d HIGH", high)
		}
		return "high", r + " severity CVE(s) — review promptly"

	case total > 0:
		var parts []string
		if high > 0 {
			parts = append(parts, fmt.Sprintf("%d HIGH", high))
		}
		if other > 0 {
			parts = append(parts, fmt.Sprintf("%d MEDIUM/LOW/unrated", other))
		}
		rationale = fmt.Sprintf("fixes %d CVE(s) (%s); none rated CRITICAL",
			total, strings.Join(parts, ", "))
		if failed > 0 {
			rationale += fmt.Sprintf("; CVE data unavailable for %d of %d packages",
				failed, len(updates))
		}
		return "medium", rationale

	case failed > 0 && failed*2 >= len(updates):
		return "medium", fmt.Sprintf(
			"CVE data unavailable for %d of %d packages — severity could not be assessed",
			failed, len(updates))

	default:
		rationale = "routine updates; no known CVEs addressed"
		if failed > 0 {
			rationale += fmt.Sprintf(" (CVE data unavailable for %d of %d packages)",
				failed, len(updates))
		}
		return "low", rationale
	}
}

// FormatFallbackSummary builds the approval detail used when ListUpdates could
// not produce structured update data at all. Updates ARE pending in this path,
// so it must never claim the system is up to date; it shows the raw
// package-manager output and states that severity could not be assessed.
func FormatFallbackSummary(rawCheckOutput string, listErr error) string {
	reason := "CVE analysis unavailable"
	if listErr != nil {
		reason += ": " + listErr.Error()
	}
	const maxRaw = 3000
	raw := strings.TrimSpace(rawCheckOutput)
	if len(raw) > maxRaw {
		raw = raw[:maxRaw] + "\n… (truncated)"
	}
	return fmt.Sprintf(
		"Risk: MEDIUM — %s; update severity could not be assessed.\n\n"+
			"Pending updates as reported by the package manager:\n\n%s",
		reason, raw)
}

// FormatUpdateSummary produces an operator-readable dashboard summary of pending
// updates. It surfaces what matters most:
//
//  1. A "Risk: LEVEL — rationale" header explaining why the request needs review.
//  2. One bullet per HIGH/CRITICAL CVE being addressed.
//  3. One bullet per package whose fixes are all MEDIUM/LOW/unrated CVEs.
//  4. One bullet summarising packages that will require a reboot.
//  5. One bullet per version-group of notable server packages (security libs,
//     container runtime, language runtimes, etc.) not already covered by a CVE bullet.
//  6. A count of remaining packages, and a call-out of packages whose CVE
//     lookup failed.
func FormatUpdateSummary(host string, osType OSType, updates []PackageUpdate) string {
	if len(updates) == 0 {
		return "All packages are up to date."
	}

	// ── Categorise packages ──────────────────────────────────────────────────

	type cat int
	const (
		catOther    cat = iota
		catReboot       // requires reboot (kernel, glibc, dracut, …)
		catNotable      // important server package, no CVE
		catMinorCVE     // only MEDIUM/LOW/unrated CVEs — gets a per-package bullet
		catCVE          // has HIGH or CRITICAL CVE — gets its own CVE bullet
	)

	pkgCat := make(map[string]cat, len(updates))
	for _, u := range updates {
		switch {
		case rebootLikely(u.Name):
			pkgCat[u.Name] = catReboot
		case isNotable(u.Name):
			pkgCat[u.Name] = catNotable
		}
	}

	// ── Collect HIGH/CRITICAL CVEs ────────────────────────────────────────────

	type cveRef struct {
		info    CVEInfo
		pkgName string
		version string
	}
	var topCVEs []cveRef
	seenCVE := make(map[string]bool)
	for _, u := range updates {
		for _, c := range u.CVEs {
			sev := strings.ToUpper(c.Severity)
			if (sev == "CRITICAL" || sev == "HIGH") && !seenCVE[c.ID] {
				seenCVE[c.ID] = true
				topCVEs = append(topCVEs, cveRef{info: c, pkgName: u.Name, version: u.NewVersion})
				// Promote the package so it doesn't also appear in notable bullets.
				pkgCat[u.Name] = catCVE
			}
		}
	}
	// Sort: CRITICAL before HIGH, then descending CVSS score.
	sort.Slice(topCVEs, func(i, j int) bool {
		si, sj := strings.ToUpper(topCVEs[i].info.Severity), strings.ToUpper(topCVEs[j].info.Severity)
		if si != sj {
			return si == "CRITICAL"
		}
		return topCVEs[i].info.CVSSScore > topCVEs[j].info.CVSSScore
	})

	// ── Collect packages whose fixes are all MEDIUM/LOW/unrated CVEs ─────────
	// These used to be invisible in approvals: the risk level said "medium"
	// while the detail showed only a bare package list. One bullet per package.

	var minorPkgs []PackageUpdate
	for _, u := range updates {
		if len(u.CVEs) == 0 || pkgCat[u.Name] == catCVE {
			continue
		}
		pkgCat[u.Name] = catMinorCVE
		minorPkgs = append(minorPkgs, u)
	}

	// ── Collect reboot packages ───────────────────────────────────────────────

	var rebootPkgs []PackageUpdate
	for _, u := range updates {
		if pkgCat[u.Name] == catReboot {
			rebootPkgs = append(rebootPkgs, u)
		}
	}

	// ── Collect notable packages (version-grouped) ────────────────────────────

	// Suppress notable packages whose version is already represented by a CVE
	// bullet — they're companion libraries covered by the same update.
	cveVersions := make(map[string]bool)
	for _, ref := range topCVEs {
		cveVersions[ref.version] = true
	}

	type versionGroup struct {
		names   []string
		version string
		desc    string // description of the root package in the group
	}
	var notableGroups []*versionGroup
	notableIdx := make(map[string]int)
	for _, u := range updates {
		if pkgCat[u.Name] != catNotable {
			continue
		}
		if cveVersions[u.NewVersion] {
			continue // companion package already implied by a CVE bullet
		}
		v := u.NewVersion
		if idx, ok := notableIdx[v]; ok {
			g := notableGroups[idx]
			g.names = append(g.names, u.Name)
			if g.desc == "" && u.Description != "" {
				g.desc = u.Description
			}
		} else {
			notableIdx[v] = len(notableGroups)
			notableGroups = append(notableGroups, &versionGroup{
				names:   []string{u.Name},
				version: v,
				desc:    u.Description,
			})
		}
	}

	// ── Count remaining ───────────────────────────────────────────────────────

	remaining := 0
	for _, u := range updates {
		if pkgCat[u.Name] == catOther {
			remaining++
		}
	}

	// ── Render ────────────────────────────────────────────────────────────────

	var sb strings.Builder

	// 1. Risk header — why this request needs review, not just what it lists.
	risk, rationale := RiskAssessment(updates)
	fmt.Fprintf(&sb, "Risk: %s — %s\n\n", strings.ToUpper(risk), rationale)

	// 2. CVE bullets — one per HIGH/CRITICAL CVE.
	for _, ref := range topCVEs {
		sev := strings.ToUpper(ref.info.Severity)
		line := fmt.Sprintf("• [%s", sev)
		if ref.info.CVSSScore > 0 {
			line += fmt.Sprintf(" %.1f", ref.info.CVSSScore)
		}
		line += fmt.Sprintf("] %s in %s → %s", ref.info.ID, ref.pkgName, ref.version)
		sb.WriteString(line + "\n")
	}

	// 3. Minor CVE bullets — packages fixing only MEDIUM/LOW/unrated CVEs.
	for _, u := range minorPkgs {
		const maxListed = 3
		var refs []string
		for i, c := range u.CVEs {
			if i == maxListed {
				refs = append(refs, fmt.Sprintf("+%d more", len(u.CVEs)-maxListed))
				break
			}
			sev := strings.ToUpper(c.Severity)
			if sev == "" || sev == "UNKNOWN" {
				sev = "unrated"
			}
			refs = append(refs, fmt.Sprintf("%s (%s)", c.ID, sev))
		}
		fmt.Fprintf(&sb, "• %s → %s fixes %s\n", u.Name, u.NewVersion, strings.Join(refs, ", "))
	}

	// 4. Reboot bullet.
	if len(rebootPkgs) > 0 {
		if len(rebootPkgs) <= 3 {
			names := make([]string, len(rebootPkgs))
			for i, p := range rebootPkgs {
				names[i] = p.Name
			}
			fmt.Fprintf(&sb, "• Reboot required — %s\n", strings.Join(names, ", "))
		} else {
			// Group reboot packages by version and summarise as "root ×N".
			rebootByVer := make(map[string][]string)
			var verOrder []string
			for _, p := range rebootPkgs {
				v := p.NewVersion
				if _, seen := rebootByVer[v]; !seen {
					verOrder = append(verOrder, v)
				}
				rebootByVer[v] = append(rebootByVer[v], p.Name)
			}
			var parts []string
			for _, v := range verOrder {
				names := rebootByVer[v]
				root := shortestName(names)
				if len(names) == 1 {
					parts = append(parts, root)
				} else {
					parts = append(parts, fmt.Sprintf("%s ×%d", root, len(names)))
				}
			}
			fmt.Fprintf(&sb, "• Reboot required — %d packages (%s)\n",
				len(rebootPkgs), strings.Join(parts, ", "))
		}
	}

	// 5. Notable package bullets.
	for _, g := range notableGroups {
		line := fmt.Sprintf("• %s → %s", strings.Join(g.names, ", "), g.version)
		if g.desc != "" {
			line += fmt.Sprintf(" (%s)", g.desc)
		}
		sb.WriteString(line + "\n")
	}

	// 6. Remaining count.
	if remaining > 0 {
		called := len(topCVEs) + len(minorPkgs) + len(rebootPkgs) + len(notableGroups)
		if called == 0 {
			fmt.Fprintf(&sb, "• %d packages ready to update\n", remaining)
		} else {
			fmt.Fprintf(&sb, "• %d additional packages\n", remaining)
		}
	}

	// 7. Packages whose CVE lookup failed — "no CVEs" must not be conflated
	// with "no CVE data".
	var failedNames []string
	for _, u := range updates {
		if u.CVELookupFailed {
			failedNames = append(failedNames, u.Name)
		}
	}
	if len(failedNames) > 0 {
		shown := failedNames
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), "…")
		}
		fmt.Fprintf(&sb, "• CVE data unavailable for %d package(s): %s\n",
			len(failedNames), strings.Join(shown, ", "))
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Debian / Ubuntu
// ---------------------------------------------------------------------------

func (p *Patcher) listDebianUpdates(ctx context.Context) ([]PackageUpdate, error) {
	if _, err := p.commander.Run(ctx, "apt-get", "update", "-q"); err != nil {
		return nil, fmt.Errorf("apt-get update: %w", err)
	}
	out, err := p.commander.Run(ctx, "apt-get", "upgrade", "--dry-run")
	if err != nil {
		return nil, fmt.Errorf("apt-get upgrade --dry-run: %w", err)
	}

	pkgs := ParseDebianInstLines(out)
	codename := debianCodename()

	// Phase 1 (serial, local commands): package descriptions and the CVE IDs
	// named in the changelog delta between the installed and candidate
	// versions. The delta is what this update actually fixes; the Ubuntu API
	// alone would report CVEs the host may have patched long ago.
	deltaIDs := make([][]string, len(pkgs))
	deltaOK := make([]bool, len(pkgs))
	for i := range pkgs {
		if ctx.Err() != nil {
			pkgs[i].CVELookupFailed = true
			continue
		}
		if desc, err := p.debianDescription(ctx, pkgs[i].Name); err == nil {
			pkgs[i].Description = desc
		} else {
			slog.Warn("updateinfo: apt-cache show failed", "pkg", pkgs[i].Name, "error", err)
		}
		deltaIDs[i], deltaOK[i] = p.debianChangelogCVEs(ctx, pkgs[i].Name, pkgs[i].OldVersion)
	}

	// Phase 2 (bounded concurrency, HTTP): severity from the Ubuntu Security
	// API, with NVD only for CVEs the API does not rate.
	client := p.httpClient()
	sem := make(chan struct{}, enrichConcurrency)
	var wg sync.WaitGroup
	for i := range pkgs {
		if pkgs[i].CVELookupFailed {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			apiCVEs, err := canonicalPackageCVEs(ctx, client, pkgs[i].Name, codename)
			if err != nil {
				slog.Warn("updateinfo: Canonical CVE lookup failed", "pkg", pkgs[i].Name, "error", err)
			}
			pkgs[i].CVEs = resolveDebianCVEs(ctx, client, deltaIDs[i], deltaOK[i], apiCVEs)
			// Flag failure only when neither source produced usable data.
			pkgs[i].CVELookupFailed = err != nil && !deltaOK[i]
		}(i)
	}
	wg.Wait()
	return pkgs, nil
}

// resolveDebianCVEs picks the CVE set for one package. When the changelog
// delta is known it is authoritative: only CVEs fixed between the installed
// and candidate versions are reported, with severity taken from the Ubuntu
// API results and a bounded number of NVD lookups for the rest. Without a
// delta the API list is used as-is — potentially overbroad, but honest about
// what the vendor tracks for the package.
func resolveDebianCVEs(ctx context.Context, client *http.Client, deltaIDs []string, deltaOK bool, apiCVEs []CVEInfo) []CVEInfo {
	if !deltaOK {
		return apiCVEs
	}
	byID := make(map[string]CVEInfo, len(apiCVEs))
	for _, c := range apiCVEs {
		byID[c.ID] = c
	}
	nvdBudget := maxNVDLookupsPerPackage
	cves := make([]CVEInfo, 0, len(deltaIDs))
	for _, id := range deltaIDs {
		if c, ok := byID[id]; ok {
			cves = append(cves, c)
			continue
		}
		c := CVEInfo{ID: id, Severity: "UNKNOWN", URL: "https://ubuntu.com/security/" + id}
		if nvdBudget > 0 {
			nvdBudget--
			if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			} else {
				slog.Debug("updateinfo: NVD lookup failed", "cve", id, "error", err)
			}
		}
		cves = append(cves, c)
	}
	return cves
}

// debianCodename returns VERSION_CODENAME from /etc/os-release (e.g. "noble"),
// or "" when unavailable. Used to scope Ubuntu Security API queries to the
// running release instead of every release Canonical tracks.
func debianCodename() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VERSION_CODENAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// debianChangelogCVEs fetches the candidate changelog for pkg and extracts the
// CVE IDs mentioned in entries newer than the installed version. The boolean
// reports whether the installed version was found in the changelog — when it
// is not (new installs, third-party repos, download failures), the caller must
// fall back to the less precise package-level CVE list.
func (p *Patcher) debianChangelogCVEs(ctx context.Context, pkg, oldVersion string) ([]string, bool) {
	if oldVersion == "" {
		return nil, false
	}
	out, err := p.commander.Run(ctx, "apt-get", "changelog", pkg)
	if err != nil {
		slog.Debug("updateinfo: apt-get changelog failed", "pkg", pkg, "error", err)
		return nil, false
	}
	return DebianChangelogCVEsSince(out, oldVersion)
}

// changelogEntryRe matches a Debian changelog entry header, e.g.
// "curl (8.5.0-2ubuntu10.6) noble-security; urgency=medium".
// Body and signature lines are indented, so ^\S anchors headers only.
var changelogEntryRe = regexp.MustCompile(`^\S+ \(([^)]+)\)`)

// DebianChangelogCVEsSince scans a Debian changelog (newest entry first) and
// returns the unique CVE IDs mentioned in entries strictly newer than
// oldVersion. The boolean is false when oldVersion never appears — the delta
// cannot be established and the result must not be trusted.
func DebianChangelogCVEsSince(changelog, oldVersion string) ([]string, bool) {
	seen := make(map[string]bool)
	var ids []string
	for _, line := range strings.Split(changelog, "\n") {
		if m := changelogEntryRe.FindStringSubmatch(line); m != nil && m[1] == oldVersion {
			return ids, true
		}
		for _, id := range cveIDRe.FindAllString(line, -1) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return nil, false
}

// ParseDebianInstLines extracts package name and versions from apt dry-run "Inst" lines.
// Example: "Inst curl [7.81.0-1ubuntu1.15] (8.5.0-2ubuntu10.6 Ubuntu:24.04/noble-updates [amd64])"
// The arch qualifier (e.g. ":amd64") is stripped from the package name.
func ParseDebianInstLines(output string) []PackageUpdate {
	re := regexp.MustCompile(`^Inst\s+(\S+)(?:\s+\[([^\]]+)\])?\s+\((\S+)`)
	var pkgs []PackageUpdate
	for _, line := range strings.Split(output, "\n") {
		m := re.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		name := m[1]
		// Strip arch qualifier (e.g. "libssl3:amd64" → "libssl3").
		if idx := strings.Index(name, ":"); idx > 0 {
			name = name[:idx]
		}
		pkgs = append(pkgs, PackageUpdate{
			Name:       name,
			OldVersion: m[2],
			NewVersion: m[3],
		})
	}
	return pkgs
}

func (p *Patcher) debianDescription(ctx context.Context, pkg string) (string, error) {
	out, err := p.commander.Run(ctx, "apt-cache", "show", "--no-all-versions", pkg)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		for _, prefix := range []string{"Description-en: ", "Description: "} {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
			}
		}
	}
	return "", nil
}

// canonicalPackageCVEs queries the Ubuntu Security API for released CVEs
// affecting pkg, scoped to the given release codename when known.
// A 404 response means the package is not tracked by the Ubuntu security team
// (common for third-party packages) and is treated as "no CVEs".
//
// Severity comes from Canonical's priority rating. NVD — slow because of its
// rate limit — is consulted only for CVEs Canonical has not rated, and only up
// to maxNVDLookupsPerPackage times.
func canonicalPackageCVEs(ctx context.Context, client *http.Client, pkg, codename string) ([]CVEInfo, error) {
	query := "package=" + url.QueryEscape(pkg) + "&limit=20&status=released"
	if codename != "" {
		query += "&version=" + url.QueryEscape(codename)
	}

	results, err := canonicalCVEQuery(ctx, client, query)
	if err != nil && codename != "" && ctx.Err() == nil {
		// The release filter is best-effort; retry unscoped if the API
		// rejects it rather than losing CVE data entirely.
		slog.Debug("updateinfo: release-scoped CVE query failed, retrying unscoped", "pkg", pkg, "error", err)
		results, err = canonicalCVEQuery(ctx, client, "package="+url.QueryEscape(pkg)+"&limit=20&status=released")
	}
	if err != nil {
		return nil, err
	}
	if results == nil {
		slog.Debug("updateinfo: package not tracked by Ubuntu security team", "pkg", pkg)
		return nil, nil
	}

	nvdBudget := maxNVDLookupsPerPackage
	cves := make([]CVEInfo, 0, len(results))
	for _, r := range results {
		c := CVEInfo{
			ID:       r.ID,
			Severity: canonicalPriorityToSeverity(r.Priority),
			URL:      "https://ubuntu.com/security/" + r.ID,
		}
		if c.Severity == "UNKNOWN" && nvdBudget > 0 {
			nvdBudget--
			if score, sev, err := cachedNVDCVSS(ctx, client, r.ID); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			} else {
				slog.Debug("updateinfo: NVD lookup failed", "cve", r.ID, "error", err)
			}
		}
		cves = append(cves, c)
	}
	return cves, nil
}

type canonicalCVEResult struct {
	ID       string `json:"id"`
	Priority string `json:"priority"`
}

// canonicalCVEQuery performs one Ubuntu Security API list request. A 404
// (untracked package) returns (nil, nil); success always returns a non-nil
// (possibly empty) slice.
func canonicalCVEQuery(ctx context.Context, client *http.Client, query string) ([]canonicalCVEResult, error) {
	apiURL := "https://ubuntu.com/security/api/v1/cves?" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-patches/1.0")

	httpResp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", httpResp.StatusCode, apiURL)
	}

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []canonicalCVEResult `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Results == nil {
		resp.Results = []canonicalCVEResult{}
	}
	return resp.Results, nil
}

func canonicalPriorityToSeverity(priority string) string {
	switch strings.ToLower(priority) {
	case "critical":
		return "CRITICAL"
	case "high":
		return "HIGH"
	case "medium":
		return "MEDIUM"
	case "low", "negligible":
		return "LOW"
	default:
		return "UNKNOWN"
	}
}

// ---------------------------------------------------------------------------
// Fedora / RHEL / CentOS
// ---------------------------------------------------------------------------

func (p *Patcher) listFedoraUpdates(ctx context.Context) ([]PackageUpdate, error) {
	out, err := p.commander.Run(ctx, "dnf", "check-update")
	if err != nil {
		var ec *ExitCodeError
		if errors.As(err, &ec) && ec.ExitCode() == 100 {
			// exit 100 = updates available — expected path
		} else {
			out2, err2 := p.commander.Run(ctx, "yum", "check-update")
			if err2 == nil {
				return nil, nil // yum exit 0 = no updates
			}
			if errors.As(err2, &ec) && ec.ExitCode() == 100 {
				out = out2
			} else {
				return nil, fmt.Errorf("dnf: %w; yum: %v", err, err2)
			}
		}
	} else {
		return nil, nil // dnf exit 0 = no updates
	}

	pkgs := ParseFedoraCheckUpdate(out)
	// Deduplicate packages (same name, different arch).
	seen := make(map[string]bool)
	deduped := pkgs[:0]
	for _, pkg := range pkgs {
		if !seen[pkg.Name] {
			seen[pkg.Name] = true
			deduped = append(deduped, pkg)
		}
	}
	pkgs = deduped

	client := p.httpClient()
	for i := range pkgs {
		if ctx.Err() != nil {
			pkgs[i].CVELookupFailed = true
			continue
		}
		desc, cves, err := p.fedoraPackageInfo(ctx, client, pkgs[i].Name)
		if err != nil {
			slog.Warn("updateinfo: fedora package info failed", "pkg", pkgs[i].Name, "error", err)
		}
		pkgs[i].Description = desc
		pkgs[i].CVEs = cves
		pkgs[i].CVELookupFailed = err != nil
	}
	return pkgs, nil
}

// ParseFedoraCheckUpdate parses "name.arch  version  repo" lines from dnf/yum check-update.
func ParseFedoraCheckUpdate(output string) []PackageUpdate {
	var pkgs []PackageUpdate
	skip := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Obsoleting") || strings.HasPrefix(trimmed, "Security:") {
			skip = true
			continue
		}
		if strings.HasPrefix(trimmed, "Last metadata") || strings.HasPrefix(trimmed, "Error") {
			continue
		}
		if skip {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		// Package lines have "name.arch" format; skip lines without a dot.
		dotIdx := strings.LastIndex(fields[0], ".")
		if dotIdx <= 0 {
			continue
		}
		name := fields[0][:dotIdx]
		if name == "" {
			continue
		}
		pkgs = append(pkgs, PackageUpdate{
			Name:       name,
			NewVersion: fields[1],
		})
	}
	return pkgs
}

func (p *Patcher) fedoraPackageInfo(ctx context.Context, client *http.Client, pkg string) (string, []CVEInfo, error) {
	var desc string
	if out, err := p.commander.Run(ctx, "dnf", "info", pkg); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Summary") && strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					desc = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// dnf updateinfo info prints the advisory text (including "CVEs:" rows) for
	// advisories that apply to *pending* updates of this package — already
	// scoped to the delta this run will install, unlike a package-level CVE
	// history. (The previous `list --cve <pkg>` form misused --cve, which
	// filters BY a CVE ID, and returned nothing.)
	updateInfoOut, uiErr := p.commander.Run(ctx, "dnf", "updateinfo", "info", pkg)
	if uiErr != nil {
		return desc, nil, fmt.Errorf("dnf updateinfo info %s: %w", pkg, uiErr)
	}
	cveIDs := ExtractCVEIDs(updateInfoOut)

	nvdBudget := maxNVDLookupsPerPackage
	var cves []CVEInfo
	for _, id := range cveIDs {
		c := CVEInfo{
			ID:       id,
			Severity: "UNKNOWN",
			URL:      "https://access.redhat.com/security/cve/" + id,
		}
		if sev, score, err := redhatCVE(ctx, client, id); err == nil {
			c.Severity = sev
			c.CVSSScore = score
		} else if nvdBudget > 0 {
			slog.Debug("updateinfo: Red Hat CVE API failed, trying NVD", "cve", id, "error", err)
			nvdBudget--
			if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			}
		}
		cves = append(cves, c)
	}
	return desc, cves, nil
}

// redhatCVE queries the Red Hat Security Data API for CVE severity and CVSS score.
func redhatCVE(ctx context.Context, client *http.Client, cveID string) (severity string, score float64, err error) {
	url := "https://access.redhat.com/labs/securitydataapi/cve/" + cveID + ".json"
	var resp struct {
		ThreatSeverity string `json:"threat_severity"`
		CVSS3          struct {
			Score float64 `json:"score"`
		} `json:"cvss3"`
	}
	if err := fetchJSON(ctx, client, url, &resp); err != nil {
		return "", 0, err
	}
	if resp.ThreatSeverity == "" {
		return "", 0, fmt.Errorf("no severity in Red Hat response for %s", cveID)
	}
	return strings.ToUpper(resp.ThreatSeverity), resp.CVSS3.Score, nil
}

// ---------------------------------------------------------------------------
// macOS (Darwin)
// ---------------------------------------------------------------------------

func (p *Patcher) listDarwinUpdates(ctx context.Context) ([]PackageUpdate, error) {
	out, err := p.commander.Run(ctx, "softwareupdate", "-l")
	if err != nil {
		return nil, fmt.Errorf("softwareupdate -l: %w", err)
	}
	if strings.Contains(out, "No new software available") {
		return nil, nil
	}

	pkgs := ParseDarwinUpdateList(out)
	client := p.httpClient()
	if err := enrichDarwinCVEs(ctx, client, pkgs); err != nil {
		slog.Warn("updateinfo: Apple security CVE lookup failed", "error", err)
		for i := range pkgs {
			pkgs[i].CVELookupFailed = true
		}
	}
	return pkgs, nil
}

// ParseDarwinUpdateList parses softwareupdate -l output into PackageUpdates.
func ParseDarwinUpdateList(output string) []PackageUpdate {
	var pkgs []PackageUpdate
	labelRe := regexp.MustCompile(`\*\s+Label:\s+(.+)`)
	titleRe := regexp.MustCompile(`Title:\s+([^,]+)`)
	var cur *PackageUpdate
	for _, line := range strings.Split(output, "\n") {
		if m := labelRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				pkgs = append(pkgs, *cur)
			}
			cur = &PackageUpdate{Name: strings.TrimSpace(m[1])}
		} else if cur != nil {
			if m := titleRe.FindStringSubmatch(line); m != nil {
				cur.Description = strings.TrimSpace(m[1])
			}
		}
	}
	if cur != nil {
		pkgs = append(pkgs, *cur)
	}
	return pkgs
}

// StripBuildNumber removes the build-number suffix from a softwareupdate label.
// "macOS Sequoia 15.4.1-24E263" → "macOS Sequoia 15.4.1"
func StripBuildNumber(label string) string {
	idx := strings.LastIndex(label, "-")
	if idx <= 0 {
		return strings.TrimSpace(label)
	}
	rest := label[idx+1:]
	// Only strip if the suffix looks like a build number (no spaces, non-empty).
	if rest != "" && !strings.ContainsAny(rest, " ") {
		return strings.TrimSpace(label[:idx])
	}
	return strings.TrimSpace(label)
}

func enrichDarwinCVEs(ctx context.Context, client *http.Client, pkgs []PackageUpdate) error {
	releases, err := appleSecurityReleases(ctx, client)
	if err != nil {
		return fmt.Errorf("fetching Apple security releases: %w", err)
	}

	for i := range pkgs {
		title := StripBuildNumber(pkgs[i].Name)
		releaseURL, ok := releases[title]
		if !ok {
			// Fuzzy match: "macOS Sequoia 15.4.1" against "macOS Sequoia 15.4.1" in map values.
			for relName, relURL := range releases {
				if strings.Contains(relName, title) {
					releaseURL = relURL
					ok = true
					break
				}
			}
		}
		if !ok {
			slog.Debug("updateinfo: no Apple security release found", "update", title)
			continue
		}
		cves, err := appleReleaseCVEs(ctx, client, releaseURL)
		if err != nil {
			slog.Warn("updateinfo: Apple release CVE fetch failed", "url", releaseURL, "error", err)
			continue
		}
		pkgs[i].CVEs = cves
	}
	return nil
}

// appleSecurityReleases fetches the Apple security releases index page and returns
// a map of product title → security-release page URL.
func appleSecurityReleases(ctx context.Context, client *http.Client) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://support.apple.com/en-us/100100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agent-patches/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	// Extract: href="/en-us/NNNNNN">About the security content of Product X.Y.Z</a>
	linkRe := regexp.MustCompile(`href="(/en-us/\d+)"[^>]*>\s*About the security content of ([^<]+)<`)
	releases := make(map[string]string)
	for _, m := range linkRe.FindAllStringSubmatch(string(body), -1) {
		releases[strings.TrimSpace(m[2])] = "https://support.apple.com" + m[1]
	}
	return releases, nil
}

// appleReleaseCVEs fetches an Apple security release page and extracts CVE IDs with NVD severity.
func appleReleaseCVEs(ctx context.Context, client *http.Client, pageURL string) ([]CVEInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agent-patches/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	cveIDs := ExtractCVEIDs(string(body))
	// Apple releases can fix hundreds of CVEs; NVD's rate limit makes rating
	// them all impossible, so spend the bounded budget and leave the rest UNKNOWN.
	nvdBudget := maxNVDLookupsPerPackage
	cves := make([]CVEInfo, 0, len(cveIDs))
	for _, id := range cveIDs {
		c := CVEInfo{
			ID:       id,
			Severity: "UNKNOWN",
			URL:      "https://nvd.nist.gov/vuln/detail/" + id,
		}
		if nvdBudget > 0 {
			nvdBudget--
			if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			} else {
				slog.Debug("updateinfo: NVD lookup failed for Apple CVE", "cve", id, "error", err)
			}
		}
		cves = append(cves, c)
	}
	return cves, nil
}

// ---------------------------------------------------------------------------
// Windows
// ---------------------------------------------------------------------------

func (p *Patcher) listWindowsUpdates(ctx context.Context) ([]PackageUpdate, error) {
	// PowerShell outputs tab-delimited UPDATE lines.
	// Pipe characters in title/description are replaced to avoid splitting ambiguity.
	script := strings.Join([]string{
		`$ErrorActionPreference = 'Stop'`,
		`$session  = New-Object -ComObject Microsoft.Update.Session`,
		`$searcher = $session.CreateUpdateSearcher()`,
		`$results  = $searcher.Search("IsInstalled=0 and Type='Software'")`,
		`foreach ($u in $results.Updates) {`,
		`  $kbs   = ($u.KBArticleIDs | ForEach-Object { "KB$_" }) -join ","`,
		`  $urls  = ($u.MoreInfoUrls  | ForEach-Object { $_ })    -join " "`,
		`  $title = $u.Title          -replace "[\r\n|||]"," "`,
		`  $desc  = $u.Description    -replace "[\r\n|||]"," "`,
		`  Write-Output "UPDATE|||$title|||$kbs|||$desc|||$urls"`,
		`}`,
	}, "; ")
	out, err := p.commander.Run(ctx, "powershell.exe", "-ExecutionPolicy", "Bypass", "-NoProfile", "-Command", script)
	if err != nil {
		return nil, fmt.Errorf("windows update list: %w", err)
	}

	client := p.httpClient()
	var pkgs []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|||", 5)
		if len(parts) < 2 || parts[0] != "UPDATE" {
			continue
		}
		title := parts[1]
		desc := ""
		urls := ""
		if len(parts) >= 4 {
			desc = strings.TrimSpace(parts[3])
		}
		if len(parts) >= 5 {
			urls = strings.TrimSpace(parts[4])
		}

		pkg := PackageUpdate{Name: title, Description: desc}
		// CVE IDs sometimes appear in description text or MoreInfoUrls.
		combined := title + " " + desc + " " + urls
		cveIDs := ExtractCVEIDs(combined)
		seen := make(map[string]bool)
		nvdBudget := maxNVDLookupsPerPackage
		for _, id := range cveIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			c := CVEInfo{
				ID:       id,
				Severity: "UNKNOWN",
				URL:      "https://msrc.microsoft.com/update-guide/en-US/vulnerability/" + id,
			}
			if nvdBudget > 0 {
				nvdBudget--
				if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
					c.CVSSScore = score
					c.Severity = sev
				} else {
					slog.Debug("updateinfo: NVD lookup failed for Windows CVE", "cve", id, "error", err)
				}
			}
			pkg.CVEs = append(pkg.CVEs, c)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// ---------------------------------------------------------------------------
// Shared utilities
// ---------------------------------------------------------------------------

// ExtractCVEIDs finds all unique CVE identifiers in s, preserving order.
func ExtractCVEIDs(s string) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, id := range cveIDRe.FindAllString(s, -1) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// cachedNVDCVSS queries the NVD API for CVSS v3 data, caching results for 24 h
// and rate-limiting to ~1 request per 7 seconds (safe under the 5/30 s limit).
func cachedNVDCVSS(ctx context.Context, client *http.Client, cveID string) (score float64, severity string, err error) {
	nvdState.mu.Lock()
	if e, ok := nvdState.cache[cveID]; ok && time.Now().Before(e.expiry) {
		nvdState.mu.Unlock()
		slog.Debug("updateinfo: NVD cache hit", "cve", cveID)
		return e.score, e.severity, e.err
	}
	// Rate limit: enforce minimum interval between actual API calls.
	const minInterval = 7 * time.Second
	wait := minInterval - time.Since(nvdState.lastCall)
	nvdState.mu.Unlock()

	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return 0, "UNKNOWN", ctx.Err()
		}
	}

	score, severity, err = nvdCVSS(ctx, client, cveID)

	// Cache successes for a day and genuine lookup failures briefly, but never
	// cache context cancellation — that is the caller's deadline expiring, not
	// a property of the CVE, and caching it would poison every lookup of this
	// CVE for the TTL (including tomorrow's run, under the old 24 h TTL).
	ttl := 24 * time.Hour
	if err != nil {
		ttl = time.Hour
	}
	nvdState.mu.Lock()
	nvdState.lastCall = time.Now()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		nvdState.cache[cveID] = nvdEntry{score, severity, err, time.Now().Add(ttl)}
	}
	nvdState.mu.Unlock()

	return score, severity, err
}

// nvdCVSS performs a single NVD API v2 lookup for the given CVE.
func nvdCVSS(ctx context.Context, client *http.Client, cveID string) (score float64, severity string, err error) {
	url := "https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=" + cveID
	var resp struct {
		Vulnerabilities []struct {
			CVE struct {
				Metrics struct {
					V31 []struct {
						Data struct {
							BaseScore    float64 `json:"baseScore"`
							BaseSeverity string  `json:"baseSeverity"`
						} `json:"cvssData"`
					} `json:"cvssMetricV31"`
					V30 []struct {
						Data struct {
							BaseScore    float64 `json:"baseScore"`
							BaseSeverity string  `json:"baseSeverity"`
						} `json:"cvssData"`
					} `json:"cvssMetricV30"`
				} `json:"metrics"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err := fetchJSON(ctx, client, url, &resp); err != nil {
		return 0, "UNKNOWN", err
	}
	if len(resp.Vulnerabilities) == 0 {
		return 0, "UNKNOWN", fmt.Errorf("NVD: %s not found", cveID)
	}
	m := resp.Vulnerabilities[0].CVE.Metrics
	if len(m.V31) > 0 {
		d := m.V31[0].Data
		return d.BaseScore, strings.ToUpper(d.BaseSeverity), nil
	}
	if len(m.V30) > 0 {
		d := m.V30[0].Data
		return d.BaseScore, strings.ToUpper(d.BaseSeverity), nil
	}
	return 0, "UNKNOWN", fmt.Errorf("NVD: no CVSS data for %s", cveID)
}

// fetchJSON performs a GET request and unmarshals the JSON response into dst.
func fetchJSON(ctx context.Context, client *http.Client, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-patches/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}

// httpClient returns the Patcher's HTTP client, falling back to defaultHTTPClient.
func (p *Patcher) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return defaultHTTPClient
}
