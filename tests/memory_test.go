package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
)

func newMemoryStore(t *testing.T) (*memory.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store := memory.New(&config.MemorySettings{Root: dir})
	return store, dir
}

// ---------------------------------------------------------------------------
// DomainStore — basic write / read
// ---------------------------------------------------------------------------

func TestMemoryDomain_WriteAndReadCurrent(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("test")

	type payload struct {
		Value int `json:"value"`
	}
	if err := d.Write(payload{Value: 42}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got payload
	if err := d.ReadCurrent(&got); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if got.Value != 42 {
		t.Errorf("ReadCurrent value = %d, want 42", got.Value)
	}
}

func TestMemoryDomain_ReadCurrentEmpty_ReturnsError(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("empty")
	var v map[string]any
	if err := d.ReadCurrent(&v); err == nil {
		t.Error("expected error reading from empty domain, got nil")
	}
}

func TestMemoryDomain_ReadHistory_Sorted(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("history")

	// Write oldest first, each in a distinct 5-minute bucket, so pruning keeps all three.
	base := time.Now()
	for _, off := range []time.Duration{10*time.Minute + time.Second, 5*time.Minute + time.Second, 0} {
		ts := base.Add(-off)
		d.Clock = func() time.Time { return ts }
		if err := d.Write(map[string]string{"off": off.String()}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	snaps, err := d.ReadHistory()
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("want 3 snapshots, got %d", len(snaps))
	}
	for i := 1; i < len(snaps); i++ {
		if !snaps[i].Timestamp.After(snaps[i-1].Timestamp) {
			t.Errorf("snapshot %d not after %d: %v vs %v",
				i, i-1, snaps[i].Timestamp, snaps[i-1].Timestamp)
		}
	}
}

func TestMemoryDomain_ReadHistory_Empty_ReturnsNil(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("nodata")
	snaps, err := d.ReadHistory()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected empty history, got %d snapshots", len(snaps))
	}
}

func TestMemoryDomain_Snapshot_DataRoundtrips(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("rt")

	type rec struct {
		Name string  `json:"name"`
		Rate float64 `json:"rate"`
	}
	in := rec{Name: "eth0", Rate: 1.5}
	if err := d.Write(in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	snaps, _ := d.ReadHistory()
	if len(snaps) != 1 {
		t.Fatalf("want 1 snapshot")
	}

	var out rec
	if err := json.Unmarshal(snaps[0].Data, &out); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if out != in {
		t.Errorf("roundtrip: got %+v, want %+v", out, in)
	}
}

// ---------------------------------------------------------------------------
// DomainStore — key-mode access (SetKey/GetKey/AllKeys/Update)
// ---------------------------------------------------------------------------

func TestMemoryDomainKey_SetAndGet(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("network")

	if err := d.SetKey("host", "server01"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	var got string
	if err := d.GetKey("host", &got); err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got != "server01" {
		t.Errorf("GetKey = %q, want %q", got, "server01")
	}
}

func TestMemoryDomainKey_SetPreservesOtherKeys(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("network")

	_ = d.SetKey("k1", "v1")
	_ = d.SetKey("k2", "v2")

	var v1, v2 string
	_ = d.GetKey("k1", &v1)
	_ = d.GetKey("k2", &v2)
	if v1 != "v1" || v2 != "v2" {
		t.Errorf("SetKey clobbered keys: k1=%q k2=%q", v1, v2)
	}
}

func TestMemoryDomainKey_GetMissingKey_ReturnsError(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("network")
	_ = d.SetKey("existing", 1)
	var v int
	if err := d.GetKey("missing", &v); err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestMemoryDomainKey_SetOverwritesValue(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("network")
	_ = d.SetKey("x", 1)
	_ = d.SetKey("x", 2)
	var v int
	if err := d.GetKey("x", &v); err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if v != 2 {
		t.Errorf("GetKey = %d, want 2", v)
	}
}

func TestMemoryDomainKey_AllKeys(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("network")
	_ = d.SetKey("k1", "v1")
	_ = d.SetKey("k2", "v2")

	all, err := d.AllKeys()
	if err != nil {
		t.Fatalf("AllKeys: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("AllKeys returned %d keys, want 2: %+v", len(all), all)
	}
}

func TestMemoryDomainKey_AllKeys_Empty_ReturnsNil(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("empty-network")
	all, err := d.AllKeys()
	if err != nil {
		t.Fatalf("AllKeys: %v", err)
	}
	if all != nil {
		t.Errorf("AllKeys on empty domain = %+v, want nil", all)
	}
}

func TestMemoryDomainKey_ConcurrentSetKey_NoLostUpdates(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("concurrent")

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("skill_state:%d", i)
			_ = d.SetKey(key, i)
		}(i)
	}
	wg.Wait()

	all, err := d.AllKeys()
	if err != nil {
		t.Fatalf("AllKeys: %v", err)
	}
	if len(all) != n {
		t.Errorf("AllKeys returned %d keys after %d concurrent SetKey calls, want %d (lost update)", len(all), n, n)
	}
}

func TestMemoryDomainUpdate_MutatesExistingSnapshot(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("update")

	type counter struct {
		N int `json:"n"`
	}
	for i := 0; i < 3; i++ {
		var c counter
		if err := d.Update(&c, func() error {
			c.N++
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	var final counter
	if err := d.ReadCurrent(&final); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if final.N != 3 {
		t.Errorf("final.N = %d, want 3", final.N)
	}
}

func TestMemoryDomainUpdate_NilStore_Noop(t *testing.T) {
	var d *memory.DomainStore
	called := false
	if err := d.Update(new(int), func() error {
		called = true
		return nil
	}); err != nil {
		t.Errorf("nil DomainStore.Update returned error: %v", err)
	}
	if called {
		t.Error("nil DomainStore.Update should not invoke mutate")
	}
}

// ---------------------------------------------------------------------------
// DomainStore — retention algorithm
// ---------------------------------------------------------------------------

func TestMemoryRetention_DeletesFilesOlderThan90Days(t *testing.T) {
	store, root := newMemoryStore(t)
	d := store.Domain("prune_old")

	now := time.Now()

	// write a snapshot 91 days in the past — beyond the last retention tier
	old := now.Add(-91 * 24 * time.Hour)
	d.Clock = func() time.Time { return old }
	if err := d.Write(map[string]string{"when": "old"}); err != nil {
		t.Fatalf("Write old: %v", err)
	}

	// write a current snapshot — this should trigger pruning and delete the old one
	d.Clock = func() time.Time { return now }
	if err := d.Write(map[string]string{"when": "now"}); err != nil {
		t.Fatalf("Write now: %v", err)
	}

	entries := jsonFilesInDir(t, filepath.Join(root, "prune_old"))
	if len(entries) != 1 {
		t.Errorf("want 1 file after pruning old snapshot, got %d: %v", len(entries), entries)
	}
}

func TestMemoryRetention_KeepsHourlyTierForBaselines(t *testing.T) {
	store, root := newMemoryStore(t)
	d := store.Domain("tiered")

	now := time.Now()

	// Written oldest-first (pruning runs relative to each write's clock):
	// - 30d old lands in the daily tier, 3d old in the hourly tier
	// - 190m and 185m fall in the same hourly bucket — only the newest survives
	// - 65m old used to be deleted under the flat 60-minute policy; under
	//   tiered retention it falls into the hourly tier and is kept
	offsets := []time.Duration{
		30 * 24 * time.Hour,
		3 * 24 * time.Hour,
		190 * time.Minute,
		185 * time.Minute,
		65 * time.Minute,
	}
	for _, off := range offsets {
		ts := now.Add(-off)
		d.Clock = func() time.Time { return ts }
		if err := d.Write(map[string]string{"age": off.String()}); err != nil {
			t.Fatalf("Write %v: %v", off, err)
		}
	}

	// trigger pruning
	d.Clock = func() time.Time { return now }
	if err := d.Write(map[string]string{"when": "now"}); err != nil {
		t.Fatalf("Write now: %v", err)
	}

	// now + 65m + one-of-two-in-same-hour + 3d + 30d = 5 files
	entries := jsonFilesInDir(t, filepath.Join(root, "tiered"))
	if len(entries) != 5 {
		t.Errorf("want 5 files under tiered retention, got %d: %v", len(entries), entries)
	}
}

func TestMemoryDomain_ReadNearest(t *testing.T) {
	store, _ := newMemoryStore(t)
	d := store.Domain("nearest")

	now := time.Now()
	for _, off := range []time.Duration{25 * time.Hour, 2 * time.Hour, 0} {
		ts := now.Add(-off)
		d.Clock = func() time.Time { return ts }
		if err := d.Write(map[string]string{"age": off.String()}); err != nil {
			t.Fatalf("Write %v: %v", off, err)
		}
	}

	snap, err := d.ReadNearest(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("ReadNearest: %v", err)
	}
	// nearest to -24h is the -25h snapshot (1h away vs 22h for the -2h one)
	wantTs := now.Add(-25 * time.Hour)
	if diff := snap.Timestamp.Sub(wantTs); diff > time.Second || diff < -time.Second {
		t.Errorf("ReadNearest timestamp = %v, want ~%v", snap.Timestamp, wantTs)
	}

	var got map[string]string
	if err := json.Unmarshal(snap.Data, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if got["age"] != (25 * time.Hour).String() {
		t.Errorf("ReadNearest data age = %q, want %q", got["age"], (25 * time.Hour).String())
	}
}

func TestMemoryDomain_ReadNearest_Empty_ReturnsError(t *testing.T) {
	store, _ := newMemoryStore(t)
	if _, err := store.Domain("nearest_empty").ReadNearest(time.Now()); err == nil {
		t.Error("expected error reading nearest from empty domain, got nil")
	}
}

func TestMemoryRetention_KeepsOnlyNewestPerBucket(t *testing.T) {
	store, root := newMemoryStore(t)
	d := store.Domain("bucket_dedup")

	now := time.Now()

	// write two snapshots both in the 10-15 min bucket
	for _, offset := range []time.Duration{12 * time.Minute, 11 * time.Minute} {
		t := now.Add(-offset)
		d.Clock = func() time.Time { return t }
		if err := d.Write(map[string]string{"age": offset.String()}); err != nil {
			fmt.Printf("Write %v: %v\n", offset, err)
		}
	}

	// trigger pruning by writing at 'now'
	d.Clock = func() time.Time { return now }
	if err := d.Write(map[string]string{"when": "now"}); err != nil {
		fmt.Printf("Write now: %v\n", err)
	}

	// expect at most 2 files: current + one representing the ~10-15 min bucket
	entries := jsonFilesInDir(t, filepath.Join(root, "bucket_dedup"))
	if len(entries) > 2 {
		t.Errorf("want ≤2 files (one per bucket), got %d: %v", len(entries), entries)
	}
}

func TestMemoryRetention_RetainsUpTo13Snapshots(t *testing.T) {
	store, root := newMemoryStore(t)
	d := store.Domain("max_retain")

	now := time.Now()

	// one snapshot per 5-min bucket: 0, 5, 10, ..., 60 = 13 buckets
	for i := 0; i <= 12; i++ {
		age := time.Duration(i)*5*time.Minute + 1*time.Second
		ts := now.Add(-age)
		d.Clock = func() time.Time { return ts }
		if err := d.Write(map[string]int{"bucket": i}); err != nil {
			t.Fatalf("Write bucket %d: %v", i, err)
		}
	}

	// trigger pruning by writing "now"
	d.Clock = func() time.Time { return now }
	if err := d.Write(map[string]string{"bucket": "current"}); err != nil {
		t.Fatalf("Write now: %v", err)
	}

	entries := jsonFilesInDir(t, filepath.Join(root, "max_retain"))
	// bucket 0 has the current file; buckets 1-12 have one each = 13 total
	if len(entries) > 13 {
		t.Errorf("want ≤13 files, got %d", len(entries))
	}
}

func TestMemoryRetention_NilStore_WriteIsNoop(t *testing.T) {
	var d *memory.DomainStore
	if err := d.Write("anything"); err != nil {
		t.Errorf("nil DomainStore.Write returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AttrsStore
// ---------------------------------------------------------------------------

func TestMemoryAttrs_SetAndGet(t *testing.T) {
	store, _ := newMemoryStore(t)
	a := store.Attrs()

	if err := a.Set("host", "server01"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got string
	if err := a.Get("host", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "server01" {
		t.Errorf("Get = %q, want %q", got, "server01")
	}
}

func TestMemoryAttrs_SetPreservesOtherKeys(t *testing.T) {
	store, _ := newMemoryStore(t)
	a := store.Attrs()

	_ = a.Set("k1", "v1")
	_ = a.Set("k2", "v2")

	var v1, v2 string
	_ = a.Get("k1", &v1)
	_ = a.Get("k2", &v2)
	if v1 != "v1" || v2 != "v2" {
		t.Errorf("Set clobbered keys: k1=%q k2=%q", v1, v2)
	}
}

func TestMemoryAttrs_GetMissingKey_ReturnsError(t *testing.T) {
	store, _ := newMemoryStore(t)
	a := store.Attrs()
	_ = a.Set("existing", 1)
	var v int
	if err := a.Get("missing", &v); err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestMemoryAttrs_SetOverwritesValue(t *testing.T) {
	store, _ := newMemoryStore(t)
	a := store.Attrs()
	_ = a.Set("x", 1)
	_ = a.Set("x", 2)
	var v int
	if err := a.Get("x", &v); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != 2 {
		t.Errorf("Get = %d, want 2", v)
	}
}

func TestMemoryAttrs_NilStore_SetIsNoop(t *testing.T) {
	var a *memory.AttrsStore
	if err := a.Set("k", "v"); err != nil {
		t.Errorf("nil AttrsStore.Set returned error: %v", err)
	}
}

func TestMemoryAttrs_StoredAsJSON(t *testing.T) {
	store, root := newMemoryStore(t)
	a := store.Attrs()
	_ = a.Set("status", "ok")

	data, err := os.ReadFile(filepath.Join(root, "attrs.json"))
	if err != nil {
		t.Fatalf("read attrs.json: %v", err)
	}
	if !strings.Contains(string(data), `"status"`) {
		t.Errorf("attrs.json missing key: %s", data)
	}
}

// ---------------------------------------------------------------------------
// Dump
// ---------------------------------------------------------------------------

func TestMemoryDump_IncludesDomainsAndAttrs(t *testing.T) {
	store, _ := newMemoryStore(t)

	if err := store.Domain("disk").Write(map[string]any{"usedPct": 42}); err != nil {
		t.Fatalf("Write domain: %v", err)
	}
	if err := store.Attrs().Set("skill_state:check_drives", map[string]string{"health": "ok"}); err != nil {
		t.Fatalf("Set attr: %v", err)
	}

	dump, err := store.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if _, ok := dump.Domains["disk"]; !ok {
		t.Errorf("Dump.Domains missing %q: %+v", "disk", dump.Domains)
	}
	if !strings.Contains(string(dump.Domains["disk"]), "42") {
		t.Errorf("Dump.Domains[disk] = %s, want it to contain 42", dump.Domains["disk"])
	}
	if _, ok := dump.Attrs["skill_state:check_drives"]; !ok {
		t.Errorf("Dump.Attrs missing %q: %+v", "skill_state:check_drives", dump.Attrs)
	}
}

func TestMemoryDump_EmptyStore_ReturnsEmptyMaps(t *testing.T) {
	store, _ := newMemoryStore(t)

	dump, err := store.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if dump.Domains == nil || len(dump.Domains) != 0 {
		t.Errorf("Dump.Domains = %+v, want empty map", dump.Domains)
	}
	if dump.Attrs == nil || len(dump.Attrs) != 0 {
		t.Errorf("Dump.Attrs = %+v, want empty map", dump.Attrs)
	}
}

func TestMemoryDump_OmitsDomainWithNoSnapshot(t *testing.T) {
	store, root := newMemoryStore(t)

	// Create an empty domain directory with no snapshots.
	if err := os.MkdirAll(filepath.Join(root, "empty_domain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dump, err := store.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if _, ok := dump.Domains["empty_domain"]; ok {
		t.Errorf("Dump.Domains should omit domain with no snapshot, got %+v", dump.Domains)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func jsonFilesInDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".tmp") {
			names = append(names, e.Name())
		}
	}
	return names
}
