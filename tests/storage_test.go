package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/skills/ping"
	"agent_patches/endpoint-server/utils/storage"
)

// ---- Store tests ----

func TestStore_All_FileNotExist(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))

	records, err := store.All()
	if err != nil {
		t.Fatalf("All() on missing file returned error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("All() = %d records, want 0", len(records))
	}
}

func TestStore_Append_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	store := storage.NewStore(path)

	if err := store.Append(storage.TaskRecord{ID: "1", Name: "ping"}); err != nil {
		t.Fatalf("Append() error: %v", err)
	}

	records, err := store.All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("All() = %d records, want 1", len(records))
	}
}

func TestStore_AppendAndAll_RoundTrip(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))

	want := storage.TaskRecord{
		ID:         "abc123",
		Name:       "ping",
		Input:      json.RawMessage(`{"key":"val"}`),
		Result:     "pong",
		ExecutedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	if err := store.Append(want); err != nil {
		t.Fatalf("Append() error: %v", err)
	}

	records, err := store.All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}

	got := records[0]
	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Result != want.Result {
		t.Errorf("Result = %q, want %q", got.Result, want.Result)
	}
	if !got.ExecutedAt.Equal(want.ExecutedAt) {
		t.Errorf("ExecutedAt = %v, want %v", got.ExecutedAt, want.ExecutedAt)
	}
}

func TestStore_Append_PreservesOrder(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))

	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		if err := store.Append(storage.TaskRecord{Name: n}); err != nil {
			t.Fatalf("Append(%q) error: %v", n, err)
		}
	}

	records, err := store.All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	if len(records) != len(names) {
		t.Fatalf("len = %d, want %d", len(records), len(names))
	}
	for i, n := range names {
		if records[i].Name != n {
			t.Errorf("records[%d].Name = %q, want %q", i, records[i].Name, n)
		}
	}
}

func TestStore_Append_RecordsError(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))

	if err := store.Append(storage.TaskRecord{Name: "failing", Error: "something went wrong"}); err != nil {
		t.Fatalf("Append() error: %v", err)
	}

	records, _ := store.All()
	if records[0].Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", records[0].Error, "something went wrong")
	}
}

// ---- WrapTool / WrapAll tests ----

func TestWrapTool_DelegatesMetadata(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))
	inner, _ := ping.NewPingTool()
	wrapped := storage.WrapTool(inner, store)

	if wrapped.Name() != inner.Name() {
		t.Errorf("Name() = %q, want %q", wrapped.Name(), inner.Name())
	}
	if wrapped.Description() != inner.Description() {
		t.Errorf("Description() = %q, want %q", wrapped.Description(), inner.Description())
	}
}

func TestWrapTool_RecordsExecution(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))
	inner, _ := ping.NewPingTool()
	wrapped := storage.WrapTool(inner, store)

	input, _ := json.Marshal(struct{}{})
	if _, err := wrapped.Execute(context.Background(), input); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	records, err := store.All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}

	r := records[0]
	if r.Name != "ping" {
		t.Errorf("Name = %q, want %q", r.Name, "ping")
	}
	if r.Result != "pong" {
		t.Errorf("Result = %q, want %q", r.Result, "pong")
	}
	if r.Error != "" {
		t.Errorf("Error = %q, want empty", r.Error)
	}
	if r.ID == "" {
		t.Error("ID should not be empty")
	}
	if r.ExecutedAt.IsZero() {
		t.Error("ExecutedAt should not be zero")
	}
}

func TestWrapTool_StillReturnsResult(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))
	inner, _ := ping.NewPingTool()
	wrapped := storage.WrapTool(inner, store)

	input, _ := json.Marshal(struct{}{})
	result, err := wrapped.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result != "pong" {
		t.Errorf("Execute() result = %q, want %q", result, "pong")
	}
}

func TestWrapAll_WrapsEveryTool(t *testing.T) {
	store := storage.NewStore(filepath.Join(t.TempDir(), "tasks.jsonl"))
	t1, _ := ping.NewPingTool()
	t2, _ := ping.NewPingTool()

	wrapped := storage.WrapAll([]tool.Tool{t1, t2}, store)
	if len(wrapped) != 2 {
		t.Fatalf("WrapAll len = %d, want 2", len(wrapped))
	}

	input, _ := json.Marshal(struct{}{})
	for _, w := range wrapped {
		w.Execute(context.Background(), input) //nolint:errcheck
	}

	records, _ := store.All()
	if len(records) != 2 {
		t.Errorf("records len = %d, want 2", len(records))
	}
}
