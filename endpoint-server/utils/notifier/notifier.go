package notifier

import (
	"context"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/memory"
)

// notification is the value written to the memory store on each Notify call.
type notification struct {
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Time    time.Time `json:"time"`
}

// Notifier persists event notifications to the agent memory store under the
// "notifications" domain. A nil Notifier is valid and silently discards all
// notifications.
type Notifier struct {
	domain *memory.DomainStore
}

// New creates a Notifier that writes to mem under the "notifications" domain.
func New(mem *memory.Store) *Notifier {
	return &Notifier{domain: mem.Domain("notifications")}
}

// Notify writes subject and body to memory. Errors are logged but not
// returned; a failed write does not affect the caller.
func (n *Notifier) Notify(_ context.Context, subject, body string) {
	if n == nil {
		return
	}
	note := notification{Subject: subject, Body: body, Time: time.Now()}
	if err := n.domain.Write(note); err != nil {
		slog.Warn("notifier: failed to write notification to memory", "subject", subject, "error", err)
		return
	}
	slog.Info("notifier: notification stored", "subject", subject)
}
