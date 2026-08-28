package toolkit

import (
	"github.com/jd-opensource/joytoken-sdk-go/agent"
	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// FileSandbox confines file tools to a single root directory. Every path the
// model supplies is resolved relative to Root and validated to stay inside it,
// so the model can never read or write outside the sandbox. MaxBytes caps the
// size of a single read or write; zero means DefaultFileMaxBytes.
//
// Root may be left empty, in which case the sandbox falls back to the current
// user's home directory — a wide boundary (tier B) that lets a host offer a
// "help me find my file" experience without pinning an exact path up front.
//
// The implementation lives in the tooldef package (the bottom of the dependency
// graph) so the root client can reuse these tools for default fallback; this is
// a type alias to keep a single source of truth.
type FileSandbox = tooldef.FileSandbox

// DefaultFileMaxBytes is the per-operation size cap used when MaxBytes is zero.
const DefaultFileMaxBytes = tooldef.DefaultFileMaxBytes

// DefaultSearchLimit caps how many matches an exploration tool returns in one
// call, so a broad query over a large tree cannot flood the model's context.
const DefaultSearchLimit = tooldef.DefaultSearchLimit

// FileRead returns a local, read-only tool that reads a UTF-8 text file from
// within the sandbox. It is side-effect free and safe under PermissionAuto.
//
// The implementation lives in tooldef so the root client can reuse it for
// default tool fallback; this delegates to keep a single source of truth.
func FileRead(sandbox FileSandbox) agent.AgentTool { return tooldef.FileRead(sandbox) }

// FileWrite returns a local tool that writes a UTF-8 text file inside the
// sandbox, creating parent directories as needed. Because it has real side
// effects, register it under PermissionAsk so the host approves each write.
func FileWrite(sandbox FileSandbox) agent.AgentTool { return tooldef.FileWrite(sandbox) }

// ListDir returns a local, read-only tool that lists the immediate entries of a
// directory inside the sandbox. It is side-effect free and safe under
// PermissionAuto and powers "help me find a file" exploration.
func ListDir(sandbox FileSandbox) agent.AgentTool { return tooldef.ListDir(sandbox) }

// FileSearch returns a local, read-only tool that recursively finds files whose
// name matches a glob pattern anywhere under the sandbox root. It is side-effect
// free and safe under PermissionAuto.
func FileSearch(sandbox FileSandbox) agent.AgentTool { return tooldef.FileSearch(sandbox) }
