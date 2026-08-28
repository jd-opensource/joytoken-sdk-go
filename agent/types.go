// Package agent provides tool-calling agent helpers built on the JoyToken client.
package agent

import (
	"context"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/tool"
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

// ToolExecuteFunc is the signature of a tool's execution function. It is an
// alias of tool.ExecuteFunc so the abstraction lives in the shared tool package
// (which both the client and agent depend on) while existing agent.ToolExecuteFunc
// references keep working unchanged.
type ToolExecuteFunc = tool.ExecuteFunc

// AgentTool describes a function the model can call. It is an alias of tool.Tool
// so tools defined against the shared package are directly usable here.
type AgentTool = tool.Tool

// ToolExecutionContext contains the state available to a tool invocation. It is
// an alias of tool.ExecutionContext.
type ToolExecutionContext = tool.ExecutionContext

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

// ToolResult records the serialized result of one tool call. IsError is true
// when the tool failed (bad arguments, a runtime error, or a panic) and Content
// carries the error message that was fed back to the model so it can correct
// itself and retry on a later step.
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
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

// Protocol selects the public compatibility shape used by JoyTokenProvider.
// Both values ultimately use the gateway's single Chat Completions endpoint.
type Protocol string

const (
	OpenAIProtocol    Protocol = "openai"
	AnthropicProtocol Protocol = "anthropic"
)

// JoyTokenProvider is a ModelProvider backed by the JoyToken client.
type JoyTokenProvider struct {
	Client   *joytoken.Client
	Protocol Protocol
}

// ProviderOption configures a JoyTokenProvider.
type ProviderOption func(*JoyTokenProvider)

func WithProtocol(protocol Protocol) ProviderOption {
	return func(provider *JoyTokenProvider) { provider.Protocol = protocol }
}

// Int returns a pointer to value for optional integer settings.
func Int(value int) *int { return &value }

// Float64 returns a pointer to value for optional floating-point settings.
func Float64(value float64) *float64 { return &value }
