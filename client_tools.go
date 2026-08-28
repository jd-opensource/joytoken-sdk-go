package joytoken

import (
	"context"
	"fmt"
)

// defaultToolMaxSteps bounds the RunChatCompletion loop so a model that keeps
// requesting tools can never spin forever.
const defaultToolMaxSteps = 8

// RunChatOptions configures one RunChatCompletion execution loop. It is an
// additive, opt-in surface: it does not change ChatCompletionRequest or any
// existing method, so callers that never touch RunChatCompletion are
// unaffected.
type RunChatOptions struct {
	// MaxSteps bounds how many model/tool round-trips the loop runs. Zero uses
	// defaultToolMaxSteps.
	MaxSteps int
}

// ToolStep records one model response and the tool results produced from it.
type ToolStep struct {
	Index            int
	AssistantMessage ChatMessage
	ToolResults      []ToolCallResult
	Usage            *Usage
	response         *ChatCompletionResponse
	// FinishReason is the raw provider finish_reason for this turn, kept for
	// diagnostics. The loop branches on the normalized FinishReasonKind, not
	// this string.
	FinishReason string
}

// ToolCallResult records the serialized result of one tool call. IsError is
// true when the tool failed (bad arguments, a runtime error, or a panic) and
// Content carries the error message fed back to the model so it can retry.
type ToolCallResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
}

// RunChatResult is the final state of a RunChatCompletion loop.
type RunChatResult struct {
	FinalText string
	Messages  []ChatMessage
	Steps     []ToolStep
	StoppedBy string
	// FinishReason is the normalized, provider-neutral classification of why
	// the loop stopped (its String() value). "malformed_function_call" here
	// means the loop exhausted its retries on a model that kept emitting
	// invalid tool-call payloads, so the caller can distinguish a real answer
	// from a vendor-side tool-calling failure instead of seeing an empty
	// FinalText with StoppedBy "stop".
	FinishReason string
}

// registerTool records a tool handler. Registering the same name again replaces
// the handler while preserving first-seen ordering.
func (c *Client) registerTool(t Tool) {
	if c.tools == nil {
		c.tools = make(map[string]Tool)
	}
	if _, exists := c.tools[t.Name]; !exists {
		c.toolOrder = append(c.toolOrder, t.Name)
	}
	c.tools[t.Name] = t
}

// chatTools returns the effective wire declarations. Request-level tools are
// copied exactly; otherwise caller-registered tools replace the defaults, and
// defaults are used only when the caller supplied no tools at all.
func (c *Client) chatTools(request ChatCompletionRequest) []ChatTool {
	if request.Tools != nil {
		tools := make([]ChatTool, len(request.Tools))
		copy(tools, request.Tools)
		return tools
	}
	byName, order := c.resolvedTools()
	tools := make([]ChatTool, 0, len(order))
	for _, name := range order {
		tools = append(tools, toolToChatTool(byName[name]))
	}
	return tools
}

// chatToolHandlers selects who, if anyone, owns execution for a chat request.
// Request-level declarations are wire contracts only; only matching tools
// explicitly registered with WithTools may execute. With no request-level
// declarations, WithTools replaces the defaults, and the SDK defaults are used
// only when the caller supplied no tools at all.
func (c *Client) chatToolHandlers(request ChatCompletionRequest) map[string]Tool {
	if request.Tools == nil {
		byName, _ := c.resolvedTools()
		return byName
	}
	allowed := make(map[string]bool, len(request.Tools))
	for _, declared := range request.Tools {
		if declared.Type == "function" && declared.Function.Name != "" {
			allowed[declared.Function.Name] = true
		}
	}
	return c.registeredToolHandlers(allowed)
}

// RunChatCompletion runs a bounded model-and-tool loop on top of the plain
// CreateChatCompletion endpoint. When the model returns tool_calls, each call
// whose name matches a registered tool is executed locally and its result is
// fed back to the model; calls with no registered handler are returned to the
// model as an error observation so it can adjust. The loop stops when the model
// returns a message with no tool_calls or when MaxSteps is reached.
//
// CreateChatCompletion delegates here only for SDK-owned defaults. Callers who
// supplied tools get primitive, non-executing behavior unless they explicitly
// invoke RunChatCompletion. If a later turn fails, the returned result is
// non-nil and retains every completed step and the accumulated transcript.
func (c *Client) RunChatCompletion(ctx context.Context, request ChatCompletionRequest, opts RunChatOptions) (*RunChatResult, error) {
	return c.runChatCompletion(ctx, request, opts, nil)
}

// runChatCompletion executes the chat loop, optionally continuing from an
// already-received first response. The latter avoids replaying and billing the
// initial request twice when a primitive promotes itself into a tool loop.
func (c *Client) runChatCompletion(ctx context.Context, request ChatCompletionRequest, opts RunChatOptions, first *ChatCompletionResponse) (*RunChatResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultToolMaxSteps
	}

	messages := append([]ChatMessage(nil), request.Messages...)
	result := &RunChatResult{StoppedBy: "max_steps"}
	handlers := c.chatToolHandlers(request)

	for step := 0; step < maxSteps; step++ {
		response := first
		if step > 0 || response == nil {
			stepRequest := request
			stepRequest.Messages = messages
			// createChatCompletionOnce owns the per-request tools decision. It
			// preserves request-level tools exactly, otherwise selects the registered
			// set or the SDK defaults according to the normal ownership rule.
			var err error
			response, err = c.createChatCompletionOnce(ctx, stepRequest)
			if err != nil {
				result.Messages = messages
				return result, err
			}
		}
		if len(response.Choices) == 0 {
			return nil, fmt.Errorf("chat completion returned no choices")
		}

		assistant := response.Choices[0].Message
		messages = append(messages, assistant)

		rawFinish := response.Choices[0].FinishReason
		kind := classifyFinishReason(rawFinish)

		toolResults, err := c.executeToolCallsWithHandlers(ctx, handlers, step, assistant.ToolCalls, messages)
		if err != nil {
			result.Messages = messages
			return result, err
		}

		result.Steps = append(result.Steps, ToolStep{
			Index:            step,
			AssistantMessage: assistant,
			ToolResults:      toolResults,
			Usage:            response.Usage,
			FinishReason:     rawFinish,
			response:         response,
		})

		// Vendor-neutral control flow: branch on the normalized kind, never on
		// the raw finish_reason string. A malformed tool call carries no usable
		// tool_calls, so without this the loop would fall through to the
		// "no tool_calls -> stop" case below and silently return empty text.
		// Instead we nudge the model to re-emit a valid payload and retry,
		// bounded by maxSteps.
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

// finalResponse returns the final model turn, including its own ID/model/usage,
// rather than mixing final content with metadata from an earlier tool turn.
func (r *RunChatResult) finalResponse(template *ChatCompletionResponse) *ChatCompletionResponse {
	var out ChatCompletionResponse
	if template != nil {
		out = *template
	}
	if len(r.Steps) > 0 {
		final := r.Steps[len(r.Steps)-1]
		if final.response != nil {
			out = *final.response
		}
		if len(out.Choices) == 0 {
			out.Choices = []ChatCompletionChoice{{}}
		}
		out.Choices[0].Message = final.AssistantMessage
		// Surface the loop's normalized finish reason rather than hardcoding
		// "stop", so a run that ended on max_steps or a malformed tool call is
		// not misreported as a clean stop. Fall back to the final step's raw
		// reason, then to "stop", when the loop recorded nothing.
		finishReason := r.FinishReason
		if finishReason == "" {
			finishReason = final.FinishReason
		}
		if finishReason == "" {
			finishReason = FinishStop.String()
		}
		out.Choices[0].FinishReason = finishReason
		if final.Usage != nil {
			out.Usage = final.Usage
		}
	}
	return &out
}

func (c *Client) executeToolCallsWithHandlers(ctx context.Context, handlers map[string]Tool, step int, calls []ToolCall, messages []ChatMessage) ([]ToolCallResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	results := make([]ToolCallResult, 0, len(calls))
	for _, call := range calls {
		result, err := runToolWithHandlers(ctx, handlers, call.ID, call.Function.Name, call.Function.Arguments, ToolExecutionContext{
			Step:     step,
			ToolCall: call,
			Messages: messages,
		})
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// runRegisteredTool resolves one model-requested tool call against the
// registered handlers and returns its ToolCallResult. It is protocol-agnostic
// (shared by the Chat Completions and Responses execution loops): callers pass
// the wire-specific identifiers (callID, name, raw JSON arguments) and an
// already-built execution context.
//
// Attribution rules, all captured here so every entry point behaves the same:
//   - name not registered -> IsError result "Tool not found" fed back to the
//     model; the call is never silently dropped and never executed on the
//     user's behalf.
//   - registered but Execute is nil -> hard error (misconfigured registration).
//   - execution error/panic or serialization failure -> IsError result fed
//     back to the model so it can adjust, without aborting the loop.
func (c *Client) runRegisteredTool(ctx context.Context, callID, name, arguments string, execution ToolExecutionContext) (ToolCallResult, error) {
	byName, _ := c.resolvedTools()
	return runToolWithHandlers(ctx, byName, callID, name, arguments, execution)
}

func runToolWithHandlers(ctx context.Context, handlers map[string]Tool, callID, name, arguments string, execution ToolExecutionContext) (ToolCallResult, error) {
	t, ok := handlers[name]
	if !ok {
		return ToolCallResult{
			ToolCallID: callID,
			ToolName:   name,
			Content:    "Tool not found: " + name,
			IsError:    true,
		}, nil
	}
	if t.Execute == nil {
		return ToolCallResult{}, fmt.Errorf("tool %q has no Execute function", t.Name)
	}
	output, err := safeExecuteTool(ctx, t.Execute, parseToolArguments(arguments), execution)
	if err != nil {
		return ToolCallResult{
			ToolCallID: callID,
			ToolName:   t.Name,
			Content:    "Tool error: " + err.Error(),
			IsError:    true,
		}, nil
	}
	content, err := stringifyToolResult(output)
	if err != nil {
		return ToolCallResult{
			ToolCallID: callID,
			ToolName:   t.Name,
			Content:    "Tool error: failed to serialize result: " + err.Error(),
			IsError:    true,
		}, nil
	}
	return ToolCallResult{ToolCallID: callID, ToolName: t.Name, Content: content}, nil
}

// messageText extracts a plain-text representation of a message's content.
func messageText(content any) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	return ""
}
