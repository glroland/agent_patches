package tests

import (
	"context"
	"testing"

	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func TestNotifier_Notify_WritesToMemory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	n := notifier.New(mem)

	n.Notify(context.Background(), "Test Subject", "Test Body")

	// Verify notification was persisted to the "notifications" domain.
	var note map[string]interface{}
	if err := mem.Domain("notifications").ReadCurrent(&note); err != nil {
		t.Fatalf("ReadCurrent(notifications): %v", err)
	}
	if note["subject"] != "Test Subject" {
		t.Errorf("subject = %v, want %q", note["subject"], "Test Subject")
	}
	if note["body"] != "Test Body" {
		t.Errorf("body = %v, want %q", note["body"], "Test Body")
	}
	if note["time"] == nil || note["time"] == "" {
		t.Error("time field should be set")
	}
}

func TestNotifier_Notify_MultipleCallsPreserveHistory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	n := notifier.New(mem)

	n.Notify(context.Background(), "First", "body1")
	n.Notify(context.Background(), "Second", "body2")

	// The most recent notification should be readable as current.
	var note map[string]interface{}
	if err := mem.Domain("notifications").ReadCurrent(&note); err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if note["subject"] != "Second" {
		t.Errorf("current subject = %v, want Second", note["subject"])
	}

	// History should contain both.
	snaps, err := mem.Domain("notifications").ReadHistory()
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(snaps) < 1 {
		t.Errorf("expected at least 1 snapshot in history, got %d", len(snaps))
	}
}

// A nil *Notifier must never panic — skills call it without checking for nil.
func TestNotifier_Nil_IsNoOp(t *testing.T) {
	var n *notifier.Notifier
	// Must not panic.
	n.Notify(context.Background(), "subject", "body")
}
