package joytoken

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
)

type RunResponseStreamOptions struct {
	MaxSteps     int
	OnTextDelta  func(delta string)
	OnToolResult func(result ToolCallResult)
}

// ResponseStream reads native Responses SSE events from the gateway.
type ResponseStream struct {
	body      io.ReadCloser
	scanner   *bufio.Scanner
	cancel    context.CancelFunc
	requestID string
}

func (c *Client) StreamResponse(ctx context.Context, request ResponseRequest) (*ResponseStream, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = true
	request.Tools = c.responseTools(request)
	requestCtx, cancel := c.withTimeout(ctx)
	req, err := c.newJSONRequest(requestCtx, http.MethodPost, c.openAIBaseURL+"/responses", request)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	res, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		cancel()
		return nil, parseAPIError(res)
	}

	return &ResponseStream{body: res.Body, scanner: newSSEScanner(res.Body), cancel: cancel, requestID: requestIDFromHeaders(res.Header)}, nil
}

func (s *ResponseStream) Recv() (*ResponseStreamEvent, error) {
	var event ResponseStreamEvent
	if err := recvSSEJSON(s.scanner, &event); err != nil {
		_ = s.Close()
		return nil, err
	}
	normalizeResponse(event.Response, s.requestID)
	if requestID := event.RequestID(); requestID != "" {
		s.requestID = requestID
	}
	return &event, nil
}

func (s *ResponseStream) Close() error {
	if s == nil || s.body == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	return s.body.Close()
}

// RunResponseStream executes the Responses tool loop while consuming each
// model turn as native SSE. Later-turn failures retain a non-nil partial result.
func (c *Client) RunResponseStream(ctx context.Context, request ResponseRequest, opts RunResponseStreamOptions) (*RunResponseResult, error) {
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
		response, err := c.streamOneResponseTurn(ctx, stepRequest, opts.OnTextDelta)
		if err != nil {
			result.Input = input
			return result, err
		}
		calls := functionCalls(response)
		input = appendResponseOutput(input, response)
		toolResults := make([]ToolCallResult, 0, len(calls))
		for _, call := range calls {
			toolResult, err := runToolWithHandlers(ctx, handlers, call.CallID, call.Name, call.Arguments, ToolExecutionContext{
				Step: step, ToolCall: ToolCall{
					ID: call.CallID, Type: "function", Function: ToolFunction{Name: call.Name, Arguments: call.Arguments}, ExtraContent: call.ExtraContent,
				},
				Messages: responseInputToChat(input, request.Instructions),
			})
			if err != nil {
				result.Input = input
				return result, err
			}
			toolResults = append(toolResults, toolResult)
			if opts.OnToolResult != nil {
				opts.OnToolResult(toolResult)
			}
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

func (c *Client) streamOneResponseTurn(ctx context.Context, request ResponseRequest, onTextDelta func(string)) (*Response, error) {
	stream, err := c.StreamResponse(ctx, request)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var final *Response
	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if event.Type == "response.output_text.delta" && event.Delta != "" && onTextDelta != nil {
			onTextDelta(event.Delta)
		}
		if event.Type == "response.completed" || event.Type == "response.incomplete" || event.Type == "response.failed" {
			final = event.Response
		}
	}
	if final == nil {
		final = &Response{Object: "response", Status: "completed"}
	}
	return final, nil
}
