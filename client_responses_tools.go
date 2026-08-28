package joytoken

import (
	"context"
	"fmt"
	"net/http"
)

type RunResponseOptions struct {
	// MaxSteps bounds model/tool round-trips. Zero uses the SDK default.
	MaxSteps int
}

type ResponseToolStep struct {
	Index       int
	Response    *Response
	ToolResults []ToolCallResult
	Usage       *ResponseUsage
}

type RunResponseResult struct {
	FinalText string
	Input     []ResponseInputItem
	Steps     []ResponseToolStep
	StoppedBy string
}

func builtinResponseTools() []ResponseTool {
	return []ResponseTool{{Type: "web_search_preview"}}
}

// responseTools applies the same ownership rule as chatTools: request-level
// tools are copied unchanged; otherwise WithTools replaces all defaults; only
// a call with no user tools receives the SDK local and hosted defaults.
func (c *Client) responseTools(request ResponseRequest) []ResponseTool {
	if request.Tools != nil {
		tools := make([]ResponseTool, len(request.Tools))
		copy(tools, request.Tools)
		return tools
	}
	byName, order := c.resolvedTools()
	tools := make([]ResponseTool, 0, len(order)+1)
	for _, name := range order {
		tool := byName[name]
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, ResponseTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters})
	}
	if len(c.toolOrder) == 0 && c.defaultBuiltinTools {
		for _, tool := range builtinResponseTools() {
			if !c.excludedDefaultTools[tool.Type] {
				tools = append(tools, tool)
			}
		}
	}
	return tools
}

func (c *Client) responseToolHandlers(request ResponseRequest) map[string]Tool {
	if request.Tools == nil {
		byName, _ := c.resolvedTools()
		return byName
	}
	allowed := make(map[string]bool, len(request.Tools))
	for _, tool := range request.Tools {
		if tool.Type == "function" && tool.Name != "" {
			allowed[tool.Name] = true
		}
	}
	return c.registeredToolHandlers(allowed)
}

func responseContentText(content any) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case []ResponseInputContentPart:
		var text string
		for _, part := range value {
			text += part.Text
		}
		return text
	case []ResponseOutputContent:
		var text string
		for _, part := range value {
			text += part.Text
		}
		return text
	case []any:
		var text string
		for _, raw := range value {
			if part, ok := raw.(map[string]any); ok {
				if value, ok := part["text"].(string); ok {
					text += value
				}
			}
		}
		return text
	default:
		return fmt.Sprint(value)
	}
}

func normalizeResponseInput(input any) []ResponseInputItem {
	switch value := input.(type) {
	case nil:
		return nil
	case string:
		if value == "" {
			return nil
		}
		return []ResponseInputItem{{Role: "user", Content: value}}
	case []ResponseInputItem:
		return append([]ResponseInputItem(nil), value...)
	default:
		return []ResponseInputItem{{Role: "user", Content: responseContentText(value)}}
	}
}

func responseInputToChat(input []ResponseInputItem, instructions string) []ChatMessage {
	messages := make([]ChatMessage, 0, len(input)+1)
	if instructions != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: instructions})
	}
	for _, item := range input {
		switch item.Type {
		case "function_call":
			call := ToolCall{ID: item.CallID, Type: "function", Function: ToolFunction{Name: item.Name, Arguments: item.Arguments}}
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
				messages[len(messages)-1].ToolCalls = append(messages[len(messages)-1].ToolCalls, call)
			} else {
				messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: []ToolCall{call}})
			}
		case "function_call_output":
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: item.CallID, Content: item.Output})
		case "reasoning", "web_search_call", "file_search_call":
			// These native Responses items have no Chat message equivalent. They
			// remain in the Responses transcript but are intentionally omitted
			// from the local ToolExecutionContext's Chat-style view.
		default:
			role := item.Role
			if role == "" {
				role = "user"
			}
			messages = append(messages, ChatMessage{Role: role, Content: responseContentText(item.Content)})
		}
	}
	return messages
}

func responseOutputToInput(item ResponseOutputItem) ResponseInputItem {
	return ResponseInputItem{
		Type: item.Type, ID: item.ID, Role: item.Role, Status: item.Status,
		Content: item.Content, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments,
		Summary: append([]any(nil), item.Summary...), EncryptedContent: item.EncryptedContent,
	}
}

func appendResponseOutput(input []ResponseInputItem, response *Response) []ResponseInputItem {
	if response == nil {
		return input
	}
	for _, item := range response.Output {
		input = append(input, responseOutputToInput(item))
	}
	return input
}

func functionCalls(response *Response) []ResponseOutputItem {
	if response == nil {
		return nil
	}
	var calls []ResponseOutputItem
	for _, item := range response.Output {
		if item.Type == "function_call" {
			calls = append(calls, item)
		}
	}
	return calls
}

func (r *RunResponseResult) finalResponse(fallback *Response) *Response {
	if len(r.Steps) == 0 {
		return fallback
	}
	return r.Steps[len(r.Steps)-1].Response
}

// CreateResponse sends an OpenAI Responses-compatible request to the gateway's
// native Responses endpoint. User-owned tools remain primitive; only the SDK
// fallback set auto-runs when no request-level or registered tools exist.
func (c *Client) CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error) {
	if request.Tools != nil || len(c.toolOrder) > 0 {
		return c.createResponseOnce(ctx, request)
	}
	run, err := c.RunResponse(ctx, request, RunResponseOptions{})
	if err != nil {
		return nil, err
	}
	return run.finalResponse(nil), nil
}

func (c *Client) createResponseOnce(ctx context.Context, request ResponseRequest) (*Response, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = false
	request.Tools = c.responseTools(request)
	var response Response
	if err := c.requestJSON(ctx, http.MethodPost, c.openAIBaseURL+"/responses", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// RunResponse executes native Responses function calls until the model returns
// a final output or MaxSteps is reached. A later-turn failure returns a non-nil
// partial result containing completed steps and accumulated input items.
func (c *Client) RunResponse(ctx context.Context, request ResponseRequest, opts RunResponseOptions) (*RunResponseResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultToolMaxSteps
	}
	input := normalizeResponseInput(request.Input)
	result := &RunResponseResult{StoppedBy: "max_steps"}
	handlers := c.responseToolHandlers(request)
	effectiveTools := c.responseTools(request)
	for step := 0; step < maxSteps; step++ {
		stepRequest := request
		stepRequest.Input = input
		stepRequest.Tools = effectiveTools
		response, err := c.createResponseOnce(ctx, stepRequest)
		if err != nil {
			result.Input = input
			return result, err
		}
		calls := functionCalls(response)
		input = appendResponseOutput(input, response)
		toolResults := make([]ToolCallResult, 0, len(calls))
		for _, call := range calls {
			toolResult, err := runToolWithHandlers(ctx, handlers, call.CallID, call.Name, call.Arguments, ToolExecutionContext{
				Step: step, ToolCall: ToolCall{ID: call.CallID, Type: "function", Function: ToolFunction{Name: call.Name, Arguments: call.Arguments}},
				Messages: responseInputToChat(input, request.Instructions),
			})
			if err != nil {
				result.Input = input
				return result, err
			}
			toolResults = append(toolResults, toolResult)
		}
		result.Steps = append(result.Steps, ResponseToolStep{Index: step, Response: response, ToolResults: toolResults, Usage: response.Usage})
		if len(calls) == 0 {
			result.FinalText = response.OutputText()
			result.StoppedBy = "stop"
			break
		}
		for _, toolResult := range toolResults {
			input = append(input, ResponseInputItem{Type: "function_call_output", CallID: toolResult.ToolCallID, Output: toolResult.Content})
		}
		request.ToolChoice = continuationToolChoice(request.ToolChoice)
	}
	result.Input = input
	return result, nil
}
