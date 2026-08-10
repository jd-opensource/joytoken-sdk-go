package joytoken

// ChatMessage is an OpenAI-compatible conversation message.
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
}

// ChatToolFunction contains the schema for a callable function.
type ChatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatCompletionRequest is an OpenAI-compatible completion request.
type ChatCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []ChatMessage  `json:"messages"`
	Stream      bool           `json:"stream,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	Stop        any            `json:"stop,omitempty"`
	Tools       []ChatTool     `json:"tools,omitempty"`
	ToolChoice  any            `json:"tool_choice,omitempty"`
	Tier        string         `json:"tier,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ChatCompletionResponse is a non-streaming completion response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id,omitempty"`
	Object  string                 `json:"object,omitempty"`
	Created int64                  `json:"created,omitempty"`
	Model   string                 `json:"model,omitempty"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
}

// ChatCompletionChoice is one generated completion choice.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
	Logprobs     any         `json:"logprobs,omitempty"`
}

// ChatCompletionChunk is one streaming completion event.
type ChatCompletionChunk struct {
	ID      string                      `json:"id,omitempty"`
	Object  string                      `json:"object,omitempty"`
	Created int64                       `json:"created,omitempty"`
	Model   string                      `json:"model,omitempty"`
	Choices []ChatCompletionChunkChoice `json:"choices"`
	Usage   *Usage                      `json:"usage,omitempty"`
}

// ChatCompletionChunkChoice is one incremental streaming choice.
type ChatCompletionChunkChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Logprobs     any            `json:"logprobs,omitempty"`
}

// ResponseInputItem is one message in a Responses API input array. Content may
// be a string or a slice of ResponseInputContentPart values.
type ResponseInputItem struct {
	Type    string `json:"type,omitempty"`
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
}

// ResponseInputContentPart is one text part in a Responses API input message.
type ResponseInputContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponseTool declares a function available to the Responses API. JoyToken
// currently supports function tools; built-in OpenAI tools are not forwarded.
type ResponseTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ResponseRequest is a request to the OpenAI-compatible Responses API. Input
// may be a string or a slice of ResponseInputItem values.
type ResponseRequest struct {
	Model           string         `json:"model"`
	Input           any            `json:"input"`
	Instructions    string         `json:"instructions,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	MaxOutputTokens *int           `json:"max_output_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	Tools           []ResponseTool `json:"tools,omitempty"`
}

// ResponseOutputContent is one content part in a Responses API output message.
type ResponseOutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

// ResponseOutputItem is one item returned in a Responses API output array.
type ResponseOutputItem struct {
	ID      string                  `json:"id,omitempty"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role,omitempty"`
	Status  string                  `json:"status,omitempty"`
	Content []ResponseOutputContent `json:"content,omitempty"`
}

// ResponseUsage reports token usage using Responses API field names.
type ResponseUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// Response is a non-streaming Responses API result or the response envelope
// included in response.created and response.completed stream events.
type Response struct {
	ID       string               `json:"id"`
	Object   string               `json:"object"`
	Status   string               `json:"status"`
	Model    string               `json:"model"`
	Output   []ResponseOutputItem `json:"output,omitempty"`
	Usage    *ResponseUsage       `json:"usage,omitempty"`
	Metadata map[string]any       `json:"metadata,omitempty"`
}

// OutputText returns all output_text parts concatenated in output order.
func (r *Response) OutputText() string {
	if r == nil {
		return ""
	}
	var text string
	for _, item := range r.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				text += part.Text
			}
		}
	}
	return text
}

// ResponseStreamEvent is one SSE event returned by the Responses API. Fields
// are populated according to Type, for example Delta on
// response.output_text.delta and Response on response.completed.
type ResponseStreamEvent struct {
	Type           string                 `json:"type"`
	SequenceNumber int                    `json:"sequence_number"`
	Response       *Response              `json:"response,omitempty"`
	OutputIndex    int                    `json:"output_index,omitempty"`
	ContentIndex   int                    `json:"content_index,omitempty"`
	ItemID         string                 `json:"item_id,omitempty"`
	Item           *ResponseOutputItem    `json:"item,omitempty"`
	Part           *ResponseOutputContent `json:"part,omitempty"`
	Delta          string                 `json:"delta,omitempty"`
	Text           string                 `json:"text,omitempty"`
}

// ImageGenerationRequest is an OpenAI-compatible image generation request.
// Model and Prompt are required by the JoyToken gateway; other fields are
// forwarded to the selected image provider.
type ImageGenerationRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 *int   `json:"n,omitempty"`
	Quality           string `json:"quality,omitempty"`
	ResponseFormat    string `json:"response_format,omitempty"`
	Size              string `json:"size,omitempty"`
	Style             string `json:"style,omitempty"`
	User              string `json:"user,omitempty"`
	Background        string `json:"background,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
}

// GeneratedImage contains one URL or base64-encoded generated image.
type GeneratedImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageGenerationResponse is an OpenAI-compatible image generation result.
// Metadata contains JoyToken routing and billing details when available.
type ImageGenerationResponse struct {
	Created  int64            `json:"created,omitempty"`
	Data     []GeneratedImage `json:"data"`
	Metadata map[string]any   `json:"metadata,omitempty"`
}

// Usage reports token and cost information for a request.
type Usage struct {
	PromptTokens     int      `json:"prompt_tokens,omitempty"`
	CompletionTokens int      `json:"completion_tokens,omitempty"`
	TotalTokens      int      `json:"total_tokens,omitempty"`
	Cost             *float64 `json:"cost,omitempty"`
	TotalCost        *float64 `json:"total_cost,omitempty"`
}

// ModelListResponse contains the models available to the caller.
type ModelListResponse struct {
	Object string      `json:"object,omitempty"`
	Data   []ModelInfo `json:"data"`
}

// ModelInfo is a model summary returned by ListModels.
type ModelInfo struct {
	ID            string         `json:"id"`
	Name          string         `json:"name,omitempty"`
	Description   string         `json:"description,omitempty"`
	ContextLength int            `json:"context_length,omitempty"`
	Pricing       map[string]any `json:"pricing,omitempty"`
}

// CatalogOption is a value-label pair used by model catalog filters.
type CatalogOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ModelMetadata contains available model catalog filter values.
type ModelMetadata struct {
	Tiers         []CatalogOption `json:"tiers"`
	SKUs          []CatalogOption `json:"skus"`
	FeatureTags   []CatalogOption `json:"featureTags"`
	IndustryPacks []CatalogOption `json:"industryPacks"`
	Providers     []CatalogOption `json:"providers"`
	UpdatedAt     string          `json:"updatedAt"`
}

// ModelMetadataResponse wraps model catalog metadata.
type ModelMetadataResponse struct {
	Code    int           `json:"code"`
	Data    ModelMetadata `json:"data"`
	Message string        `json:"message"`
}

// PricingTier describes a JoyToken credit conversion tier.
type PricingTier struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	USDPerCredit  string `json:"usdPerCredit"`
	CreditsPerUSD string `json:"creditsPerUsd"`
	Unit          string `json:"unit"`
	RateVersion   string `json:"rateVersion"`
	SortOrder     int32  `json:"sortOrder"`
	UpdatedAt     string `json:"updatedAt"`
}

// PricingSKU describes an available pricing SKU.
type PricingSKU struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Pricing contains the current tier and SKU catalog.
type Pricing struct {
	Tiers          []PricingTier `json:"tiers"`
	SKUs           []PricingSKU  `json:"skus"`
	CurrentVersion string        `json:"currentVersion"`
	UpdatedAt      string        `json:"updatedAt"`
}

// PricingResponse wraps the current pricing catalog.
type PricingResponse struct {
	Code    int     `json:"code"`
	Data    Pricing `json:"data"`
	Message string  `json:"message"`
}

// MessageContentBlock is an Anthropic-compatible content block.
type MessageContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
}

// MessageParam is an Anthropic-compatible input message.
type MessageParam struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// MessageTool declares a tool available to Anthropic Messages.
type MessageTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// MessageRequest is an Anthropic-compatible Messages request.
type MessageRequest struct {
	Model       string         `json:"model"`
	MaxTokens   int            `json:"max_tokens"`
	Messages    []MessageParam `json:"messages"`
	System      any            `json:"system,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Tools       []MessageTool  `json:"tools,omitempty"`
	Tier        string         `json:"tier,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// MessageUsage reports Anthropic-compatible token usage.
type MessageUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// MessageResponse is a non-streaming Anthropic-compatible response.
type MessageResponse struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"`
	Role         string                `json:"role"`
	Content      []MessageContentBlock `json:"content"`
	Model        string                `json:"model"`
	StopReason   *string               `json:"stop_reason,omitempty"`
	StopSequence *string               `json:"stop_sequence,omitempty"`
	Usage        MessageUsage          `json:"usage"`
	Metadata     map[string]any        `json:"metadata,omitempty"`
}

// MessageStreamEvent is one Anthropic-compatible streaming event.
type MessageStreamEvent struct {
	Type         string               `json:"type"`
	Index        *int                 `json:"index,omitempty"`
	Message      *MessageResponse     `json:"message,omitempty"`
	ContentBlock *MessageContentBlock `json:"content_block,omitempty"`
	Delta        map[string]any       `json:"delta,omitempty"`
	Usage        *MessageUsage        `json:"usage,omitempty"`
	Error        map[string]any       `json:"error,omitempty"`
	Metadata     map[string]any       `json:"metadata,omitempty"`
}
