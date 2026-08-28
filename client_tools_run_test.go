package joytoken

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRunChatCompletionExecutesTools verifies that RunChatCompletion drives a
// bounded model/tool loop on the Chat Completions endpoint: the first turn
// returns a tool_call for a locally registered tool, the loop executes it and
// feeds the result back, and the second turn returns a plain text answer that
// stops the loop.
func TestRunChatCompletionExecutesTools(t *testing.T) {
	var turns int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openai/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		turns++
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			// First turn: assistant asks to call the local echo tool.
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
			return
		}
		// Second turn: no tool_calls, just a final text answer.
		_, _ = w.Write([]byte(`{"id":"chatcmpl_2","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"final answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`))
	}))
	defer server.Close()

	echo := Tool{
		Name:        "echo",
		Description: "echoes its text argument",
		Execute: func(_ context.Context, args any, _ ToolExecutionContext) (any, error) {
			return args, nil
		},
	}
	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
		WithTools(echo),
	)

	result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "call echo"},
		},
	}, RunChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if turns != 2 {
		t.Fatalf("expected 2 model turns (tool call + final), got %d", turns)
	}
	if result.StoppedBy != "stop" {
		t.Fatalf("expected loop to stop naturally, got %q", result.StoppedBy)
	}
	if result.FinalText != "final answer" {
		t.Fatalf("expected final text %q, got %q", "final answer", result.FinalText)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 steps recorded, got %d", len(result.Steps))
	}
	if len(result.Steps[0].ToolResults) != 1 {
		t.Fatalf("expected 1 tool result on the first step, got %d", len(result.Steps[0].ToolResults))
	}
	if result.Steps[0].ToolResults[0].ToolName != "echo" {
		t.Fatalf("expected first tool result to be echo, got %q", result.Steps[0].ToolResults[0].ToolName)
	}
}

// TestRunChatCompletionNoToolsSingleTurn verifies that when the first turn
// returns no tool_calls, the loop stops after a single round-trip.
func TestRunChatCompletionNoToolsSingleTurn(t *testing.T) {
	var turns int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openai/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		turns++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)

	result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	}, RunChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if turns != 1 {
		t.Fatalf("expected 1 turn when no tools are called, got %d", turns)
	}
	if result.StoppedBy != "stop" {
		t.Fatalf("expected loop to stop naturally, got %q", result.StoppedBy)
	}
	if result.FinalText != "hello" {
		t.Fatalf("expected final text %q, got %q", "hello", result.FinalText)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step recorded, got %d", len(result.Steps))
	}
}

// TestRunChatCompletionKeepsOneToolSetPerTurn verifies that a continuation
// request still declares the effective tools exactly once. The model must keep
// access to tools for sequential calls, without duplicate declarations being
// introduced by the execution loop.
func TestRunChatCompletionKeepsOneToolSetPerTurn(t *testing.T) {
	var turns int
	var toolNamesPerTurn [][]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openai/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		turns++

		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Tools []json.RawMessage `json:"tools"`
		}
		_ = json.Unmarshal(body, &payload)
		names := make([]string, 0, len(payload.Tools))
		for _, raw := range payload.Tools {
			var tool ChatTool
			if err := json.Unmarshal(raw, &tool); err != nil {
				t.Fatalf("decode tool declaration: %v", err)
			}
			names = append(names, tool.Function.Name)
		}
		toolNamesPerTurn = append(toolNamesPerTurn, names)

		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl_2","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"final answer"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	echo := Tool{
		Name:        "echo",
		Description: "echoes its text argument",
		Execute: func(_ context.Context, args any, _ ToolExecutionContext) (any, error) {
			return args, nil
		},
	}
	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
		WithTools(echo),
	)

	_, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "call echo"},
		},
	}, RunChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if turns != 2 {
		t.Fatalf("expected 2 model turns, got %d", turns)
	}
	if len(toolNamesPerTurn) != 2 {
		t.Fatalf("expected to capture 2 request bodies, got %d", len(toolNamesPerTurn))
	}
	for turn, names := range toolNamesPerTurn {
		if len(names) != 1 || names[0] != "echo" {
			t.Fatalf("turn %d should declare exactly one echo tool, got %v", turn+1, names)
		}
	}
}
