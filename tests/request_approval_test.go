package tests

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"agent_patches/endpoint-server/approvalapi"
	"agent_patches/endpoint-server/memory"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/status"
	"agent_patches/endpoint-server/utils/config"
)

// Reproduces the reported bug: when the agent process is shut down (e.g. a
// redeploy) while an approval is still in flight, the timeline entry — what
// the UI renders the Approve/Reject card from — must reflect "cancelled" too,
// not just the attrs store. Before the fix, only attrs was patched, so the
// card kept showing "pending" forever after restart, and any decision on it
// 409'd because attrs already disagreed.
func TestRequestApproval_ContextCancelled_PatchesTimelineToo(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before RequestApproval ever polls

	_, err := reqapproval.RequestApproval(ctx, mem, nil, "Clear old logs", "detail", "rm -rf /var/log/old", "low")
	if err == nil {
		t.Fatal("RequestApproval: want error on cancelled context, got nil")
	}

	var entries []status.TimelineEntry
	if rerr := mem.Domain("timeline").ReadCurrent(&entries); rerr != nil {
		t.Fatalf("ReadCurrent: %v", rerr)
	}
	if len(entries) != 1 {
		t.Fatalf("timeline entries = %d, want 1", len(entries))
	}
	if entries[0].Status == nil || *entries[0].Status != "cancelled" {
		t.Errorf("timeline entry status = %v, want \"cancelled\"", entries[0].Status)
	}

	// The attrs entry (used by approvalapi) must agree.
	var attrEntry reqapproval.ApprovalEntry
	if gerr := mem.Attrs().Get(reqapproval.AttrsKey(entries[0].ID), &attrEntry); gerr != nil {
		t.Fatalf("Attrs().Get: %v", gerr)
	}
	if attrEntry.Status != "cancelled" {
		t.Errorf("attrs entry status = %q, want \"cancelled\"", attrEntry.Status)
	}
}

// End-to-end: once an approval has been cancelled by a shutdown, a decision
// submitted against it must 409 with a message that explains why — not the
// generic "already decided" wording, which reads as if a human beat them to
// it.
func TestApprovalAPI_DecisionOnCancelled_Returns409WithClearMessage(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = reqapproval.RequestApproval(ctx, mem, nil, "Clear old logs", "detail", "rm -rf /var/log/old", "low")

	var entries []status.TimelineEntry
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly 1 timeline entry, got %d (err=%v)", len(entries), err)
	}
	id := entries[0].ID

	svc := approvalapi.New(mem)
	rec := doApprovalDecision(t, svc, id, "approved")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "restarted") {
		t.Errorf("body = %q, want it to explain the agent restarted, not generic \"already decided\"", body)
	}
}
