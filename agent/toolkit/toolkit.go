// Package toolkit provides a built-in tool set for agents, following a
// convention-over-configuration model: when a host application does not supply
// its own tools, the agent can inject a safe default set (compute-class,
// zero-cost, local tools).
//
// Tools fall into two groups:
//
//   - Zero-config tools (Calculator, DateTime) need no credentials, no network,
//     and no host state. They are safe to inject automatically and make up the
//     Default set.
//   - Host-configured local fallback tools (FileRead/FileWrite, HTTPFetch,
//     SQLQuery) require the host to supply a sandbox root, an allowlist, or a
//     *sql.DB before they are safe. They are NOT part of Default; the host must
//     configure and Register them explicitly, choosing an appropriate
//     Permission (PermissionAuto for reads, PermissionAsk for writes).
package toolkit

import (
	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// Toolkit is a registry of built-in agent tools. It keeps a stable ordering of
// registered tools and applies a shared permission policy and middleware chain
// to every tool it exposes.
type Toolkit struct {
	permission Permission
	middleware []Middleware
	byName     map[string]agent.AgentTool
	order      []string
}

// Option customizes a Toolkit at construction time.
type Option func(*Toolkit)

// WithPermission sets the permission policy applied to every tool.
func WithPermission(p Permission) Option {
	return func(t *Toolkit) { t.permission = p }
}

// WithMiddleware appends middleware applied to every tool, outermost first.
func WithMiddleware(mw ...Middleware) Option {
	return func(t *Toolkit) { t.middleware = append(t.middleware, mw...) }
}

// New creates an empty Toolkit with the given options.
func New(options ...Option) *Toolkit {
	t := &Toolkit{
		permission: Permission{Mode: PermissionAuto},
		byName:     make(map[string]agent.AgentTool),
	}
	for _, option := range options {
		option(t)
	}
	return t
}

// Register adds one or more tools to the toolkit. Later registrations with the
// same name overwrite earlier ones while preserving first-seen ordering.
func (t *Toolkit) Register(tools ...agent.AgentTool) *Toolkit {
	for _, tool := range tools {
		if _, exists := t.byName[tool.Name]; !exists {
			t.order = append(t.order, tool.Name)
		}
		t.byName[tool.Name] = tool
	}
	return t
}

// Tools returns the registered tools in stable order, each wrapped with the
// toolkit's permission policy and middleware chain.
func (t *Toolkit) Tools() []agent.AgentTool {
	tools := make([]agent.AgentTool, 0, len(t.order))
	for _, name := range t.order {
		tool := t.byName[name]
		tool.Execute = t.wrap(tool.Name, tool.Execute)
		tools = append(tools, tool)
	}
	return tools
}

// wrap applies the permission check and middleware chain around a tool's
// Execute function. Middleware registered first is the outermost layer.
func (t *Toolkit) wrap(name string, execute agent.ToolExecuteFunc) agent.ToolExecuteFunc {
	handler := execute
	handler = permissionMiddleware(name, t.permission)(name, handler)
	for i := len(t.middleware) - 1; i >= 0; i-- {
		handler = t.middleware[i](name, handler)
	}
	return handler
}

// Default returns the safe default tool set: local, zero-cost compute tools
// that require no credentials and no network access. It is the set injected
// when a host application does not configure any tools of its own.
//
// Only zero-config tools belong here. The host-configured local fallback tools
// (FileRead/FileWrite, HTTPFetch, SQLQuery) are intentionally excluded: they
// need a sandbox root, an allowlist, or a database handle, so the host must
// build and Register them explicitly.
func Default(options ...Option) *Toolkit {
	return New(options...).Register(
		Calculator(),
		DateTime(),
	)
}

// Workspace assembles a ready-to-use file toolset over a single wide boundary
// (tier B), so a host can offer a "help me find and read a file" experience
// with one call instead of wiring each tool by hand.
//
// The root defines the boundary the model may explore. Pass an explicit
// directory (e.g. the user's Desktop or a chosen project folder), or pass ""
// to fall back to the current user's home directory — either way the host does
// not need to know the exact file up front; the model discovers it via search.
//
// Permission split, matching how Codex/Claude behave:
//   - Read-only exploration (file_search, list_dir, file_read) runs under
//     PermissionAuto: the model may freely locate and read files inside root.
//   - file_write has real side effects and runs under PermissionAsk, so the
//     host approves each write with the resolved absolute path in hand. If ask
//     is nil, file_write is omitted entirely (read-only workspace).
//
// The returned tools are already wrapped with their permission policy and can
// be passed straight to joytoken.WithTools.
func Workspace(root string, ask PermissionFunc) []agent.AgentTool {
	sandbox := FileSandbox{Root: root}

	read := New(WithPermission(Permission{Mode: PermissionAuto})).Register(
		FileSearch(sandbox),
		ListDir(sandbox),
		FileRead(sandbox),
	)
	tools := read.Tools()

	if ask != nil {
		write := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).Register(
			FileWrite(sandbox),
		)
		tools = append(tools, write.Tools()...)
	}
	return tools
}
