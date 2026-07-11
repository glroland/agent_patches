package main

import (
	"encoding/json"
	"testing"

	"agent_patches/llmmodel"
)

func TestResolveModel(t *testing.T) {
	cases := []struct {
		name          string
		upstreamModel string
		body          string
		wantModel     string // expected "model" field after resolution; "" means unchanged/absent
	}{
		{
			name:          "sentinel replaced when upstream model configured",
			upstreamModel: "Qwen3-Coder-Next-GGUF",
			body:          `{"model":"DEFAULT","messages":[]}`,
			wantModel:     "Qwen3-Coder-Next-GGUF",
		},
		{
			name:          "explicit model passes through untouched",
			upstreamModel: "Qwen3-Coder-Next-GGUF",
			body:          `{"model":"gpt-4o","messages":[]}`,
			wantModel:     "gpt-4o",
		},
		{
			name:          "sentinel left as-is when no upstream model configured",
			upstreamModel: "",
			body:          `{"model":"DEFAULT","messages":[]}`,
			wantModel:     "DEFAULT",
		},
		{
			name:          "missing model field passes through untouched",
			upstreamModel: "Qwen3-Coder-Next-GGUF",
			body:          `{"messages":[]}`,
			wantModel:     "",
		},
		{
			name:          "non-JSON body passes through untouched",
			upstreamModel: "Qwen3-Coder-Next-GGUF",
			body:          `not json`,
			wantModel:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Gateway{upstreamModel: tc.upstreamModel}
			got := g.resolveModel([]byte(tc.body))

			var fields map[string]json.RawMessage
			if err := json.Unmarshal(got, &fields); err != nil {
				if tc.wantModel != "" {
					t.Fatalf("resolveModel produced invalid JSON: %v (body: %s)", err, got)
				}
				if string(got) != tc.body {
					t.Fatalf("non-JSON body was mutated: got %q, want %q", got, tc.body)
				}
				return
			}

			raw, ok := fields["model"]
			if !ok {
				if tc.wantModel != "" {
					t.Fatalf("expected model %q, but model field is absent", tc.wantModel)
				}
				return
			}
			var model string
			if err := json.Unmarshal(raw, &model); err != nil {
				t.Fatalf("model field is not a string: %v", err)
			}
			if model != tc.wantModel {
				t.Errorf("model = %q, want %q", model, tc.wantModel)
			}
		})
	}
}

func TestDefaultSentinelValue(t *testing.T) {
	if llmmodel.Default != "DEFAULT" {
		t.Errorf("llmmodel.Default = %q, want %q", llmmodel.Default, "DEFAULT")
	}
}
