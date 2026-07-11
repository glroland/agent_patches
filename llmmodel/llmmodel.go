// Package llmmodel holds the sentinel model name shared between
// endpoint-server and llm-gateway so a per-agent model is optional.
package llmmodel

// Default is sent as the "model" field by an endpoint-server agent whose
// agent.model config is unset. llm-gateway recognizes this sentinel and
// substitutes its own configured upstream model (GATEWAY_UPSTREAM_MODEL)
// before forwarding the request, so an operator only has to name the model
// once, at the gateway, for every agent that doesn't override it.
const Default = "DEFAULT"
