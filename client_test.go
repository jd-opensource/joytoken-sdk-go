package joytoken

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateChatCompletion(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion returned error: %v", err)
	}
	if got := response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("expected hello, got %v", got)
	}
}

func TestStreamChatCompletion(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv returned error: %v", err)
	}
	if got := chunk.Choices[0].Delta["content"]; got != "hello" {
		t.Fatalf("expected hello, got %v", got)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestGenerateImage(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	response, err := client.GenerateImage(context.Background(), ImageGenerationRequest{
		Model:  ModelAuto,
		Prompt: "A JoyToken logo on a black background",
		Size:   "1024x1024",
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}
	if got := response.Data[0].URL; got != "https://example.com/generated.png" {
		t.Fatalf("expected generated image URL, got %q", got)
	}
	usage, ok := response.Metadata["usage"].(map[string]any)
	if !ok || usage["credits_used"] != "1.25" {
		t.Fatalf("unexpected image metadata: %#v", response.Metadata)
	}
}

func TestStreamChatCompletionHandlesLargeSSEEvent(t *testing.T) {
	content := strings.Repeat("x", 70*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"` + content + `"},"finish_reason":null}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithAPIBaseURL(server.URL),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv returned error: %v", err)
	}
	got, ok := chunk.Choices[0].Delta["content"].(string)
	if !ok || got != content {
		t.Fatalf("expected large string content, got %T", chunk.Choices[0].Delta["content"])
	}
}

func TestStreamChatCompletionHandlesMultilineSSEEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":\n"))
		_, _ = w.Write([]byte("data: [{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto})
	if err != nil {
		t.Fatalf("StreamChatCompletion returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv returned error: %v", err)
	}
	if got := chunk.Choices[0].Delta["content"]; got != "hello" {
		t.Fatalf("expected hello, got %v", got)
	}
}

func TestListModels(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if got := models.Data.Models[0].ModelID; got != "auto" {
		t.Fatalf("expected auto, got %s", got)
	}
}

func TestListModelsWithOptionsAddsLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("locale"); got != "zh" {
			t.Errorf("locale = %q, want zh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"models":[{"modelId":"auto","modelKey":"auto","displayName":"auto","alias":"auto","tier":"standard","tags":["lock"],"description":"localized","customerInputMtok":200,"customerOutputMtok":900,"customerCachereadMtok":20,"customerCachewriteMtok":250,"customerImageInputMtok":"","customerImageOutputMtok":"","customerImageCachedInputMtok":"","provider":"auto","featureTags":["agent"],"scenarioTags":[],"mciScore":7.57}]}}`))
	}))
	defer server.Close()

	client := NewClient(WithAPIBaseURL(server.URL))
	models, err := client.ListModelsWithOptions(context.Background(), ListModelsOptions{Locale: ModelLocaleZH})
	if err != nil {
		t.Fatalf("ListModelsWithOptions returned error: %v", err)
	}
	if got := models.Data.Models[0].ModelID; got != "auto" {
		t.Fatalf("expected auto, got %s", got)
	}
	if got := models.Data.Models[0].DisplayName; got != "auto" {
		t.Fatalf("expected displayName auto, got %s", got)
	}
	if got := models.Data.Models[0].CustomerCachereadMtok; got != 20 {
		t.Fatalf("expected customerCachereadMtok 20, got %v", got)
	}
}

func TestListModelsWithOptionsRejectsInvalidLocale(t *testing.T) {
	client := NewClient()
	_, err := client.ListModelsWithOptions(context.Background(), ListModelsOptions{Locale: ModelLocale("zh-CN")})
	if err == nil || !strings.Contains(err.Error(), "model locale") {
		t.Fatalf("error = %v, want invalid model locale", err)
	}
}

func TestModelCatalogAndPricing(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL))
	meta, err := client.GetModelMeta(context.Background())
	if err != nil {
		t.Fatalf("GetModelMeta returned error: %v", err)
	}
	if meta.Data.Providers[0].Value != "openai" {
		t.Fatalf("providers = %#v", meta.Data.Providers)
	}

	pricing, err := client.GetPricing(context.Background())
	if err != nil {
		t.Fatalf("GetPricing returned error: %v", err)
	}
	if pricing.Data.Tiers[0].CreditsPerUSD != "500" || pricing.Data.SKUs[0].Code != "lock" {
		t.Fatalf("pricing = %#v", pricing.Data)
	}
}

func TestListModelsDoesNotRequireAPIKey(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if got := models.Data.Models[0].ModelID; got != "auto" {
		t.Fatalf("expected auto, got %s", got)
	}
}

func TestWithTimeoutCancelsResponseBody(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithTimeout(20*time.Millisecond),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
	)
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: "auto"})
	if err == nil {
		t.Fatal("expected response body timeout")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("server did not receive request")
	}
}

func TestAuthenticatedRequestsRequireAPIKey(t *testing.T) {
	client := NewClient(WithAPIKey(" \t "))
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "chat completions",
			call: func() error {
				_, err := client.CreateChatCompletion(ctx, ChatCompletionRequest{Model: "auto"})
				return err
			},
		},
		{
			name: "images",
			call: func() error {
				_, err := client.GenerateImage(ctx, ImageGenerationRequest{Model: "auto", Prompt: "hello"})
				return err
			},
		},
		{
			name: "model metadata",
			call: func() error {
				_, err := client.GetModelMeta(ctx)
				return err
			},
		},
		{
			name: "pricing",
			call: func() error {
				_, err := client.GetPricing(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrMissingAPIKey) {
				t.Fatalf("expected ErrMissingAPIKey, got %v", err)
			}
		})
	}
}

func TestModelRequestsRequireAuto(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "chat completions", call: func() error {
			_, err := client.CreateChatCompletion(ctx, ChatCompletionRequest{Model: "unsupported-model"})
			return err
		}},
		{name: "chat completions stream", call: func() error {
			_, err := client.StreamChatCompletion(ctx, ChatCompletionRequest{Model: "unsupported-model"})
			return err
		}},
		{name: "images", call: func() error {
			_, err := client.GenerateImage(ctx, ImageGenerationRequest{Model: "unsupported-model", Prompt: "hello"})
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil || !strings.Contains(err.Error(), `model must be "auto"`) {
				t.Fatalf("expected auto-only model error, got %v", err)
			}
		})
	}
}

func TestSDKAuthenticationAndRequestHeadersTakePrecedence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openai/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-api-key") != "" {
				t.Errorf("unexpected OpenAI authentication headers: %#v", r.Header)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
		WithHeader("Authorization", "Bearer custom"),
		WithHeader("x-api-key", "custom-key"),
	)
	if _, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: "auto"}); err != nil {
		t.Fatalf("CreateChatCompletion returned error: %v", err)
	}
}

func newMockServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ModelListResponse{Object: "list", Data: ModelListData{Models: []ModelInfo{{ModelID: "auto", ModelKey: "auto", DisplayName: "auto", Alias: "auto"}}}})
			return
		}

		validAuth := r.Header.Get("Authorization") == "Bearer test-key"
		if !validAuth {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"missing api key"}}`))
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/meta":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"tiers":[{"value":"standard","label":"Standard"}],"skus":[],"featureTags":[],"industryPacks":[],"providers":[{"value":"openai","label":"OpenAI"}],"updatedAt":"2026-07-27T09:00:00Z"},"message":"success"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/pricing":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"tiers":[{"code":"standard","name":"Standard","description":"","usdPerCredit":"0.002","creditsPerUsd":"500","unit":"USD/Credit","rateVersion":"2026-07","sortOrder":2,"updatedAt":"2026-07-27T09:00:00Z"}],"skus":[{"code":"lock","name":"Lock","description":""}],"currentVersion":"2026-07","updatedAt":"2026-07-27T09:00:00Z"},"message":"success"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/chat/completions":
			var payload ChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if payload.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
				ID: "chatcmpl_test",
				Choices: []ChatCompletionChoice{
					{Index: 0, Message: ChatMessage{Role: "assistant", Content: "hello"}, FinishReason: "stop"},
				},
				Usage: &Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/images/generations":
			var payload ImageGenerationRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode image request: %v", err)
			}
			if payload.Model != ModelAuto || payload.Prompt != "A JoyToken logo on a black background" || payload.Size != "1024x1024" {
				t.Errorf("unexpected image request: %#v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ImageGenerationResponse{
				Created: 1793395200,
				Data: []GeneratedImage{{
					URL:           "https://example.com/generated.png",
					RevisedPrompt: payload.Prompt,
				}},
				Metadata: map[string]any{"usage": map[string]any{"credits_used": "1.25"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}
