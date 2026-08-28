package toolkit

import (
	"github.com/jd-opensource/joytoken-sdk-go/agent"
	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// DateTime returns a zero-dependency, local, side-effect-free tool that reports
// the current date and time. It accepts an optional IANA timezone name (e.g.
// "Asia/Shanghai") and an optional Go reference-layout format string. Because
// it only reads the clock and needs no credentials, it is safe to run under
// PermissionAuto and is part of the default tool set.
//
// The implementation lives in the tooldef package so the root client can reuse
// it for default tool fallback; this delegates to keep a single source of truth.
func DateTime() agent.AgentTool { return tooldef.DateTime() }