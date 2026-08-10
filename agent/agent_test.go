package agent

import (
	"context"
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

type loopProvider struct {
	calls int
}

func (p *loopProvider) Complete(_ context.Context, request ModelRequest) (ModelResponse, error) {
	p.calls++
	if p.calls == 1 {
		return ModelResponse{
			Message: joytoken.ChatMessage{
				Role: "assistant",
				ToolCalls: []joytoken.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: joytoken.ToolFunction{
						Name:      "lookup",
						Arguments: `{"id":"42"}`,
					},
				}},
			},
			Usage: &joytoken.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7, Cost: float64Pointer(0.01)},
		}, nil
	}
	return ModelResponse{
		Message: joytoken.ChatMessage{Role: "assistant", Content: "record:42"},
		Usage:   &joytoken.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10, Cost: float64Pointer(0.02)},
	}, nil
}

func TestAgentRunsToolLoop(t *testing.T) {
	provider := &loopProvider{}
	agent := New(AgentOptions{
		Model:    provider,
		StopWhen: []StopCondition{StepCountIs(4), MaxToolCalls(4)},
		Tools: []AgentTool{{
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
			Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
				arguments := input.(map[string]any)
				return "record:" + arguments["id"].(string), nil
			},
		}},
	})

	result, err := agent.Run(context.Background(), "lookup 42")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.FinalText != "record:42" || len(result.Steps) != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Steps[0].ToolResults[0].Content != "record:42" {
		t.Fatalf("unexpected tool result: %#v", result.Steps[0].ToolResults[0])
	}
	if result.Usage.TotalTokens != 17 || result.Usage.Cost == nil || *result.Usage.Cost != 0.03 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}

func TestAgentKeepsRunMaxStepsCap(t *testing.T) {
	provider := &alwaysToolProvider{}
	agent := New(AgentOptions{
		Model:    provider,
		StopWhen: []StopCondition{MaxToolCalls(100)},
		Tools:    []AgentTool{{Name: "lookup", Execute: func(context.Context, any, ToolExecutionContext) (any, error) { return "continue", nil }}},
	})

	maxSteps := 2
	result, err := agent.RunWithOptions(context.Background(), AgentRunOptions{Input: "loop", MaxSteps: &maxSteps})
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}
	if provider.calls != 2 || result.StoppedBy != "step_count:2" {
		t.Fatalf("unexpected stop result: calls=%d result=%#v", provider.calls, result)
	}
}

type alwaysToolProvider struct {
	calls int
}

func (p *alwaysToolProvider) Complete(_ context.Context, _ ModelRequest) (ModelResponse, error) {
	p.calls++
	return ModelResponse{Message: joytoken.ChatMessage{
		Role: "assistant",
		ToolCalls: []joytoken.ToolCall{{
			ID: "call", Type: "function", Function: joytoken.ToolFunction{Name: "lookup", Arguments: `{}`},
		}},
	}}, nil
}

func float64Pointer(value float64) *float64 { return &value }
