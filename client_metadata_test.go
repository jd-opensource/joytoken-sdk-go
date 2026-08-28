package joytoken

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatUsageAndRequestIDFallbacks(t *testing.T) {
	t.Run("non-streaming chat uses billing and header fallbacks", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-ID", "header-chat-id")
			_, _ = w.Write([]byte(`{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"metadata":{"billing":{"input_tokens":7,"output_tokens":3}}}`))
		}))
		defer server.Close()

		client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
		response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Usage == nil || response.Usage.PromptTokens != 7 || response.Usage.CompletionTokens != 3 || response.Usage.TotalTokens != 10 {
			t.Fatalf("usage fallback failed: %#v", response.Usage)
		}
		if response.RequestID() != "header-chat-id" || RequestIDFromMetadata(response.Metadata) != "header-chat-id" {
			t.Fatalf("request ID fallback failed: metadata=%#v", response.Metadata)
		}
	})

	t.Run("explicit protocol usage remains authoritative", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"metadata":{"billing":{"input_tokens":90,"output_tokens":80}}}`))
		}))
		defer server.Close()

		client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
		response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Usage == nil || response.Usage.PromptTokens != 1 || response.Usage.CompletionTokens != 2 || response.Usage.TotalTokens != 3 {
			t.Fatalf("protocol usage was overwritten: %#v", response.Usage)
		}
	})
}

func TestMetadataOnlyChatStreamChunkIsSafeAndCarriesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "header-stream-id")
		_, _ = w.Write([]byte("data: {\"metadata\":{\"request_id\":\"body-stream-id\",\"billing\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	metadataChunk, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if metadataChunk.Choices == nil || len(metadataChunk.Choices) != 0 {
		t.Fatalf("metadata-only choices must be a non-nil empty slice: %#v", metadataChunk.Choices)
	}
	if metadataChunk.Usage == nil || metadataChunk.Usage.TotalTokens != 6 {
		t.Fatalf("metadata usage fallback failed: %#v", metadataChunk.Usage)
	}
	if metadataChunk.RequestID() != "body-stream-id" {
		t.Fatalf("body request ID must beat the header fallback: %#v", metadataChunk.Metadata)
	}

	textChunk, err := stream.Recv()
	if err != nil || len(textChunk.Choices) != 1 || textChunk.Choices[0].Delta["content"] != "ok" {
		t.Fatalf("text chunk=%#v err=%v", textChunk, err)
	}
	if textChunk.RequestID() != "body-stream-id" {
		t.Fatalf("learned stream request ID was not propagated: %#v", textChunk.Metadata)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Recv error=%v want EOF", err)
	}
}

func TestUsageFallbackSurvivesResponsesAndMessagesAdapters(t *testing.T) {
	t.Run("Responses", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"auto","output":[],"metadata":{"request_id":"resp-id","billing":{"input_tokens":"11","output_tokens":"5"}}}`))
		}))
		defer server.Close()

		client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
		response, err := client.CreateResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "hello", Tools: []ResponseTool{}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Usage == nil || response.Usage.InputTokens != 11 || response.Usage.OutputTokens != 5 || response.Usage.TotalTokens != 16 {
			t.Fatalf("Responses usage fallback failed: %#v", response.Usage)
		}
		if response.RequestID() != "resp-id" {
			t.Fatalf("Responses request ID=%q", response.RequestID())
		}
	})

	t.Run("Messages", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chat_1","model":"auto","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"metadata":{"request_id":"message-id","billing":{"input_tokens":13,"output_tokens":6}}}`))
		}))
		defer server.Close()

		client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
		response, err := client.CreateMessage(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 32, Messages: []MessageParam{{Role: "user", Content: "hello"}}, Tools: []MessageTool{}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Usage.InputTokens != 13 || response.Usage.OutputTokens != 6 {
			t.Fatalf("Messages usage fallback failed: %#v", response.Usage)
		}
		if response.RequestID() != "message-id" {
			t.Fatalf("Messages request ID=%q", response.RequestID())
		}
	})
}

func TestMessageStreamUsesMetadataOnlyUsageChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"metadata\":{\"request_id\":\"message-stream-id\",\"billing\":{\"input_tokens\":8,\"output_tokens\":3}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
	stream, err := client.StreamMessage(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 32, Messages: []MessageParam{{Role: "user", Content: "hello"}}, Tools: []MessageTool{}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var delta *MessageStreamEvent
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if event.Type == "message_delta" {
			delta = event
			break
		}
	}
	if delta.Usage == nil || delta.Usage.InputTokens != 8 || delta.Usage.OutputTokens != 3 {
		t.Fatalf("Messages stream usage fallback failed: %#v", delta.Usage)
	}
	if delta.RequestID() != "message-stream-id" {
		t.Fatalf("Messages stream request ID=%q metadata=%#v", delta.RequestID(), delta.Metadata)
	}
}

func TestAPIErrorRequestIDFallsBackToBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary","request_id":"body-error-id"}}`))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithOpenAIBaseURL(server.URL))
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RequestID != "body-error-id" {
		t.Fatalf("API error=%#v request ID fallback failed", err)
	}
}
