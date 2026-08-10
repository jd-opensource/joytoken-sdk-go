package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

func TestJoyTokenProviderOpenAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hello"}}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5},
		})
	}))
	defer server.Close()

	client := joytoken.NewClient(
		joytoken.WithAPIKey("test-key"),
		joytoken.WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)
	provider := NewJoyTokenProvider(client)
	response, err := provider.Complete(context.Background(), ModelRequest{Messages: []joytoken.ChatMessage{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if response.Message.Content != "hello" || response.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestJoyTokenProviderAnthropicConversion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected Anthropic headers: %#v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["system"] != "Be concise" {
			t.Errorf("system = %#v", body["system"])
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 3 {
			t.Fatalf("messages = %#v", body["messages"])
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %#v", body["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant", "model": "auto",
			"content": []any{
				map[string]any{"type": "text", "text": "use tool"},
				map[string]any{"type": "tool_use", "id": "tool_1", "name": "lookup", "input": map[string]any{"id": "42"}},
			},
			"usage": map[string]any{"input_tokens": 7, "output_tokens": 4},
		})
	}))
	defer server.Close()

	client := joytoken.NewClient(
		joytoken.WithAPIKey("test-key"),
		joytoken.WithAnthropicBaseURL(server.URL+"/anthropic/v1"),
	)
	provider := NewJoyTokenProvider(client, WithProtocol(AnthropicProtocol))
	response, err := provider.Complete(context.Background(), ModelRequest{
		Messages: []joytoken.ChatMessage{
			{Role: "system", Content: "Be concise"},
			{Role: "user", Content: "Look up 42"},
			{Role: "assistant", Content: nil, ToolCalls: []joytoken.ToolCall{{
				ID: "tool_0", Type: "function", Function: joytoken.ToolFunction{Name: "lookup", Arguments: `{"id":"42"}`},
			}}},
			{Role: "tool", ToolCallID: "tool_0", Content: "record:42"},
		},
		Tools: []joytoken.ChatTool{{Type: "function", Function: joytoken.ChatToolFunction{Name: "lookup", Parameters: map[string]any{"type": "object"}}}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if response.Message.ToolCalls[0].Function.Name != "lookup" || response.Usage.TotalTokens != 11 {
		t.Fatalf("unexpected response: %#v", response)
	}
}
