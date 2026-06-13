package tests

import (
	"context"
	"encoding/json"
	"testing"

	"agent_patches/endpoint-server/skills/ping"
)

func TestNewPingTool_NoError(t *testing.T) {
	_, err := ping.NewPingTool()
	if err != nil {
		t.Fatalf("NewPingTool() unexpected error: %v", err)
	}
}

func TestPingTool_Name(t *testing.T) {
	tool, err := ping.NewPingTool()
	if err != nil {
		t.Fatalf("NewPingTool() unexpected error: %v", err)
	}

	if got := tool.Name(); got != "ping" {
		t.Errorf("Name() = %q, want %q", got, "ping")
	}
}

func TestPingTool_Description(t *testing.T) {
	tool, err := ping.NewPingTool()
	if err != nil {
		t.Fatalf("NewPingTool() unexpected error: %v", err)
	}

	if tool.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestPingTool_Execute_ReturnsWorld(t *testing.T) {
	tool, err := ping.NewPingTool()
	if err != nil {
		t.Fatalf("NewPingTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result != "pong" {
		t.Errorf("Execute() result = %q, want %q", result, "world")
	}
}
