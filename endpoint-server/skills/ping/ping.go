package ping

import (
	"context"

	"agent_patches/endpoint-server/a2a/tool"
)

type pingInput struct{}

// NewPingTool returns a task tool that responds with "world".
func NewPingTool() (tool.Tool, error) {
	return tool.New(
		"ping",
		"Returns 'pong' as a response to any request.",
		func(_ context.Context, _ pingInput) (string, error) {
			return "pong", nil
		},
	)
}
