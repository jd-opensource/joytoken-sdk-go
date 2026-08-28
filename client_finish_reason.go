package joytoken

import "strings"

// FinishReasonKind is the SDK's provider-neutral classification of a model
// turn's finish_reason. Different gateways and model vendors spell their
// terminal states differently (OpenAI: "stop"/"tool_calls"/"length";
// Gemini: "STOP"/"MALFORMED_FUNCTION_CALL"/"MAX_TOKENS"; Anthropic:
// "end_turn"/"tool_use"/"max_tokens"). The tool loop should never branch on
// those raw strings directly; it normalizes them into this small enum so the
// loop's control flow stays vendor-agnostic and new providers only need a new
// mapping entry here.
type FinishReasonKind int

const (
	// FinishUnknown is an unrecognized or empty finish_reason. Treated as a
	// normal stop by the loop but kept distinct for diagnostics.
	FinishUnknown FinishReasonKind = iota
	// FinishStop is a clean end of turn: the model produced its final answer.
	FinishStop
	// FinishToolCalls means the model asked to call one or more tools.
	FinishToolCalls
	// FinishLength means the turn was cut off by a token/length limit.
	FinishLength
	// FinishContentFilter means the turn was blocked by a safety filter.
	FinishContentFilter
	// FinishMalformedToolCall means the model attempted a tool call but emitted
	// an unparseable/invalid payload that the gateway rejected. This is the
	// Gemini-family "malformed_function_call" case: the response carries no
	// usable tool_calls even though the model was trying to call one. It is
	// transient and worth retrying with a corrective nudge.
	FinishMalformedToolCall
)

// String renders the kind for diagnostics and StoppedBy values.
func (k FinishReasonKind) String() string {
	switch k {
	case FinishStop:
		return "stop"
	case FinishToolCalls:
		return "tool_calls"
	case FinishLength:
		return "length"
	case FinishContentFilter:
		return "content_filter"
	case FinishMalformedToolCall:
		return "malformed_function_call"
	default:
		return "unknown"
	}
}

// classifyFinishReason maps a raw provider finish_reason string to a
// provider-neutral FinishReasonKind. Matching is case-insensitive and tolerant
// of the separator differences seen across vendors (snake_case vs SCREAMING,
// "toolCalls" etc.), so one entry covers each family. Unknown strings fall
// through to FinishUnknown, which the loop treats as a benign stop.
func classifyFinishReason(raw string) FinishReasonKind {
	norm := strings.ToLower(strings.TrimSpace(raw))
	norm = strings.ReplaceAll(norm, "-", "_")
	norm = strings.ReplaceAll(norm, " ", "_")

	switch norm {
	case "":
		return FinishUnknown
	case "stop", "end_turn", "endturn", "complete", "completed":
		return FinishStop
	case "tool_calls", "toolcalls", "tool_use", "tooluse", "function_call", "functioncall":
		return FinishToolCalls
	case "length", "max_tokens", "maxtokens", "model_length":
		return FinishLength
	case "content_filter", "contentfilter", "safety", "blocklist", "prohibited_content", "recitation":
		return FinishContentFilter
	case "malformed_function_call", "malformedfunctioncall", "malformed_tool_call":
		return FinishMalformedToolCall
	default:
		return FinishUnknown
	}
}

// malformedToolCallNudge is appended to the transcript as a user turn when a
// step comes back malformed. It steers the model to retry the tool call with a
// strictly valid, minimal JSON payload — the most common fix for the
// Gemini-family malformed_function_call, whose usual cause is invalid JSON in
// string arguments (unescaped operators, unbalanced braces).
const malformedToolCallNudge = "Your previous tool call could not be parsed. " +
	"Retry the tool call with a strictly valid JSON arguments object: " +
	"double-quote every key and string value, escape special characters, " +
	"and include only the fields the tool schema declares."
