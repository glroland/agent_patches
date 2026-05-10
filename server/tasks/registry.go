package tasks

import "agent_patches/server/tool"

// Registry holds all registered task tools available to the agent.
type Registry struct {
	tools []tool.Tool
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(t tool.Tool) {
	r.tools = append(r.tools, t)
}

func (r *Registry) Tools() []tool.Tool {
	return r.tools
}
