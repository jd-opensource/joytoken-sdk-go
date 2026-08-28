package agent

import (
	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/tool"
)

// DefineTool returns tool unchanged and documents the intended construction
// point for code that shares tool definitions across packages. It delegates to
// tool.Define so both layers share one construction helper.
func DefineTool(t AgentTool) AgentTool { return tool.Define(t) }

func toChatTool(t AgentTool) joytoken.ChatTool {
	return tool.ToChatTool(t)
}

func parseToolArguments(value string) any {
	return tool.ParseArguments(value)
}

func stringifyToolResult(value any) (string, error) {
	return tool.StringifyResult(value)
}