package joytoken

import (
	"context"
	"fmt"
	"os"

	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

// ShellPermissionRequest describes a pending shell invocation presented to the
// host for approval. The SDK never renders UI; the host decides. Command is the
// model-supplied command line and WorkingDir is the resolved directory it would
// run in, so the host can show the user exactly what would execute and where.
type ShellPermissionRequest struct {
	ToolName   string
	Input      any
	Command    string
	WorkingDir string
	Step       int
}

// ShellPermissionFunc lets the host approve or reject a default shell call.
// Returning false blocks the command. The shell tool is always declared to the
// model; this callback is what makes it runnable. With no callback configured,
// the declaration is still sent but every invocation is refused at execution
// time, so the model sees the capability yet nothing runs without host approval.
type ShellPermissionFunc func(ctx context.Context, request ShellPermissionRequest) (allow bool, err error)

// WithShellWorkspace overrides the directory the default shell tool runs
// commands in. When unset, it defaults to the current working directory
// (os.Getwd), matching the file tools' project-workspace model.
func WithShellWorkspace(dir string) Option {
	return func(c *Client) { c.shellWorkingDir = dir }
}

// WithShellPermission installs the approval callback for the default shell tool.
// The shell tool is always declared to the model; this callback is what allows
// its commands to actually run. Because a shell command can do anything the host
// process can, every invocation is gated: with no callback configured, the
// command is refused at execution time. The resolved working directory is passed
// to the callback so the host can show the user exactly what would run and where.
func WithShellPermission(ask ShellPermissionFunc) Option {
	return func(c *Client) { c.shellPermission = ask }
}

// shellSandbox returns the sandbox used by the default shell tool. An empty
// configured directory falls back to the current working directory so commands
// run in the project the host is actually running in.
func (c *Client) shellSandbox() tooldef.ShellSandbox {
	dir := c.shellWorkingDir
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	return tooldef.ShellSandbox{WorkingDir: dir}
}

// defaultShellTools returns the shell tool, always injected into the execution
// loops so the model can see the capability. Its Execute is wrapped so every
// command is gated: with a ShellPermissionFunc configured the host approves each
// call, and with no callback the command is refused at execution time.
func (c *Client) defaultShellTools() []Tool {
	sandbox := c.shellSandbox()
	shell := tooldef.Shell(sandbox)
	shell.Execute = c.gateShell(sandbox, shell.Execute)
	return []Tool{shell}
}

// gateShell wraps a shell Execute with the host approval callback, failing
// safe: if no callback is configured, or approval returns an error or false,
// the command never runs.
func (c *Client) gateShell(sandbox tooldef.ShellSandbox, next ToolExecuteFunc) ToolExecuteFunc {
	return func(ctx context.Context, input any, execution ToolExecutionContext) (any, error) {
		ask := c.shellPermission
		if ask == nil {
			return nil, fmt.Errorf("shell rejected: no ShellPermissionFunc configured (set WithShellPermission to enable)")
		}
		command := ""
		if obj, ok := input.(map[string]any); ok {
			command, _ = obj["command"].(string)
		}
		allow, err := ask(ctx, ShellPermissionRequest{
			ToolName:   "shell",
			Input:      input,
			Command:    command,
			WorkingDir: sandbox.WorkingDir,
			Step:       execution.Step,
		})
		if err != nil {
			return nil, fmt.Errorf("shell permission check failed: %w", err)
		}
		if !allow {
			return nil, fmt.Errorf("shell rejected by permission handler")
		}
		return next(ctx, input, execution)
	}
}
