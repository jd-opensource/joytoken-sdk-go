package agent

import (
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

func TestAnthropicProviderAdapterPreservesToolCallExtraContent(t *testing.T) {
	extra := map[string]any{"google": map[string]any{"thought_signature": "opaque-signature"}}
	request := toAnthropicRequest(ModelRequest{Messages: []joytoken.ChatMessage{{
		Role: "assistant",
		ToolCalls: []joytoken.ToolCall{{
			ID: "call_1", Type: "function", Function: joytoken.ToolFunction{Name: "echo", Arguments: `{"text":"hello"}`},
			ExtraContent: extra,
		}},
	}}})
	if len(request.Messages) != 1 {
		t.Fatalf("messages=%d", len(request.Messages))
	}
	blocks, ok := request.Messages[0].Content.([]joytoken.MessageContentBlock)
	if !ok || len(blocks) != 1 {
		t.Fatalf("content=%#v", request.Messages[0].Content)
	}
	assertAgentThoughtSignature(t, blocks[0].ExtraContent)

	normalized := normalizeAnthropicMessage(&joytoken.MessageResponse{Content: []joytoken.MessageContentBlock{{
		Type: "tool_use", ID: "call_1", Name: "echo", Input: map[string]any{"text": "hello"}, ExtraContent: extra,
	}}})
	if len(normalized.ToolCalls) != 1 {
		t.Fatalf("tool_calls=%d", len(normalized.ToolCalls))
	}
	assertAgentThoughtSignature(t, normalized.ToolCalls[0].ExtraContent)
}

func assertAgentThoughtSignature(t *testing.T, extra map[string]any) {
	t.Helper()
	google, ok := extra["google"].(map[string]any)
	if !ok || google["thought_signature"] != "opaque-signature" {
		t.Fatalf("extra_content=%#v", extra)
	}
}
