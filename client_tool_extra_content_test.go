package joytoken

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

const testThoughtSignature = "opaque-gemini-thought-signature"

func toolCallWithThoughtSignatureJSON() string {
	return `{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hello\"}"},"extra_content":{"google":{"thought_signature":"` + testThoughtSignature + `"}}}`
}

func assertThoughtSignature(t *testing.T, extra map[string]any) {
	t.Helper()
	google, ok := extra["google"].(map[string]any)
	if !ok {
		t.Fatalf("missing google extra_content: %#v", extra)
	}
	if got := google["thought_signature"]; got != testThoughtSignature {
		t.Fatalf("thought_signature=%#v, want %q", got, testThoughtSignature)
	}
}

func thoughtSignatureEchoTool(t *testing.T, executed *map[string]any) Tool {
	t.Helper()
	return Tool{
		Name: "echo",
		Execute: func(_ context.Context, input any, execution ToolExecutionContext) (any, error) {
			*executed = execution.ToolCall.ExtraContent
			return input, nil
		},
	}
}

func assertChatContinuationSignature(t *testing.T, body []byte) {
	t.Helper()
	wire := decodeChatRequest(t, body)
	for _, message := range wire.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			assertThoughtSignature(t, message.ToolCalls[0].ExtraContent)
			return
		}
	}
	t.Fatalf("continuation omitted assistant tool_call: %s", body)
}

// toolCallWithTopLevelSignatureJSON returns a tool_call carrying the provider
// signature at the TOP LEVEL (thought_signature) rather than nested under
// extra_content. Gemini via the gateway Chat Completions endpoint uses this shape.
func toolCallWithTopLevelSignatureJSON() string {
	return `{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hello\"}"},"thought_signature":"` + testThoughtSignature + `"}`
}

// assertChatContinuationTopLevelSignature verifies the continuation request
// echoes back the top-level thought_signature verbatim on the assistant tool_call.
func assertChatContinuationTopLevelSignature(t *testing.T, body []byte) {
	t.Helper()
	wire := decodeChatRequest(t, body)
	for _, message := range wire.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			if got := message.ToolCalls[0].ThoughtSignature; got != testThoughtSignature {
				t.Fatalf("continuation top-level thought_signature=%q, want %q", got, testThoughtSignature)
			}
			return
		}
	}
	t.Fatalf("continuation omitted assistant tool_call: %s", body)
}

func TestRunChatCompletionPreservesToolCallExtraContent(t *testing.T) {
	var executed map[string]any
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		if index == 0 {
			return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[` + toolCallWithThoughtSignatureJSON() + `]},"finish_reason":"tool_calls"}]}`
		}
		assertChatContinuationSignature(t, body)
		return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(thoughtSignatureEchoTool(t, &executed)))

	result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo hello"}},
	}, RunChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" || len(transport.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
	}
	assertThoughtSignature(t, executed)
	assertThoughtSignature(t, result.Steps[0].AssistantMessage.ToolCalls[0].ExtraContent)
}

func TestRunChatCompletionPreservesTopLevelThoughtSignature(t *testing.T) {
	var executed string
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		if index == 0 {
			return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[` + toolCallWithTopLevelSignatureJSON() + `]},"finish_reason":"tool_calls"}]}`
		}
		assertChatContinuationTopLevelSignature(t, body)
		return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	}}
	echo := Tool{
		Name: "echo",
		Execute: func(_ context.Context, input any, execution ToolExecutionContext) (any, error) {
			executed = execution.ToolCall.ThoughtSignature
			return input, nil
		},
	}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))

	result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo hello"}},
	}, RunChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" || len(transport.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
	}
	if executed != testThoughtSignature {
		t.Fatalf("execution top-level thought_signature=%q, want %q", executed, testThoughtSignature)
	}
	if got := result.Steps[0].AssistantMessage.ToolCalls[0].ThoughtSignature; got != testThoughtSignature {
		t.Fatalf("step top-level thought_signature=%q, want %q", got, testThoughtSignature)
	}
}

func TestRunChatCompletionStreamPreservesToolCallExtraContent(t *testing.T) {
	var executed map[string]any
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		if index == 0 {
			return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\"},\"extra_content\":{\"google\":{\"thought_signature\":\"" + testThoughtSignature + "\"}}}]},\"finish_reason\":null}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"text\\\":\\\"hello\\\"}\"},\"extra_content\":{\"google\":{\"provider_marker\":\"kept\"}}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n"
		}
		assertChatContinuationSignature(t, body)
		wire := decodeChatRequest(t, body)
		for _, message := range wire.Messages {
			if message.Role == "assistant" && len(message.ToolCalls) > 0 {
				google := message.ToolCalls[0].ExtraContent["google"].(map[string]any)
				if google["provider_marker"] != "kept" {
					t.Fatalf("streamed nested extra_content was not merged: %#v", google)
				}
			}
		}
		return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(thoughtSignatureEchoTool(t, &executed)))

	result, err := client.RunChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo hello"}},
	}, RunChatStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" || len(transport.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
	}
	assertThoughtSignature(t, executed)
}

func TestRunMessagePreservesToolCallExtraContent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "non-stream"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			var executed map[string]any
			transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
				if index == 0 {
					if streaming {
						return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hello\\\"}\"},\"extra_content\":{\"google\":{\"thought_signature\":\"" + testThoughtSignature + "\"}}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
					}
					return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[` + toolCallWithThoughtSignatureJSON() + `]},"finish_reason":"tool_calls"}]}`
				}
				assertChatContinuationSignature(t, body)
				if streaming {
					return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				}
				return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
			}}
			client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(thoughtSignatureEchoTool(t, &executed)))
			request := MessageRequest{Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo hello"}}}
			var result *RunMessageResult
			var err error
			if streaming {
				result, err = client.RunMessageStream(context.Background(), request, RunMessageStreamOptions{})
			} else {
				result, err = client.RunMessage(context.Background(), request, RunMessageOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.FinalText != "done" || len(transport.requests) != 2 {
				t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
			}
			assertThoughtSignature(t, executed)
			blocks := toolUseBlocks(result.Steps[0].Response)
			if len(blocks) != 1 {
				t.Fatalf("tool_use blocks=%d", len(blocks))
			}
			assertThoughtSignature(t, blocks[0].ExtraContent)
		})
	}
}

func TestRunMessagePreservesTopLevelThoughtSignature(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "non-stream"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			var executed string
			transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
				if index == 0 {
					if streaming {
						return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hello\\\"}\"},\"thought_signature\":\"" + testThoughtSignature + "\"}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
					}
					return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[` + toolCallWithTopLevelSignatureJSON() + `]},"finish_reason":"tool_calls"}]}`
				}
				assertChatContinuationTopLevelSignature(t, body)
				if streaming {
					return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				}
				return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
			}}
			echo := Tool{Name: "echo", Execute: func(_ context.Context, input any, execution ToolExecutionContext) (any, error) {
				executed = execution.ToolCall.ThoughtSignature
				return input, nil
			}}
			client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))
			request := MessageRequest{Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo hello"}}}
			var result *RunMessageResult
			var err error
			if streaming {
				result, err = client.RunMessageStream(context.Background(), request, RunMessageStreamOptions{})
			} else {
				result, err = client.RunMessage(context.Background(), request, RunMessageOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.FinalText != "done" || len(transport.requests) != 2 {
				t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
			}
			if executed != testThoughtSignature {
				t.Fatalf("execution top-level thought_signature=%q, want %q", executed, testThoughtSignature)
			}
			blocks := toolUseBlocks(result.Steps[0].Response)
			if len(blocks) != 1 {
				t.Fatalf("tool_use blocks=%d", len(blocks))
			}
			if got := blocks[0].ThoughtSignature; got != testThoughtSignature {
				t.Fatalf("tool_use block top-level thought_signature=%q, want %q", got, testThoughtSignature)
			}
		})
	}
}

func TestRunResponsePreservesFunctionCallExtraContent(t *testing.T) {
	var executed map[string]any
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		if index == 0 {
			return "application/json", `{"id":"first","object":"response","status":"completed","model":"auto","output":[{"type":"function_call","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hello\"}","extra_content":{"google":{"thought_signature":"` + testThoughtSignature + `"}}}]}`
		}
		var wire map[string]any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		input, ok := wire["input"].([]any)
		if !ok {
			t.Fatalf("Responses input=%#v", wire["input"])
		}
		var found bool
		for _, raw := range input {
			item, _ := raw.(map[string]any)
			if item["type"] == "function_call" {
				extra, _ := item["extra_content"].(map[string]any)
				assertThoughtSignature(t, extra)
				found = true
			}
		}
		if !found {
			t.Fatalf("continuation omitted function_call input: %s", body)
		}
		return "application/json", `{"id":"final","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(thoughtSignatureEchoTool(t, &executed)))

	result, err := client.RunResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo hello"}, RunResponseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" || len(transport.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
	}
	assertThoughtSignature(t, executed)
}

func TestRunResponseStreamPreservesFunctionCallExtraContent(t *testing.T) {
	var executed map[string]any
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		if index == 0 {
			return "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"first\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hello\\\"}\",\"extra_content\":{\"google\":{\"thought_signature\":\"" + testThoughtSignature + "\"}}}]}}\n\ndata: [DONE]\n\n"
		}
		var wire map[string]any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		input, ok := wire["input"].([]any)
		if !ok {
			t.Fatalf("Responses input=%#v", wire["input"])
		}
		var found bool
		for _, raw := range input {
			item, _ := raw.(map[string]any)
			if item["type"] == "function_call" {
				extra, _ := item["extra_content"].(map[string]any)
				assertThoughtSignature(t, extra)
				found = true
			}
		}
		if !found {
			t.Fatalf("continuation omitted function_call input: %s", body)
		}
		return "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"final\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(thoughtSignatureEchoTool(t, &executed)))

	result, err := client.RunResponseStream(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo hello"}, RunResponseStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" || len(transport.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(transport.requests))
	}
	assertThoughtSignature(t, executed)
}
