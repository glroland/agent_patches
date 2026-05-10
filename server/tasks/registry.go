package tasks

import "github.com/anthropics/anthropic-sdk-go"

// Registry holds all registered task tools available to the agent.
type Registry struct {
	tools []anthropic.BetaTool
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(tool anthropic.BetaTool) {
	r.tools = append(r.tools, tool)
}

func (r *Registry) Tools() []anthropic.BetaTool {
	return r.tools
}
