package joytoken

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type defaultToolMatrixCase struct {
	name       string
	arguments  string
	wantResult string
}

var defaultToolMatrixCases = []defaultToolMatrixCase{
	{name: "calculator", arguments: `{"expression":"(17+25)*2"}`, wantResult: `"result":84`},
	{name: "datetime", arguments: `{"timezone":"UTC","format":"2006"}`, wantResult: `"timezone":"UTC"`},
	{name: "file_search", arguments: `{"pattern":"*.txt"}`, wantResult: `"seed.txt"`},
	{name: "list_dir", arguments: `{"path":"."}`, wantResult: `"seed.txt"`},
	{name: "file_read", arguments: `{"path":"seed.txt"}`, wantResult: `"content":"matrix-seed"`},
	{name: "file_write", arguments: `{"path":"written.txt","content":"matrix-write-ok"}`, wantResult: `"bytes":15`},
	{name: "shell", arguments: `{"command":"printf matrix-shell-ok"}`, wantResult: `"output":"matrix-shell-ok"`},
}

// TestDefaultToolsCompleteChatMatrix exercises every Client default tool through
// both model/tool loop implementations. The transport is in-memory so failures
// here are SDK-owned: no gateway routing or provider behavior is involved.
func TestDefaultToolsCompleteChatMatrix(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "non_streaming"
		if streaming {
			mode = "streaming"
		}
		for _, testCase := range defaultToolMatrixCases {
			t.Run(mode+"/"+testCase.name, func(t *testing.T) {
				runDefaultToolMatrixCase(t, testCase, streaming)
			})
		}
	}
}

func runDefaultToolMatrixCase(t *testing.T, testCase defaultToolMatrixCase, streaming bool) {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "seed.txt"), []byte("matrix-seed"), 0o600); err != nil {
		t.Fatal(err)
	}

	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		wire := decodeChatRequest(t, body)
		assertCompleteDefaultToolSet(t, index, wire.Tools)
		if wire.Stream != streaming {
			t.Fatalf("turn %d stream=%v want %v", index, wire.Stream, streaming)
		}
		if index == 0 {
			return defaultToolCallResponse(testCase, streaming)
		}
		if index != 1 {
			t.Fatalf("unexpected third model turn: %d", index)
		}
		if len(wire.Messages) != 3 || wire.Messages[1].Role != "assistant" || wire.Messages[2].Role != "tool" {
			t.Fatalf("continuation transcript is incomplete: %+v", wire.Messages)
		}
		if wire.Messages[2].Name != testCase.name || wire.Messages[2].ToolCallID != "call_1" {
			t.Fatalf("continuation tool result identity mismatch: %+v", wire.Messages[2])
		}
		result, _ := wire.Messages[2].Content.(string)
		if !strings.Contains(result, testCase.wantResult) {
			t.Fatalf("%s result=%q does not contain %q", testCase.name, result, testCase.wantResult)
		}
		if streaming {
			return "text/event-stream", "data: {\"id\":\"final\",\"model\":\"auto\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		}
		return "application/json", `{"id":"final","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	}}

	fileApprovals := 0
	shellApprovals := 0
	client := NewClient(
		WithAPIKey("test-key"),
		WithHTTPClient(transport),
		WithFileWorkspace(workspace),
		WithShellWorkspace(workspace),
		WithFilePermission(func(_ context.Context, request FilePermissionRequest) (bool, error) {
			fileApprovals++
			return request.ToolName == "file_write" && request.Root == workspace, nil
		}),
		WithShellPermission(func(_ context.Context, request ShellPermissionRequest) (bool, error) {
			shellApprovals++
			return request.ToolName == "shell" && request.WorkingDir == workspace && request.Command == "printf matrix-shell-ok", nil
		}),
	)
	request := ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "run " + testCase.name}}}

	if streaming {
		result, err := client.RunChatCompletionStream(context.Background(), request, RunChatStreamOptions{})
		assertDefaultToolMatrixResult(t, result, err, testCase.name)
	} else {
		result, err := client.RunChatCompletion(context.Background(), request, RunChatOptions{})
		assertDefaultToolMatrixResult(t, result, err, testCase.name)
	}

	if got, want := fileApprovals, boolInt(testCase.name == "file_write"); got != want {
		t.Fatalf("file approvals=%d want %d", got, want)
	}
	if got, want := shellApprovals, boolInt(testCase.name == "shell"); got != want {
		t.Fatalf("shell approvals=%d want %d", got, want)
	}
	if testCase.name == "file_write" {
		content, err := os.ReadFile(filepath.Join(workspace, "written.txt"))
		if err != nil || string(content) != "matrix-write-ok" {
			t.Fatalf("file_write disk result=%q err=%v", content, err)
		}
	}
}

func defaultToolCallResponse(testCase defaultToolMatrixCase, streaming bool) (string, string) {
	arguments, _ := json.Marshal(testCase.arguments)
	if streaming {
		return "text/event-stream", "data: {\"id\":\"first\",\"model\":\"auto\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"" + testCase.name + "\",\"arguments\":" + string(arguments) + "}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	}
	return "application/json", `{"id":"first","model":"auto","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + testCase.name + `","arguments":` + string(arguments) + `}}]},"finish_reason":"tool_calls"}]}`
}

func assertCompleteDefaultToolSet(t *testing.T, turn int, tools []ChatTool) {
	t.Helper()
	want := map[string]bool{
		"calculator": true, "datetime": true, "file_search": true, "list_dir": true,
		"file_read": true, "file_write": true, "shell": true,
	}
	if len(tools) != len(want) {
		t.Fatalf("turn %d tool count=%d want %d: %+v", turn, len(tools), len(want), tools)
	}
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		name := tool.Function.Name
		if !want[name] {
			t.Fatalf("turn %d has unexpected tool %q", turn, name)
		}
		if seen[name] {
			t.Fatalf("turn %d duplicated tool %q", turn, name)
		}
		seen[name] = true
	}
}

func assertDefaultToolMatrixResult(t *testing.T, result *RunChatResult, err error, toolName string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.StoppedBy != "stop" || result.FinalText != "done" || len(result.Steps) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Steps[0].ToolResults) != 1 {
		t.Fatalf("tool results=%d want 1", len(result.Steps[0].ToolResults))
	}
	toolResult := result.Steps[0].ToolResults[0]
	if toolResult.ToolName != toolName || toolResult.IsError {
		t.Fatalf("unexpected tool result: %+v", toolResult)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestHostedToolVariantsPassThroughTogether(t *testing.T) {
	t.Run("chat_non_streaming", func(t *testing.T) {
		assertChatHostedToolVariants(t, false)
	})
	t.Run("chat_streaming", func(t *testing.T) {
		assertChatHostedToolVariants(t, true)
	})
	t.Run("responses_non_streaming", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
			if request.URL.Path != "/openai/v1/responses" {
				t.Fatalf("unexpected path %q", request.URL.Path)
			}
			wire := decodeResponseRequest(t, body)
			assertResponseHostedToolVariants(t, wire.Tools)
			return "application/json", `{"id":"response_1","object":"response","status":"completed","model":"auto","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`
		}}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
		response, err := client.CreateResponse(context.Background(), ResponseRequest{
			Model: ModelAuto, Input: "search", Tools: hostedResponseToolVariants(),
		})
		if err != nil || response.OutputText() != "done" {
			t.Fatalf("response=%+v err=%v", response, err)
		}
	})
	t.Run("responses_streaming", func(t *testing.T) {
		transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
			if request.URL.Path != "/openai/v1/responses" {
				t.Fatalf("unexpected path %q", request.URL.Path)
			}
			wire := decodeResponseRequest(t, body)
			if !wire.Stream {
				t.Fatal("Responses stream request lost stream=true")
			}
			assertResponseHostedToolVariants(t, wire.Tools)
			return "text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\ndata: [DONE]\n\n"
		}}
		client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
		stream, err := client.StreamResponse(context.Background(), ResponseRequest{
			Model: ModelAuto, Input: "search", Tools: hostedResponseToolVariants(),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		event, err := stream.Recv()
		if err != nil || event.Delta != "done" {
			t.Fatalf("event=%+v err=%v", event, err)
		}
	})
}

func TestExplicitUserToolSuppressesDefaultsInBothChatLoops(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "non_streaming"
		if streaming {
			mode = "streaming"
		}
		for _, registered := range []bool{false, true} {
			ownership := "passthrough_only"
			if registered {
				ownership = "user_handler"
			}
			t.Run(mode+"/"+ownership, func(t *testing.T) {
				assertExplicitUserToolLoop(t, streaming, registered)
			})
		}
	}
}

func TestChatLoopsReportMaxStepsFinishReason(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "non_streaming"
		if streaming {
			mode = "streaming"
		}
		t.Run(mode, func(t *testing.T) {
			transport := &adapterHTTPClient{handle: func(_ int, _ *http.Request, _ []byte) (string, string) {
				return defaultToolCallResponse(defaultToolMatrixCase{name: "calculator", arguments: `{"expression":"1+2"}`}, streaming)
			}}
			client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
			request := ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "calculate"}}}
			var result *RunChatResult
			var err error
			if streaming {
				result, err = client.RunChatCompletionStream(context.Background(), request, RunChatStreamOptions{MaxSteps: 1})
			} else {
				result, err = client.RunChatCompletion(context.Background(), request, RunChatOptions{MaxSteps: 1})
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.StoppedBy != "max_steps" || result.FinishReason != "max_steps" {
				t.Fatalf("stopped_by=%q finish_reason=%q", result.StoppedBy, result.FinishReason)
			}
			response := result.finalResponse(nil)
			if len(response.Choices) != 1 || response.Choices[0].FinishReason != "max_steps" {
				t.Fatalf("final response=%+v", response)
			}
		})
	}
}

func assertExplicitUserToolLoop(t *testing.T, streaming, registered bool) {
	t.Helper()
	executions := 0
	transport := &adapterHTTPClient{handle: func(index int, _ *http.Request, body []byte) (string, string) {
		wire := decodeChatRequest(t, body)
		if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != "calculator" || wire.Tools[0].Function.Description != "user-owned calculator" {
			t.Fatalf("turn %d did not preserve the exact user tool: %+v", index, wire.Tools)
		}
		if index == 0 {
			return defaultToolCallResponse(defaultToolMatrixCase{name: "calculator", arguments: `{"expression":"1+2"}`}, streaming)
		}
		if len(wire.Messages) != 3 || wire.Messages[2].Role != "tool" {
			t.Fatalf("continuation transcript is incomplete: %+v", wire.Messages)
		}
		content, _ := wire.Messages[2].Content.(string)
		if registered {
			if content != `{"owner":"user"}` {
				t.Fatalf("registered user result=%q", content)
			}
		} else if !strings.Contains(content, "Tool not found: calculator") {
			t.Fatalf("SDK default captured an explicitly declared user tool: %q", content)
		}
		if streaming {
			return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		}
		return "application/json", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	}}

	options := []Option{WithAPIKey("test-key"), WithHTTPClient(transport)}
	if registered {
		options = append(options, WithTools(Tool{
			Name: "calculator",
			Execute: func(_ context.Context, _ any, _ ToolExecutionContext) (any, error) {
				executions++
				return map[string]any{"owner": "user"}, nil
			},
		}))
	}
	client := NewClient(options...)
	request := ChatCompletionRequest{
		Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "calculate"}},
		Tools: []ChatTool{{Type: "function", Function: ChatToolFunction{
			Name: "calculator", Description: "user-owned calculator", Parameters: map[string]any{"type": "object"},
		}}},
	}
	if streaming {
		result, err := client.RunChatCompletionStream(context.Background(), request, RunChatStreamOptions{})
		if err != nil || result.FinalText != "done" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	} else {
		result, err := client.RunChatCompletion(context.Background(), request, RunChatOptions{})
		if err != nil || result.FinalText != "done" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	wantExecutions := boolInt(registered)
	if executions != wantExecutions {
		t.Fatalf("user handler executions=%d want %d", executions, wantExecutions)
	}
}

func assertChatHostedToolVariants(t *testing.T, streaming bool) {
	t.Helper()
	transport := &adapterHTTPClient{handle: func(_ int, request *http.Request, body []byte) (string, string) {
		if request.URL.Path != "/openai/v1/chat/completions" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		var wire struct {
			Stream bool             `json:"stream"`
			Tools  []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Stream != streaming {
			t.Fatalf("stream=%v want %v", wire.Stream, streaming)
		}
		assertRawHostedToolTypes(t, wire.Tools)
		if streaming {
			return "text/event-stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		}
		return "application/json", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	}}
	client := NewClient(WithAPIKey("test-key"), WithHTTPClient(transport))
	request := ChatCompletionRequest{Model: ModelAuto, Messages: []ChatMessage{{Role: "user", Content: "search"}}, Tools: hostedChatToolVariants()}
	if streaming {
		stream, err := client.StreamChatCompletion(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
		return
	}
	response, err := client.CreateChatCompletion(context.Background(), request)
	if err != nil || len(response.Choices) != 1 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func hostedChatToolVariants() []ChatTool {
	return []ChatTool{
		{Type: "web_search"},
		{Type: "web_search_20250305"},
		{Type: "web_search_preview", SearchContextSize: "medium"},
	}
}

func hostedResponseToolVariants() []ResponseTool {
	return []ResponseTool{
		{Type: "web_search"},
		{Type: "web_search_20250305"},
		{Type: "web_search_preview", SearchContextSize: "medium"},
	}
}

func assertRawHostedToolTypes(t *testing.T, tools []map[string]any) {
	t.Helper()
	if len(tools) != 3 {
		t.Fatalf("hosted tool count=%d want 3: %#v", len(tools), tools)
	}
	want := []string{"web_search", "web_search_20250305", "web_search_preview"}
	for index, tool := range tools {
		if tool["type"] != want[index] {
			t.Fatalf("tool[%d] type=%#v want %q", index, tool["type"], want[index])
		}
		if _, hasFunction := tool["function"]; hasFunction {
			t.Fatalf("hosted tool[%d] was incorrectly wrapped as function: %#v", index, tool)
		}
	}
	if tools[2]["search_context_size"] != "medium" {
		t.Fatalf("web_search_preview options lost: %#v", tools[2])
	}
}

func assertResponseHostedToolVariants(t *testing.T, tools []ResponseTool) {
	t.Helper()
	if len(tools) != 3 {
		t.Fatalf("hosted tool count=%d want 3: %+v", len(tools), tools)
	}
	want := []string{"web_search", "web_search_20250305", "web_search_preview"}
	for index, tool := range tools {
		if tool.Type != want[index] || tool.Name != "" || tool.Parameters != nil {
			t.Fatalf("tool[%d] changed: %+v", index, tool)
		}
	}
	if tools[2].SearchContextSize != "medium" {
		t.Fatalf("web_search_preview options lost: %+v", tools[2])
	}
}
