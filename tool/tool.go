// Package tool re-exports the shared tool abstraction for code that composes
// tools (toolkits, MCP/Skill adapters) without pulling in the whole client
// surface. The concrete types are defined in the tooldef package — the actual
// bottom of the dependency graph — and re-exported by the root JoyToken client
// package; the aliases here forward to the root package for a stable,
// tool-focused import path. Because everything is a type alias down to tooldef,
// both the client execution loop and the agent package share one definition
// with no import cycle.
package tool

import (
	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// ExecuteFunc is an alias of joytoken.ToolExecuteFunc.
type ExecuteFunc = joytoken.ToolExecuteFunc

// Tool is an alias of joytoken.Tool.
type Tool = joytoken.Tool

// ExecutionContext is an alias of joytoken.ToolExecutionContext.
type ExecutionContext = joytoken.ToolExecutionContext

// Define returns tool unchanged; it delegates to joytoken.DefineTool.
func Define(t Tool) Tool { return joytoken.DefineTool(t) }