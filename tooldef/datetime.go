package tooldef

import (
	"context"
	"fmt"
	"time"
)

// DateTime returns a zero-dependency, local, side-effect-free tool that reports
// the current date and time. It accepts an optional IANA timezone name (e.g.
// "Asia/Shanghai") and an optional Go reference-layout format string. Because
// it only reads the clock and needs no credentials, it is safe to run under
// PermissionAuto and is part of the default tool set.
func DateTime() Tool {
	return Tool{
		Name:        "datetime",
		Description: "Get the current date and time. Optionally specify an IANA timezone (e.g. \"Asia/Shanghai\") and a Go time layout.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "IANA timezone name, e.g. \"UTC\" or \"Asia/Shanghai\". Defaults to UTC.",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Go reference time layout, e.g. \"2006-01-02 15:04:05\". Defaults to RFC3339.",
				},
			},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			timezone := optionalStringArg(input, "timezone")
			format := optionalStringArg(input, "format")

			location := time.UTC
			if timezone != "" {
				loc, err := time.LoadLocation(timezone)
				if err != nil {
					return nil, fmt.Errorf("datetime: invalid timezone %q: %w", timezone, err)
				}
				location = loc
			}
			if format == "" {
				format = time.RFC3339
			}

			now := time.Now().In(location)
			return map[string]any{
				"datetime": now.Format(format),
				"timezone": location.String(),
				"unix":     now.Unix(),
			}, nil
		},
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