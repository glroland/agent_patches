package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"agent_patches/server/tool"
)

// TaskRecord captures a single tool execution persisted to the tasks file.
type TaskRecord struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	Result     string          `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	ExecutedAt time.Time       `json:"executed_at"`
}

// Store is a thread-safe, append-only JSONL log of TaskRecords.
// The backing file is created on the first Append if it does not exist.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a Store backed by the file at path.
func NewStore(path string) *Store {
	slog.Debug("storage: initialised", "path", path)
	return &Store{path: path}
}

// Append serialises record as a single JSON line and appends it to the file.
func (s *Store) Append(record TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", s.path, err)
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("storage: marshal record: %w", err)
	}

	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// All reads and returns every TaskRecord from the file.
// Returns an empty slice (not an error) when the file does not yet exist.
func (s *Store) All() ([]TaskRecord, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", s.path, err)
	}
	defer f.Close()

	var records []TaskRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r TaskRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("storage: parse record: %w", err)
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

// storedTool wraps a Tool, recording every execution in a Store.
type storedTool struct {
	inner tool.Tool
	store *Store
}

// WrapTool returns a Tool that delegates to t and records each call in store.
// Storage failures are logged but never abort task execution.
func WrapTool(t tool.Tool, store *Store) tool.Tool {
	return &storedTool{inner: t, store: store}
}

// WrapAll wraps every tool in tools with store and returns the new slice.
func WrapAll(tools []tool.Tool, store *Store) []tool.Tool {
	wrapped := make([]tool.Tool, len(tools))
	for i, t := range tools {
		wrapped[i] = WrapTool(t, store)
	}
	return wrapped
}

func (t *storedTool) Name() string                 { return t.inner.Name() }
func (t *storedTool) Description() string          { return t.inner.Description() }
func (t *storedTool) InputSchema() json.RawMessage { return t.inner.InputSchema() }

func (t *storedTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	slog.Debug("storage: executing tool", "tool", t.inner.Name())

	result, execErr := t.inner.Execute(ctx, input)

	record := TaskRecord{
		ID:         fmt.Sprintf("%x", time.Now().UnixNano()),
		Name:       t.inner.Name(),
		Input:      input,
		Result:     result,
		ExecutedAt: time.Now().UTC(),
	}
	if execErr != nil {
		record.Result = ""
		record.Error = execErr.Error()
		slog.Warn("storage: tool execution error", "tool", t.inner.Name(), "error", execErr)
	} else {
		slog.Debug("storage: tool executed", "tool", t.inner.Name(), "result_len", len(result))
	}

	if err := t.store.Append(record); err != nil {
		slog.Warn("storage: failed to persist record", "tool", t.inner.Name(), "error", err)
	}

	return result, execErr
}
