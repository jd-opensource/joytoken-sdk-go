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
		Model: "joy/mock",
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
		Model: "joy/mock",
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

func TestCreateResponse(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	maxTokens := 128
	response, err := client.CreateResponse(context.Background(), ResponseRequest{
		Model:           "joy/mock",
		Input:           "hello",
		Instructions:    "Be concise",
		MaxOutputTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("CreateResponse returned error: %v", err)
	}
	if got := response.OutputText(); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
	if response.Usage == nil || response.Usage.InputTokens != 1 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
	}
}

func TestStreamResponse(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	stream, err := client.StreamResponse(context.Background(), ResponseRequest{Model: "joy/mock", Input: "hello"})
	if err != nil {
		t.Fatalf("StreamResponse returned error: %v", err)
	}
	defer stream.Close()

	events := make([]*ResponseStreamEvent, 0)
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv returned error: %v", recvErr)
		}
		events = append(events, event)
	}
	if len(events) != 4 || events[0].Type != "response.created" || events[1].Type != "response.output_text.delta" || events[3].Type != "response.completed" {
		t.Fatalf("unexpected Responses events: %#v", events)
	}
	if events[1].Delta != "hello" || events[3].Response == nil {
		t.Fatalf("unexpected event payloads: %#v", events)
	}
}

func TestGenerateImage(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	response, err := client.GenerateImage(context.Background(), ImageGenerationRequest{
		Model:  "joy/image-mock",
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
		Model: "joy/mock",
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
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{Model: "joy/mock"})
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

func TestCreateMessage(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithAnthropicBaseURL(server.URL+"/anthropic/v1"))
	temperature := 0.7
	response, err := client.CreateMessage(context.Background(), MessageRequest{
		Model:       "joy/mock",
		MaxTokens:   128,
		Temperature: &temperature,
		Tier:        "standard",
		Messages: []MessageParam{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if got := response.Content[0].Text; got != "hello" {
		t.Fatalf("expected hello, got %v", got)
	}
}

func TestStreamMessage(t *testing.T) {
	server := newMockServer(t)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithAnthropicBaseURL(server.URL+"/anthropic/v1"))
	temperature := 0.7
	stream, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:       "joy/mock",
		MaxTokens:   128,
		Temperature: &temperature,
		Tier:        "standard",
		Messages: []MessageParam{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("StreamMessage returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv returned error: %v", err)
	}
	if event.Type != "content_block_delta" || event.Delta["text"] != "hello" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
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
	if got := models.Data[0].ID; got != "joy/mock" {
		t.Fatalf("expected joy/mock, got %s", got)
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
	if got := models.Data[0].ID; got != "joy/mock" {
		t.Fatalf("expected joy/mock, got %s", got)
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
			name: "responses stream",
			call: func() error {
				_, err := client.StreamResponse(ctx, ResponseRequest{Model: "auto", Input: "hello"})
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
			name: "messages",
			call: func() error {
				_, err := client.CreateMessage(ctx, MessageRequest{Model: "auto", MaxTokens: 16})
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

func TestSDKAuthenticationAndRequestHeadersTakePrecedence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openai/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-api-key") != "" {
				t.Errorf("unexpected OpenAI authentication headers: %#v", r.Header)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
		case "/anthropic/v1/messages":
			if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
				t.Errorf("unexpected Anthropic headers: %#v", r.Header)
			}
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"auto","usage":{"input_tokens":1,"output_tokens":1}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL+"/openai/v1"),
		WithAnthropicBaseURL(server.URL+"/anthropic/v1"),
		WithHeader("Authorization", "Bearer custom"),
		WithHeader("x-api-key", "custom-key"),
		WithHeader("anthropic-version", "custom-version"),
	)
	if _, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: "auto"}); err != nil {
		t.Fatalf("CreateChatCompletion returned error: %v", err)
	}
	if _, err := client.CreateMessage(context.Background(), MessageRequest{Model: "auto", MaxTokens: 16}); err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
}

func newMockServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ModelListResponse{Object: "list", Data: []ModelInfo{{ID: "joy/mock"}}})
			return
		}

		isAnthropic := r.URL.Path == "/anthropic/v1/messages"
		validAuth := r.Header.Get("Authorization") == "Bearer test-key"
		if isAnthropic {
			validAuth = r.Header.Get("x-api-key") == "test-key" &&
				r.Header.Get("anthropic-version") == "2023-06-01" &&
				r.Header.Get("Authorization") == ""
		}
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
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/responses":
			var payload ResponseRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode Responses request: %v", err)
			}
			if payload.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":\"joy/mock\"}}\n\n"))
				_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"hello\",\"item_id\":\"resp_test-msg\"}\n\n"))
				_, _ = w.Write([]byte("event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"sequence_number\":2,\"text\":\"hello\"}\n\n"))
				_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"joy/mock\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}]}}\n\n"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{
				ID: "resp_test", Object: "response", Status: "completed", Model: "joy/mock",
				Output: []ResponseOutputItem{{Type: "message", Role: "assistant", Status: "completed", Content: []ResponseOutputContent{{Type: "output_text", Text: "hello"}}}},
				Usage:  &ResponseUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/images/generations":
			var payload ImageGenerationRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode image request: %v", err)
			}
			if payload.Model != "joy/image-mock" || payload.Prompt != "A JoyToken logo on a black background" || payload.Size != "1024x1024" {
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
		case r.Method == http.MethodPost && r.URL.Path == "/anthropic/v1/messages":
			var payload MessageRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if payload.Temperature == nil || *payload.Temperature != 0.7 || payload.Tier != "standard" {
				t.Errorf("unexpected Anthropic request options: temperature=%v tier=%q", payload.Temperature, payload.Tier)
			}
			if payload.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"))
				return
			}
			stopReason := "end_turn"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(MessageResponse{
				ID:         "msg_test",
				Type:       "message",
				Role:       "assistant",
				Content:    []MessageContentBlock{{Type: "text", Text: "hello"}},
				Model:      "joy/mock",
				StopReason: &stopReason,
				Usage:      MessageUsage{InputTokens: 1, OutputTokens: 1},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}
