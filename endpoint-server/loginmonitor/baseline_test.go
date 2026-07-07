package loginmonitor

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func newTestMonitor(t *testing.T, cfg config.LoginMonitorSettings) *Monitor {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return New(mem, notifier.New(mem), incidents.New(mem), cfg)
}

func loginAt(user, source string, remote bool, hour int) LoginEvent {
	return LoginEvent{
		EventType: EventLogin,
		Username:  user,
		Remote:    remote,
		SourceIP:  source,
		Timestamp: time.Date(2026, 1, 5, hour, 0, 0, 0, time.UTC),
	}
}

func TestCheckAgainstBaseline(t *testing.T) {
	tests := []struct {
		name         string
		prior        []LoginEvent
		ev           LoginEvent
		wantUnusual  bool
		wantReason   string
		wantSeverity skillstate.Health
	}{
		{
			name:         "brand new user, remote is critical",
			prior:        nil,
			ev:           loginAt("alice", "10.0.0.5", true, 10),
			wantUnusual:  true,
			wantReason:   "new_user",
			wantSeverity: skillstate.HealthCritical,
		},
		{
			name:         "brand new user, local is warning",
			prior:        nil,
			ev:           loginAt("alice", "", false, 10),
			wantUnusual:  true,
			wantReason:   "new_user",
			wantSeverity: skillstate.HealthWarning,
		},
		{
			name: "known user, new remote source is critical",
			prior: []LoginEvent{
				loginAt("alice", "10.0.0.5", true, 10),
			},
			ev:           loginAt("alice", "10.0.0.99", true, 10),
			wantUnusual:  true,
			wantReason:   "new_source",
			wantSeverity: skillstate.HealthCritical,
		},
		{
			name: "known user, new local source is warning",
			prior: []LoginEvent{
				loginAt("alice", "10.0.0.5", true, 10),
			},
			ev:           loginAt("alice", "", false, 10),
			wantUnusual:  true,
			wantReason:   "new_source",
			wantSeverity: skillstate.HealthWarning,
		},
		{
			name: "known user and source, off-hours with enough history",
			prior: []LoginEvent{
				loginAt("alice", "10.0.0.5", true, 9),
				loginAt("alice", "10.0.0.5", true, 10),
				loginAt("alice", "10.0.0.5", true, 11),
				loginAt("alice", "10.0.0.5", true, 9),
				loginAt("alice", "10.0.0.5", true, 10),
			},
			ev:           loginAt("alice", "10.0.0.5", true, 2),
			wantUnusual:  true,
			wantReason:   "unusual_time",
			wantSeverity: skillstate.HealthWarning,
		},
		{
			name: "known user, source, and hour bucket — no hit",
			prior: []LoginEvent{
				loginAt("alice", "10.0.0.5", true, 9),
				loginAt("alice", "10.0.0.5", true, 10),
				loginAt("alice", "10.0.0.5", true, 11),
				loginAt("alice", "10.0.0.5", true, 9),
				loginAt("alice", "10.0.0.5", true, 10),
			},
			ev:          loginAt("alice", "10.0.0.5", true, 10),
			wantUnusual: false,
		},
		{
			name: "off-hours skipped below BaselineMinEvents",
			prior: []LoginEvent{
				loginAt("alice", "10.0.0.5", true, 9),
				loginAt("alice", "10.0.0.5", true, 10),
			},
			ev:          loginAt("alice", "10.0.0.5", true, 2),
			wantUnusual: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMonitor(t, config.LoginMonitorSettings{})
			ev := tt.ev
			m.checkAgainstBaseline(&ev, tt.prior)

			if ev.Unusual != tt.wantUnusual {
				t.Fatalf("Unusual = %v, want %v", ev.Unusual, tt.wantUnusual)
			}
			if !tt.wantUnusual {
				return
			}
			if ev.UnusualReason != tt.wantReason {
				t.Errorf("UnusualReason = %q, want %q", ev.UnusualReason, tt.wantReason)
			}

			states, err := skillstate.LoadAll(m.mem)
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			if len(states) != 1 || states[0].Health != tt.wantSeverity {
				t.Errorf("skillstate = %+v, want severity %q", states, tt.wantSeverity)
			}

			incs, err := m.incidents.All()
			if err != nil {
				t.Fatalf("incidents.All: %v", err)
			}
			if len(incs) != 1 || incs[0].Severity != string(tt.wantSeverity) {
				t.Errorf("incidents = %+v, want one with severity %q", incs, tt.wantSeverity)
			}
		})
	}
}

func TestCheckAgainstBaselineDisabled(t *testing.T) {
	m := newTestMonitor(t, config.LoginMonitorSettings{DisableUnusualLoginBaseline: true})
	ev := loginAt("alice", "10.0.0.5", true, 10)
	m.checkAgainstBaseline(&ev, nil)

	if ev.Unusual {
		t.Fatalf("expected no anomaly when baseline check is disabled, got reason %q", ev.UnusualReason)
	}
	incs, _ := m.incidents.All()
	if len(incs) != 0 {
		t.Fatalf("expected no incidents when disabled, got %+v", incs)
	}
}
