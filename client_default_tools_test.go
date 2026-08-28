package joytoken

import (
	"context"
	"testing"
)

// hasChatTool reports whether a tool with the given function name is present.
func hasChatTool(tools []ChatTool, name string) bool {
	for _, t := range tools {
		if t.Function.Name == name {
			return true
		}
	}
	return false
}

func hasResponseToolType(tools []ResponseTool, toolType string) bool {
	for _, tool := range tools {
		if tool.Type == toolType {
			return true
		}
	}
	return false
}

func TestDefaultLocalToolsInjectedByDefault(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	tools := client.chatTools(ChatCompletionRequest{})
	if !hasChatTool(tools, "calculator") {
		t.Fatalf("expected calculator to be injected by default, got %+v", tools)
	}
	if !hasChatTool(tools, "datetime") {
		t.Fatalf("expected datetime to be injected by default, got %+v", tools)
	}
	responseTools := client.responseTools(ResponseRequest{})
	messageTools := client.messageTools(MessageRequest{})
	if !hasResponseFunctionTool(responseTools, "calculator") || !hasResponseFunctionTool(responseTools, "datetime") {
		t.Fatalf("expected Responses defaults to include calculator and datetime, got %+v", responseTools)
	}
	if !hasMessageTool(messageTools, "calculator") || !hasMessageTool(messageTools, "datetime") {
		t.Fatalf("expected Messages defaults to include calculator and datetime, got %+v", messageTools)
	}
}

func hasResponseFunctionTool(tools []ResponseTool, name string) bool {
	for _, tool := range tools {
		if tool.Type == "function" && tool.Name == name {
			return true
		}
	}
	return false
}

func hasMessageTool(tools []MessageTool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestDefaultLocalToolsDisabled(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"), WithDefaultLocalTools(false))

	tools := client.chatTools(ChatCompletionRequest{})
	if hasChatTool(tools, "calculator") || hasChatTool(tools, "datetime") {
		t.Fatalf("expected no default local tools when disabled, got %+v", tools)
	}
}

func TestHostedResponseToolsAreOptIn(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	if tools := client.responseTools(ResponseRequest{}); hasResponseToolType(tools, "web_search_preview") {
		t.Fatalf("hosted Responses tools must not be injected by default: %+v", tools)
	}

	client = NewClient(WithAPIKey("test-key"), WithDefaultBuiltinTools(true))
	if tools := client.responseTools(ResponseRequest{}); !hasResponseToolType(tools, "web_search_preview") {
		t.Fatalf("expected opt-in web_search_preview tool, got %+v", tools)
	}
}

func TestExplicitHostedResponseToolIsAlwaysForwarded(t *testing.T) {
	requestTool := ResponseTool{Type: "web_search_preview", SearchContextSize: "medium"}
	client := NewClient(WithAPIKey("test-key"))
	tools := client.responseTools(ResponseRequest{Tools: []ResponseTool{requestTool}})
	if len(tools) != 1 || tools[0].Type != requestTool.Type || tools[0].SearchContextSize != requestTool.SearchContextSize {
		t.Fatalf("explicit hosted tool was not forwarded unchanged: %+v", tools)
	}
}

// TestSideEffectingToolsDeclaredWithoutCallback verifies that file_write and
// shell are always declared to the model even with no permission callback, so
// the model can see the capability. Execution without a callback is covered by
// TestSideEffectingToolsRefusedWithoutCallback.
func TestSideEffectingToolsDeclaredWithoutCallback(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	tools := client.chatTools(ChatCompletionRequest{})
	if !hasChatTool(tools, "file_write") {
		t.Fatalf("expected file_write declared by default, got %+v", tools)
	}
	if !hasChatTool(tools, "shell") {
		t.Fatalf("expected shell declared by default, got %+v", tools)
	}
}

// TestSideEffectingToolsRefusedWithoutCallback verifies that with no permission
// callback configured, invoking file_write or shell is refused at execution
// time (fail-safe) rather than silently running.
func TestSideEffectingToolsRefusedWithoutCallback(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	res, err := client.runRegisteredTool(context.Background(), "call-fw", "file_write", `{"path":"x.txt","content":"hi"}`, ToolExecutionContext{})
	if err == nil && !res.IsError {
		t.Fatalf("expected file_write refused without permission callback, got %+v", res)
	}

	res, err = client.runRegisteredTool(context.Background(), "call-sh", "shell", `{"command":"echohi"}`, ToolExecutionContext{})
	if err == nil && !res.IsError {
		t.Fatalf("expected shell refused without permission callback, got %+v", res)
	}
}

// TestWithoutDefaultToolsExcludesLocal verifies WithoutDefaultTools drops the
// named local tools while leaving the rest of the default set intact.
func TestWithoutDefaultToolsExcludesLocal(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"), WithoutDefaultTools("shell", "file_write"))

	tools := client.chatTools(ChatCompletionRequest{})
	if hasChatTool(tools, "shell") {
		t.Fatalf("expected shell excluded, got %+v", tools)
	}
	if hasChatTool(tools, "file_write") {
		t.Fatalf("expected file_write excluded, got %+v", tools)
	}
	if !hasChatTool(tools, "calculator") {
		t.Fatalf("expected calculator still present, got %+v", tools)
	}
	if !hasChatTool(tools, "file_read") {
		t.Fatalf("expected file_read still present, got %+v", tools)
	}
}

func TestDefaultLocalToolsAreExecutable(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	res, err := client.runRegisteredTool(context.Background(), "call-1", "calculator", `{"expression":"1+2"}`, ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected default calculator to execute, got error content: %s", res.Content)
	}
}

func TestRegisteredToolWinsOverDefault(t *testing.T) {
	custom := Tool{
		Name:        "calculator",
		Description: "custom override",
		Execute: func(_ context.Context, _ any, _ ToolExecutionContext) (any, error) {
			return "overridden", nil
		},
	}
	client := NewClient(WithAPIKey("test-key"), WithTools(custom))

	byName, _ := client.resolvedTools()
	if byName["calculator"].Description != "custom override" {
		t.Fatalf("expected registered calculator to win, got %+v", byName["calculator"])
	}
}

func TestRegisteredToolSetReplacesRemainingDefaults(t *testing.T) {
	custom := Tool{Name: "calculator", Description: "custom"}
	client := NewClient(WithAPIKey("test-key"), WithTools(custom))
	chatTools := client.chatTools(ChatCompletionRequest{})
	responseTools := client.responseTools(ResponseRequest{})
	messageTools := client.messageTools(MessageRequest{})
	if len(chatTools) != 1 || chatTools[0].Function.Name != "calculator" || chatTools[0].Function.Description != "custom" {
		t.Fatalf("expected only the registered Chat tool, got %+v", chatTools)
	}
	if len(responseTools) != 1 || responseTools[0].Name != "calculator" || responseTools[0].Description != "custom" {
		t.Fatalf("expected only the registered Responses tool, got %+v", responseTools)
	}
	if len(messageTools) != 1 || messageTools[0].Name != "calculator" || messageTools[0].Description != "custom" {
		t.Fatalf("expected only the registered Messages tool, got %+v", messageTools)
	}
}

func TestExplicitEmptyRequestToolsDisablesDefaults(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	chatTools := client.chatTools(ChatCompletionRequest{Tools: []ChatTool{}})
	responseTools := client.responseTools(ResponseRequest{Tools: []ResponseTool{}})
	messageTools := client.messageTools(MessageRequest{Tools: []MessageTool{}})
	if chatTools == nil || len(chatTools) != 0 || responseTools == nil || len(responseTools) != 0 || messageTools == nil || len(messageTools) != 0 {
		t.Fatalf("expected explicit empty tools to remain non-nil and empty: chat=%#v response=%#v message=%#v", chatTools, responseTools, messageTools)
	}
}
