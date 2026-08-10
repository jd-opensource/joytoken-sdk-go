# github.com/jd-opensource/joytoken-sdk-go/agent

Agent helpers for building bounded tool-calling loops on top of the JoyToken Go client.

```go
import (
    "context"
    "os"

    joytoken "github.com/jd-opensource/joytoken-sdk-go"
    "github.com/jd-opensource/joytoken-sdk-go/agent"
)

client := joytoken.NewClient(joytoken.WithAPIKey(os.Getenv("JOY_TOKEN_API_KEY")))
provider := agent.NewJoyTokenProvider(client)
ctx := context.Background()

runner := agent.New(agent.AgentOptions{
    Model: provider,
    StopWhen: []agent.StopCondition{
        agent.StepCountIs(6),
        agent.MaxToolCalls(4),
    },
    Tools: []agent.AgentTool{{
        Name:        "lookup",
        Description: "Look up internal data",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{"id": map[string]any{"type": "string"}},
            "required": []string{"id"},
        },
        Execute: func(ctx context.Context, input any, execution agent.ToolExecutionContext) (any, error) {
            return "record:42", nil
        },
    }},
})

result, err := runner.Run(ctx, "Summarize record 42")
```

The provider supports both JoyToken's OpenAI-compatible and Anthropic-compatible routes:

```go
provider := agent.NewJoyTokenProvider(client, agent.WithProtocol(agent.AnthropicProtocol))
```

Every run has a hard eight-step limit by default. Use `RunWithOptions` with `MaxSteps: agent.Int(6)` or add custom `StopCondition` values. Tool execution, state, and approval logic remain in your application.
