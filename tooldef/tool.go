package tooldef

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolExecuteFunc is the signature of a tool's execution function. Naming the
// type lets middleware and toolkits compose Execute functions without repeating
// the full signature.
type ToolExecuteFunc func(ctx context.Context, input any, execution ToolExecutionContext) (any, error)

// Tool describes a function the model can call. It is the shared tool
// abstraction. It lives in tooldef (the very bottom of the dependency graph) so
// both the root client execution loop and the agent package can use it, and so
// concrete tool implementations (Calculator, DateTime) can be defined here and
// reused by the root client without an import cycle.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     ToolExecuteFunc
}

// ToolExecutionContext contains the state available to a tool invocation.
type ToolExecutionContext struct {
	Step     int
	ToolCall ToolCall
	Messages []ChatMessage
}

// DefineTool returns tool unchanged and documents the intended construction
// point for code that shares tool definitions across packages.
func DefineTool(t Tool) Tool { return t }

// ToChatTool converts a Tool into the wire-level ChatTool sent to the model.
func ToChatTool(t Tool) ChatTool {
	parameters := t.Parameters
	if parameters == nil {
		parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return ChatTool{
		Type: "function",
		Function: ChatToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  parameters,
		},
	}
}

// ParseArguments decodes a tool call's raw JSON arguments into a generic value.
// An empty string yields an empty object, and invalid JSON is wrapped as
// {"raw": value} so a malformed model response never crashes the loop.
func ParseArguments(value string) any {
	if value == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return map[string]any{"raw": value}
	}
	return parsed
}

// StringifyResult serializes a tool result into the string content fed back to
// the model as a tool-role message.
func StringifyResult(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	if stringValue, ok := value.(string); ok {
		return stringValue, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("serialize tool result: %w", err)
	}
	return string(raw), nil
}

// SafeExecute runs a tool's Execute function and recovers from panics, turning
// them into errors so a single tool never crashes the execution loop.
func SafeExecute(ctx context.Context, execute ToolExecuteFunc, input any, execution ToolExecutionContext) (output any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return execute(ctx, input, execution)
}