package joytoken

import (
	"context"
	"errors"
	"io"
)

// RunChatStreamOptions configures one RunChatCompletionStream execution loop.
// It is additive and opt-in: it does not change StreamChatCompletion or any
// existing method.
type RunChatStreamOptions struct {
	// MaxSteps bounds how many model/tool round-trips the loop runs. Zero uses
	// defaultToolMaxSteps.
	MaxSteps int

	// OnTextDelta, when set, receives each incremental assistant text chunk as
	// it streams in, across every step of the loop. It lets callers render
	// tokens live while the loop still handles tool execution automatically.
	OnTextDelta func(delta string)

	// OnToolResult, when set, is called after each tool call is executed with
	// its result, so callers can surface tool activity mid-stream.
	OnToolResult func(result ToolCallResult)
}

// RunChatCompletionStream runs the same bounded model-and-tool loop as
// RunChatCompletion, but each model turn is consumed as a Server-Sent-Events
// stream so text can be surfaced token-by-token through
// RunChatStreamOptions.OnTextDelta. Tool calls accumulated during a stream are
// executed after that stream ends, their results are fed back, and the next
// turn opens a fresh stream. The loop stops when a streamed turn produces no
// tool calls or when MaxSteps is reached.
//
// This is an additive entry point: StreamChatCompletion keeps its raw,
// non-executing streaming semantics unchanged, and the same registered tools
// drive it as the non-streaming loops. If a later turn fails, the returned
// result retains every completed step and the accumulated transcript.
func (c *Client) RunChatCompletionStream(ctx context.Context, request ChatCompletionRequest, opts RunChatStreamOptions) (*RunChatResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultToolMaxSteps
	}

	messages := append([]ChatMessage(nil), request.Messages...)
	result := &RunChatResult{StoppedBy: "max_steps"}
	handlers := c.chatToolHandlers(request)
	effectiveTools := c.chatTools(request)

	for step := 0; step < maxSteps; step++ {
		stepRequest := request
		stepRequest.Messages = messages
		stepRequest.Tools = effectiveTools

		assistant, usage, rawFinish, err := c.streamOneChatTurn(ctx, stepRequest, opts.OnTextDelta)
		if err != nil {
			result.Messages = messages
			return result, err
		}
		messages = append(messages, assistant)
		kind := classifyFinishReason(rawFinish)

		toolResults, err := c.executeToolCallsWithHandlers(ctx, handlers, step, assistant.ToolCalls, messages)
		if err != nil {
			result.Messages = messages
			return result, err
		}
		if opts.OnToolResult != nil {
			for _, tr := range toolResults {
				opts.OnToolResult(tr)
			}
		}

		result.Steps = append(result.Steps, ToolStep{
			Index:            step,
			AssistantMessage: assistant,
			ToolResults:      toolResults,
			Usage:            usage,
			FinishReason:     rawFinish,
		})

		// Same vendor-neutral handling as the non-streaming loop: a malformed
		// tool call yields no usable tool_calls, so nudge the model to re-emit
		// a valid payload and retry instead of silently stopping with empty text.
		if kind == FinishMalformedToolCall && len(assistant.ToolCalls) == 0 {
			result.StoppedBy = kind.String()
			result.FinishReason = kind.String()
			messages = append(messages, ChatMessage{Role: "user", Content: malformedToolCallNudge})
			continue
		}

		if len(assistant.ToolCalls) == 0 {
			result.FinalText = messageText(assistant.Content)
			result.StoppedBy = "stop"
			result.FinishReason = FinishStop.String()
			break
		}

		for _, tr := range toolResults {
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: tr.ToolCallID,
				Name:       tr.ToolName,
				Content:    tr.Content,
			})
		}
		request.ToolChoice = continuationToolChoice(request.ToolChoice)
	}

	result.Messages = messages
	if result.StoppedBy == "max_steps" {
		result.FinishReason = "max_steps"
	}
	return result, nil
}

// streamOneChatTurn consumes a single streaming turn, invoking onTextDelta for
// each text chunk, and reassembles the full assistant message (text plus any
// accumulated tool_calls) so the loop can execute tools exactly as the
// non-streaming path does.
func (c *Client) streamOneChatTurn(ctx context.Context, request ChatCompletionRequest, onTextDelta func(string)) (ChatMessage, *Usage, string, error) {
	stream, err := c.StreamChatCompletion(ctx, request)
	if err != nil {
		return ChatMessage{}, nil, "", err
	}
	defer stream.Close()

	var text string
	var usage *Usage
	var finishReason string
	acc := newToolCallAccumulator()

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return ChatMessage{}, nil, "", err
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if delta := deltaText(choice.Delta); delta != "" {
				text += delta
				if onTextDelta != nil {
					onTextDelta(delta)
				}
			}
			acc.add(choice.Delta)
			// The finish_reason arrives on the terminal chunk (often with an
			// empty delta). Keep the last non-empty one so the loop can classify
			// a malformed tool call the same way the non-streaming path does.
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}

	msg := ChatMessage{Role: "assistant", Content: text, ToolCalls: acc.toolCalls()}
	return msg, usage, finishReason, nil
}

// deltaText extracts the incremental text content from a streaming delta map.
func deltaText(delta map[string]any) string {
	if delta == nil {
		return ""
	}
	if content, ok := delta["content"].(string); ok {
		return content
	}
	return ""
}

// toolCallAccumulator reassembles streamed tool_calls, whose id, name, and
// argument string arrive in fragments across multiple delta chunks keyed by
// index.
type toolCallAccumulator struct {
	order []int
	byIdx map[int]*ToolCall
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{byIdx: make(map[int]*ToolCall)}
}

// add merges the tool_calls fragment carried by one delta map into the
// accumulator. The wire shape is delta["tool_calls"] = [ {index, id, type,
// function:{name, arguments}, extra_content:{...}} ... ] with any field
// possibly absent per chunk. extra_content is an opaque vendor extension; for
// example, Gemini puts its required thought signature there.
func (a *toolCallAccumulator) add(delta map[string]any) {
	raw, ok := delta["tool_calls"].([]any)
	if !ok {
		return
	}
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		idx := 0
		if f, ok := m["index"].(float64); ok {
			idx = int(f)
		}
		call, exists := a.byIdx[idx]
		if !exists {
			call = &ToolCall{Type: "function"}
			a.byIdx[idx] = call
			a.order = append(a.order, idx)
		}
		if id, ok := m["id"].(string); ok && id != "" {
			call.ID = id
		}
		if t, ok := m["type"].(string); ok && t != "" {
			call.Type = t
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				call.Function.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				call.Function.Arguments += args
			}
		}
		if extra, ok := m["extra_content"].(map[string]any); ok {
			call.ExtraContent = mergeJSONObject(call.ExtraContent, extra)
		}
	}
}

// mergeJSONObject recursively combines streamed JSON object fragments without
// interpreting vendor-specific keys or values.
func mergeJSONObject(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for key, value := range src {
		srcObject, srcIsObject := value.(map[string]any)
		dstObject, dstIsObject := dst[key].(map[string]any)
		if srcIsObject {
			if !dstIsObject {
				dstObject = nil
			}
			dst[key] = mergeJSONObject(dstObject, srcObject)
			continue
		}
		dst[key] = value
	}
	return dst
}

// toolCalls returns the accumulated tool calls in first-seen index order.
func (a *toolCallAccumulator) toolCalls() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		calls = append(calls, *a.byIdx[idx])
	}
	return calls
}
