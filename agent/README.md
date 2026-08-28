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

The provider supports both public compatibility shapes. Both are translated to
JoyToken's single Chat Completions gateway route:

```go
provider := agent.NewJoyTokenProvider(client, agent.WithProtocol(agent.AnthropicProtocol))
```

Every run has a hard eight-step limit by default. Use `RunWithOptions` with `MaxSteps: agent.Int(6)` or add custom `StopCondition` values.

## Built-in toolkit (optional)

The example above wires tools by hand. If you just want a safe, ready-to-use tool
set, the `agent/toolkit` package provides one plus permission and middleware:

```go
import "github.com/jd-opensource/joytoken-sdk-go/agent/toolkit"

// Zero config: injects a safe default set (calculator, datetime) only when you
// pass no Tools of your own. An explicit empty slice keeps "no tools".
runner := toolkit.NewAgent(agent.AgentOptions{Model: provider})
```

Tools split into two groups:

- **Zero-config defaults** (`Calculator`, `DateTime`) — no credentials, no
  network; auto-injected and safe under `PermissionAuto`.
- **Host-configured fallbacks** (`FileRead`/`FileWrite`, `HTTPFetch`, `SQLQuery`) —
  need a sandbox root, host allowlist, or `*sql.DB`. Register them explicitly and
  pick a permission mode.

```go
tk := toolkit.New(
    toolkit.WithPermission(toolkit.Permission{Mode: toolkit.PermissionAsk, Ask: askHandler}),
    toolkit.WithMiddleware(toolkit.Timeout(15*time.Second), toolkit.Audit(logFn)),
).Register(
    toolkit.Calculator(), toolkit.DateTime(),
    toolkit.FileWrite(toolkit.FileSandbox{Root: "/data/workspace"}),
)
runner := agent.New(agent.AgentOptions{Model: provider, Tools: tk.Tools()})
```

Permission is `fail-safe`: side-effecting tools set to `PermissionAsk` are
**denied** unless an `Ask` callback is provided. The callback is where your app
approves each call — see runnable CLI and Web examples in
[`example/permission_example_test.go`](../example/permission_example_test.go).
