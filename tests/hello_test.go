package tests

import (
	"context"
	"encoding/json"
	"testing"

	"agent_patches/server/tasks"
)

func TestNewHelloTool_NoError(t *testing.T) {
	_, err := tasks.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}
}

func TestHelloTool_Name(t *testing.T) {
	tool, err := tasks.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	if got := tool.Name(); got != "hello" {
		t.Errorf("Name() = %q, want %q", got, "hello")
	}
}

func TestHelloTool_Description(t *testing.T) {
	tool, err := tasks.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	if tool.Description() == "" {
		t.Error("Description() returned empty string")
	}
}

func TestHelloTool_Execute_ReturnsWorld(t *testing.T) {
	tool, err := tasks.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}

	input, _ := json.Marshal(struct{}{})
	blocks, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("Execute() returned no content blocks")
	}

	block := blocks[0]
	if block.OfText == nil {
		t.Fatal("Execute() result block has no text")
	}
	if got := block.OfText.Text; got != "world" {
		t.Errorf("Execute() text = %q, want %q", got, "world")
	}
}
