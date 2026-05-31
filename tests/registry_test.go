package tests

import (
	"testing"

	"agent_patches/endpoint-server/tasks"
	"agent_patches/endpoint-server/tasks/hello"
)

func TestRegistry_EmptyByDefault(t *testing.T) {
	r := tasks.NewRegistry()
	if got := len(r.Tools()); got != 0 {
		t.Errorf("new registry has %d tools, want 0", got)
	}
}

func TestRegistry_Register(t *testing.T) {
	r := tasks.NewRegistry()

	tool, err := hello.NewHelloTool()
	if err != nil {
		t.Fatalf("NewHelloTool() unexpected error: %v", err)
	}
	r.Register(tool)

	if got := len(r.Tools()); got != 1 {
		t.Errorf("after Register() len = %d, want 1", got)
	}
}

func TestRegistry_Tools_RetainsOrder(t *testing.T) {
	r := tasks.NewRegistry()

	tool1, _ := hello.NewHelloTool()
	tool2, _ := hello.NewHelloTool()
	r.Register(tool1)
	r.Register(tool2)

	tools := r.Tools()
	if len(tools) != 2 {
		t.Fatalf("len = %d, want 2", len(tools))
	}
	if tools[0].Name() != tool1.Name() || tools[1].Name() != tool2.Name() {
		t.Error("Tools() did not preserve registration order")
	}
}
