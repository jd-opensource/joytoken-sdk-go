package joytoken

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type RunMessageOptions struct {
	// MaxSteps bounds model/tool round-trips. Zero uses the SDK default.
	MaxSteps int
}

// RunMessageStreamOptions configures an Anthropic-compatible streaming tool
// loop. It mirrors the Chat and Responses streaming run options while keeping
// the wire request on the gateway's single Chat Completions endpoint.
type RunMessageStreamOptions struct {
	// MaxSteps bounds model/tool round-trips. Zero uses the SDK default.
	MaxSteps int
	// OnTextDelta receives each Messages-compatible text increment.
	OnTextDelta func(delta string)
	// OnToolResult runs after each local tool execution.
	OnToolResult func(result ToolCallResult)
}

type MessageToolStep struct {
	Index       int
	Response    *MessageResponse
	ToolResults []ToolCallResult
	Usage       *MessageUsage
}

type RunMessageResult struct {
	FinalText string
	Messages  []MessageParam
	Steps     []MessageToolStep
	StoppedBy string
}

func toMessageTool(tool Tool) MessageTool {
	schema := tool.Parameters
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return MessageTool{Name: tool.Name, Description: tool.Description, InputSchema: schema}
}

func (c *Client) messageTools(request MessageRequest) []MessageTool {
	if request.Tools != nil {
		tools := make([]MessageTool, len(request.Tools))
		copy(tools, request.Tools)
		return tools
	}
	byName, order := c.resolvedTools()
	tools := make([]MessageTool, 0, len(order))
	for _, name := range order {
		tools = append(tools, toMessageTool(byName[name]))
	}
	return tools
}

func (c *Client) messageToolHandlers(request MessageRequest) map[string]Tool {
	if request.Tools == nil {
		byName, _ := c.resolvedTools()
		return byName
	}
	allowed := make(map[string]bool, len(request.Tools))
	for _, tool := range request.Tools {
		if tool.Name != "" {
			allowed[tool.Name] = true
		}
	}
	return c.registeredToolHandlers(allowed)
}

func messageToolToChat(tool MessageTool) ChatTool {
	return ChatTool{Type: "function", Function: ChatToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}}
}

func messageBlocks(content any) []MessageContentBlock {
	switch value := content.(type) {
	case []MessageContentBlock:
		return append([]MessageContentBlock(nil), value...)
	case string:
		if value == "" {
			return nil
		}
		return []MessageContentBlock{{Type: "text", Text: value}}
	case []any:
		blocks := make([]MessageContentBlock, 0, len(value))
		for _, raw := range value {
			data, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var block MessageContentBlock
			if json.Unmarshal(data, &block) == nil {
				blocks = append(blocks, block)
			}
		}
		return blocks
	default:
		return nil
	}
}

func anthropicSystemText(system any) string {
	blocks := messageBlocks(system)
	if len(blocks) == 0 {
		if text, ok := system.(string); ok {
			return text
		}
	}
	var text []string
	for _, block := range blocks {
		if block.Text != "" {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n\n")
}

func messageInputToChat(request MessageRequest) []ChatMessage {
	messages := make([]ChatMessage, 0, len(request.Messages)+1)
	if system := anthropicSystemText(request.System); system != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: system})
	}
	for _, message := range request.Messages {
		blocks := messageBlocks(message.Content)
		if message.Role == "assistant" {
			var text string
			var calls []ToolCall
			for _, block := range blocks {
				switch block.Type {
				case "text":
					text += block.Text
				case "tool_use":
					arguments, _ := json.Marshal(block.Input)
					calls = append(calls, ToolCall{ID: block.ID, Type: "function", Function: ToolFunction{Name: block.Name, Arguments: string(arguments)}})
				}
			}
			messages = append(messages, ChatMessage{Role: "assistant", Content: text, ToolCalls: calls})
			continue
		}
		var userText string
		flushText := func() {
			if userText != "" {
				messages = append(messages, ChatMessage{Role: "user", Content: userText})
				userText = ""
			}
		}
		for _, block := range blocks {
			switch block.Type {
			case "tool_result":
				flushText()
				messages = append(messages, ChatMessage{Role: "tool", ToolCallID: block.ToolUseID, Content: responseContentText(block.Content)})
			case "text":
				userText += block.Text
			}
		}
		flushText()
		if len(blocks) == 0 {
			messages = append(messages, ChatMessage{Role: "user", Content: responseContentText(message.Content)})
		}
	}
	return messages
}

func anthropicToolChoice(choice any) any {
	if typed, ok := choice.(MessageToolChoice); ok {
		choice = map[string]any{"type": typed.Type, "name": typed.Name}
	}
	if typed, ok := choice.(*MessageToolChoice); ok && typed != nil {
		choice = map[string]any{"type": typed.Type, "name": typed.Name}
	}
	object, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	typeName, _ := object["type"].(string)
	switch typeName {
	case "any":
		return "required"
	case "tool":
		name, _ := object["name"].(string)
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	case "auto", "none":
		return typeName
	default:
		return choice
	}
}

func messageRequestToChat(request MessageRequest, tools []MessageTool) ChatCompletionRequest {
	chatTools := make([]ChatTool, 0, len(tools))
	for _, tool := range tools {
		chatTools = append(chatTools, messageToolToChat(tool))
	}
	var maxTokens *int
	if request.MaxTokens > 0 {
		value := request.MaxTokens
		maxTokens = &value
	}
	return ChatCompletionRequest{
		Model: request.Model, Messages: messageInputToChat(request), Temperature: request.Temperature,
		MaxTokens: maxTokens, Tools: chatTools, ToolChoice: anthropicToolChoice(request.ToolChoice),
		Tier: request.Tier, Metadata: request.Metadata,
	}
}

func chatStopReason(reason string, hasTools bool) string {
	if hasTools {
		return "tool_use"
	}
	switch classifyFinishReason(reason) {
	case FinishLength:
		return "max_tokens"
	case FinishContentFilter:
		return "refusal"
	default:
		return "end_turn"
	}
}

func chatResponseToMessage(chat *ChatCompletionResponse) *MessageResponse {
	response := &MessageResponse{Type: "message", Role: "assistant"}
	if chat == nil {
		return response
	}
	response.ID = chat.ID
	response.Model = chat.Model
	response.Metadata = chat.Metadata
	if chat.Usage != nil {
		response.Usage = MessageUsage{InputTokens: chat.Usage.PromptTokens, OutputTokens: chat.Usage.CompletionTokens}
	}
	if len(chat.Choices) == 0 {
		return response
	}
	choice := chat.Choices[0]
	if text := messageText(choice.Message.Content); text != "" {
		response.Content = append(response.Content, MessageContentBlock{Type: "text", Text: text})
	}
	for _, call := range choice.Message.ToolCalls {
		input := map[string]any{}
		_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
		response.Content = append(response.Content, MessageContentBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input})
	}
	stopReason := chatStopReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	response.StopReason = &stopReason
	return response
}

func toolUseBlocks(response *MessageResponse) []MessageContentBlock {
	if response == nil {
		return nil
	}
	var blocks []MessageContentBlock
	for _, block := range response.Content {
		if block.Type == "tool_use" {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func messageResponseText(response *MessageResponse) string {
	if response == nil {
		return ""
	}
	var text string
	for _, block := range response.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}

func (r *RunMessageResult) finalResponse(fallback *MessageResponse) *MessageResponse {
	if len(r.Steps) == 0 {
		return fallback
	}
	return r.Steps[len(r.Steps)-1].Response
}

// CreateMessage exposes Anthropic Messages semantics over the gateway's single
// Chat Completions endpoint.
func (c *Client) CreateMessage(ctx context.Context, request MessageRequest) (*MessageResponse, error) {
	if request.Tools != nil || len(c.toolOrder) > 0 {
		return c.createMessageOnce(ctx, request)
	}
	run, err := c.RunMessage(ctx, request, RunMessageOptions{})
	if err != nil {
		return nil, err
	}
	return run.finalResponse(nil), nil
}

func (c *Client) createMessageOnce(ctx context.Context, request MessageRequest) (*MessageResponse, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	effectiveTools := c.messageTools(request)
	chat, err := c.createChatCompletionOnce(ctx, messageRequestToChat(request, effectiveTools))
	if err != nil {
		return nil, err
	}
	return chatResponseToMessage(chat), nil
}

// RunMessage executes Anthropic-compatible tool_use blocks until the model
// returns a final message or MaxSteps is reached. A later-turn failure returns
// a non-nil partial result containing completed steps and the transcript.
func (c *Client) RunMessage(ctx context.Context, request MessageRequest, opts RunMessageOptions) (*RunMessageResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultToolMaxSteps
	}
	messages := append([]MessageParam(nil), request.Messages...)
	result := &RunMessageResult{StoppedBy: "max_steps"}
	handlers := c.messageToolHandlers(request)
	effectiveTools := c.messageTools(request)
	for step := 0; step < maxSteps; step++ {
		stepRequest := request
		stepRequest.Messages = messages
		stepRequest.Tools = effectiveTools
		response, err := c.createMessageOnce(ctx, stepRequest)
		if err != nil {
			result.Messages = messages
			return result, err
		}
		blocks := toolUseBlocks(response)
		toolResults := make([]ToolCallResult, 0, len(blocks))
		if len(blocks) > 0 {
			messages = append(messages, MessageParam{Role: "assistant", Content: response.Content})
		}
		for _, block := range blocks {
			arguments, _ := json.Marshal(block.Input)
			executionRequest := stepRequest
			executionRequest.Messages = messages
			toolResult, err := runToolWithHandlers(ctx, handlers, block.ID, block.Name, string(arguments), ToolExecutionContext{
				Step: step, ToolCall: ToolCall{ID: block.ID, Type: "function", Function: ToolFunction{Name: block.Name, Arguments: string(arguments)}},
				Messages: messageInputToChat(executionRequest),
			})
			if err != nil {
				result.Messages = messages
				return result, err
			}
			toolResults = append(toolResults, toolResult)
		}
		result.Steps = append(result.Steps, MessageToolStep{Index: step, Response: response, ToolResults: toolResults, Usage: &response.Usage})
		if len(blocks) == 0 {
			result.FinalText = messageResponseText(response)
			result.StoppedBy = "stop"
			break
		}
		results := make([]MessageContentBlock, 0, len(toolResults))
		for _, toolResult := range toolResults {
			results = append(results, MessageContentBlock{Type: "tool_result", ToolUseID: toolResult.ToolCallID, Content: toolResult.Content})
		}
		messages = append(messages, MessageParam{Role: "user", Content: results})
		request.ToolChoice = continuationToolChoice(request.ToolChoice)
	}
	result.Messages = messages
	return result, nil
}

// RunMessageStream runs a bounded Anthropic-compatible tool loop while each
// model turn is consumed as SSE. Text deltas and local tool results are exposed
// through callbacks, and the returned result contains the complete Messages
// transcript and per-turn responses.
func (c *Client) RunMessageStream(ctx context.Context, request MessageRequest, opts RunMessageStreamOptions) (*RunMessageResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultToolMaxSteps
	}
	messages := append([]MessageParam(nil), request.Messages...)
	result := &RunMessageResult{StoppedBy: "max_steps"}
	handlers := c.messageToolHandlers(request)
	effectiveTools := c.messageTools(request)
	for step := 0; step < maxSteps; step++ {
		stepRequest := request
		stepRequest.Messages = messages
		stepRequest.Tools = effectiveTools
		response, err := c.streamOneMessageTurn(ctx, stepRequest, opts.OnTextDelta)
		if err != nil {
			result.Messages = messages
			return result, err
		}
		blocks := toolUseBlocks(response)
		toolResults := make([]ToolCallResult, 0, len(blocks))
		if len(blocks) > 0 {
			messages = append(messages, MessageParam{Role: "assistant", Content: response.Content})
		}
		for _, block := range blocks {
			arguments, _ := json.Marshal(block.Input)
			executionRequest := stepRequest
			executionRequest.Messages = messages
			toolResult, err := runToolWithHandlers(ctx, handlers, block.ID, block.Name, string(arguments), ToolExecutionContext{
				Step: step, ToolCall: ToolCall{ID: block.ID, Type: "function", Function: ToolFunction{Name: block.Name, Arguments: string(arguments)}},
				Messages: messageInputToChat(executionRequest),
			})
			if err != nil {
				result.Messages = messages
				return result, err
			}
			toolResults = append(toolResults, toolResult)
			if opts.OnToolResult != nil {
				opts.OnToolResult(toolResult)
			}
		}
		result.Steps = append(result.Steps, MessageToolStep{Index: step, Response: response, ToolResults: toolResults, Usage: &response.Usage})
		if len(blocks) == 0 {
			result.FinalText = messageResponseText(response)
			result.StoppedBy = "stop"
			break
		}
		results := make([]MessageContentBlock, 0, len(toolResults))
		for _, toolResult := range toolResults {
			results = append(results, MessageContentBlock{Type: "tool_result", ToolUseID: toolResult.ToolCallID, Content: toolResult.Content})
		}
		messages = append(messages, MessageParam{Role: "user", Content: results})
		request.ToolChoice = continuationToolChoice(request.ToolChoice)
	}
	result.Messages = messages
	return result, nil
}

func (c *Client) streamOneMessageTurn(ctx context.Context, request MessageRequest, onTextDelta func(string)) (*MessageResponse, error) {
	stream, err := c.StreamMessage(ctx, request)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	response := &MessageResponse{Type: "message", Role: "assistant", Model: request.Model}
	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				response.ID = event.Message.ID
				response.Type = event.Message.Type
				response.Role = event.Message.Role
				response.Model = event.Message.Model
				response.Usage = event.Message.Usage
				response.Metadata = event.Message.Metadata
			}
		case "content_block_start":
			if event.Index == nil || event.ContentBlock == nil {
				continue
			}
			for len(response.Content) <= *event.Index {
				response.Content = append(response.Content, MessageContentBlock{})
			}
			response.Content[*event.Index] = *event.ContentBlock
		case "content_block_delta":
			if event.Index == nil {
				continue
			}
			for len(response.Content) <= *event.Index {
				response.Content = append(response.Content, MessageContentBlock{})
			}
			text, _ := event.Delta["text"].(string)
			if text != "" {
				response.Content[*event.Index].Type = "text"
				response.Content[*event.Index].Text += text
				if onTextDelta != nil {
					onTextDelta(text)
				}
			}
		case "message_delta":
			if reason, ok := event.Delta["stop_reason"].(string); ok {
				response.StopReason = &reason
			}
			if sequence, ok := event.Delta["stop_sequence"].(string); ok {
				response.StopSequence = &sequence
			}
			if event.Usage != nil {
				response.Usage = *event.Usage
			}
			if event.Metadata != nil {
				response.Metadata = event.Metadata
			}
		}
	}
	if stream.id != "" {
		response.ID = stream.id
	}
	if stream.model != "" {
		response.Model = stream.model
	}
	if stream.usage != nil {
		response.Usage = MessageUsage{InputTokens: stream.usage.PromptTokens, OutputTokens: stream.usage.CompletionTokens}
	}
	if stream.metadata != nil {
		response.Metadata = stream.metadata
	}
	return response, nil
}

// MessageStream adapts Chat Completions SSE into Anthropic Messages events.
type MessageStream struct {
	chat        *ChatCompletionStream
	queue       []*MessageStreamEvent
	started     bool
	textStarted bool
	completed   bool
	id          string
	model       string
	usage       *Usage
	metadata    map[string]any
	tools       *toolCallAccumulator
}

func (c *Client) StreamMessage(ctx context.Context, request MessageRequest) (*MessageStream, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	effectiveTools := c.messageTools(request)
	chat, err := c.StreamChatCompletion(ctx, messageRequestToChat(request, effectiveTools))
	if err != nil {
		return nil, err
	}
	return &MessageStream{chat: chat, model: request.Model, tools: newToolCallAccumulator()}, nil
}

func messageIndex(value int) *int { return &value }

func (s *MessageStream) pop() *MessageStreamEvent {
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event
}

func (s *MessageStream) Recv() (*MessageStreamEvent, error) {
	if len(s.queue) > 0 {
		return s.pop(), nil
	}
	if !s.started {
		s.started = true
		return &MessageStreamEvent{Type: "message_start", Message: &MessageResponse{ID: s.id, Type: "message", Role: "assistant", Model: s.model}}, nil
	}
	if s.completed {
		return nil, io.EOF
	}
	for {
		chunk, err := s.chat.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
			if s.textStarted {
				s.queue = append(s.queue, &MessageStreamEvent{Type: "content_block_stop", Index: messageIndex(0)})
			}
			index := 1
			if !s.textStarted {
				index = 0
			}
			calls := s.tools.toolCalls()
			for _, call := range calls {
				input := map[string]any{}
				_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
				s.queue = append(s.queue,
					&MessageStreamEvent{Type: "content_block_start", Index: messageIndex(index), ContentBlock: &MessageContentBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input}},
					&MessageStreamEvent{Type: "content_block_stop", Index: messageIndex(index)},
				)
				index++
			}
			stopReason := chatStopReason("stop", len(calls) > 0)
			usage := &MessageUsage{}
			if s.usage != nil {
				usage.InputTokens = s.usage.PromptTokens
				usage.OutputTokens = s.usage.CompletionTokens
			}
			s.queue = append(s.queue,
				&MessageStreamEvent{Type: "message_delta", Delta: map[string]any{"stop_reason": stopReason}, Usage: usage, Metadata: s.metadata},
				&MessageStreamEvent{Type: "message_stop"},
			)
			s.completed = true
			return s.pop(), nil
		}
		if chunk.ID != "" {
			s.id = chunk.ID
		}
		if chunk.Model != "" {
			s.model = chunk.Model
		}
		if chunk.Usage != nil {
			s.usage = chunk.Usage
		}
		if chunk.Metadata != nil {
			s.metadata = chunk.Metadata
		}
		for _, choice := range chunk.Choices {
			s.tools.add(choice.Delta)
			if delta := deltaText(choice.Delta); delta != "" {
				if !s.textStarted {
					s.textStarted = true
					s.queue = append(s.queue, &MessageStreamEvent{Type: "content_block_start", Index: messageIndex(0), ContentBlock: &MessageContentBlock{Type: "text"}})
				}
				s.queue = append(s.queue, &MessageStreamEvent{Type: "content_block_delta", Index: messageIndex(0), Delta: map[string]any{"type": "text_delta", "text": delta}})
				return s.pop(), nil
			}
		}
	}
}

func (s *MessageStream) Close() error {
	if s == nil || s.chat == nil {
		return nil
	}
	return s.chat.Close()
}
