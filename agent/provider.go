package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// NewJoyTokenProvider creates a ModelProvider backed by a JoyToken Client.
func NewJoyTokenProvider(client *joytoken.Client, opts ...ProviderOption) *JoyTokenProvider {
	provider := &JoyTokenProvider{
		Client:   client,
		Protocol: OpenAIProtocol,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(provider)
		}
	}
	return provider
}

// Complete sends one provider-neutral model request to JoyToken.
func (p *JoyTokenProvider) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if p == nil || p.Client == nil {
		return ModelResponse{}, fmt.Errorf("joytoken provider client is required")
	}
	if p.Protocol == AnthropicProtocol {
		return p.completeAnthropic(ctx, request)
	}

	response, err := p.Client.CreateChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model:       joytoken.ModelAuto,
		Messages:    request.Messages,
		Temperature: request.Temperature,
		MaxTokens:   request.MaxTokens,
		Tools:       request.Tools,
		Tier:        request.Tier,
		Metadata:    request.Metadata,
	})
	if err != nil {
		return ModelResponse{}, err
	}
	if len(response.Choices) == 0 {
		return ModelResponse{}, fmt.Errorf("joytoken did not return a chat completion message")
	}
	return ModelResponse{Message: response.Choices[0].Message, Usage: response.Usage, Raw: response}, nil
}

func (p *JoyTokenProvider) completeAnthropic(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	response, err := p.Client.CreateMessage(ctx, toAnthropicRequest(request))
	if err != nil {
		return ModelResponse{}, err
	}
	return ModelResponse{
		Message: normalizeAnthropicMessage(response),
		Usage:   normalizeAnthropicUsage(response),
		Raw:     response,
	}, nil
}

func toAnthropicRequest(request ModelRequest) joytoken.MessageRequest {
	systemBlocks := make([]string, 0)
	messages := make([]joytoken.MessageParam, 0, len(request.Messages))
	for _, message := range request.Messages {
		switch {
		case message.Role == "system":
			if text := textContent(message.Content); text != "" {
				systemBlocks = append(systemBlocks, text)
			}
		case message.Role == "tool":
			appendAnthropicMessage(&messages, joytoken.MessageParam{
				Role: "user",
				Content: []joytoken.MessageContentBlock{{
					Type:      "tool_result",
					ToolUseID: valueOrDefault(message.ToolCallID, "unknown"),
					Content:   textContent(message.Content),
				}},
			})
		case message.Role == "assistant" && len(message.ToolCalls) > 0:
			content := make([]joytoken.MessageContentBlock, 0, len(message.ToolCalls)+1)
			if text := textContent(message.Content); text != "" {
				content = append(content, joytoken.MessageContentBlock{Type: "text", Text: text})
			}
			for _, toolCall := range message.ToolCalls {
				content = append(content, joytoken.MessageContentBlock{
					Type:  "tool_use",
					ID:    toolCall.ID,
					Name:  toolCall.Function.Name,
					Input: parseToolInput(toolCall.Function.Arguments),
				})
			}
			appendAnthropicMessage(&messages, joytoken.MessageParam{Role: "assistant", Content: content})
		default:
			role := message.Role
			if role != "assistant" {
				role = "user"
			}
			appendAnthropicMessage(&messages, joytoken.MessageParam{Role: role, Content: anthropicContent(message.Content)})
		}
	}

	maxTokens := 1024
	if request.MaxTokens != nil {
		maxTokens = *request.MaxTokens
	}
	return joytoken.MessageRequest{
		Model:       joytoken.ModelAuto,
		MaxTokens:   maxTokens,
		Messages:    messages,
		System:      joinSystemBlocks(systemBlocks),
		Temperature: request.Temperature,
		Tools:       anthropicTools(request.Tools),
		Tier:        request.Tier,
		Metadata:    request.Metadata,
	}
}

func appendAnthropicMessage(messages *[]joytoken.MessageParam, message joytoken.MessageParam) {
	items := *messages
	if len(items) > 0 {
		previous := &items[len(items)-1]
		previousBlocks, previousOK := previous.Content.([]joytoken.MessageContentBlock)
		messageBlocks, messageOK := message.Content.([]joytoken.MessageContentBlock)
		if previous.Role == message.Role && previousOK && messageOK {
			previous.Content = append(previousBlocks, messageBlocks...)
			return
		}
	}
	*messages = append(items, message)
}

func anthropicContent(content any) any {
	if blocks, ok := content.([]joytoken.MessageContentBlock); ok {
		return blocks
	}
	if text, ok := content.(string); ok {
		return text
	}
	return ""
}

func joinSystemBlocks(blocks []string) any {
	if len(blocks) == 0 {
		return nil
	}
	return strings.Join(blocks, "\n\n")
}

func anthropicTools(tools []joytoken.ChatTool) []joytoken.MessageTool {
	if len(tools) == 0 {
		return nil
	}
	converted := make([]joytoken.MessageTool, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Function.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		converted = append(converted, joytoken.MessageTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: parameters,
		})
	}
	return converted
}

func parseToolInput(arguments string) map[string]any {
	if arguments == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return map[string]any{}
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return object
}

func normalizeAnthropicMessage(response *joytoken.MessageResponse) joytoken.ChatMessage {
	if response == nil {
		return joytoken.ChatMessage{Role: "assistant"}
	}
	text := make([]string, 0)
	toolCalls := make([]joytoken.ToolCall, 0)
	for _, block := range response.Content {
		if block.Type == "text" && block.Text != "" {
			text = append(text, block.Text)
		}
		if block.Type == "tool_use" && block.ID != "" && block.Name != "" {
			arguments, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, joytoken.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: joytoken.ToolFunction{
					Name:      block.Name,
					Arguments: string(arguments),
				},
			})
		}
	}
	var content any
	if len(text) > 0 {
		content = strings.Join(text, "")
	}
	return joytoken.ChatMessage{Role: "assistant", Content: content, ToolCalls: toolCalls}
}

func normalizeAnthropicUsage(response *joytoken.MessageResponse) *joytoken.Usage {
	if response == nil {
		return nil
	}
	total := response.Usage.InputTokens + response.Usage.OutputTokens
	return &joytoken.Usage{
		PromptTokens:     response.Usage.InputTokens,
		CompletionTokens: response.Usage.OutputTokens,
		TotalTokens:      total,
	}
}
