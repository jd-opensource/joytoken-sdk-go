package joytoken

import "github.com/jd-opensource/joytoken-sdk-go/tooldef"

// ModelAuto is the only model value accepted by JoyToken requests.
const ModelAuto = "auto"

// The tool-related wire types live in the tooldef package (the bottom of the
// dependency graph) so concrete tool implementations can be defined there and
// reused by this client's execution loop without an import cycle. They are
// re-exported here as type aliases (=) so every downstream reference to
// joytoken.ChatMessage etc. keeps working unchanged and JSON serialization is
// byte-for-byte identical.

// ChatMessage is an OpenAI-compatible conversation message.
type ChatMessage = tooldef.ChatMessage

// ToolCall describes a model-requested function call.
type ToolCall = tooldef.ToolCall

// ToolFunction identifies a function and its JSON arguments.
type ToolFunction = tooldef.ToolFunction

// ChatTool declares a tool available to Chat Completions.
type ChatTool = tooldef.ChatTool

// ChatToolFunction contains the schema for a callable function.
type ChatToolFunction = tooldef.ChatToolFunction

// ChatCompletionRequest is an OpenAI-compatible completion request. Model must
// be ModelAuto.
type ChatCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []ChatMessage  `json:"messages"`
	Stream      bool           `json:"stream,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	Stop        any            `json:"stop,omitempty"`
	Tools       []ChatTool     `json:"tools"`
	ToolChoice  any            `json:"tool_choice,omitempty"`
	Tier        string         `json:"tier,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ChatCompletionResponse is a non-streaming completion response.
type ChatCompletionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Object   string                 `json:"object,omitempty"`
	Created  int64                  `json:"created,omitempty"`
	Model    string                 `json:"model,omitempty"`
	Choices  []ChatCompletionChoice `json:"choices"`
	Usage    *Usage                 `json:"usage,omitempty"`
	Metadata map[string]any         `json:"metadata,omitempty"`
}

// RequestID returns the Gateway request ID carried in response metadata. The
// SDK also copies a successful HTTP request ID header here when one is present.
func (r *ChatCompletionResponse) RequestID() string {
	if r == nil {
		return ""
	}
	return RequestIDFromMetadata(r.Metadata)
}

// ChatCompletionChoice is one generated completion choice.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
	Logprobs     any         `json:"logprobs,omitempty"`
}

// ChatCompletionChunk is one streaming completion event. Gateway metadata and
// usage events may have an empty Choices slice; callers must check len(Choices)
// or range over it instead of indexing Choices[0] unconditionally.
type ChatCompletionChunk struct {
	ID       string                      `json:"id,omitempty"`
	Object   string                      `json:"object,omitempty"`
	Created  int64                       `json:"created,omitempty"`
	Model    string                      `json:"model,omitempty"`
	Choices  []ChatCompletionChunkChoice `json:"choices"`
	Usage    *Usage                      `json:"usage,omitempty"`
	Metadata map[string]any              `json:"metadata,omitempty"`
}

// RequestID returns the Gateway request ID carried by this stream event.
func (c *ChatCompletionChunk) RequestID() string {
	if c == nil {
		return ""
	}
	return RequestIDFromMetadata(c.Metadata)
}

// ChatCompletionChunkChoice is one incremental streaming choice.
type ChatCompletionChunkChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Logprobs     any            `json:"logprobs,omitempty"`
}

// ResponseInputItem is one OpenAI Responses-compatible input item. It covers
// message input plus the function_call/function_call_output items used by the
// SDK's native Responses tool loop.
type ResponseInputItem struct {
	Type             string `json:"type,omitempty"`
	ID               string `json:"id,omitempty"`
	Role             string `json:"role,omitempty"`
	Status           string `json:"status,omitempty"`
	Content          any    `json:"content,omitempty"`
	CallID           string `json:"call_id,omitempty"`
	Name             string `json:"name,omitempty"`
	Arguments        string `json:"arguments,omitempty"`
	Output           string `json:"output,omitempty"`
	Summary          []any  `json:"summary,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// ResponseInputContentPart is one text part in a Responses input message.
type ResponseInputContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponseTool declares either a function tool or a vendor-hosted Responses
// tool. Declarations are sent directly to the gateway's Responses endpoint.
type ResponseTool struct {
	Type string `json:"type"`

	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`

	SearchContextSize string                      `json:"search_context_size,omitempty"`
	UserLocation      *ResponseToolUserLocation   `json:"user_location,omitempty"`
	VectorStoreIDs    []string                    `json:"vector_store_ids,omitempty"`
	MaxNumResults     *int                        `json:"max_num_results,omitempty"`
	Filters           map[string]any              `json:"filters,omitempty"`
	RankingOptions    *ResponseToolRankingOptions `json:"ranking_options,omitempty"`
}

type ResponseToolUserLocation struct {
	Type     string `json:"type,omitempty"`
	Country  string `json:"country,omitempty"`
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type ResponseToolRankingOptions struct {
	Ranker         string   `json:"ranker,omitempty"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}

// ResponseRequest is an OpenAI Responses-compatible request sent directly to
// /openai/v1/responses. Model must be ModelAuto.
type ResponseRequest struct {
	Model              string         `json:"model"`
	Input              any            `json:"input"`
	Instructions       string         `json:"instructions,omitempty"`
	Stream             bool           `json:"stream,omitempty"`
	MaxOutputTokens    *int           `json:"max_output_tokens,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	Tools              []ResponseTool `json:"tools"`
	ToolChoice         any            `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Include            []string       `json:"include,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Tier               string         `json:"tier,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

type ResponseOutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

type ResponseOutputItem struct {
	ID               string                  `json:"id,omitempty"`
	Type             string                  `json:"type"`
	Role             string                  `json:"role,omitempty"`
	Status           string                  `json:"status,omitempty"`
	Content          []ResponseOutputContent `json:"content,omitempty"`
	CallID           string                  `json:"call_id,omitempty"`
	Name             string                  `json:"name,omitempty"`
	Arguments        string                  `json:"arguments,omitempty"`
	Summary          []any                   `json:"summary,omitempty"`
	EncryptedContent string                  `json:"encrypted_content,omitempty"`
	Action           map[string]any          `json:"action,omitempty"`
	Results          []any                   `json:"results,omitempty"`
}

type ResponseUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

type Response struct {
	ID                string               `json:"id"`
	Object            string               `json:"object"`
	CreatedAt         int64                `json:"created_at,omitempty"`
	Status            string               `json:"status"`
	Model             string               `json:"model"`
	Output            []ResponseOutputItem `json:"output,omitempty"`
	Usage             *ResponseUsage       `json:"usage,omitempty"`
	Metadata          map[string]any       `json:"metadata,omitempty"`
	Error             map[string]any       `json:"error,omitempty"`
	IncompleteDetails map[string]any       `json:"incomplete_details,omitempty"`
}

// RequestID returns the Gateway request ID carried in response metadata.
func (r *Response) RequestID() string {
	if r == nil {
		return ""
	}
	return RequestIDFromMetadata(r.Metadata)
}

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
	Arguments      string                 `json:"arguments,omitempty"`
	Error          map[string]any         `json:"error,omitempty"`
}

// RequestID returns the Gateway request ID from this event's response envelope.
func (e *ResponseStreamEvent) RequestID() string {
	if e == nil || e.Response == nil {
		return ""
	}
	return e.Response.RequestID()
}

// ImageGenerationRequest is an OpenAI-compatible image generation request.
// Model must be ModelAuto and Prompt is required by the JoyToken gateway;
// other fields are forwarded to the selected image provider.
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

// RequestID returns the Gateway request ID carried in image metadata.
func (r *ImageGenerationResponse) RequestID() string {
	if r == nil {
		return ""
	}
	return RequestIDFromMetadata(r.Metadata)
}

// Usage reports token and cost information for a request.
type Usage struct {
	PromptTokens     int      `json:"prompt_tokens,omitempty"`
	CompletionTokens int      `json:"completion_tokens,omitempty"`
	TotalTokens      int      `json:"total_tokens,omitempty"`
	Cost             *float64 `json:"cost,omitempty"`
	TotalCost        *float64 `json:"total_cost,omitempty"`
}

// ModelLocale selects the language used for localized model descriptions.
type ModelLocale string

const (
	// ModelLocaleZH requests Chinese model descriptions.
	ModelLocaleZH ModelLocale = "zh"
	// ModelLocaleEN requests English model descriptions.
	ModelLocaleEN ModelLocale = "en"
)

// ListModelsOptions configures a public model catalog request.
type ListModelsOptions struct {
	// Locale selects zh or en. An empty value leaves the parameter unset, so
	// the API returns its default English descriptions.
	Locale ModelLocale
}

// ModelListResponse contains the models available to the caller.
type ModelListResponse struct {
	Code    int           `json:"code,omitempty"`
	Message string        `json:"message,omitempty"`
	Object  string        `json:"object,omitempty"`
	Data    ModelListData `json:"data"`
}

// ModelListData is the data envelope returned by the model catalog.
type ModelListData struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo is a model summary returned by ListModels.
type ModelInfo struct {
	ModelID                      string   `json:"modelId,omitempty"`
	ModelKey                     string   `json:"modelKey,omitempty"`
	DisplayName                  string   `json:"displayName,omitempty"`
	Alias                        string   `json:"alias,omitempty"`
	Tier                         string   `json:"tier,omitempty"`
	Tags                         []string `json:"tags,omitempty"`
	Description                  string   `json:"description,omitempty"`
	CustomerInputMtok            float64  `json:"customerInputMtok,omitempty"`
	CustomerOutputMtok           float64  `json:"customerOutputMtok,omitempty"`
	CustomerCachereadMtok        float64  `json:"customerCachereadMtok,omitempty"`
	CustomerCachewriteMtok       float64  `json:"customerCachewriteMtok,omitempty"`
	CustomerImageInputMtok       string   `json:"customerImageInputMtok,omitempty"`
	CustomerImageOutputMtok      string   `json:"customerImageOutputMtok,omitempty"`
	CustomerImageCachedInputMtok string   `json:"customerImageCachedInputMtok,omitempty"`
	Provider                     string   `json:"provider,omitempty"`
	FeatureTags                  []string `json:"featureTags,omitempty"`
	ScenarioTags                 []string `json:"scenarioTags,omitempty"`
	MCIScore                     float64  `json:"mciScore,omitempty"`
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

// MessageContentBlock is an Anthropic Messages-compatible content block.
type MessageContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
}

type MessageParam struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type MessageTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// MessageToolChoice is the Anthropic-compatible tool_choice shape. Type may be
// "auto", "any", "tool", or "none"; Name is used with Type "tool".
type MessageToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// MessageRequest is an Anthropic Messages-compatible request translated to the
// gateway's Chat Completions endpoint.
type MessageRequest struct {
	Model       string         `json:"model"`
	MaxTokens   int            `json:"max_tokens"`
	Messages    []MessageParam `json:"messages"`
	System      any            `json:"system,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Tools       []MessageTool  `json:"tools,omitempty"`
	ToolChoice  any            `json:"tool_choice,omitempty"`
	Tier        string         `json:"tier,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type MessageUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

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

// RequestID returns the Gateway request ID preserved by the Messages adapter.
func (r *MessageResponse) RequestID() string {
	if r == nil {
		return ""
	}
	return RequestIDFromMetadata(r.Metadata)
}

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

// RequestID returns the Gateway request ID from event metadata or its message.
func (e *MessageStreamEvent) RequestID() string {
	if e == nil {
		return ""
	}
	if requestID := RequestIDFromMetadata(e.Metadata); requestID != "" {
		return requestID
	}
	if e.Message != nil {
		return e.Message.RequestID()
	}
	return ""
}
