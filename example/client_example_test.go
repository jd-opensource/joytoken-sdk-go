package joytokenexample

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

func TestGoClientExample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer example-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(joytoken.ModelListResponse{
				Object: "list",
				Data: joytoken.ModelListData{Models: []joytoken.ModelInfo{
					{ModelID: "auto", ModelKey: "auto", DisplayName: "auto", Alias: "auto"},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(joytoken.ChatCompletionResponse{
				ID: "chatcmpl_example",
				Choices: []joytoken.ChatCompletionChoice{
					{Index: 0, Message: joytoken.ChatMessage{Role: "assistant", Content: "pong"}, FinishReason: "stop"},
				},
				Usage: &joytoken.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := joytoken.NewClient(
		joytoken.WithAPIKey("example-key"),
		joytoken.WithAPIBaseURL(server.URL),
		joytoken.WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if got := models.Data.Models[0].ModelID; got != "auto" {
		t.Fatalf("expected auto, got %s", got)
	}

	completion, err := client.CreateChatCompletion(context.Background(), joytoken.ChatCompletionRequest{
		Model: joytoken.ModelAuto,
		Messages: []joytoken.ChatMessage{
			{Role: "user", Content: "ping"},
		},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion returned error: %v", err)
	}
	if got := completion.Choices[0].Message.Content; got != "pong" {
		t.Fatalf("expected pong, got %v", got)
	}
}
