package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_patches/endpoint-server/connmonitor"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/check_network_connections"
	"agent_patches/endpoint-server/utils/config"
)

func TestCheckNetworkConnections_ReportsActiveAndHistory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	gather := func(context.Context) ([]connmonitor.Conn, error) {
		return []connmonitor.Conn{
			{Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 22, RemoteAddr: "192.168.1.10", RemotePort: 54321, State: "ESTABLISHED", Process: "sshd"},
			{Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 51000, RemoteAddr: "93.184.216.34", RemotePort: 443, State: "ESTABLISHED", Process: "curl"},
		}, nil
	}
	mon := connmonitor.NewWithGatherer(mem, config.NetworkMonitorSettings{}, gather)
	if _, err := mon.PollOnce(context.Background()); err != nil {
		t.Fatalf("seed PollOnce: %v", err)
	}

	tl, err := check_network_connections.NewCheckNetworkConnectionsTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNetworkConnectionsTool: %v", err)
	}

	out, err := tl.Execute(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, want := range []string{"Active Connections", "Inbound", "Outbound", "sshd", "curl", "Recent Activity"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestCheckNetworkConnections_ReportsRecentCloseEvent(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	polls := [][]connmonitor.Conn{
		{{Proto: "tcp", LocalAddr: "192.168.1.5", LocalPort: 22, RemoteAddr: "192.168.1.10", RemotePort: 54321, State: "ESTABLISHED", Process: "sshd"}},
		{}, // the connection above closes
	}
	call := 0
	gather := func(context.Context) ([]connmonitor.Conn, error) {
		c := polls[call]
		call++
		return c, nil
	}
	mon := connmonitor.NewWithGatherer(mem, config.NetworkMonitorSettings{}, gather)
	ctx := context.Background()
	if _, err := mon.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce 1: %v", err)
	}
	if _, err := mon.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce 2: %v", err)
	}

	tl, err := check_network_connections.NewCheckNetworkConnectionsTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNetworkConnectionsTool: %v", err)
	}
	out, err := tl.Execute(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out, "No active connections.") {
		t.Errorf("report should show no active connections after close:\n%s", out)
	}
	if !strings.Contains(out, "close") {
		t.Errorf("report missing the close event in Recent Activity:\n%s", out)
	}
}

func TestCheckNetworkConnections_LiveFallbackWhenNoHistory(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})

	tl, err := check_network_connections.NewCheckNetworkConnectionsTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNetworkConnectionsTool: %v", err)
	}

	out, err := tl.Execute(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Whether or not the live gatherer succeeds on the test machine, the
	// fallback path always notes that no background history exists yet.
	if !strings.Contains(out, "Background connection monitor has not recorded any history yet.") {
		t.Errorf("report missing no-history note:\n%s", out)
	}
}

func TestNewCheckNetworkConnectionsTool_NameAndDescription(t *testing.T) {
	mem := memory.New(&config.MemorySettings{Root: t.TempDir()})
	tl, err := check_network_connections.NewCheckNetworkConnectionsTool(mem)
	if err != nil {
		t.Fatalf("NewCheckNetworkConnectionsTool: %v", err)
	}
	if tl.Name() != "check_network_connections" {
		t.Errorf("Name() = %q, want check_network_connections", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description() is empty")
	}
}
