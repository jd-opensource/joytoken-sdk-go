// Package tooldef holds the tool-related wire types and the sharedTool
// abstraction at the very bottom of the JoyToken dependency graph.
//
// Historically the Tool abstraction and its wire types (ChatMessage, ToolCall,
// ...) lived in the root joytoken package. That made it impossible for the root
// client to reuse concrete tool implementations (Calculator, DateTime) that
// were themselves defined in an upper package (agent/toolkit), because any tool
// implementation must reference the Tool abstraction, and the root package
// cannot import a package that imports it back (import cycle).
//
// tooldef breaks that cycle: it depends on nothing else in the module, so the
// root joytoken package, the tool package, the agent package, and the toolkit
// package can all depend on it one-way. The root package re-exports these types
// via type aliases, so existing callers using joytoken.ChatMessage /
// joytoken.Tool are unaffected and the JSON wire format is unchanged (aliases
// are the identical type).
package tooldef

import "encoding/json"

// ChatMessage is an OpenAI-compatible conversation message. It lives here (not
// in the root package) only because the Tool execution context references it;
// the root package re-exports it verbatim via a type alias.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall describes a model-requested function call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
	// ThoughtSignature is an opaque, provider-specific reasoning token that some
	// upstreams (notably Gemini via the gateway's Chat Completions endpoint)
	// return at the top level of the tool_call object. It MUST be echoed back
	// verbatim on the continuation turn or the provider rejects the request, so
	// we capture it here rather than dropping it during (de)serialization.
	ThoughtSignature string `json:"thought_signature,omitempty"`
	// ExtraContent carries opaque provider metadata that must survive a tool
	// round trip, such as Gemini's google.thought_signature extension.
	ExtraContent map[string]any `json:"extra_content,omitempty"`
}

// ToolFunction identifies a function and its JSON arguments.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatTool declares a tool available to Chat Completions.
type ChatTool struct {
	Type     string           `json:"type"`
	Function ChatToolFunction `json:"function"`

	// Vendor-hosted tool fields are used by the Responses compatibility adapter.
	// They are kept here so those declarations can be forwarded through the
	// gateway's single Chat Completions endpoint without losing information.
	SearchContextSize string         `json:"search_context_size,omitempty"`
	UserLocation      any            `json:"user_location,omitempty"`
	VectorStoreIDs    []string       `json:"vector_store_ids,omitempty"`
	MaxNumResults     *int           `json:"max_num_results,omitempty"`
	Filters           map[string]any `json:"filters,omitempty"`
	RankingOptions    any            `json:"ranking_options,omitempty"`
}

// MarshalJSON omits the function envelope for vendor-hosted tools while
// preserving the long-standing function-tool wire shape.
func (t ChatTool) MarshalJSON() ([]byte, error) {
	if t.Type == "" || t.Type == "function" {
		type functionTool struct {
			Type     string           `json:"type"`
			Function ChatToolFunction `json:"function"`
		}
		toolType := t.Type
		if toolType == "" {
			toolType = "function"
		}
		return json.Marshal(functionTool{Type: toolType, Function: t.Function})
	}
	payload := map[string]any{"type": t.Type}
	if t.SearchContextSize != "" {
		payload["search_context_size"] = t.SearchContextSize
	}
	if t.UserLocation != nil {
		payload["user_location"] = t.UserLocation
	}
	if len(t.VectorStoreIDs) > 0 {
		payload["vector_store_ids"] = t.VectorStoreIDs
	}
	if t.MaxNumResults != nil {
		payload["max_num_results"] = t.MaxNumResults
	}
	if t.Filters != nil {
		payload["filters"] = t.Filters
	}
	if t.RankingOptions != nil {
		payload["ranking_options"] = t.RankingOptions
	}
	return json.Marshal(payload)
}

// ChatToolFunction contains the schema for a callable function.
type ChatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}
