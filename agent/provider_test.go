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

func TestJoyTokenProviderAnthropicAdapterUsesChatGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("expected system + user messages after conversion, got %#v", body["messages"])
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("expected only caller tool, got %#v", body["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chat_test", "model": "auto",
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "tool_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"id":"42"}`},
				}}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 4, "total_tokens": 11},
		})
	}))
	defer server.Close()

	client := joytoken.NewClient(joytoken.WithAPIKey("test-key"), joytoken.WithOpenAIBaseURL(server.URL+"/openai/v1"))
	provider := NewJoyTokenProvider(client, WithProtocol(AnthropicProtocol))
	response, err := provider.Complete(context.Background(), ModelRequest{
		Messages: []joytoken.ChatMessage{{Role: "system", Content: "Be concise"}, {Role: "user", Content: "Look up 42"}},
		Tools:    []joytoken.ChatTool{{Type: "function", Function: joytoken.ChatToolFunction{Name: "lookup", Parameters: map[string]any{"type": "object"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Function.Name != "lookup" || response.Usage.TotalTokens != 11 {
		t.Fatalf("unexpected response: %#v", response)
	}
}
