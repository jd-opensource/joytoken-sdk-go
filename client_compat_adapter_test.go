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

type adapterHTTPClient struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	handle   func(index int, request *http.Request, body []byte) (string, string)
}

type statusHTTPClient struct {
	mu     sync.Mutex
	calls  int
	status int
	body   string
}

func (c *statusHTTPClient) Do(*http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &http.Response{
		StatusCode: c.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

func (c *adapterHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	index := len(c.requests)
	c.requests = append(c.requests, request.Clone(request.Context()))
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	contentType, responseBody := "application/json", `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	if c.handle != nil {
		contentType, responseBody = c.handle(index, request, body)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(responseBody))}, nil
}

func decodeChatRequest(t *testing.T, body []byte) ChatCompletionRequest {
	t.Helper()
	var request ChatCompletionRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode chat request: %v\n%s", err, body)
	}
	return request
}

func decodeResponseRequest(t *testing.T, body []byte) ResponseRequest {
	t.Helper()
	var request ResponseRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode Responses request: %v\n%s", err, body)
	}
	return request
}

func TestRequestToolsAreForwardedWithoutDefaultsOrExecution(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, _ *http.Request, _ []byte) (string, string) {
		return "application/json", `{"id":"one","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"1+2\"}"}}]},"finish_reason":"tool_calls"}]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    ModelAuto,
		Messages: []ChatMessage{{Role: "user", Content: "calculate"}},
		Tools:    []ChatTool{{Type: "function", Function: ChatToolFunction{Name: "calculator", Description: "user calculator"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("expected one passthrough request, got %d", len(transport.requests))
	}
	wire := decodeChatRequest(t, transport.bodies[0])
	if len(wire.Tools) != 1 || wire.Tools[0].Function.Description != "user calculator" {
		t.Fatalf("request tools were not forwarded exactly: %+v", wire.Tools)
	}
	if len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected user tool call to remain visible: %+v", response)
	}
}

func TestRequestToolsRemainExactOnContinuation(t *testing.T) {
	transport := &adapterHTTPClient{}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	userTool := ChatTool{Type: "function", Function: ChatToolFunction{Name: "calculator", Description: "user-owned continuation calculator"}}
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto,
		Messages: []ChatMessage{
			{Role: "user", Content: "calculate"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolFunction{Name: "calculator", Arguments: `{"expression":"1+2"}`}}}},
			{Role: "tool", ToolCallID: "call_1", Content: `{"result":3}`},
		},
		Tools: []ChatTool{userTool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.bodies) != 1 {
		t.Fatalf("expected one primitive continuation request, got %d", len(transport.bodies))
	}
	wire := decodeChatRequest(t, transport.bodies[0])
	if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != userTool.Function.Name || wire.Tools[0].Function.Description != userTool.Function.Description {
		t.Fatalf("continuation tools were not preserved exactly: %+v", wire.Tools)
	}
}

func TestDefaultClientDoesNotRetryModel503(t *testing.T) {
	transport := &statusHTTPClient{
		status: http.StatusServiceUnavailable,
		body:   `{"error":{"message":"provider invoke failed","type":"upstream_error"}}`,
	}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "hello"}}, Tools: []ChatTool{},
	})
	if err == nil {
		t.Fatal("expected 503 error")
	}
	transport.mu.Lock()
	calls := transport.calls
	transport.mu.Unlock()
	if calls != 1 {
		t.Fatalf("default client must issue exactly one non-idempotent model request, got %d", calls)
	}
}

func TestForcedToolChoiceRelaxesOnContinuation(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
			wire := decodeChatRequest(t, body)
			if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "echo" {
				t.Fatalf("turn %d lost tools: %+v", index, wire.Tools)
			}
			if index == 0 {
				choice, _ := wire.ToolChoice.(map[string]any)
				if choice["type"] != "function" {
					t.Fatalf("first turn lost forced choice: %#v", wire.ToolChoice)
				}
				return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`
			}
			if wire.ToolChoice != "auto" {
				t.Fatalf("continuation must relax forced choice to auto, got %#v", wire.ToolChoice)
			}
			return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
		}}
		echo := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))
		result, err := client.RunChatCompletion(context.Background(), ChatCompletionRequest{
			Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "echo"}},
			ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "echo"}},
		}, RunChatOptions{})
		if err != nil || result.FinalText != "done" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("responses", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
			wire := decodeResponseRequest(t, body)
			if len(wire.Tools) != 1 || wire.Tools[0].Name != "echo" {
				t.Fatalf("turn %d lost tools: %+v", index, wire.Tools)
			}
			if index == 0 {
				choice, _ := wire.ToolChoice.(map[string]any)
				if choice["type"] != "function" {
					t.Fatalf("first turn lost forced choice: %#v", wire.ToolChoice)
				}
				return "application/json", `{"id":"first","object":"response","status":"completed","model":"auto","output":[{"type":"function_call","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hi\"}"}]}`
			}
			if wire.ToolChoice != "auto" {
				t.Fatalf("continuation must relax forced choice to auto, got %#v", wire.ToolChoice)
			}
			return "application/json", `{"id":"final","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`
		}}
		echo := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))
		result, err := client.RunResponse(context.Background(), ResponseRequest{
			Model: ModelAuto, Input: "echo", ToolChoice: map[string]any{"type": "function", "name": "echo"},
		}, RunResponseOptions{})
		if err != nil || result.FinalText != "done" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
			wire := decodeChatRequest(t, body)
			if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "echo" {
				t.Fatalf("turn %d lost tools: %+v", index, wire.Tools)
			}
			if index == 0 {
				choice, _ := wire.ToolChoice.(map[string]any)
				if choice["type"] != "function" {
					t.Fatalf("first turn lost forced choice: %#v", wire.ToolChoice)
				}
				return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`
			}
			if wire.ToolChoice != "auto" {
				t.Fatalf("continuation must relax forced choice to auto, got %#v", wire.ToolChoice)
			}
			return "application/json", `{"id":"final","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
		}}
		echo := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))
		result, err := client.RunMessage(context.Background(), MessageRequest{
			Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo"}},
			ToolChoice: MessageToolChoice{Type: "tool", Name: "echo"},
		}, RunMessageOptions{})
		if err != nil || result.FinalText != "done" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestGatewayMetadataSurvivesCompatibilityAdapters(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(_ int, _ *http.Request, _ []byte) (string, string) {
			return "application/json", `{"id":"chat_1","model":"auto","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"metadata":{"request_id":"req_1","tag":["chat"]}}`
		}}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
		response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Metadata["request_id"] != "req_1" {
			t.Fatalf("chat metadata was lost: %#v", response.Metadata)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(_ int, _ *http.Request, _ []byte) (string, string) {
			return "application/json", `{"id":"chat_1","model":"auto","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"metadata":{"request_id":"req_2","tag":["chat"]}}`
		}}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
		response, err := client.CreateMessage(context.Background(), MessageRequest{
			Model: ModelAuto, MaxTokens: 32, Messages: []MessageParam{{Role: "user", Content: "hello"}}, Tools: []MessageTool{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.Metadata["request_id"] != "req_2" {
			t.Fatalf("anthropic adapter metadata was lost: %#v", response.Metadata)
		}
	})
}

func TestAPIBaseURLAlsoConfiguresSingleModelGateway(t *testing.T) {
	transport := &adapterHTTPClient{}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithAPIBaseURL("https://gateway.example.test/base/"))
	_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Tools: []ChatTool{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := transport.requests[0].URL.String(); got != "https://gateway.example.test/base/openai/v1/chat/completions" {
		t.Fatalf("unexpected model gateway URL: %s", got)
	}
}

func TestRegisteredToolsReplaceDefaults(t *testing.T) {
	transport := &adapterHTTPClient{}
	custom := Tool{Name: "weather", Description: "user weather", Execute: func(context.Context, any, ToolExecutionContext) (any, error) { return "sunny", nil }}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(custom))
	if _, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "weather"}}}); err != nil {
		t.Fatal(err)
	}
	wire := decodeChatRequest(t, transport.bodies[0])
	if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "weather" {
		t.Fatalf("registered user set should replace defaults: %+v", wire.Tools)
	}
}

func TestDefaultToolLoopDoesNotReplayFirstRequest(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, _ []byte) (string, string) {
		if index == 0 {
			return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"1+2\"}"}}]},"finish_reason":"tool_calls"}]}`
		}
		return "application/json", `{"id":"second","model":"auto","choices":[{"message":{"role":"assistant","content":"3"},"finish_reason":"stop"}]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	response, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "1+2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("expected exactly tool turn + final turn, got %d requests", len(transport.requests))
	}
	second := decodeChatRequest(t, transport.bodies[1])
	if len(second.Messages) < 3 || second.Messages[len(second.Messages)-1].Role != "tool" {
		t.Fatalf("second request must continue first response, got %+v", second.Messages)
	}
	if response.ID != "second" || messageText(response.Choices[0].Message.Content) != "3" {
		t.Fatalf("expected metadata and content from final response: %+v", response)
	}
}

func TestResponsesUsesNativeGatewayEndpoint(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/responses" {
			t.Fatalf("unexpected gateway path: %s", request.URL.Path)
		}
		var wire ResponseRequest
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatalf("decode Responses request: %v\n%s", err, body)
		}
		if wire.Input != "hello" || wire.Instructions != "be concise" {
			t.Fatalf("unexpected native Responses request: %+v", wire)
		}
		if len(wire.Tools) != 1 || wire.Tools[0].Name != "lookup" {
			t.Fatalf("unexpected native Responses tools: %+v", wire.Tools)
		}
		return "application/json", `{"id":"resp_1","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"world","annotations":[]}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	response, err := client.CreateResponse(context.Background(), ResponseRequest{
		Model: ModelAuto, Input: "hello", Instructions: "be concise",
		Tools: []ResponseTool{{Type: "function", Name: "lookup", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.OutputText() != "world" || response.Usage == nil || response.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected Responses result: %+v", response)
	}
}

func TestNativeResponsesPreservesExplicitEmptyTools(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/responses" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if !bytes.Contains(body, []byte(`"tools":[]`)) {
			t.Fatalf("explicit empty tools must remain on the wire: %s", body)
		}
		return "application/json", `{"id":"resp_empty","object":"response","status":"completed","model":"auto","output":[]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	if _, err := client.CreateResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "hello", Tools: []ResponseTool{}}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeResponsesKeepsToolsAndOptionsOnContinuation(t *testing.T) {
	parallel := true
	store := false
	registered := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
	transport := &adapterHTTPClient{handle: func(index int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/responses" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		wire := decodeResponseRequest(t, body)
		if len(wire.Tools) != 1 || wire.Tools[0].Name != "echo" {
			t.Fatalf("turn %d lost or duplicated tools: %+v", index, wire.Tools)
		}
		if wire.ToolChoice != "auto" || wire.ParallelToolCalls == nil || !*wire.ParallelToolCalls || wire.Store == nil || *wire.Store {
			t.Fatalf("turn %d lost Responses options: %+v", index, wire)
		}
		if index == 0 {
			return "application/json", `{"id":"first","object":"response","status":"completed","model":"auto","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hi\"}"}]}`
		}
		input, ok := wire.Input.([]any)
		if !ok || len(input) < 3 {
			t.Fatalf("continuation input missing call/output history: %#v", wire.Input)
		}
		last, _ := input[len(input)-1].(map[string]any)
		if last["type"] != "function_call_output" || last["call_id"] != "call_1" {
			t.Fatalf("unexpected continuation output: %#v", last)
		}
		return "application/json", `{"id":"final","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(registered))
	result, err := client.RunResponse(context.Background(), ResponseRequest{
		Model: ModelAuto, Input: "echo", ToolChoice: "auto", ParallelToolCalls: &parallel, Store: &store,
	}, RunResponseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 2 || result.FinalText != "done" || result.Steps[len(result.Steps)-1].Response.ID != "final" {
		t.Fatalf("unexpected native Responses run: requests=%d result=%+v", len(transport.requests), result)
	}
}

func TestAnthropicAdapterUsesOnlyChatGateway(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/chat/completions" {
			t.Fatalf("unexpected gateway path: %s", request.URL.Path)
		}
		wire := decodeChatRequest(t, body)
		if len(wire.Messages) != 2 || wire.Messages[0].Role != "system" || wire.Messages[1].Content != "hello" {
			t.Fatalf("unexpected Anthropic conversion: %+v", wire.Messages)
		}
		if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "lookup" {
			t.Fatalf("unexpected Anthropic tools conversion: %+v", wire.Tools)
		}
		return "application/json", `{"id":"chat_1","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"tool_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"42\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	response, err := client.CreateMessage(context.Background(), MessageRequest{
		Model: ModelAuto, MaxTokens: 128, System: "be concise",
		Messages: []MessageParam{{Role: "user", Content: "hello"}},
		Tools:    []MessageTool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 || response.Content[0].Type != "tool_use" || response.Content[0].Name != "lookup" {
		t.Fatalf("unexpected Anthropic result: %+v", response)
	}
	if response.StopReason == nil || *response.StopReason != "tool_use" {
		t.Fatalf("unexpected stop reason: %+v", response.StopReason)
	}
}

func TestResponseStreamUsesNativeResponsesSSE(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/responses" || !bytes.Contains(body, []byte(`"stream":true`)) {
			t.Fatalf("unexpected streaming request: %s %s", request.URL.Path, body)
		}
		return "text/event-stream", "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":\"auto\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"output_index\":0,\"content_index\":0,\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\",\"annotations\":[] }]}]}}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	responseStream, err := client.StreamResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "hello", Tools: []ResponseTool{}})
	if err != nil {
		t.Fatal(err)
	}
	defer responseStream.Close()
	var sawDelta, sawCompleted bool
	for {
		event, err := responseStream.Recv()
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sawDelta = sawDelta || event.Type == "response.output_text.delta" && event.Delta == "hi"
		sawCompleted = sawCompleted || event.Type == "response.completed" && event.Response.OutputText() == "hi"
	}
	if !sawDelta || !sawCompleted {
		t.Fatalf("missing Responses stream events: delta=%v completed=%v", sawDelta, sawCompleted)
	}
}

func TestRunResponseStreamUsesNativeToolContinuation(t *testing.T) {
	registered := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
	transport := &adapterHTTPClient{handle: func(index int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/responses" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		wire := decodeResponseRequest(t, body)
		if len(wire.Tools) != 1 || wire.Tools[0].Name != "echo" || !wire.Stream {
			t.Fatalf("turn %d has wrong native stream tools/request: %+v", index, wire)
		}
		if index == 0 {
			return "text/event-stream", "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"first\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":\"auto\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_1\",\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"first\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"id\":\"fc_1\",\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_1\",\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hi\\\"}\"}]}}\n\ndata: [DONE]\n\n"
		}
		input, ok := wire.Input.([]any)
		if !ok || len(input) < 3 {
			t.Fatalf("stream continuation input missing output: %#v", wire.Input)
		}
		return "text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":0,\"output_index\":0,\"content_index\":0,\"delta\":\"done\"}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"final\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"auto\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(registered))
	var deltas []string
	var toolResults []ToolCallResult
	result, err := client.RunResponseStream(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo"}, RunResponseStreamOptions{
		OnTextDelta: func(delta string) { deltas = append(deltas, delta) },
		OnToolResult: func(result ToolCallResult) {
			toolResults = append(toolResults, result)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 2 || result.FinalText != "done" || len(toolResults) != 1 || strings.Join(deltas, "") != "done" {
		t.Fatalf("unexpected native stream run: requests=%d deltas=%v tools=%+v result=%+v", len(transport.requests), deltas, toolResults, result)
	}
}

func TestRunResponseAndRunMessageExecuteUserHandlers(t *testing.T) {
	for _, protocol := range []string{"responses", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			transport := &adapterHTTPClient{handle: func(index int, request *http.Request, body []byte) (string, string) {
				if protocol == "responses" {
					if request.URL.Path != "/openai/v1/responses" {
						t.Fatalf("unexpected Responses path: %s", request.URL.Path)
					}
					if index == 0 {
						return "application/json", `{"id":"first","object":"response","status":"completed","model":"auto","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hi\"}"}]}`
					}
					var wire ResponseRequest
					if err := json.Unmarshal(body, &wire); err != nil {
						t.Fatal(err)
					}
					input, ok := wire.Input.([]any)
					if !ok || len(input) == 0 {
						t.Fatalf("tool result was not sent as native Responses input: %#v", wire.Input)
					}
					last, _ := input[len(input)-1].(map[string]any)
					if last["type"] != "function_call_output" || last["call_id"] != "call_1" {
						t.Fatalf("unexpected native tool output: %#v", last)
					}
					return "application/json", `{"id":"second","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`
				}
				if request.URL.Path != "/openai/v1/chat/completions" {
					t.Fatalf("unexpected Messages path: %s", request.URL.Path)
				}
				if index == 0 {
					return "application/json", `{"id":"first","model":"auto","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`
				}
				wire := decodeChatRequest(t, body)
				if len(wire.Messages) == 0 || wire.Messages[len(wire.Messages)-1].Role != "tool" {
					t.Fatalf("tool result was not converted back to chat transcript: %+v", wire.Messages)
				}
				return "application/json", `{"id":"second","model":"auto","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
			}}
			echo := Tool{Name: "echo", Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) { return input, nil }}
			client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport), WithTools(echo))
			switch protocol {
			case "responses":
				result, err := client.RunResponse(context.Background(), ResponseRequest{Model: ModelAuto, Input: "echo"}, RunResponseOptions{})
				if err != nil || result.FinalText != "done" {
					t.Fatalf("RunResponse result=%+v err=%v", result, err)
				}
			case "anthropic":
				result, err := client.RunMessage(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 128, Messages: []MessageParam{{Role: "user", Content: "echo"}}}, RunMessageOptions{})
				if err != nil || result.FinalText != "done" {
					t.Fatalf("RunMessage result=%+v err=%v", result, err)
				}
			}
			if len(transport.requests) != 2 {
				t.Fatalf("expected two model turns, got %d", len(transport.requests))
			}
		})
	}
}

func TestAnthropicStreamAdapterEmitsTextDelta(t *testing.T) {
	transport := &adapterHTTPClient{handle: func(_ int, _ *http.Request, _ []byte) (string, string) {
		return "text/event-stream", "data: {\"id\":\"chat_1\",\"model\":\"auto\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	stream, err := client.StreamMessage(context.Background(), MessageRequest{Model: ModelAuto, MaxTokens: 64, Messages: []MessageParam{{Role: "user", Content: "hello"}}, Tools: []MessageTool{}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var sawDelta, sawStop bool
	for {
		event, err := stream.Recv()
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type == "content_block_delta" && event.Delta["text"] == "hi" {
			sawDelta = true
		}
		if event.Type == "message_stop" {
			sawStop = true
		}
	}
	if !sawDelta || !sawStop {
		t.Fatalf("missing Anthropic stream events: delta=%v stop=%v", sawDelta, sawStop)
	}
}

func errorsIsEOF(err error) bool { return err == io.EOF }
