package tooldef

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFileSandboxRejectsSymlinkEscape verifies that a symlink living inside the
// sandbox root cannot be used to read, list, or write outside of it. Lexical
// containment alone would pass these paths (they are all under root), so this
// exercises the real-path check added to resolve.
func TestFileSandboxRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically requires elevated privileges on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()

	// A secret file the sandbox must never expose.
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// A symlink inside root pointing at the outside directory, and one pointing
	// directly at the secret file.
	dirLink := filepath.Join(root, "escape_dir")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	fileLink := filepath.Join(root, "escape_file")
	if err := os.Symlink(secret, fileLink); err != nil {
		t.Fatalf("symlink file: %v", err)
	}

	sandbox := FileSandbox{Root: root}

	assertSymlinkRejected := func(t *testing.T, name string, _ any, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected symlink escape to be rejected, got nil error", name)
		}
		if !strings.Contains(err.Error(), "escapes the sandbox root via a symlink") {
			t.Fatalf("%s: expected symlink-escape error, got %v", name, err)
		}
	}

	read := FileRead(sandbox)
	out, err := read.Execute(context.Background(), map[string]any{"path": "escape_file"}, ToolExecutionContext{})
	assertSymlinkRejected(t, "file_read via symlinked file", out, err)

	list := ListDir(sandbox)
	out, err = list.Execute(context.Background(), map[string]any{"path": "escape_dir"}, ToolExecutionContext{})
	assertSymlinkRejected(t, "list_dir via symlinked dir", out, err)

	write := FileWrite(sandbox)
	out, err = write.Execute(context.Background(), map[string]any{"path": "escape_dir/planted.txt", "content": "x"}, ToolExecutionContext{})
	assertSymlinkRejected(t, "file_write into symlinked dir", out, err)

	// The planted file must not have been created outside the sandbox.
	if _, statErr := os.Stat(filepath.Join(outside, "planted.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("write escaped the sandbox: planted file exists (stat err=%v)", statErr)
	}
}

// TestFileSandboxAllowsLegitimatePaths is the positive control: ordinary reads
// and writes that stay within the sandbox continue to work after the symlink
// hardening.
func TestFileSandboxAllowsLegitimatePaths(t *testing.T) {
	root := t.TempDir()
	sandbox := FileSandbox{Root: root}

	write := FileWrite(sandbox)
	if _, err := write.Execute(context.Background(), map[string]any{"path": "sub/note.txt", "content": "hello"}, ToolExecutionContext{}); err != nil {
		t.Fatalf("legitimate write failed: %v", err)
	}

	read := FileRead(sandbox)
	out, err := read.Execute(context.Background(), map[string]any{"path": "sub/note.txt"}, ToolExecutionContext{})
	if err != nil {
		t.Fatalf("legitimate read failed: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected read result type %T", out)
	}
	if result["content"] != "hello" {
		t.Fatalf("expected content %q, got %v", "hello", result["content"])
	}
}
