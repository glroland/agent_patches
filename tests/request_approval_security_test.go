package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent_patches/endpoint-server/memory"
	reqapproval "agent_patches/endpoint-server/skills/request_approval"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

// writeDecision polls attrs in a tight loop until a pending approval entry
// appears for the given title, then writes the requested decision. It is
// intended to run as a goroutine concurrent with RequestApproval.
func writeDecision(ctx context.Context, mem *memory.Store, title, decision string, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}

		all, err := mem.Attrs().All()
		if err != nil {
			continue
		}
		for key := range all {
			if !strings.HasPrefix(key, "approval:") {
				continue
			}
			var entry reqapproval.ApprovalEntry
			if err := mem.Attrs().Get(key, &entry); err != nil || entry.Status != "pending" {
				continue
			}
			if entry.Title != title {
				continue
			}
			now := time.Now()
			entry.Status = decision
			entry.DecidedAt = &now
			_ = mem.Attrs().Set(key, entry)
			return
		}
	}
}

// TestRequestApproval_OperatorApproves_ReturnsApproved verifies the happy path:
// an operator writing "approved" to the approval attrs entry causes
// RequestApproval to return "approved" once the poller fires.
//
// Note: the poll interval is 5 s by design (package constant), so this test
// takes up to 5 s.
func TestRequestApproval_OperatorApproves_ReturnsApproved(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const title = "Patch nginx"
	writerDone := make(chan struct{})
	go writeDecision(ctx, mem, title, "approved", writerDone)

	result, err := reqapproval.RequestApproval(ctx, mem, nil, title, "security patches", "apt-get upgrade nginx", "low", "low")
	if err != nil {
		t.Fatalf("RequestApproval: unexpected error: %v", err)
	}
	if result != "approved" {
		t.Errorf("result = %q, want %q", result, "approved")
	}

	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Error("writer goroutine did not finish")
	}
}

// TestRequestApproval_OperatorRejects_ReturnsRejected mirrors the approved test
// for the rejected decision path.
func TestRequestApproval_OperatorRejects_ReturnsRejected(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const title = "Delete audit logs"
	writerDone := make(chan struct{})
	go writeDecision(ctx, mem, title, "rejected", writerDone)

	result, err := reqapproval.RequestApproval(ctx, mem, nil, title, "free up disk space", "rm -rf /var/log/audit", "low", "medium")
	if err != nil {
		t.Fatalf("RequestApproval: unexpected error: %v", err)
	}
	if result != "rejected" {
		t.Errorf("result = %q, want %q", result, "rejected")
	}

	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Error("writer goroutine did not finish")
	}
}

// TestRequestApproval_HighRisk_NotifiesImmediately verifies that a high-risk
// approval request triggers the notifier synchronously when the request is
// created — before any operator decision — so the operator gets an out-of-band
// alert without needing to poll the dashboard.
func TestRequestApproval_HighRisk_NotifiesImmediately(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	notify := notifier.New(mem)

	// Cancel context quickly — we only need to verify that the notification
	// fires before the approval loop starts polling.
	ctx, cancel := context.WithCancel(context.Background())

	notifyFired := make(chan struct{})
	go func() {
		// Poll the notifications domain until the high-risk alert appears.
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			var note struct {
				Subject string `json:"subject"`
			}
			if err := mem.Domain("notifications").ReadCurrent(&note); err != nil {
				continue
			}
			if strings.Contains(note.Subject, "Approval Required") {
				close(notifyFired)
				cancel() // signal RequestApproval to stop
				return
			}
		}
	}()

	_, _ = reqapproval.RequestApproval(ctx, mem, notify, "Wipe temp partition", "reclaim disk", "rm -rf /tmp/*", "low", "high")

	select {
	case <-notifyFired:
		// Good — notification arrived before we cancelled.
	case <-time.After(5 * time.Second):
		t.Fatal("high-risk notifier did not fire within 5 seconds of RequestApproval being called")
	}
}

// TestRequestApproval_HighImportance_NotifiesImmediately mirrors the high-risk
// test for the independent importance dimension: a low-risk-but-high-importance
// request (e.g. a critical CVE fix that is operationally safe to apply) must
// still notify immediately, since importance alone should be enough to alert
// the operator without the misuse of risk as a stand-in for urgency.
func TestRequestApproval_HighImportance_NotifiesImmediately(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	notify := notifier.New(mem)

	ctx, cancel := context.WithCancel(context.Background())

	notifyFired := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			var note struct {
				Subject string `json:"subject"`
			}
			if err := mem.Domain("notifications").ReadCurrent(&note); err != nil {
				continue
			}
			if strings.Contains(note.Subject, "Approval Required") {
				close(notifyFired)
				cancel()
				return
			}
		}
	}()

	_, _ = reqapproval.RequestApproval(ctx, mem, notify, "Patch critical CVE", "fixes a critical CVE", "apt-get upgrade openssl", "high", "low")

	select {
	case <-notifyFired:
		// Good — notification arrived before we cancelled.
	case <-time.After(5 * time.Second):
		t.Fatal("high-importance notifier did not fire within 5 seconds of RequestApproval being called")
	}
}

// TestRequestApproval_LowImportanceLowRisk_DoesNotNotifyImmediately verifies
// the inverse: a request that is neither high risk nor high importance must
// NOT fire the immediate out-of-band notification. Operators are only
// notified on timeout or if the request was explicitly marked high on either
// dimension.
func TestRequestApproval_LowImportanceLowRisk_DoesNotNotifyImmediately(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	notify := notifier.New(mem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the approval loop exits immediately

	_, _ = reqapproval.RequestApproval(ctx, mem, notify, "Reload nginx config", "apply new vhost", "nginx -s reload", "low", "low")

	var note struct {
		Subject string `json:"subject"`
	}
	// Either no notification was written at all, or the subject does not
	// contain the immediate-alert prefix. A timed-out or cancelled approval
	// may still write a notification, so we only check that the immediate-
	// alert subject did not fire.
	if err := mem.Domain("notifications").ReadCurrent(&note); err == nil {
		if strings.Contains(note.Subject, "Approval Required") {
			t.Errorf("low-importance, low-risk approval unexpectedly fired immediate notification: %q", note.Subject)
		}
	}
}
