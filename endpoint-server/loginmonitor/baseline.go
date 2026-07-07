// baseline.go compares a new login against the host's own accumulated
// login_history and flags it when it deviates from that user's established
// pattern — a user logging in for the first time ever, from a source never
// seen for that user before, or at a time of day that's never occurred in
// their history. Complements checkUnusualSource (which only compares against
// the operator-configured allowed_sources list) with a self-learned baseline
// that needs no configuration.
package loginmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent_patches/endpoint-server/skillstate"
)

const (
	// baselineDefaultMinEvents is used when cfg.BaselineMinEvents is unset.
	baselineDefaultMinEvents = 5

	// baselineTimeBucketHours is the width of the hour-of-day bucket used to
	// evaluate the "unusual_time" signal.
	baselineTimeBucketHours = 4
)

// checkAgainstBaseline compares ev against prior — the login history for this
// host, which must NOT include ev itself — and, on a deviation, sets
// ev.Unusual/ev.UnusualReason and raises the matching escalations (skillstate,
// incident ledger, and — for the critical tier — email). At most one reason
// fires per login: new_user takes priority over new_source, which takes
// priority over unusual_time.
func (m *Monitor) checkAgainstBaseline(ev *LoginEvent, prior []LoginEvent) {
	if m.cfg.DisableUnusualLoginBaseline {
		return
	}

	minEvents := m.cfg.BaselineMinEvents
	if minEvents <= 0 {
		minEvents = baselineDefaultMinEvents
	}

	var seenUser bool
	priorCount := 0
	knownSources := make(map[string]bool)
	knownHours := make(map[int]bool)
	for _, h := range prior {
		if h.Username != ev.Username || (h.EventType != EventLogin && h.EventType != EventExisting) {
			continue
		}
		seenUser = true
		priorCount++
		knownSources[sourceIdentity(h)] = true
		knownHours[h.Timestamp.UTC().Hour()/baselineTimeBucketHours] = true
	}

	var reason, fingerprint, title, detail string
	var severity skillstate.Health
	switch {
	case !seenUser:
		reason = "new_user"
		fingerprint = fmt.Sprintf("unusual-login-newuser-%s", ev.Username)
		title = fmt.Sprintf("first-ever login for user %q", ev.Username)
		detail = fmt.Sprintf("User %q logged in for the first time in recorded history (origin: %s).",
			ev.Username, originDescription(*ev))
		severity = severityFor(ev.Remote)

	case !knownSources[sourceIdentity(*ev)]:
		reason = "new_source"
		fingerprint = fmt.Sprintf("unusual-login-newsource-%s-%s", ev.Username, sourceIdentity(*ev))
		title = fmt.Sprintf("login for %q from a never-seen source", ev.Username)
		detail = fmt.Sprintf("User %q logged in from %s, which has not been seen for this user before.",
			ev.Username, originDescription(*ev))
		severity = severityFor(ev.Remote)

	case priorCount >= minEvents && !knownHours[ev.Timestamp.UTC().Hour()/baselineTimeBucketHours]:
		reason = "unusual_time"
		fingerprint = fmt.Sprintf("unusual-login-offhours-%s", ev.Username)
		title = fmt.Sprintf("off-hours login for %q", ev.Username)
		detail = fmt.Sprintf("User %q logged in at %s UTC, a time of day not seen in their prior %d logins.",
			ev.Username, ev.Timestamp.UTC().Format("15:04"), priorCount)
		severity = skillstate.HealthWarning

	default:
		return
	}

	ev.Unusual = true
	ev.UnusualReason = reason

	_ = skillstate.Save(m.mem, "check_interactive_logins", severity, fmt.Sprintf("%s: %s", reason, detail))
	if _, _, err := m.incidents.Report(fingerprint, title, detail, string(severity)); err != nil {
		slog.Warn("loginmonitor: failed to report unusual-login incident", "fingerprint", fingerprint, "error", err)
	}
	slog.Warn("loginmonitor: unusual login detected", "user", ev.Username, "reason", reason, "severity", severity)

	if severity == skillstate.HealthCritical {
		subject := fmt.Sprintf("[CRITICAL] %s", title)
		body := fmt.Sprintf("%s\n\nUser:     %s\nSession:  %s\nOrigin:   %s\nTime:     %s",
			detail, ev.Username, ev.SessionID, originDescription(*ev), ev.Timestamp.Format(time.RFC1123))
		m.notify.Notify(context.Background(), subject, body)
	}
}

// sourceIdentity returns the stable identity used to compare login origins
// across history: the resolved source IP, falling back to the raw remote
// host value, falling back to a sentinel for local (non-remote) sessions.
func sourceIdentity(ev LoginEvent) string {
	switch {
	case !ev.Remote:
		return "local"
	case ev.SourceIP != "":
		return ev.SourceIP
	case ev.RemoteHost != "":
		return ev.RemoteHost
	default:
		return "unknown"
	}
}

// originDescription renders a human-readable origin for alert text.
func originDescription(ev LoginEvent) string {
	if !ev.Remote {
		return "local console"
	}
	id := sourceIdentity(ev)
	if ev.ResolvedHostname != "" && ev.ResolvedHostname != id {
		return fmt.Sprintf("%s (%s)", id, ev.ResolvedHostname)
	}
	return id
}

// severityFor applies the "remote is scarier than local" judgment already
// used by checkUnusualSource to identity-based anomalies.
func severityFor(remote bool) skillstate.Health {
	if remote {
		return skillstate.HealthCritical
	}
	return skillstate.HealthWarning
}
