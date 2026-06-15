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

const (
	maxAge      = 60 * time.Minute
	bucketWidth = 5 * time.Minute
)

// Store is the root memory store. Use Domain to get a history-backed domain
// store, or Attrs to get the global attribute store.
type Store struct {
	root string
}

// New creates a Store rooted at cfg.Root.
func New(cfg *config.MemorySettings) *Store {
	return &Store{root: cfg.Root}
}

// Domain returns a DomainStore for the named domain, backed by a subdirectory
// of the root. The directory is created on first Write.
func (s *Store) Domain(name string) *DomainStore {
	return &DomainStore{
		root:  filepath.Join(s.root, name),
		Clock: time.Now,
	}
}

// Attrs returns the shared attribute store, backed by attrs.json in the root.
func (s *Store) Attrs() *AttrsStore {
	return &AttrsStore{
		path: filepath.Join(s.root, "attrs.json"),
	}
}

// ---------------------------------------------------------------------------
// DomainStore
// ---------------------------------------------------------------------------

// DomainStore persists timestamped JSON snapshots for one domain.
// It retains one snapshot per 5-minute bucket covering the last 60 minutes;
// anything older is deleted on each write.
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

// ReadCurrent unmarshals the most recent snapshot into v.
// Returns an error if no snapshots exist.
// A nil DomainStore always returns an error.
func (d *DomainStore) ReadCurrent(v any) error {
	if d == nil {
		return fmt.Errorf("memory: nil DomainStore")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

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

// prune deletes snapshots that fall outside the retention policy.
// For each 5-minute age bucket within the last 60 minutes, only the newest
// snapshot is kept. Any snapshot older than 60 minutes is deleted.
// Caller must hold d.mu.
func (d *DomainStore) prune(now time.Time) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return
	}

	// bucket index -> path of the newest file in that bucket
	type entry struct {
		path string
		ns   int64
	}
	buckets := make(map[int]entry)
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
		bucket := int(age / bucketWidth)
		path := filepath.Join(d.root, e.Name())
		if prev, ok := buckets[bucket]; !ok || ns > prev.ns {
			if ok {
				toDelete = append(toDelete, prev.path)
			}
			buckets[bucket] = entry{path: path, ns: ns}
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
