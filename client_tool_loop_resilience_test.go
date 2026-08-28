package joytoken

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type failSecondTransport struct {
	mu               sync.Mutex
	firstContentType string
	firstBody        string
	bodies           [][]byte
}

func (t *failSecondTransport) Do(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	index := len(t.bodies)
	t.bodies = append(t.bodies, append([]byte(nil), body...))
	t.mu.Unlock()
	if index == 0 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{t.firstContentType}},
			Body:       io.NopCloser(strings.NewReader(t.firstBody)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"provider invoke failed","type":"upstream_error"}}`)),
	}, nil
}

func echoHandler() Tool {
	return Tool{Name: "echo", Description: "Echo input.", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
		return input, nil
	}}
}

func TestRunLoopsReturnPartialResultOnContinuationError(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "application/json", firstBody: `{"id":"first","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo"}}}, RunChatOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || result.FinishReason != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Messages) != 3 {
			t.Fatalf("expected one preserved Chat tool step, result=%+v err=%v", result, err)
		}
	})

	t.Run("responses", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "application/json", firstBody: `{"id":"first","object":"response","status":"completed","model":"auto","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hi\"}"}]}`}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo"}, RunResponseOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Input) != 3 {
			t.Fatalf("expected one preserved Responses tool step, result=%+v err=%v", result, err)
		}
	})

	t.Run("messages", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "application/json", firstBody: `{"id":"first","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunMessage(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo"}}}, RunMessageOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Messages) != 3 {
			t.Fatalf("expected one preserved Messages tool step, result=%+v err=%v", result, err)
		}
	})

	t.Run("chat_stream", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "text/event-stream", firstBody: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunChatCompletionStream(context.Background(), ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo"}}}, RunChatStreamOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || result.FinishReason != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Messages) != 3 {
			t.Fatalf("expected one preserved streaming Chat tool step, result=%+v err=%v", result, err)
		}
	})

	t.Run("responses_stream", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "text/event-stream", firstBody: "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"first\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"id\":\"fc_1\",\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_1\",\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}]}}\n\ndata: [DONE]\n\n"}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunResponseStream(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo"}, RunResponseStreamOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Input) != 3 {
			t.Fatalf("expected one preserved streaming Responses tool step, result=%+v err=%v", result, err)
		}
	})

	t.Run("messages_stream", func(t *testing.T) {
		transport := &failSecondTransport{firstContentType: "text/event-stream", firstBody: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
		result, err := client.RunMessageStream(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo"}}}, RunMessageStreamOptions{})
		if err == nil || result == nil || result.StoppedBy != "error" || len(result.Steps) != 1 || len(result.Steps[0].ToolResults) != 1 || len(result.Messages) != 3 {
			t.Fatalf("expected one preserved streaming Messages tool step, result=%+v err=%v", result, err)
		}
	})
}

func TestRunMessageStreamExecutesToolsAndRetainsDeclarations(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(index int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/chat/completions" {
			t.Fatalf("unexpected Messages stream path: %s", request.URL.Path)
		}
		var wire ChatCompletionRequest
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if !wire.Stream || len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "echo" {
			t.Fatalf("turn %d did not retain the user tool: %+v", index, wire)
		}
		if index == 0 {
			return "text/event-stream", "data: {\"id\":\"first\",\"model\":\"auto\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
		}
		if len(wire.Messages) == 0 || wire.Messages[len(wire.Messages)-1].Role != "tool" {
			t.Fatalf("Messages stream continuation omitted tool result: %+v", wire.Messages)
		}
		return "text/event-stream", "data: {\"id\":\"final\",\"model\":\"auto\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echoHandler()))
	var text strings.Builder
	var toolResults int
	result, err := client.RunMessageStream(context.Background(), MessageRequest{
		Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo"}},
	}, RunMessageStreamOptions{
		OnTextDelta: func(delta string) { text.WriteString(delta) },
		OnToolResult: func(ToolCallResult) {
			toolResults++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 2 || len(result.Steps) != 2 || result.FinalText != "done" || result.StoppedBy != "stop" {
		t.Fatalf("unexpected Messages streaming result: requests=%d result=%+v", len(transport.requests), result)
	}
	if result.Steps[0].Response.ID != "first" || result.Steps[1].Response.ID != "final" {
		t.Fatalf("streamed Messages IDs were not preserved: %+v", result.Steps)
	}
	if toolResults != 1 || text.String() != "done" {
		t.Fatalf("unexpected callbacks: tools=%d text=%q", toolResults, text.String())
	}
}
