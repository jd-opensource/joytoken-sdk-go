package agent

import (
	"encoding/json"
	"fmt"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// DefineTool returns tool unchanged and documents the intended construction
// point for code that shares tool definitions across packages.
func DefineTool(tool AgentTool) AgentTool { return tool }

func toChatTool(tool AgentTool) joytoken.ChatTool {
	parameters := tool.Parameters
	if parameters == nil {
		parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return joytoken.ChatTool{
		Type: "function",
		Function: joytoken.ChatToolFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  parameters,
		},
	}
}

func parseToolArguments(value string) any {
	if value == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return map[string]any{"raw": value}
	}
	return parsed
}

func stringifyToolResult(value any) (string, error) {
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
