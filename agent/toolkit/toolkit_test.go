package toolkit

import (
	"context"
	"testing"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

func TestCalculatorEvaluatesExpression(t *testing.T) {
	tool := Calculator()
	out, err := tool.Execute(context.Background(), map[string]any{"expression": "(2 + 3) * 4.5"}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)["result"].(float64)
	if result != 22.5 {
		t.Fatalf("expected 22.5, got %v", result)
	}
}

func TestCalculatorRejectsDivisionByZero(t *testing.T) {
	tool := Calculator()
	if _, err := tool.Execute(context.Background(), map[string]any{"expression": "1/0"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected division by zero error")
	}
}

func TestEvalExpressionCases(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"1 + 2 * 3", 7},
		{"-5 + 3", -2},
		{"10 % 3", 1},
		{"2 * (3 + 4)", 14},
		{"100 / 4 / 5", 5},
	}
	for _, c := range cases {
		got, err := evalExpression(c.expr)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("%q: expected %v, got %v", c.expr, c.want, got)
		}
	}
}

func TestDateTimeDefaultsToUTC(t *testing.T) {
	tool := DateTime()
	out, err := tool.Execute(context.Background(), map[string]any{}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tz := out.(map[string]any)["timezone"].(string); tz != "UTC" {
		t.Fatalf("expected UTC, got %q", tz)
	}
}

func TestDateTimeRejectsInvalidTimezone(t *testing.T) {
	tool := DateTime()
	if _, err := tool.Execute(context.Background(), map[string]any{"timezone": "Nowhere/Nope"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestDefaultToolkitContainsCalculatorAndDateTime(t *testing.T) {
	tools := Default().Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 default tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"calculator", "datetime"} {
		if !names[want] {
			t.Fatalf("default tool set missing %q", want)
		}
	}
}

func TestWithDefaultsInjectsWhenToolsNil(t *testing.T) {
	opts := WithDefaults(agent.AgentOptions{})
	if len(opts.Tools) == 0 {
		t.Fatal("expected default tools to be injected when Tools is nil")
	}
}

func TestWithDefaultsPreservesExplicitEmptySlice(t *testing.T) {
	opts := WithDefaults(agent.AgentOptions{Tools: []agent.AgentTool{}})
	if opts.Tools == nil || len(opts.Tools) != 0 {
		t.Fatalf("expected explicit empty slice to be preserved, got %v", opts.Tools)
	}
}

func TestPermissionDenyBlocksExecution(t *testing.T) {
	tk := New(WithPermission(Permission{Mode: PermissionDeny})).Register(Calculator())
	tool := tk.Tools()[0]
	if _, err := tool.Execute(context.Background(), map[string]any{"expression": "1+1"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected denied error")
	}
}

func TestPermissionAskWithoutHandlerFailsSafe(t *testing.T) {
	tk := New(WithPermission(Permission{Mode: PermissionAsk})).Register(Calculator())
	tool := tk.Tools()[0]
	if _, err := tool.Execute(context.Background(), map[string]any{"expression": "1+1"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected fail-safe denial without handler")
	}
}

func TestPermissionAskWithHandlerAllows(t *testing.T) {
	called := false
	ask := func(context.Context, PermissionRequest) (bool, error) {
		called = true
		return true, nil
	}
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).Register(Calculator())
	tool := tk.Tools()[0]
	if _, err := tool.Execute(context.Background(), map[string]any{"expression": "1+1"}, agent.ToolExecutionContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected permission handler to be called")
	}
}
