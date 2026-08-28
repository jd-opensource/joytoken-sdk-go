package joytoken

import (
	"context"
	"fmt"
	"os"

	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// FilePermissionRequest describes a pending file_write invocation presented to
// the host for approval. The SDK never renders UI; the host decides. Path is
// the model-supplied relative path and Root is the resolved absolute sandbox
// root, so the host can show the user exactly where a write would land.
type FilePermissionRequest struct {
	ToolName string
	Input    any
	Root     string
	Step     int
}

// FilePermissionFunc lets the host approve or reject a default file_write call.
// Returning false blocks the write. The file_write tool is always declared to
// the model; this callback is what makes it runnable. With no callback
// configured, the declaration is still sent but every write is refused at
// execution time, so the model sees the capability yet nothing is written
// without host approval. Read/exploration tools remain always available.
type FilePermissionFunc func(ctx context.Context, request FilePermissionRequest) (allow bool, err error)

// WithFileWorkspace overrides the sandbox root used by the default file tools.
// When unset, the root defaults to the current working directory (os.Getwd),
// matching the Codex/Claude "project workspace" model. Pass an explicit
// directory to widen or narrow the boundary the model may explore.
func WithFileWorkspace(root string) Option {
	return func(c *Client) { c.fileWorkspaceRoot = root }
}

// WithFilePermission installs the approval callback for the default file_write
// tool. file_write is always declared to the model; this callback is what
// allows writes to actually happen. Read and exploration tools (file_search,
// list_dir, file_read) are side-effect free and always runnable. Every write is
// gated: with no callback configured, the write is refused at execution time,
// otherwise the host approves each write with the resolved absolute root in hand.
func WithFilePermission(ask FilePermissionFunc) Option {
	return func(c *Client) { c.filePermission = ask }
}

// fileWorkspaceSandbox returns the sandbox used by the default file tools. An
// empty configured root falls back to the current working directory so the
// exposed surface is the project the host is actually running in.
func (c *Client) fileWorkspaceSandbox() tooldef.FileSandbox {
	root := c.fileWorkspaceRoot
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	return tooldef.FileSandbox{Root: root}
}

// defaultFileTools returns the file tools injected by default into the
// execution loops. Read and exploration tools are always included and are safe
// to run without approval (they are side-effect free and confined to the
// sandbox). file_write is also always included so the model can see it, but its
// Execute is wrapped so every write is gated: with a FilePermissionFunc the host
// approves each write, and with no callback the write is refused at execution time.
func (c *Client) defaultFileTools() []Tool {
	sandbox := c.fileWorkspaceSandbox()

	write := tooldef.FileWrite(sandbox)
	write.Execute = c.gateFileWrite(sandbox, write.Execute)

	return []Tool{
		tooldef.FileSearch(sandbox),
		tooldef.ListDir(sandbox),
		tooldef.FileRead(sandbox),
		write,
	}
}

// gateFileWrite wraps a file_write Execute with the host approval callback,
// failing safe: if no callback is configured, or approval returns an error or
// false, the write never runs.
func (c *Client) gateFileWrite(sandbox tooldef.FileSandbox, next ToolExecuteFunc) ToolExecuteFunc {
	root := sandbox.Root
	if resolved, err := (tooldef.FileSandbox{Root: root}).AbsRoot(); err == nil {
		root = resolved
	}
	return func(ctx context.Context, input any, execution ToolExecutionContext) (any, error) {
		ask := c.filePermission
		if ask == nil {
			return nil, fmt.Errorf("file_write rejected: no FilePermissionFunc configured (set WithFilePermission to enable)")
		}
		allow, err := ask(ctx, FilePermissionRequest{
			ToolName: "file_write",
			Input:    input,
			Root:     root,
			Step:     execution.Step,
		})
		if err != nil {
			return nil, fmt.Errorf("file_write permission check failed: %w", err)
		}
		if !allow {
			return nil, fmt.Errorf("file_write rejected by permission handler")
		}
		return next(ctx, input, execution)
	}
}
