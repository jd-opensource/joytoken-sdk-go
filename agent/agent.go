package agent

import (
	"context"
	"fmt"
	"strings"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/tool"
)

// Agent runs a bounded model-and-tool loop.
type Agent struct {
	options     AgentOptions
	toolsByName map[string]AgentTool
	toolOrder   []string
}

// New creates an Agent from its options.
func New(options AgentOptions) *Agent {
	toolsByName := make(map[string]AgentTool, len(options.Tools))
	toolOrder := make([]string, 0, len(options.Tools))
	for _, tool := range options.Tools {
		if _, exists := toolsByName[tool.Name]; !exists {
			toolOrder = append(toolOrder, tool.Name)
		}
		toolsByName[tool.Name] = tool
	}
	return &Agent{options: options, toolsByName: toolsByName, toolOrder: toolOrder}
}

// Run executes an agent run with a single user input and the default run options.
func (a *Agent) Run(ctx context.Context, input string) (AgentResult, error) {
	return a.RunWithOptions(ctx, AgentRunOptions{Input: input})
}

// RunWithOptions executes an agent run with explicit messages, input and limits.
func (a *Agent) RunWithOptions(ctx context.Context, runOptions AgentRunOptions) (AgentResult, error) {
	if a.options.Model == nil {
		return AgentResult{}, fmt.Errorf("agent model provider is required")
	}

	messages := a.initialMessages(runOptions)
	steps := make([]AgentStep, 0)
	stopWhen := append([]StopCondition{}, a.options.StopWhen...)
	maxSteps := 8
	if runOptions.MaxSteps != nil {
		maxSteps = *runOptions.MaxSteps
	}
	stopWhen = append(stopWhen, StepCountIs(maxSteps))
	usage := emptyUsage()
	toolCalls := 0

	for step := 1; ; step++ {
		stoppedBy := shouldStop(stopWhen, AgentState{
			Step:      step - 1,
			ToolCalls: toolCalls,
			Usage:     usage,
			Messages:  messages,
		})
		if stoppedBy != "" {
			return AgentResult{
				FinalText: lastAssistantText(messages),
				Messages:  messages,
				Steps:     steps,
				Usage:     usage,
				StoppedBy: stoppedBy,
			}, nil
		}

		response, err := a.options.Model.Complete(ctx, ModelRequest{
			Messages:    messages,
			Tools:       a.chatTools(),
			Temperature: a.options.Temperature,
			MaxTokens:   a.options.MaxTokens,
			Tier:        a.options.Tier,
			Metadata:    mergeMetadata(a.options.Metadata, runOptions.Metadata),
		})
		if err != nil {
			return AgentResult{}, err
		}

		usage = addUsage(usage, response.Usage)
		assistantMessage := response.Message
		messages = append(messages, assistantMessage)

		toolResults, err := a.executeToolCalls(ctx, step, assistantMessage.ToolCalls, messages)
		if err != nil {
			return AgentResult{}, err
		}
		toolCalls += len(toolResults)
		for _, result := range toolResults {
			messages = append(messages, joytoken.ChatMessage{
				Role:       "tool",
				ToolCallID: result.ToolCallID,
				Content:    result.Content,
			})
		}

		steps = append(steps, AgentStep{
			Index:            step,
			AssistantMessage: assistantMessage,
			ToolResults:      toolResults,
			Usage:            response.Usage,
		})

		if len(toolResults) == 0 {
			return AgentResult{
				FinalText: textContent(assistantMessage.Content),
				Messages:  messages,
				Steps:     steps,
				Usage:     usage,
			}, nil
		}
	}
}

func (a *Agent) initialMessages(runOptions AgentRunOptions) []joytoken.ChatMessage {
	messages := make([]joytoken.ChatMessage, 0, len(runOptions.Messages)+2)
	if a.options.System != "" {
		messages = append(messages, joytoken.ChatMessage{Role: "system", Content: a.options.System})
	}
	messages = append(messages, runOptions.Messages...)
	if runOptions.Input != "" {
		messages = append(messages, joytoken.ChatMessage{Role: "user", Content: runOptions.Input})
	}
	return messages
}

func (a *Agent) chatTools() []joytoken.ChatTool {
	tools := make([]joytoken.ChatTool, 0, len(a.toolOrder))
	for _, name := range a.toolOrder {
		tools = append(tools, toChatTool(a.toolsByName[name]))
	}
	return tools
}

func (a *Agent) executeToolCalls(ctx context.Context, step int, calls []joytoken.ToolCall, messages []joytoken.ChatMessage) ([]ToolResult, error) {
	results := make([]ToolResult, 0, len(calls))
	for _, call := range calls {
		tool, ok := a.toolsByName[call.Function.Name]
		if !ok {
			results = append(results, ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Function.Name,
				Content:    "Tool not found: " + call.Function.Name,
				IsError:    true,
			})
			continue
		}
		if tool.Execute == nil {
			// A registered tool with no Execute is a host configuration bug,
			// not something the model can fix by retrying, so fail hard.
			return nil, fmt.Errorf("tool %q has no Execute function", tool.Name)
		}
		output, err := safeExecute(ctx, tool.Execute, parseToolArguments(call.Function.Arguments), ToolExecutionContext{
			Step:     step,
			ToolCall: call,
			Messages: messages,
		})
		if err != nil {
			// Tool failures (bad arguments, runtime errors, recovered panics)
			// are fed back to the model as an observation so it can correct
			// itself on a later step, instead of aborting the whole run.
			results = append(results, ToolResult{
				ToolCallID: call.ID,
				ToolName:   tool.Name,
				Content:    "Tool error: " + err.Error(),
				IsError:    true,
			})
			continue
		}
		content, err := stringifyToolResult(output)
		if err != nil {
			results = append(results, ToolResult{
				ToolCallID: call.ID,
				ToolName:   tool.Name,
				Content:    "Tool error: failed to serialize result: " + err.Error(),
				IsError:    true,
			})
			continue
		}
		results = append(results, ToolResult{ToolCallID: call.ID, ToolName: tool.Name, Content: content})
	}
	return results, nil
}

// safeExecute runs a tool's Execute function and converts any panic into an
// error so a single misbehaving tool cannot crash the whole agent process. It
// delegates to tool.SafeExecute so the client and agent share one recovery path.
func safeExecute(ctx context.Context, execute ToolExecuteFunc, input any, execution ToolExecutionContext) (output any, err error) {
	return tool.SafeExecute(ctx, execute, input, execution)
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func mergeMetadata(base, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func lastAssistantText(messages []joytoken.ChatMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			return textContent(messages[index].Content)
		}
	}
	return ""
}

func textContent(content any) string {
	if value, ok := content.(string); ok {
		return value
	}
	if blocks, ok := content.([]joytoken.MessageContentBlock); ok {
		var builder strings.Builder
		for _, block := range blocks {
			if block.Type == "text" || block.Text != "" {
				builder.WriteString(block.Text)
			}
		}
		return builder.String()
	}
	return ""
}
