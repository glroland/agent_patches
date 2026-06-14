package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agent_patches/endpoint-server/a2a/tool"
	"agent_patches/endpoint-server/utils/config"
)

// toolCallResponse returns a chat completion JSON response that calls the
// given tool with "{}" arguments.
func toolCallResponse(toolName string) string {
	return `{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 0,
		"model": "test-model",
		"choices": [{
			"index": 0,
			"finish_reason": "tool_calls",
			"logprobs": null,
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [{
					"id": "call_1",
					"type": "function",
					"function": {"name": "` + toolName + `", "arguments": "{}"}
				}]
			}
		}]
	}`
}

// TestRun_RepeatedIdenticalToolCall_DoesNotExhaustMaxIter verifies that when
// the model keeps calling the same tool with identical arguments instead of
// producing a final answer (as observed with some local models), the agent
// short-circuits with the tool's last result rather than failing with
// "max iterations exceeded".
func TestRun_RepeatedIdenticalToolCall_DoesNotExhaustMaxIter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolCallResponse("check_drives")))
	}))
	defer srv.Close()

	var execCount int32
	checkDrives, err := tool.New("check_drives", "reports disk usage",
		func(_ context.Context, _ struct{}) (string, error) {
			atomic.AddInt32(&execCount, 1)
			return "Mount: / Used: 94.7%", nil
		},
	)
	if err != nil {
		t.Fatalf("tool.New: %v", err)
	}

	cfg := &config.Settings{Agent: config.AgentSettings{
		Model:     "test-model",
		MaxTokens: 100,
		MaxIter:   10,
		BaseURL:   srv.URL,
		APIKey:    "test",
	}}

	a := New([]tool.Tool{checkDrives}, cfg)

	output, err := a.Run(context.Background(), "check disk usage")
	if err != nil {
		t.Fatalf("Run() returned error, want short-circuit success: %v", err)
	}
	if !strings.Contains(output, "94.7%") {
		t.Errorf("Run() output = %q, want it to contain the last tool result", output)
	}

	// The tool itself should only have been executed once; subsequent
	// identical calls are served from the cached result.
	if got := atomic.LoadInt32(&execCount); got != 1 {
		t.Errorf("tool executed %d times, want 1", got)
	}
}

// TestRun_FinalAnswer_TerminatesNormally verifies the normal (non-repeating)
// path still works: a single tool call followed by a non-tool_calls finish.
func TestRun_FinalAnswer_TerminatesNormally(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&requestCount, 1) == 1 {
			w.Write([]byte(toolCallResponse("check_drives")))
			return
		}
		w.Write([]byte(`{
			"id": "chatcmpl-2",
			"object": "chat.completion",
			"created": 0,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"finish_reason": "stop",
				"logprobs": null,
				"message": {"role": "assistant", "content": "Disk usage looks fine."}
			}]
		}`))
	}))
	defer srv.Close()

	checkDrives, err := tool.New("check_drives", "reports disk usage",
		func(_ context.Context, _ struct{}) (string, error) {
			return "Mount: / Used: 50%", nil
		},
	)
	if err != nil {
		t.Fatalf("tool.New: %v", err)
	}

	cfg := &config.Settings{Agent: config.AgentSettings{
		Model:     "test-model",
		MaxTokens: 100,
		MaxIter:   10,
		BaseURL:   srv.URL,
		APIKey:    "test",
	}}

	a := New([]tool.Tool{checkDrives}, cfg)

	output, err := a.Run(context.Background(), "check disk usage")
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if output != "Disk usage looks fine." {
		t.Errorf("Run() output = %q, want final assistant message", output)
	}
}
