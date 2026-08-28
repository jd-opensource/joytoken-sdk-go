package tool

import (
	"context"
	"encoding/json"
	"fmt"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// ToChatTool converts a Tool into the wire-level joytoken.ChatTool that is sent
// to the model in a chat request.
func ToChatTool(t Tool) joytoken.ChatTool {
	parameters := t.Parameters
	if parameters == nil {
		parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return joytoken.ChatTool{
		Type: "function",
		Function: joytoken.ChatToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  parameters,
		},
	}
}

// ParseArguments decodes a tool call's raw JSON arguments into a generic value.
// An empty string yields an empty object, and invalid JSON is wrapped as {"raw": value}
// so a malformed model response never crashes the execution loop.
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
// them into errors so a single tool never brings down the execution loop.
func SafeExecute(ctx context.Context, execute ExecuteFunc, input any, execution ExecutionContext) (output any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return execute(ctx, input, execution)
}
