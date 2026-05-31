package tests

import (
	"context"
	"encoding/json"
	"testing"

	"agent_patches/endpoint-server/tasks/hello"
)

func TestNewHelloTool_NoError(t *testing.T) {
	_, err := hello.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}
}

func TestHelloTool_Name(t *testing.T) {
	tool, err := hello.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	if got := tool.Name(); got != "hello" {
		t.Errorf("Name() = %q, want %q", got, "hello")
	}
}

func TestHelloTool_Description(t *testing.T) {
	tool, err := hello.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	if tool.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestHelloTool_Execute_ReturnsWorld(t *testing.T) {
	tool, err := hello.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result != "world" {
		t.Errorf("Execute() result = %q, want %q", result, "world")
	}
}
