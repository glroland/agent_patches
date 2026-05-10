package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// Tool is the provider-agnostic interface implemented by all agent tools.
type Tool interface {
	Name() string
	Description() string
	// InputSchema returns a JSON Schema object describing the tool's parameters.
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

type typedTool[T any] struct {
	name        string
	description string
	schema      json.RawMessage
	handler     func(context.Context, T) (string, error)
}

// New creates a Tool from a typed handler. The JSON schema for T is derived
// via reflection and sent to the model as the function parameters definition.
func New[T any](name, description string, handler func(context.Context, T) (string, error)) (Tool, error) {
	r := &jsonschema.Reflector{DoNotReference: true}
	var zero T
	schema, err := json.Marshal(r.Reflect(&zero))
	if err != nil {
		return nil, fmt.Errorf("tool %q: schema: %w", name, err)
	}
	return &typedTool[T]{name: name, description: description, schema: schema, handler: handler}, nil
}

func (t *typedTool[T]) Name() string                 { return t.name }
func (t *typedTool[T]) Description() string          { return t.description }
func (t *typedTool[T]) InputSchema() json.RawMessage { return t.schema }

func (t *typedTool[T]) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in T
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("tool %q: unmarshal input: %w", t.name, err)
		}
	}
	return t.handler(ctx, in)
}
