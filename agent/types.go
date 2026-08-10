// Package agent provides tool-calling agent helpers built on the JoyToken client.
package agent

import (
	"context"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// ModelProvider is the model interface used by Agent.
type ModelProvider interface {
	Complete(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

// ModelRequest is the provider-neutral request used by an agent loop.
type ModelRequest struct {
	Messages    []joytoken.ChatMessage
	Tools       []joytoken.ChatTool
	Temperature *float64
	MaxTokens   *int
	Tier        string
	Metadata    map[string]any
}

// ModelResponse is the provider-neutral model response used by an agent loop.
type ModelResponse struct {
	Message joytoken.ChatMessage
	Usage   *joytoken.Usage
	Raw     any
}

// AgentTool describes a function the model can call.
type AgentTool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, input any, execution ToolExecutionContext) (any, error)
}

// ToolExecutionContext contains the state available to a tool invocation.
type ToolExecutionContext struct {
	Step     int
	ToolCall joytoken.ToolCall
	Messages []joytoken.ChatMessage
}

// AgentOptions configures an Agent.
type AgentOptions struct {
	Model       ModelProvider
	System      string
	Tools       []AgentTool
	StopWhen    []StopCondition
	Temperature *float64
	MaxTokens   *int
	Tier        string
	Metadata    map[string]any
}

// AgentRunOptions configures one Agent run.
type AgentRunOptions struct {
	Messages []joytoken.ChatMessage
	Input    string
	MaxSteps *int
	Metadata map[string]any
}

// AgentResult is the final state of an agent run.
type AgentResult struct {
	FinalText string
	Messages  []joytoken.ChatMessage
	Steps     []AgentStep
	Usage     UsageSummary
	StoppedBy string
}

// AgentStep records one model response and its tool results.
type AgentStep struct {
	Index            int
	AssistantMessage joytoken.ChatMessage
	ToolResults      []ToolResult
	Usage            *joytoken.Usage
}

// ToolResult records the serialized result of one tool call.
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
}

// UsageSummary accumulates usage over an agent run.
type UsageSummary struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             *float64
}

// AgentState is the state passed to a stop condition.
type AgentState struct {
	Step      int
	ToolCalls int
	Usage     UsageSummary
	Messages  []joytoken.ChatMessage
}

// StopDecision describes whether a stop condition has been reached.
type StopDecision struct {
	Stop   bool
	Reason string
}

// StopCondition decides whether an agent run should stop.
type StopCondition func(state AgentState) StopDecision

// Protocol identifies the provider wire format.
type Protocol string

const (
	// OpenAIProtocol selects the OpenAI-compatible Chat Completions endpoint.
	OpenAIProtocol Protocol = "openai"
	// AnthropicProtocol selects the Anthropic-compatible Messages endpoint.
	AnthropicProtocol Protocol = "anthropic"
)

// JoyTokenProvider is a ModelProvider backed by the JoyToken client.
type JoyTokenProvider struct {
	Client   *joytoken.Client
	Protocol Protocol
}

// ProviderOption configures a JoyTokenProvider.
type ProviderOption func(*JoyTokenProvider)

// WithProtocol configures the JoyToken wire protocol used by the provider.
func WithProtocol(protocol Protocol) ProviderOption {
	return func(provider *JoyTokenProvider) {
		provider.Protocol = protocol
	}
}

// Int returns a pointer to value for optional integer settings.
func Int(value int) *int { return &value }

// Float64 returns a pointer to value for optional floating-point settings.
func Float64(value float64) *float64 { return &value }
