package toolkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// newFileWriteGateway drives the agent loop with a first-turn file_write
// tool_call (writing relPath/content) and a plain second-turn answer. It lets a
// test exercise the real side-effecting file tool through the toolkit
// permission gate over the full HTTP loop — the seam calculator/datetime never
// reach because they have no disk effect.
func newFileWriteGateway(t *testing.T, relPath, content, finalText string, calls *int32) *httptest.Server {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": relPath, "content": content})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call_fw", "type": "function",
						"function": map[string]any{"name": "file_write", "arguments": string(args)},
					}},
				}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": finalText}}},
		})
	}))
}

// TestE2EFileWriteApprovedHitsDisk verifies that an approved file_write runs the
// real side-effecting tool over the full loop: the file lands on disk inside the
// sandbox and the tool result is fed back without error.
func TestE2EFileWriteApprovedHitsDisk(t *testing.T) {
	root := t.TempDir()
	var calls int32
	server := newFileWriteGateway(t, "out/report.md", "hello disk", "written", &calls)
	defer server.Close()

	ask := func(context.Context, PermissionRequest) (bool, error) { return true, nil }
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).
		Register(FileWrite(FileSandbox{Root: root}))
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "write the report")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	first := result.Steps[0].ToolResults[0]
	if first.IsError {
		t.Fatalf("expected approved file_write to succeed, got %#v", first)
	}
	// The file must actually exist inside the sandbox with the written content.
	data, readErr := os.ReadFile(filepath.Join(root, "out", "report.md"))
	if readErr != nil {
		t.Fatalf("expected file written to disk, got read error: %v", readErr)
	}
	if string(data) != "hello disk" {
		t.Fatalf("expected written content %q, got %q", "hello disk", string(data))
	}
	if result.FinalText != "written" {
		t.Fatalf("expected final text, got %q", result.FinalText)
	}
}

// TestE2EFileWriteDeniedLeavesDiskUntouched verifies the fail-safe path for a
// side-effecting tool: a Deny policy blocks file_write over the full loop, the
// error is fed back, and nothing is written to disk.
func TestE2EFileWriteDeniedLeavesDiskUntouched(t *testing.T) {
	root := t.TempDir()
	var calls int32
	server := newFileWriteGateway(t, "out/report.md", "should not persist", "done", &calls)
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionDeny})).
		Register(FileWrite(FileSandbox{Root: root}))
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "write the report")
	if err != nil {
		t.Fatalf("Run returned error, expected denial fed back: %v", err)
	}

	first := result.Steps[0].ToolResults[0]
	if !first.IsError || !strings.Contains(first.Content, "denied") {
		t.Fatalf("expected denied error fed back, got %#v", first)
	}
	// The file must NOT exist: the permission gate must run before the tool.
	if _, statErr := os.Stat(filepath.Join(root, "out", "report.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file written on denial (stat err=%v)", statErr)
	}
	if result.FinalText != "done" {
		t.Fatalf("expected run to finish after denial, got %q", result.FinalText)
	}
}

// TestE2EFileWriteAskNoHandlerLeavesDiskUntouched verifies the nil-handler
// fail-safe for a side-effecting tool: Ask mode with no handler denies the
// write and leaves the disk untouched.
func TestE2EFileWriteAskNoHandlerLeavesDiskUntouched(t *testing.T) {
	root := t.TempDir()
	var calls int32
	server := newFileWriteGateway(t, "out/report.md", "nope", "done", &calls)
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionAsk})).
		Register(FileWrite(FileSandbox{Root: root}))
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "write the report")
	if err != nil {
		t.Fatalf("Run returned error, expected fail-safe fed back: %v", err)
	}
	first := result.Steps[0].ToolResults[0]
	if !first.IsError || !strings.Contains(first.Content, "no permission handler") {
		t.Fatalf("expected fail-safe error fed back, got %#v", first)
	}
	if _, statErr := os.Stat(filepath.Join(root, "out", "report.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file written under nil handler (stat err=%v)", statErr)
	}
}
