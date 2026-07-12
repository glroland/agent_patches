// Command migrate-memory is a one-time migration for endpoint-server hosts
// whose agent_memory predates the move of network, disk-trend, incident,
// and skill/responsibility-run state out of the flat AttrsStore into their
// own memory domains ("Network", "Disk Trends", "Incidents", "Skill
// States"). It operates directly on a memory root directory on disk, so it
// must be run locally on each host (see deploy/linux/migrate_memory.sh for
// fleet-wide orchestration) while the agent_patches service is stopped —
// running it against a live agent's memory root risks a lost update if the
// agent and this tool write attrs.json at the same time.
//
// The domain/key names below mirror endpoint-server's connmonitor,
// analyze_network_utilization, check_drives, incidents, skillstate, and
// loop packages as of the migration this tool performs. They are
// intentionally hardcoded rather than imported: this is a frozen migration
// for a specific historical schema change, and should keep working
// correctly even if those packages' internal constants are renamed or
// removed later.
//
// Usage:
//
//	migrate-memory -root /opt/agent_patches/data/memory [-dry-run] [-purge]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
)

const (
	networkDomain     = "Network"
	diskTrendsDomain  = "Disk Trends"
	incidentsDomain   = "Incidents"
	skillStatesDomain = "Skill States"

	connectionHistoryKey = "connection_history"
	rateBaselineKey      = "network_rate_baseline"
	diskTrendsKey        = "disk_trends"
	smartTrendsKey       = "smart_trends"
	incidentsKey         = "incidents"

	skillStatePrefix        = "skill_state:"
	responsibilityRunPrefix = "responsibility_run:"
)

func main() {
	root := flag.String("root", "", "Memory root directory (required) — the same path as memory.root in config.yaml")
	dryRun := flag.Bool("dry-run", false, "Print what would change without writing or deleting anything")
	purge := flag.Bool("purge", false, "Remove migrated keys from attrs.json after they've been written to their new domain. Off by default so the old copy survives as a backup until you're satisfied the migration looks right; re-run with -purge once you are.")
	flag.Parse()

	if *root == "" {
		fmt.Fprintln(os.Stderr, "usage: migrate-memory -root <memory-root> [-dry-run] [-purge]")
		os.Exit(2)
	}

	mem := memory.New(&config.MemorySettings{Root: *root})

	attrs, err := mem.Attrs().All()
	if err != nil {
		log.Fatalf("read attrs.json: %v", err)
	}
	if len(attrs) == 0 {
		fmt.Println("no attrs.json (or empty) — nothing to migrate")
		return
	}

	var actions []string
	var purgeKeys []string

	moveKey := func(key, domain string) {
		raw, ok := attrs[key]
		if !ok {
			return
		}
		verb := "migrating"
		if *dryRun {
			verb = "would migrate"
		}
		actions = append(actions, fmt.Sprintf("  %s %-32s -> domain %q", verb, key, domain))
		if !*dryRun {
			if err := mem.Domain(domain).SetKey(key, json.RawMessage(raw)); err != nil {
				log.Fatalf("write %q into domain %q: %v", key, domain, err)
			}
		}
		purgeKeys = append(purgeKeys, key)
	}

	moveKey(connectionHistoryKey, networkDomain)
	moveKey(rateBaselineKey, networkDomain)
	moveKey(diskTrendsKey, diskTrendsDomain)
	moveKey(smartTrendsKey, diskTrendsDomain)

	// Incidents is a whole-blob domain (Write/ReadCurrent), not key mode.
	if raw, ok := attrs[incidentsKey]; ok {
		verb := "migrating"
		if *dryRun {
			verb = "would migrate"
		}
		actions = append(actions, fmt.Sprintf("  %s %-32s -> domain %q (whole blob)", verb, incidentsKey, incidentsDomain))
		if !*dryRun {
			if err := mem.Domain(incidentsDomain).Write(json.RawMessage(raw)); err != nil {
				log.Fatalf("write %q into domain %q: %v", incidentsKey, incidentsDomain, err)
			}
		}
		purgeKeys = append(purgeKeys, incidentsKey)
	}

	// Skill states and responsibility runs: an unbounded number of
	// dynamically-named keys, one per skill/responsibility. Sorted for
	// deterministic, readable -dry-run output.
	var dynamicKeys []string
	for k := range attrs {
		if strings.HasPrefix(k, skillStatePrefix) || strings.HasPrefix(k, responsibilityRunPrefix) {
			dynamicKeys = append(dynamicKeys, k)
		}
	}
	sort.Strings(dynamicKeys)
	for _, k := range dynamicKeys {
		moveKey(k, skillStatesDomain)
	}

	if len(actions) == 0 {
		fmt.Println("nothing to migrate — attrs.json has none of the old keys")
		return
	}

	sort.Strings(actions)
	for _, a := range actions {
		fmt.Println(a)
	}

	if *dryRun {
		fmt.Printf("\n%d key(s) would be migrated (dry run — nothing written)\n", len(actions))
		return
	}
	fmt.Printf("\n%d key(s) migrated\n", len(actions))

	if !*purge {
		fmt.Println("old copies left in attrs.json (re-run with -purge once you've verified the new domains look right)")
		return
	}

	for _, k := range purgeKeys {
		if err := mem.Attrs().Delete(k); err != nil {
			log.Fatalf("delete %q from attrs.json: %v", k, err)
		}
	}
	fmt.Printf("%d old key(s) removed from attrs.json\n", len(purgeKeys))
}
