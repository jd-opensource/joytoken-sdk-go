package joytokenexample

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	agent "github.com/jd-opensource/joytoken-sdk-go/agent"
)

func TestGoAgentExample(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer example-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []any{map[string]any{
							"id": "call_1", "type": "function",
							"function": map[string]any{"name": "lookup", "arguments": `{"id":"42"}`},
						}},
					},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "record:42"}}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 2, "total_tokens": 4},
		})
	}))
	defer server.Close()

	client := joytoken.NewClient(
		joytoken.WithAPIKey("example-key"),
		joytoken.WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)
	runner := agent.New(agent.AgentOptions{
		Model: agent.NewJoyTokenProvider(client),
		Tools: []agent.AgentTool{{
			Name: "lookup",
			Execute: func(_ context.Context, _ any, _ agent.ToolExecutionContext) (any, error) {
				return "record:42", nil
			},
		}},
	})
	result, err := runner.Run(context.Background(), "Summarize record 42")
	if err != nil {
		t.Fatalf("Agent.Run returned error: %v", err)
	}
	if result.FinalText != "record:42" {
		t.Fatalf("expected record:42, got %q", result.FinalText)
	}
}
