package toolkit

import (
	"strings"
	"testing"
)

func TestStringArgCoercesScalars(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"string", "hello", "hello"},
		{"integer", float64(42), "42"},
		{"float", 4.5, "4.5"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stringArg(map[string]any{"k": tc.value}, "k")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestStringArgRejectsStructuredValues(t *testing.T) {
	for _, value := range []any{[]any{1, 2}, map[string]any{"a": 1}, nil} {
		if _, err := stringArg(map[string]any{"k": value}, "k"); err == nil {
			t.Fatalf("expected error for %T", value)
		}
	}
}

func TestStringArgRejectsMissingAndNonObject(t *testing.T) {
	if _, err := stringArg(map[string]any{}, "k"); err == nil {
		t.Fatal("expected missing-argument error")
	}
	if _, err := stringArg("not-an-object", "k"); err == nil {
		t.Fatal("expected non-object error")
	}
}

func TestStringArgEnforcesMaxBytes(t *testing.T) {
	big := strings.Repeat("a", MaxArgBytes+1)
	if _, err := stringArg(map[string]any{"k": big}, "k"); err == nil {
		t.Fatal("expected size-limit error")
	}
}
