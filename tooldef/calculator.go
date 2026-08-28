package tooldef

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Calculator returns a zero-dependency, local, side-effect-free tool that
// evaluates an arithmetic expression. It supports + - * / % and parentheses
// over floating-point numbers. Because it has no side effects and needs no
// credentials, it is safe to run under PermissionAuto and is part of the
// default tool set.
func Calculator() Tool {
	return Tool{
		Name:        "calculator",
		Description: "Evaluate a math expression. Supports + - * / %, parentheses and decimals, e.g. \"(2 + 3) * 4.5\".",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "The arithmetic expression to evaluate.",
				},
			},
			"required": []string{"expression"},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			expression, err := stringArg(input, "expression")
			if err != nil {
				return nil, err
			}
			value, err := evalExpression(expression)
			if err != nil {
				return nil, fmt.Errorf("calculator: %w", err)
			}
			return map[string]any{"result": value}, nil
		},
	}
}

// MaxArgBytes caps the length of a single string argument extracted from tool
// input. It is a coarse guard against a model sending a pathologically large
// value that would be buffered in memory before a tool's own size checks run.
const MaxArgBytes = 1 << 20 // 1 MiB

// stringArg extracts a required string field from a tool's structured input.
// It accepts JSON strings directly and coerces numbers and booleans to their
// string form, because models frequently emit e.g. {"expression": 42} instead
// of {"expression": "42"}. The extracted value is bounded by MaxArgBytes.
func stringArg(input any, key string) (string, error) {
	object, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("expected object input, got %T", input)
	}
	raw, ok := object[key]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	value, err := coerceString(raw, key)
	if err != nil {
		return "", err
	}
	if len(value) > MaxArgBytes {
		return "", fmt.Errorf("argument %q is %d bytes, exceeds limit of %d", key, len(value), MaxArgBytes)
	}
	return value, nil
}

// coerceString converts a JSON-decoded scalar into a string. Objects, arrays
// and null are rejected because no tool expects a structured value where a
// string argument is required.
func coerceString(raw any, key string) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		// JSON numbers decode to float64; render integers without a trailing
		// ".0" so "42" stays "42" rather than becoming "42.000000".
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("argument %q must be a string, got %T", key, raw)
	}
}
