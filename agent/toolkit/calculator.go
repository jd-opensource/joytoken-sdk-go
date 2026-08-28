package toolkit

import (
	"github.com/jd-opensource/joytoken-sdk-go/agent"
	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// Calculator returns a zero-dependency, local, side-effect-free tool that
// evaluates an arithmetic expression. It supports + - * / % and parentheses
// over floating-point numbers. Because it has no side effects and needs no
// credentials, it is safe to run under PermissionAuto and is part of the
// default tool set.
//
// The implementation lives in the tooldef package so the root client can reuse
// it for default tool fallback; this delegates to keep a single source of truth.
func Calculator() agent.AgentTool { return tooldef.Calculator() }
