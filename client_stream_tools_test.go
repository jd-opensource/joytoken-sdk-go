package joytoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunChatCompletionStreamExecutesTools verifies that RunChatCompletionStream
// consumes each Chat Completions turn as an SSE stream, reassembles a streamed
// tool_call (whose id/name/arguments arrive in fragments), executes it locally,
// feeds the result back, and continues until a turn produces no tool_calls —
// mirroring the non-streaming RunChatCompletion loop.
func TestRunChatCompletionStreamExecutesTools(t *testing.T) {
	var turns int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		turns++
		w.Header().Set("Content-Type", "text/event-stream")
		if turns == 1 {
			// First turn: stream a text delta, then a tool_call split across
			// several chunks so the accumulator has to stitch it together.
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"working\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\"}}]},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"text\\\":\"}}]},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		// Second turn: no tool_calls, just a final text answer.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
		WithOpenAIBaseURL(server.URL),
		WithDefaultLocalTools(false),
		WithTools(echo),
	)

	var text strings.Builder
	var toolResults int
	result, err := client.RunChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "call echo"},
		},
	}, RunChatStreamOptions{
		OnTextDelta:  func(delta string) { text.WriteString(delta) },
		OnToolResult: func(_ ToolCallResult) { toolResults++ },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if turns != 2 {
		t.Fatalf("expected 2 model turns (tool call + final), got %d", turns)
	}
	if toolResults != 1 {
		t.Fatalf("expected 1 tool result, got %d", toolResults)
	}
	if result.StoppedBy != "stop" {
		t.Fatalf("expected loop to stop naturally, got %q", result.StoppedBy)
	}
	if result.FinalText != "done" {
		t.Fatalf("expected final text %q, got %q", "done", result.FinalText)
	}
	if got := text.String(); got != "workingdone" {
		t.Fatalf("expected streamed text %q, got %q", "workingdone", got)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 steps recorded, got %d", len(result.Steps))
	}
	// The reassembled tool call arguments should be fully stitched together.
	if len(result.Steps[0].AssistantMessage.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in first step, got %d", len(result.Steps[0].AssistantMessage.ToolCalls))
	}
	if got := result.Steps[0].AssistantMessage.ToolCalls[0].Function.Arguments; got != `{"text":"hi"}` {
		t.Fatalf("expected stitched arguments %q, got %q", `{"text":"hi"}`, got)
	}
}

// TestRunChatCompletionStreamNoToolsMatchesPrimitive pins down the "scheme one"
// guarantee: when a streamed turn produces no tool_calls, RunChatCompletionStream
// behaves exactly like the raw StreamChatCompletion primitive — it makes a single
// request, surfaces text token-by-token through OnTextDelta, ends the moment the
// stream reaches EOF, and never opens a second turn. This proves that callers who
// do not need tools see an identical streaming experience whether they use the
// primitive or this auto-executing entry point.
func TestRunChatCompletionStreamNoToolsMatchesPrimitive(t *testing.T) {
	var turns int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		turns++
		w.Header().Set("Content-Type", "text/event-stream")
		// A single turn with only text deltas and no tool_calls: the loop must
		// stop right after this stream's EOF, identical to the primitive.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(
		WithAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL),
		WithDefaultLocalTools(false),
	)

	var text strings.Builder
	var toolResults int
	result, err := client.RunChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "say hello"},
		},
	}, RunChatStreamOptions{
		OnTextDelta:  func(delta string) { text.WriteString(delta) },
		OnToolResult: func(_ ToolCallResult) { toolResults++ },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exactly one request: no tool_call means no second turn — same network
	// footprint as StreamChatCompletion.
	if turns != 1 {
		t.Fatalf("expected exactly 1 model turn with no tools, got %d", turns)
	}
	if toolResults != 0 {
		t.Fatalf("expected no tool results, got %d", toolResults)
	}
	// Text is streamed token-by-token, identical to the primitive.
	if got := text.String(); got != "hello" {
		t.Fatalf("expected streamed text %q, got %q", "hello", got)
	}
	// Loop ends naturally at EOF, not by hitting MaxSteps.
	if result.StoppedBy != "stop" {
		t.Fatalf("expected loop to stop naturally, got %q", result.StoppedBy)
	}
	if result.FinalText != "hello" {
		t.Fatalf("expected final text %q, got %q", "hello", result.FinalText)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step recorded, got %d", len(result.Steps))
	}
	if len(result.Steps[0].AssistantMessage.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls in the only step, got %d", len(result.Steps[0].AssistantMessage.ToolCalls))
	}
}