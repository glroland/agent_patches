package patching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
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

// FormatUpdateReport renders a []PackageUpdate as a human-readable report
// suitable for an email body or log output.
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
	client := p.httpClient()
	for i := range pkgs {
		if desc, err := p.debianDescription(ctx, pkgs[i].Name); err == nil {
			pkgs[i].Description = desc
		} else {
			slog.Warn("updateinfo: apt-cache show failed", "pkg", pkgs[i].Name, "error", err)
		}
		cves, err := canonicalPackageCVEs(ctx, client, pkgs[i].Name)
		if err != nil {
			slog.Warn("updateinfo: Canonical CVE lookup failed", "pkg", pkgs[i].Name, "error", err)
		}
		pkgs[i].CVEs = cves
	}
	return pkgs, nil
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
// affecting pkg, then supplements with NVD CVSS scores.
// A 404 response means the package is not tracked by the Ubuntu security team
// (common for third-party packages) and is treated as "no CVEs".
func canonicalPackageCVEs(ctx context.Context, client *http.Client, pkg string) ([]CVEInfo, error) {
	apiURL := fmt.Sprintf(
		"https://ubuntu.com/security/api/v1/cves?package=%s&limit=20&status=released",
		pkg,
	)
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
		slog.Debug("updateinfo: package not tracked by Ubuntu security team", "pkg", pkg)
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
		Results []struct {
			ID       string `json:"id"`
			Priority string `json:"priority"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	cves := make([]CVEInfo, 0, len(resp.Results))
	for _, r := range resp.Results {
		c := CVEInfo{
			ID:       r.ID,
			Severity: canonicalPriorityToSeverity(r.Priority),
			URL:      "https://ubuntu.com/security/" + r.ID,
		}
		if score, sev, err := cachedNVDCVSS(ctx, client, r.ID); err == nil {
			c.CVSSScore = score
			if c.Severity == "UNKNOWN" {
				c.Severity = sev
			}
		} else {
			slog.Debug("updateinfo: NVD lookup failed", "cve", r.ID, "error", err)
		}
		cves = append(cves, c)
	}
	return cves, nil
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
		desc, cves, err := p.fedoraPackageInfo(ctx, client, pkgs[i].Name)
		if err != nil {
			slog.Warn("updateinfo: fedora package info failed", "pkg", pkgs[i].Name, "error", err)
		}
		pkgs[i].Description = desc
		pkgs[i].CVEs = cves
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

	// dnf updateinfo list --cve lists CVEs fixed by updates to this package.
	updateInfoOut, _ := p.commander.Run(ctx, "dnf", "updateinfo", "list", "--cve", pkg)
	cveIDs := ExtractCVEIDs(updateInfoOut)

	var cves []CVEInfo
	for _, id := range cveIDs {
		c := CVEInfo{
			ID:  id,
			URL: "https://access.redhat.com/security/cve/" + id,
		}
		if sev, score, err := redhatCVE(ctx, client, id); err == nil {
			c.Severity = sev
			c.CVSSScore = score
		} else {
			slog.Debug("updateinfo: Red Hat CVE API failed, trying NVD", "cve", id, "error", err)
			if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			} else {
				c.Severity = "UNKNOWN"
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
	cves := make([]CVEInfo, 0, len(cveIDs))
	for _, id := range cveIDs {
		c := CVEInfo{
			ID:       id,
			Severity: "UNKNOWN",
			URL:      "https://nvd.nist.gov/vuln/detail/" + id,
		}
		if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
			c.CVSSScore = score
			c.Severity = sev
		} else {
			slog.Debug("updateinfo: NVD lookup failed for Apple CVE", "cve", id, "error", err)
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
			if score, sev, err := cachedNVDCVSS(ctx, client, id); err == nil {
				c.CVSSScore = score
				c.Severity = sev
			} else {
				slog.Debug("updateinfo: NVD lookup failed for Windows CVE", "cve", id, "error", err)
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

	nvdState.mu.Lock()
	nvdState.lastCall = time.Now()
	nvdState.cache[cveID] = nvdEntry{score, severity, err, time.Now().Add(24 * time.Hour)}
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
