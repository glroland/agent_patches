package hello

import (
	"context"

	"agent_patches/endpoint-server/tool"
)

type helloInput struct{}

// NewHelloTool returns a task tool that responds with "world".
func NewHelloTool() (tool.Tool, error) {
	return tool.New(
		"hello",
		"Returns 'world' as a response to any greeting or hello request.",
		func(_ context.Context, _ helloInput) (string, error) {
			return "world", nil
		},
	)
}
