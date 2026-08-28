package toolkit

import (
	"encoding/json"
	"fmt"
	"strconv"
)

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

// optionalStringArg extracts an optional string field from a tool's structured
// input, returning "" when the field is absent or not a string.
func optionalStringArg(input any, key string) string {
	object, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	raw, ok := object[key]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return value
}