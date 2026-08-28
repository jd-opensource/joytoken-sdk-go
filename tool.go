package joytoken

import (
	"context"

	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// The shared Tool abstraction lives in the tooldef package (the bottom of the
// dependency graph) so concrete tool implementations (Calculator, DateTime)
// can be defined there and reused by this client's execution loop without an
// import cycle. The types and helpers are re-exported here as aliases (=) and
// thin delegators so every downstream reference keeps working unchanged.

// ToolExecuteFunc is the signature of a tool's execution function.
type ToolExecuteFunc = tooldef.ToolExecuteFunc

// Tool describes a function the model can call.
type Tool = tooldef.Tool

// ToolExecutionContext contains the state available to a tool invocation.
type ToolExecutionContext = tooldef.ToolExecutionContext

// DefineTool returns tool unchanged and documents the intended construction
// point for code that shares tool definitions across packages.
func DefineTool(t Tool) Tool { return tooldef.DefineTool(t) }

// toolToChatTool converts a Tool into the wire-level ChatTool sent to the model.
func toolToChatTool(t Tool) ChatTool { return tooldef.ToChatTool(t) }

// parseToolArguments decodes a tool call's raw JSON arguments into a generic
// value.
func parseToolArguments(value string) any { return tooldef.ParseArguments(value) }

// stringifyToolResult serializes a tool result into the string content fed back
// to the model as a tool-role message.
func stringifyToolResult(value any) (string, error) { return tooldef.StringifyResult(value) }

// safeExecuteTool runs a tool's Execute function and recovers from panics,
// turning them into errors so a single tool never crashes the execution loop.
func safeExecuteTool(ctx context.Context, execute ToolExecuteFunc, input any, execution ToolExecutionContext) (any, error) {
	return tooldef.SafeExecute(ctx, execute, input, execution)
}