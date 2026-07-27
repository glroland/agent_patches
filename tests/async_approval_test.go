package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"agent_patches/endpoint-server/approvalapi"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/policy"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/skills/run_approved_command"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// run_approved_command must file the approval and return immediately — the
// agent run is never parked waiting on the operator.
func TestRunApprovedCommand_ReturnsImmediatelyWithPendingApproval(t *testing.T) {
	tool, mem := newRunApprovedCommandTool(t)

	start := time.Now()
	out, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "systemctl restart fake-service"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Execute took %v — the async flow must not block on the operator", elapsed)
	}
	if !strings.Contains(out, "NOT been executed") || !strings.Contains(out, "pending") {
		t.Errorf("output = %q, want pending-approval message", out)
	}

	// A pending auto-execute approval must exist in attrs and on the timeline.
	attrs, err := mem.Attrs().All()
	if err != nil {
		t.Fatalf("read attrs: %v", err)
	}
	found := false
	for k := range attrs {
		if !strings.HasPrefix(k, "approval:") {
			continue
		}
		var entry reqapproval.ApprovalEntry
		if err := mem.Attrs().Get(k, &entry); err != nil {
			t.Fatalf("read approval entry: %v", err)
		}
		found = true
		if entry.Status != "pending" || !entry.AutoExecute {
			t.Errorf("approval entry = %+v, want pending auto-execute", entry)
		}
	}
	if !found {
		t.Fatal("no approval entry created")
	}

	var entries []map[string]any
	if err := mem.Domain("timeline").ReadCurrent(&entries); err != nil || len(entries) != 1 {
		t.Errorf("timeline entries = %d (err %v), want 1 pending approval card", len(entries), err)
	}
}

// Approving via the decision endpoint must execute the stored command in the
// background and record the output on the approval entry and timeline.
func TestApprovalAPI_ApproveExecutesAutoExecuteCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executes via sh -c")
	}

	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	policies := policy.New(mem)
	tool, err := run_approved_command.NewRunApprovedCommandTool(mem, nil, policies)
	if err != nil {
		t.Fatalf("NewRunApprovedCommandTool: %v", err)
	}

	marker := filepath.Join(t.TempDir(), "ran")
	if _, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "touch "+marker)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Find the pending approval ID.
	attrs, _ := mem.Attrs().All()
	var id string
	for k := range attrs {
		if strings.HasPrefix(k, "approval:") {
			id = strings.TrimPrefix(k, "approval:")
		}
	}
	if id == "" {
		t.Fatal("no pending approval found")
	}

	h := approvalapi.New(mem, nil, policies).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/approvals/"+id+"/decision",
		strings.NewReader(`{"decision":"approved"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("decision status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Execution is detached — poll briefly for the marker file and output.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker file not created — approved command did not execute")
		}
		time.Sleep(50 * time.Millisecond)
	}

	var entry reqapproval.ApprovalEntry
	for time.Now().Before(deadline) {
		_ = mem.Attrs().Get(reqapproval.AttrsKey(id), &entry)
		if entry.Output != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if entry.Status != "approved" {
		t.Errorf("approval status = %q, want approved", entry.Status)
	}
	if entry.Output == "" {
		t.Error("approval entry has no recorded output after execution")
	}
}

// Rejecting an auto-execute approval must not run the command.
func TestApprovalAPI_RejectDoesNotExecute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executes via sh -c")
	}

	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	policies := policy.New(mem)
	tool, err := run_approved_command.NewRunApprovedCommandTool(mem, nil, policies)
	if err != nil {
		t.Fatalf("NewRunApprovedCommandTool: %v", err)
	}

	marker := filepath.Join(t.TempDir(), "ran")
	if _, err := tool.Execute(context.Background(), runApprovedCommandInput(t, "touch "+marker)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	attrs, _ := mem.Attrs().All()
	var id string
	for k := range attrs {
		if strings.HasPrefix(k, "approval:") {
			id = strings.TrimPrefix(k, "approval:")
		}
	}

	h := approvalapi.New(mem, nil, policies).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/approvals/"+id+"/decision",
		strings.NewReader(`{"decision":"rejected"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("decision status = %d, want 200", rec.Code)
	}

	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("marker file exists — rejected command was executed")
	}
}

// The expiry sweeper must time out stale pending approvals so async requests
// don't linger forever.
func TestExpirePending_TimesOutStaleApprovals(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	stale := reqapproval.ApprovalEntry{
		ID:             "stale-1",
		Title:          "old request",
		ProposedAction: "systemctl restart foo",
		Risk:           "low",
		Status:         "pending",
		RequestedAt:    time.Now().Add(-50 * time.Hour),
		AutoExecute:    true,
	}
	fresh := reqapproval.ApprovalEntry{
		ID:          "fresh-1",
		Title:       "new request",
		Status:      "pending",
		RequestedAt: time.Now().Add(-1 * time.Hour),
		AutoExecute: true,
	}
	if err := mem.Attrs().Set(reqapproval.AttrsKey(stale.ID), stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := mem.Attrs().Set(reqapproval.AttrsKey(fresh.ID), fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	expired := reqapproval.ExpirePending(context.Background(), mem, nil, time.Now())
	if expired != 1 {
		t.Errorf("ExpirePending = %d, want 1", expired)
	}

	var got reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey(stale.ID), &got); err != nil {
		t.Fatalf("read stale: %v", err)
	}
	if got.Status != "timed_out" {
		t.Errorf("stale status = %q, want timed_out", got.Status)
	}
	if err := mem.Attrs().Get(reqapproval.AttrsKey(fresh.ID), &got); err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("fresh status = %q, want still pending", got.Status)
	}
}

// High-risk approvals (e.g. patches requiring a reboot) get a 48h timeout
// instead of the 24h default, since they're more likely to need a
// maintenance window before an operator can act.
func TestExpirePending_HighRiskGetsLongerTimeout(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	pastDefaultNotHighRisk := reqapproval.ApprovalEntry{
		ID:          "high-risk-60h",
		Title:       "past 48h default, still within 96h",
		Risk:        "high",
		Status:      "pending",
		RequestedAt: time.Now().Add(-60 * time.Hour),
	}
	pastHighRiskTimeout := reqapproval.ApprovalEntry{
		ID:          "high-risk-100h",
		Title:       "past the 96h high-risk timeout",
		Risk:        "high",
		Status:      "pending",
		RequestedAt: time.Now().Add(-100 * time.Hour),
	}
	if err := mem.Attrs().Set(reqapproval.AttrsKey(pastDefaultNotHighRisk.ID), pastDefaultNotHighRisk); err != nil {
		t.Fatalf("seed pastDefaultNotHighRisk: %v", err)
	}
	if err := mem.Attrs().Set(reqapproval.AttrsKey(pastHighRiskTimeout.ID), pastHighRiskTimeout); err != nil {
		t.Fatalf("seed pastHighRiskTimeout: %v", err)
	}

	expired := reqapproval.ExpirePending(context.Background(), mem, nil, time.Now())
	if expired != 1 {
		t.Errorf("ExpirePending = %d, want 1", expired)
	}

	var got reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey(pastDefaultNotHighRisk.ID), &got); err != nil {
		t.Fatalf("read pastDefaultNotHighRisk: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("60h-old high-risk status = %q, want still pending (96h timeout)", got.Status)
	}
	if err := mem.Attrs().Get(reqapproval.AttrsKey(pastHighRiskTimeout.ID), &got); err != nil {
		t.Fatalf("read pastHighRiskTimeout: %v", err)
	}
	if got.Status != "timed_out" {
		t.Errorf("100h-old high-risk status = %q, want timed_out", got.Status)
	}
}

// EscalatePending must send a single reminder for high-importance/high-risk
// approvals once they pass the halfway point of their timeout window, and
// never send it twice for the same approval.
func TestEscalatePending_SendsReminderOnceAtHalfway(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	notify := notifier.New(mem)

	tooEarly := reqapproval.ApprovalEntry{
		ID:          "too-early",
		Title:       "not yet halfway",
		Risk:        "high",
		Status:      "pending",
		RequestedAt: time.Now().Add(-1 * time.Hour), // 96h timeout, halfway = 48h
	}
	pastHalfway := reqapproval.ApprovalEntry{
		ID:          "past-halfway",
		Title:       "past halfway",
		Risk:        "high",
		Status:      "pending",
		RequestedAt: time.Now().Add(-50 * time.Hour),
	}
	lowRisk := reqapproval.ApprovalEntry{
		ID:          "low-risk-old",
		Title:       "low importance/risk, never escalates",
		Importance:  "low",
		Risk:        "low",
		Status:      "pending",
		RequestedAt: time.Now().Add(-30 * time.Hour), // past its own 24h halfway (48h default timeout)
	}
	for _, e := range []reqapproval.ApprovalEntry{tooEarly, pastHalfway, lowRisk} {
		if err := mem.Attrs().Set(reqapproval.AttrsKey(e.ID), e); err != nil {
			t.Fatalf("seed %s: %v", e.ID, err)
		}
	}

	sent := reqapproval.EscalatePending(context.Background(), mem, notify, time.Now())
	if sent != 1 {
		t.Fatalf("EscalatePending = %d, want 1", sent)
	}

	var gotPastHalfway reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey(pastHalfway.ID), &gotPastHalfway); err != nil {
		t.Fatalf("read past-halfway: %v", err)
	}
	if !gotPastHalfway.Escalated || gotPastHalfway.Status != "pending" {
		t.Errorf("past-halfway entry = %+v, want Escalated=true and still pending", gotPastHalfway)
	}

	var gotTooEarly reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey(tooEarly.ID), &gotTooEarly); err != nil {
		t.Fatalf("read too-early: %v", err)
	}
	if gotTooEarly.Escalated {
		t.Error("too-early entry was escalated before reaching the halfway point")
	}

	var gotLowRisk reqapproval.ApprovalEntry
	if err := mem.Attrs().Get(reqapproval.AttrsKey(lowRisk.ID), &gotLowRisk); err != nil {
		t.Fatalf("read low-risk: %v", err)
	}
	if gotLowRisk.Escalated {
		t.Error("low importance/risk entry should never escalate")
	}

	// A second sweep must not send a duplicate reminder for the same approval.
	if sent := reqapproval.EscalatePending(context.Background(), mem, notify, time.Now()); sent != 0 {
		t.Errorf("second EscalatePending sweep = %d, want 0 (already escalated)", sent)
	}
}
