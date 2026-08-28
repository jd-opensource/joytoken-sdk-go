package tooldef

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// resultMap runs a tool's Execute and asserts it returned a map without error.
func resultMap(t *testing.T, tool Tool, input map[string]any) map[string]any {
	t.Helper()
	out, err := tool.Execute(context.Background(), input, ToolExecutionContext{})
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", tool.Name, err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("%s: expected map result, got %T", tool.Name, out)
	}
	return m
}

func TestCalculatorEvaluatesExpression(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"(2 + 3) * 4.5", 22.5},
		{"10 % 3", 1},
		{"-4 + 2", -2},
		{"2 * (3 + 4) - 1", 13},
	}
	calc := Calculator()
	for _, tc := range cases {
		m := resultMap(t, calc, map[string]any{"expression": tc.expr})
		got, ok := m["result"].(float64)
		if !ok {
			t.Fatalf("calculator(%q): result not float64, got %T", tc.expr, m["result"])
		}
		if got != tc.want {
			t.Fatalf("calculator(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestCalculatorRejectsDivisionByZero(t *testing.T) {
	_, err := Calculator().Execute(context.Background(), map[string]any{"expression": "1 / 0"}, ToolExecutionContext{})
	if err == nil {
		t.Fatal("calculator: expected division by zero error, got nil")
	}
}

func TestCalculatorRejectsMissingArgument(t *testing.T) {
	_, err := Calculator().Execute(context.Background(), map[string]any{}, ToolExecutionContext{})
	if err == nil {
		t.Fatal("calculator: expected missing-argument error, got nil")
	}
}

func TestCalculatorCoercesNumericArgument(t *testing.T) {
	// Models frequently emit {"expression": 42} instead of a string; the tool
	// must coerce the scalar rather than reject it.
	m := resultMap(t, Calculator(), map[string]any{"expression": float64(42)})
	if m["result"].(float64) != 42 {
		t.Fatalf("calculator numeric coercion = %v, want 42", m["result"])
	}
}

func TestDateTimeDefaultsToUTC(t *testing.T) {
	m := resultMap(t, DateTime(), map[string]any{})
	if m["timezone"] != "UTC" {
		t.Fatalf("datetime default timezone = %v, want UTC", m["timezone"])
	}
	if _, ok := m["datetime"].(string); !ok {
		t.Fatalf("datetime: expected string datetime field, got %T", m["datetime"])
	}
	if _, ok := m["unix"].(int64); !ok {
		t.Fatalf("datetime: expected int64 unix field, got %T", m["unix"])
	}
}

func TestDateTimeHonorsTimezoneAndFormat(t *testing.T) {
	m := resultMap(t, DateTime(), map[string]any{
		"timezone": "Asia/Shanghai",
		"format":   "2006-01-02 15:04:05",
	})
	if m["timezone"] != "Asia/Shanghai" {
		t.Fatalf("datetime timezone = %v, want Asia/Shanghai", m["timezone"])
	}
	got := m["datetime"].(string)
	if _, err := time.Parse("2006-01-02 15:04:05", got); err != nil {
		t.Fatalf("datetime %q does not match requested layout: %v", got, err)
	}
}

func TestDateTimeRejectsInvalidTimezone(t *testing.T) {
	_, err := DateTime().Execute(context.Background(), map[string]any{"timezone": "Not/AZone"}, ToolExecutionContext{})
	if err == nil {
		t.Fatal("datetime: expected invalid-timezone error, got nil")
	}
}

func TestEvalExpressionCases(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"1 + 2 + 3", 6},
		{"2 * 3 + 4", 10},
		{"(1 + 2) * (3 + 4)", 21},
		{"-(2 + 3)", -5},
		{"7 % 4", 3},
		{"3.5 * 2", 7},
	}
	for _, tc := range cases {
		got, err := EvalExpression(tc.expr)
		if err != nil {
			t.Fatalf("EvalExpression(%q): unexpected error: %v", tc.expr, err)
		}
		if got != tc.want {
			t.Fatalf("EvalExpression(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestEvalExpressionErrors(t *testing.T) {
	cases := []string{
		"",       // empty
		"1 +",    // dangling operator
		"(1 + 2", // unbalanced paren
		"1 / 0",  // division by zero
		"5 % 0",  // modulo by zero
		"1 2",    // unexpected trailing token
		"abc",    // not a number
	}
	for _, expr := range cases {
		if _, err := EvalExpression(expr); err == nil {
			t.Fatalf("EvalExpression(%q): expected error, got nil", expr)
		}
	}
}

// TestFileWriteReadRoundTrip verifies that file_write and file_read agree on
// content and byte count inside a temp sandbox, exercising the full round trip.
func TestFileWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sandbox := FileSandbox{Root: dir}
	const body = "hello sandbox\n"

	wm := resultMap(t, FileWrite(sandbox), map[string]any{
		"path":    "notes/todo.txt",
		"content": body,
	})
	if wm["bytes"].(int) != len(body) {
		t.Fatalf("file_write bytes = %v, want %d", wm["bytes"], len(body))
	}
	// The file must physically exist under the sandbox root.
	if _, err := os.Stat(filepath.Join(dir, "notes", "todo.txt")); err != nil {
		t.Fatalf("file_write did not create file: %v", err)
	}

	rm := resultMap(t, FileRead(sandbox), map[string]any{"path": "notes/todo.txt"})
	if rm["content"] != body {
		t.Fatalf("file_read content = %q, want %q", rm["content"], body)
	}
	if rm["bytes"].(int) != len(body) {
		t.Fatalf("file_read bytes = %v, want %d", rm["bytes"], len(body))
	}
}

// TestListDirListsEntries checks that list_dir reports files and folders in the
// sandbox with their name/dir flags.
func TestListDirListsEntries(t *testing.T) {
	dir := t.TempDir()
	sandbox := FileSandbox{Root: dir}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	m := resultMap(t, ListDir(sandbox), map[string]any{"path": "."})
	entries, ok := m["entries"].([]map[string]any)
	if !ok {
		t.Fatalf("list_dir entries not []map, got %T", m["entries"])
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e["name"].(string)] = e["dir"].(bool)
	}
	if _, ok := seen["a.txt"]; !ok {
		t.Fatalf("list_dir missing a.txt, entries=%v", entries)
	}
	if isDir, ok := seen["sub"]; !ok || !isDir {
		t.Fatalf("list_dir missing sub dir or wrong dir flag, entries=%v", entries)
	}
	if m["truncated"].(bool) {
		t.Fatal("list_dir unexpectedly truncated a two-entry directory")
	}
}

// TestFileSearchMatchesGlob checks that file_search finds files by glob pattern
// recursively and returns paths relative to the sandbox root.
func TestFileSearchMatchesGlob(t *testing.T) {
	dir := t.TempDir()
	sandbox := FileSandbox{Root: dir}
	mustWrite := func(rel string) {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("report.pdf")
	mustWrite("deep/nested/summary.pdf")
	mustWrite("notes.txt")

	m := resultMap(t, FileSearch(sandbox), map[string]any{"pattern": "*.pdf"})
	if m["count"].(int) != 2 {
		t.Fatalf("file_search count = %v, want 2", m["count"])
	}
	matches := m["matches"].([]string)
	joined := filepath.Join(matches...)
	if !contains(matches, "report.pdf") || !contains(matches, filepath.Join("deep", "nested", "summary.pdf")) {
		t.Fatalf("file_search matches missing expected pdfs: %v (%s)", matches, joined)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestFileSandboxRejectsTraversal verifies that a "../" relative path and an
// absolute path are both rejected before any I/O happens.
func TestFileSandboxRejectsTraversal(t *testing.T) {
	sandbox := FileSandbox{Root: t.TempDir()}
	if _, err := FileRead(sandbox).Execute(context.Background(), map[string]any{"path": "../escape.txt"}, ToolExecutionContext{}); err == nil {
		t.Fatal("file_read: expected traversal rejection for ../escape.txt")
	}
	if _, err := FileRead(sandbox).Execute(context.Background(), map[string]any{"path": "/etc/passwd"}, ToolExecutionContext{}); err == nil {
		t.Fatal("file_read: expected rejection for absolute path")
	}
}

// TestShellRunsSafeCommand runs a harmless echo and checks output + exit_code.
// It only uses non-destructive commands; side-effecting behavior is gated by
// the host permission layer, not exercised here.
func TestShellRunsSafeCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell echo assertions target the Unix shell")
	}
	m := resultMap(t, Shell(ShellSandbox{WorkingDir: t.TempDir()}), map[string]any{
		"command": "echo hello",
	})
	if m["exit_code"].(int) != 0 {
		t.Fatalf("shell echo exit_code = %v, want 0", m["exit_code"])
	}
	if got := m["output"].(string); got != "hello\n" {
		t.Fatalf("shell echo output = %q, want %q", got, "hello\n")
	}
	if m["truncated"].(bool) {
		t.Fatal("shell echo unexpectedly truncated")
	}
}

// TestShellReportsNonZeroExit verifies a failing command surfaces its exit code
// rather than an error, so the model can react to it.
func TestShellReportsNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exit-code assertion targets the Unix shell")
	}
	m := resultMap(t, Shell(ShellSandbox{}), map[string]any{"command": "exit 3"})
	if m["exit_code"].(int) != 3 {
		t.Fatalf("shell exit 3 exit_code = %v, want 3", m["exit_code"])
	}
}

// TestShellTimesOut verifies the sandbox timeout kills a long command and the
// result is flagged timed_out instead of hanging the caller.
func TestShellTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep-based timeout assertion targets the Unix shell")
	}
	m := resultMap(t, Shell(ShellSandbox{Timeout: 100 * time.Millisecond}), map[string]any{
		"command": "sleep 5",
	})
	if timedOut, _ := m["timed_out"].(bool); !timedOut {
		t.Fatalf("shell sleep 5 with 100ms timeout should time out, got %v", m)
	}
	if m["exit_code"].(int) != -1 {
		t.Fatalf("timed-out shell exit_code = %v, want -1", m["exit_code"])
	}
}
