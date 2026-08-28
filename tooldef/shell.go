package tooldef

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// DefaultShellTimeout bounds how long a single shell command may run before it
// is killed. Zero on the sandbox falls back to this value.
const DefaultShellTimeout = 30 * time.Second

// DefaultShellOutputBytes caps how many bytes of combined stdout/stderr are
// returned to the model, so a chatty command cannot flood the context window.
const DefaultShellOutputBytes = 64 << 10 // 64 KiB

// ShellSandbox confines the shell tool: WorkingDir is the directory the command
// runs in (empty means the process's current directory), Timeout bounds a single
// invocation (zero means DefaultShellTimeout), and MaxOutputBytes caps returned
// output (zero means DefaultShellOutputBytes).
//
// Unlike FileSandbox, this type does not attempt to jail the command itself: a
// shell command is inherently powerful, so the safety boundary is the host's
// permission gate (see the root client's WithShellPermission), not lexical path
// containment. The sandbox only scopes where the command starts and how much it
// can run and emit.
type ShellSandbox struct {
	WorkingDir     string
	Timeout        time.Duration
	MaxOutputBytes int
}

func (s ShellSandbox) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultShellTimeout
}

func (s ShellSandbox) maxOutputBytes() int {
	if s.MaxOutputBytes > 0 {
		return s.MaxOutputBytes
	}
	return DefaultShellOutputBytes
}

// Shell returns a local tool that runs a shell command and returns its combined
// output. Because a command can read, write, delete, or exfiltrate anything the
// host process can touch, this tool has real, unbounded side effects: register
// it behind a permission gate (the root client injects it only when a
// ShellPermissionFunc is configured) so the host approves each invocation.
//
// The command runs through the platform shell ("sh -c" on Unix, "cmd /C" on
// Windows) inside the configured working directory, is killed after the
// sandbox timeout, and its combined stdout/stderr is truncated to the sandbox
// output cap before being handed back to the model.
func Shell(sandbox ShellSandbox) Tool {
	return Tool{
		Name:        "shell",
		Description: "Run a shell command and return its combined stdout/stderr. Use it to build, test, inspect, or manipulate the workspace. The command runs in the configured working directory and is subject to a time limit.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command line to execute, e.g. \"ls -la\" or \"go test ./...\".",
				},
			},
			"required": []string{"command"},
		},
		Execute: func(ctx context.Context, input any, _ ToolExecutionContext) (any, error) {
			command, err := stringArg(input, "command")
			if err != nil {
				return nil, err
			}

			runCtx, cancel := context.WithTimeout(ctx, sandbox.timeout())
			defer cancel()

			name, args := shellInvocation(command)
			cmd := exec.CommandContext(runCtx, name, args...)
			if sandbox.WorkingDir != "" {
				cmd.Dir = sandbox.WorkingDir
			}

			var combined bytes.Buffer
			cmd.Stdout = &combined
			cmd.Stderr = &combined

			runErr := cmd.Run()

			out, truncated := truncateBytes(combined.Bytes(), sandbox.maxOutputBytes())
			result := map[string]any{
				"command":   command,
				"output":    out,
				"truncated": truncated,
			}

			if runCtx.Err() == context.DeadlineExceeded {
				result["timed_out"] = true
				result["exit_code"] = -1
				return result, nil
			}
			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					result["exit_code"] = exitErr.ExitCode()
					return result, nil
				}
				return nil, fmt.Errorf("shell: %w", runErr)
			}
			result["exit_code"] = 0
			return result, nil
		},
	}
}

// shellInvocation returns the platform shell and the arguments needed to run a
// single command line through it.
func shellInvocation(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

// truncateBytes returns at most max bytes of data as a string and reports
// whether truncation occurred.
func truncateBytes(data []byte, max int) (string, bool) {
	if max > 0 && len(data) > max {
		return string(data[:max]), true
	}
	return string(data), false
}
