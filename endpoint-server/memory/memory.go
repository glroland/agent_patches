package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent_patches/endpoint-server/utils/config"
)

// retentionTier keeps one snapshot per bucket for snapshots newer than horizon.
// Tiers are evaluated in order; a snapshot falls into the first tier whose
// horizon covers its age. Anything older than the last tier's horizon is
// deleted.
type retentionTier struct {
	horizon time.Duration
	bucket  time.Duration
}

// retentionTiers implements tiered baseline retention: full 5-minute
// resolution for the last hour (as before), hourly snapshots for a week, and
// daily snapshots for 90 days. The long tiers give the agent a baseline of
// "normal" to compare current readings against (growth rates, anomalies).
var retentionTiers = []retentionTier{
	{horizon: 60 * time.Minute, bucket: 5 * time.Minute},
	{horizon: 7 * 24 * time.Hour, bucket: time.Hour},
	{horizon: 90 * 24 * time.Hour, bucket: 24 * time.Hour},
}

// maxAge is the oldest any snapshot may be before it is pruned.
var maxAge = retentionTiers[len(retentionTiers)-1].horizon

// Store is the root memory store. Use Domain to get a history-backed domain
// store, or Attrs to get the global attribute store.
//
// Domain and Attrs always return the same instance for a given name/path so
// that the per-instance mutex actually serialises concurrent callers.
type Store struct {
	root  string
	mu    sync.Mutex
	doms  map[string]*DomainStore
	attrs *AttrsStore
}

// New creates a Store rooted at cfg.Root.
func New(cfg *config.MemorySettings) *Store {
	return &Store{
		root: cfg.Root,
		doms: make(map[string]*DomainStore),
	}
}

// Domain returns the DomainStore for the named domain, backed by a
// subdirectory of the root. The same instance is returned on every call with
// the same name so the intra-instance mutex serialises concurrent access.
func (s *Store) Domain(name string) *DomainStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.doms[name]; ok {
		return d
	}
	d := &DomainStore{root: filepath.Join(s.root, name), Clock: time.Now}
	s.doms[name] = d
	return d
}

// Attrs returns the shared attribute store, backed by attrs.json in the root.
// The same instance is returned on every call.
func (s *Store) Attrs() *AttrsStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attrs == nil {
		s.attrs = &AttrsStore{path: filepath.Join(s.root, "attrs.json")}
	}
	return s.attrs
}

// Dump is a snapshot of every memory domain's most recent value plus all
// attrs, for diagnostic / UI display.
type Dump struct {
	Domains map[string]json.RawMessage `json:"domains"`
	Attrs   map[string]json.RawMessage `json:"attrs"`
}

// Clear removes all memory: every domain's snapshots and the attrs store.
// The root directory itself is preserved. Returns the first error encountered,
// but always attempts to remove every entry.
func (s *Store) Clear() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("memory: clear readdir %s: %w", s.root, err)
	}
	var errs []string
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("memory: clear: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Dump reads the current snapshot of every domain (subdirectory of the
// store root) and all attrs. Domains with no snapshot yet are omitted.
func (s *Store) Dump() (Dump, error) {
	dump := Dump{Domains: map[string]json.RawMessage{}, Attrs: map[string]json.RawMessage{}}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return dump, nil
		}
		return Dump{}, fmt.Errorf("memory: readdir %s: %w", s.root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var raw json.RawMessage
		if err := s.Domain(e.Name()).ReadCurrent(&raw); err != nil {
			continue
		}
		dump.Domains[e.Name()] = raw
	}

	attrs, err := s.Attrs().All()
	if err != nil {
		return Dump{}, err
	}
	for k, v := range attrs {
		dump.Attrs[k] = v
	}

	return dump, nil
}

// ---------------------------------------------------------------------------
// DomainStore
// ---------------------------------------------------------------------------

// DomainStore persists timestamped JSON snapshots for one domain.
// Retention is tiered (see retentionTiers): one snapshot per 5-minute bucket
// for the last hour, one per hour for the last 7 days, and one per day for
// the last 90 days; anything older is deleted on each write.
type DomainStore struct {
	root  string
	mu    sync.Mutex
	Clock func() time.Time // overridable; defaults to time.Now
}

// Snapshot is one history entry returned by ReadHistory.
type Snapshot struct {
	Timestamp time.Time
	Data      json.RawMessage
}

// Write marshals v to JSON and stores it as a timestamped snapshot, then
// prunes the directory to the retention window. The write is atomic
// (temp-file + rename). A nil DomainStore is safe to call — Write is a no-op.
func (d *DomainStore) Write(v any) error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.writeLocked(v)
}

// ReadCurrent unmarshals the most recent snapshot into v.
// Returns an error if no snapshots exist.
// A nil DomainStore always returns an error.
func (d *DomainStore) ReadCurrent(v any) error {
	if d == nil {
		return fmt.Errorf("memory: nil DomainStore")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.readCurrentLocked(v)
}

// Update atomically loads the domain's current snapshot into dst (leaving
// dst at its existing/zero value if no snapshot exists yet), invokes mutate
// to modify it, and persists the result as a new snapshot — all under one
// lock, so concurrent Update calls on the same domain can't lose each
// other's changes the way separate ReadCurrent+Write calls could.
// A nil DomainStore is safe to call — Update is a no-op that does not
// invoke mutate.
func (d *DomainStore) Update(dst any, mutate func() error) error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.readCurrentLocked(dst); err != nil {
		// No snapshot yet (or unreadable) — proceed with dst at its current
		// (typically zero) value, matching the Attrs Get-then-ignore-error
		// pattern used throughout the codebase for optional prior state.
		_ = err
	}

	if err := mutate(); err != nil {
		return err
	}

	return d.writeLocked(dst)
}

// SetKey atomically merges key=value into the domain's current snapshot
// (treated as a map[string]json.RawMessage), preserving any other keys
// already present, and persists the result as a new snapshot. Mirrors
// AttrsStore.Set, but scoped to one domain (with tiered history retention)
// instead of the single global attrs file.
func (d *DomainStore) SetKey(key string, value any) error {
	val, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("memory: marshal value for key %q: %w", key, err)
	}

	var bucket map[string]json.RawMessage
	return d.Update(&bucket, func() error {
		if bucket == nil {
			bucket = map[string]json.RawMessage{}
		}
		bucket[key] = val
		return nil
	})
}

// GetKey reads a single key out of the domain's current snapshot (map
// mode). Returns an error if the domain has no snapshot yet or the key is
// absent. Mirrors AttrsStore.Get. A nil DomainStore always returns an error.
func (d *DomainStore) GetKey(key string, v any) error {
	if d == nil {
		return fmt.Errorf("memory: nil DomainStore")
	}

	var bucket map[string]json.RawMessage
	if err := d.ReadCurrent(&bucket); err != nil {
		return err
	}

	val, ok := bucket[key]
	if !ok {
		return fmt.Errorf("memory: key %q not found", key)
	}
	return json.Unmarshal(val, v)
}

// AllKeys returns every key/value pair in the domain's current snapshot
// (map mode). Returns nil, nil if the domain has no snapshot yet. Mirrors
// AttrsStore.All. A nil DomainStore returns nil, nil.
func (d *DomainStore) AllKeys() (map[string]json.RawMessage, error) {
	if d == nil {
		return nil, nil
	}

	var bucket map[string]json.RawMessage
	if err := d.ReadCurrent(&bucket); err != nil {
		return nil, nil //nolint:nilerr
	}
	return bucket, nil
}

// writeLocked marshals v to JSON and stores it as a new timestamped
// snapshot, then prunes the directory to the retention window. The write is
// atomic (temp-file + rename). Caller must hold d.mu.
func (d *DomainStore) writeLocked(v any) error {
	now := d.Clock()

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("memory: marshal: %w", err)
	}

	if err := os.MkdirAll(d.root, 0o755); err != nil {
		return fmt.Errorf("memory: mkdir %s: %w", d.root, err)
	}

	name := fmt.Sprintf("%d.json", now.UnixNano())
	path := filepath.Join(d.root, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("memory: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename: %w", err)
	}

	d.prune(now)
	return nil
}

// readCurrentLocked unmarshals the most recent snapshot into v. Returns an
// error if no snapshots exist. Caller must hold d.mu.
func (d *DomainStore) readCurrentLocked(v any) error {
	name, err := d.newestFile()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Join(d.root, name))
	if err != nil {
		return fmt.Errorf("memory: read %s: %w", name, err)
	}
	return json.Unmarshal(data, v)
}

// ReadHistory returns all retained snapshots sorted oldest-first.
// A nil DomainStore returns nil, nil.
func (d *DomainStore) ReadHistory() ([]Snapshot, error) {
	if d == nil {
		return nil, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: readdir %s: %w", d.root, err)
	}

	var snaps []Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d.root, e.Name()))
		if err != nil {
			continue
		}
		snaps = append(snaps, Snapshot{
			Timestamp: time.Unix(0, ns),
			Data:      json.RawMessage(data),
		})
	}

	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Timestamp.Before(snaps[j].Timestamp)
	})
	return snaps, nil
}

// ReadNearest returns the retained snapshot whose timestamp is closest to
// target, in either direction. Returns an error if no snapshots exist.
// A nil DomainStore always returns an error.
func (d *DomainStore) ReadNearest(target time.Time) (Snapshot, error) {
	if d == nil {
		return Snapshot{}, fmt.Errorf("memory: nil DomainStore")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("memory: readdir %s: %w", d.root, err)
	}

	var bestName string
	var bestNs int64
	var bestDist time.Duration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil {
			continue
		}
		dist := target.Sub(time.Unix(0, ns))
		if dist < 0 {
			dist = -dist
		}
		if bestName == "" || dist < bestDist {
			bestName, bestNs, bestDist = e.Name(), ns, dist
		}
	}
	if bestName == "" {
		return Snapshot{}, fmt.Errorf("memory: no snapshots in %s", d.root)
	}

	data, err := os.ReadFile(filepath.Join(d.root, bestName))
	if err != nil {
		return Snapshot{}, fmt.Errorf("memory: read %s: %w", bestName, err)
	}
	return Snapshot{Timestamp: time.Unix(0, bestNs), Data: json.RawMessage(data)}, nil
}

// newestFile returns the filename of the most recent snapshot in d.root.
// Caller must hold d.mu.
func (d *DomainStore) newestFile() (string, error) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("memory: no snapshots in %s", d.root)
		}
		return "", fmt.Errorf("memory: readdir %s: %w", d.root, err)
	}

	var newestName string
	var newestNs int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil {
			continue
		}
		if ns > newestNs {
			newestNs = ns
			newestName = e.Name()
		}
	}
	if newestName == "" {
		return "", fmt.Errorf("memory: no snapshots in %s", d.root)
	}
	return newestName, nil
}

// prune deletes snapshots that fall outside the tiered retention policy.
// A snapshot is assigned to the first tier whose horizon covers its age; only
// the newest snapshot per tier bucket is kept. Any snapshot older than the
// last tier's horizon is deleted. Caller must hold d.mu.
func (d *DomainStore) prune(now time.Time) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return
	}

	// bucket key (tier index + bucket index within tier) -> newest file
	type bucketKey struct {
		tier   int
		bucket int
	}
	type entry struct {
		path string
		ns   int64
	}
	buckets := make(map[bucketKey]entry)
	var toDelete []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil {
			continue
		}
		t := time.Unix(0, ns)
		age := now.Sub(t)

		if age > maxAge {
			toDelete = append(toDelete, filepath.Join(d.root, e.Name()))
			continue
		}
		if age < 0 {
			age = 0
		}
		key := bucketKey{}
		for i, tier := range retentionTiers {
			if age <= tier.horizon {
				key = bucketKey{tier: i, bucket: int(age / tier.bucket)}
				break
			}
		}
		path := filepath.Join(d.root, e.Name())
		if prev, ok := buckets[key]; !ok || ns > prev.ns {
			if ok {
				toDelete = append(toDelete, prev.path)
			}
			buckets[key] = entry{path: path, ns: ns}
		} else {
			toDelete = append(toDelete, path)
		}
	}

	for _, f := range toDelete {
		_ = os.Remove(f)
	}
}

// ---------------------------------------------------------------------------
// AttrsStore
// ---------------------------------------------------------------------------

// AttrsStore is a key-value store backed by a single attrs.json file.
// It has no history retention; each Set overwrites the previous value for
// that key. A nil AttrsStore is safe to call — Set and Get are no-ops.
type AttrsStore struct {
	path string
	mu   sync.Mutex
}

// Set persists key=value into attrs.json. The write is atomic.
// A nil AttrsStore is safe to call.
func (a *AttrsStore) Set(key string, value any) error {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return fmt.Errorf("memory: mkdir for attrs: %w", err)
	}

	attrs := make(map[string]json.RawMessage)
	if raw, err := os.ReadFile(a.path); err == nil {
		_ = json.Unmarshal(raw, &attrs)
	}

	val, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("memory: marshal value for key %q: %w", key, err)
	}
	attrs[key] = val

	out, err := json.MarshalIndent(attrs, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal attrs: %w", err)
	}

	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("memory: write attrs tmp: %w", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename attrs: %w", err)
	}
	return nil
}

// Delete removes key from attrs.json. It is a no-op if the key does not exist.
// A nil AttrsStore is safe to call.
func (a *AttrsStore) Delete(key string) error {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	attrs := make(map[string]json.RawMessage)
	if raw, err := os.ReadFile(a.path); err == nil {
		_ = json.Unmarshal(raw, &attrs)
	}

	if _, ok := attrs[key]; !ok {
		return nil
	}
	delete(attrs, key)

	out, err := json.MarshalIndent(attrs, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal attrs: %w", err)
	}

	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("memory: write attrs tmp: %w", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename attrs: %w", err)
	}
	return nil
}

// All returns every key/value pair in attrs.json. Returns nil, nil if the
// file does not exist yet. A nil AttrsStore returns nil, nil.
func (a *AttrsStore) All() (map[string]json.RawMessage, error) {
	if a == nil {
		return nil, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	raw, err := os.ReadFile(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: read attrs: %w", err)
	}

	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return nil, fmt.Errorf("memory: parse attrs: %w", err)
	}
	return attrs, nil
}

// Get reads the value for key into v.
// Returns an error if the key does not exist.
// A nil AttrsStore always returns an error.
func (a *AttrsStore) Get(key string, v any) error {
	if a == nil {
		return fmt.Errorf("memory: nil AttrsStore")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	raw, err := os.ReadFile(a.path)
	if err != nil {
		return fmt.Errorf("memory: read attrs: %w", err)
	}

	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return fmt.Errorf("memory: parse attrs: %w", err)
	}

	val, ok := attrs[key]
	if !ok {
		return fmt.Errorf("memory: key %q not found", key)
	}
	return json.Unmarshal(val, v)
}
