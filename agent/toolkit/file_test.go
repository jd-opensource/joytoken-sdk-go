package toolkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

func TestFileWriteThenRead(t *testing.T) {
	root := t.TempDir()
	sandbox := FileSandbox{Root: root}

	write := FileWrite(sandbox)
	if _, err := write.Execute(context.Background(), map[string]any{
		"path":    "sub/note.txt",
		"content": "hello",
	}, agent.ToolExecutionContext{}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "sub", "note.txt")); err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}

	read := FileRead(sandbox)
	out, err := read.Execute(context.Background(), map[string]any{"path": "sub/note.txt"}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if content := out.(map[string]any)["content"].(string); content != "hello" {
		t.Fatalf("expected \"hello\", got %q", content)
	}
}

func TestFileReadRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	// Create a secret file outside the sandbox root.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer os.Remove(outside)

	read := FileRead(FileSandbox{Root: root})
	if _, err := read.Execute(context.Background(), map[string]any{"path": "../secret.txt"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestFileReadRejectsAbsolutePath(t *testing.T) {
	read := FileRead(FileSandbox{Root: t.TempDir()})
	if _, err := read.Execute(context.Background(), map[string]any{"path": "/etc/passwd"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
}

func TestFileWriteEnforcesSizeLimit(t *testing.T) {
	write := FileWrite(FileSandbox{Root: t.TempDir(), MaxBytes: 4})
	if _, err := write.Execute(context.Background(), map[string]any{
		"path":    "big.txt",
		"content": "too large",
	}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected size limit to be enforced")
	}
}

func TestFileReadMissingFile(t *testing.T) {
	read := FileRead(FileSandbox{Root: t.TempDir()})
	if _, err := read.Execute(context.Background(), map[string]any{"path": "nope.txt"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestFileSandboxEmptyRootFallsBackToHome documents the deliberate wide-boundary
// behavior: an empty Root is NOT an error — the sandbox falls back to the
// current user's home directory so a host can offer a "just find my file"
// experience without pinning an exact path. The previous test asserted an
// error here and only passed by accident (the probe file happened not to exist
// under home). We instead assert the real contract: AbsRoot resolves to home,
// and a read of an existing file under home succeeds while paths still cannot
// escape that boundary.
func TestFileSandboxEmptyRootFallsBackToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("home directory unavailable: %v", err)
	}

	sandbox := FileSandbox{}
	root, err := sandbox.AbsRoot()
	if err != nil {
		t.Fatalf("AbsRoot with empty Root should fall back to home, got error: %v", err)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		t.Fatalf("filepath.Abs(home): %v", err)
	}
	if root != absHome {
		t.Fatalf("empty Root should resolve to home %q, got %q", absHome, root)
	}

	// Write a probe file under home and read it back through the sandbox to
	// prove the fallback root is actually usable, then clean it up.
	probe := filepath.Join(home, ".joytoken_sandbox_probe.txt")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		t.Skipf("cannot write probe under home: %v", err)
	}
	defer os.Remove(probe)

	read := FileRead(sandbox)
	out, err := read.Execute(context.Background(), map[string]any{"path": ".joytoken_sandbox_probe.txt"}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("read under home-fallback root failed: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok || result["content"] != "ok" {
		t.Fatalf("unexpected read result %#v", out)
	}

	// The fallback must still confine paths: an absolute path is rejected.
	if _, err := read.Execute(context.Background(), map[string]any{"path": "/etc/hosts"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected absolute path to be rejected even under home fallback")
	}
}
