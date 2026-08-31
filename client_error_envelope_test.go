package joytoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errorEnvelopeServer answers HTTP 200 with a body-level error envelope, the
// way the Gateway signals a failed run (for example a failed orchestration)
// without a non-2xx status. streaming toggles the SSE vs JSON shape.
func errorEnvelopeServer(t *testing.T, streaming bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":{"models":[{"model_id":"auto","model_key":"auto","display_name":"auto","alias":"auto"}]}}`))
			return
		}
		w.Header().Set("X-Request-Id", "req-envelope-1")
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			// A normal delta first, then a body-level error envelope event.
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"orchestration failed\",\"type\":\"server_error\"},\"choices\":[]}\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"orchestration failed","type":"server_error"},"choices":[]}`))
	}))
}

func TestCreateChatCompletionSurfacesBodyErrorEnvelope(t *testing.T) {
	server := errorEnvelopeServer(t, false)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    ModelAuto,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("expected an error for a 200 error envelope, got nil")
	}
	if !IsAPIError(err) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "orchestration failed") {
		t.Fatalf("expected error to carry the gateway message, got %v", err)
	}
}

func TestStreamChatCompletionSurfacesBodyErrorEnvelope(t *testing.T) {
	server := errorEnvelopeServer(t, true)
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithAPIBaseURL(server.URL), WithOpenAIBaseURL(server.URL+"/openai/v1"))
	stream, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    ModelAuto,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion returned error: %v", err)
	}
	defer stream.Close()

	// First event is a normal delta.
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv returned error: %v", err)
	}
	// Second event is a body-level error envelope and must surface as *APIError.
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("expected an error for a streamed error envelope, got nil")
	}
	if !IsAPIError(err) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "orchestration failed") {
		t.Fatalf("expected error to carry the gateway message, got %v", err)
	}
}