package connmonitor

import (
	"testing"
	"time"

	"agent_patches/endpoint-server/incidents"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skillstate"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/notifier"
)

func newTestMonitor(t *testing.T, cfg config.NetworkMonitorSettings) *Monitor {
	t.Helper()
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	return NewWithGatherer(mem, notifier.New(mem), incidents.New(mem), cfg, nil)
}

func inbound(proto string, port int, remote string, process string) ConnEvent {
	return ConnEvent{
		EventType: EventOpen, Direction: DirectionInbound,
		Proto: proto, LocalAddr: "192.168.1.5", LocalPort: port,
		RemoteAddr: remote, RemotePort: 54321, Process: process,
		Timestamp: time.Now().UTC(),
	}
}

func outbound(remote string, process string) ConnEvent {
	return ConnEvent{
		EventType: EventOpen, Direction: DirectionOutbound,
		Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000,
		RemoteAddr: remote, RemotePort: 443, Process: process,
		Timestamp: time.Now().UTC(),
	}
}

func TestCheckAgainstBaseline(t *testing.T) {
	tests := []struct {
		name         string
		prior        []ConnEvent
		ev           ConnEvent
		wantUnusual  bool
		wantReason   string
		wantSeverity skillstate.Health
	}{
		{
			name:         "new inbound port is critical",
			prior:        nil,
			ev:           inbound("tcp", 22, "192.168.1.10", "sshd"),
			wantUnusual:  true,
			wantReason:   "new_inbound_port",
			wantSeverity: skillstate.HealthCritical,
		},
		{
			name: "known inbound port, new process is critical",
			prior: []ConnEvent{
				inbound("tcp", 22, "192.168.1.10", "sshd"),
			},
			ev:           inbound("tcp", 22, "192.168.1.11", "backdoor"),
			wantUnusual:  true,
			wantReason:   "new_process",
			wantSeverity: skillstate.HealthCritical,
		},
		{
			name: "new outbound remote host is warning",
			prior: []ConnEvent{
				outbound("93.184.216.34", "curl"),
			},
			ev:           outbound("203.0.113.9", "curl"),
			wantUnusual:  true,
			wantReason:   "new_remote_host",
			wantSeverity: skillstate.HealthWarning,
		},
		{
			name: "everything known — no hit",
			prior: []ConnEvent{
				inbound("tcp", 22, "192.168.1.10", "sshd"),
				outbound("93.184.216.34", "curl"),
			},
			ev:          outbound("93.184.216.34", "curl"),
			wantUnusual: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMonitor(t, config.NetworkMonitorSettings{})
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

func TestCheckAgainstBaselineOwnPortExempt(t *testing.T) {
	m := newTestMonitor(t, config.NetworkMonitorSettings{OwnPort: 9976})
	ev := inbound("tcp", 9976, "192.168.1.184", "patches-endpoint-server")
	m.checkAgainstBaseline(&ev, nil)

	if ev.Unusual {
		t.Fatalf("expected no anomaly for the agent's own listening port, got reason %q", ev.UnusualReason)
	}
	incs, _ := m.incidents.All()
	if len(incs) != 0 {
		t.Fatalf("expected no incidents for own-port traffic, got %+v", incs)
	}
}

func TestCheckAgainstBaselineDisabled(t *testing.T) {
	m := newTestMonitor(t, config.NetworkMonitorSettings{DisableUnusualConnectionBaseline: true})
	ev := inbound("tcp", 22, "192.168.1.10", "sshd")
	m.checkAgainstBaseline(&ev, nil)

	if ev.Unusual {
		t.Fatalf("expected no anomaly when baseline check is disabled, got reason %q", ev.UnusualReason)
	}
	incs, _ := m.incidents.All()
	if len(incs) != 0 {
		t.Fatalf("expected no incidents when disabled, got %+v", incs)
	}
}
