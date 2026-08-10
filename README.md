# JoyToken SDK for Go

English | [简体中文](README_CN.md)

First-party Go client for JoyToken's public developer API.

The module is distributed directly from GitHub and does not require a separate package-registry publication.

```bash
go get github.com/jd-opensource/joytoken-sdk-go
```

The default endpoint is `https://api.joytokens.ai`. Set `JOY_TOKEN_API_BASE_URL` (or pass `WithAPIBaseURL`) to use another environment. Requests time out after 60 seconds by default; pass `WithTimeout(0)` to disable that limit.

```go
client := joytoken.NewClient(
    joytoken.WithAPIKey(os.Getenv("JOY_TOKEN_API_KEY")),
)

completion, err := client.CreateChatCompletion(ctx, joytoken.ChatCompletionRequest{
    Model: "auto",
    Messages: []joytoken.ChatMessage{
        {Role: "user", Content: "Say hello"},
    },
})
```

OpenAI Responses:

```go
response, err := client.CreateResponse(ctx, joytoken.ResponseRequest{
    Model: "auto",
    Input: "Say hello",
})
if err != nil {
    return err
}
fmt.Println(response.OutputText())
```

OpenAI Images:

```go
image, err := client.GenerateImage(ctx, joytoken.ImageGenerationRequest{
    Model:  "auto",
    Prompt: "A neon JoyToken logo on a black background",
    Size:   "1024x1024",
})
if err != nil {
    return err
}
fmt.Println(image.Data[0].URL)
```

Anthropic Messages:

```go
message, err := client.CreateMessage(ctx, joytoken.MessageRequest{
    Model:     "auto",
    MaxTokens: 1024,
    Messages: []joytoken.MessageParam{
        {Role: "user", Content: "Say hello"},
    },
})
```

The client supports:

- `POST /openai/v1/chat/completions`
- streaming chat completions via SSE
- `POST /openai/v1/responses`
- streaming Responses text events via SSE
- `POST /openai/v1/images/generations`
- `POST /anthropic/v1/messages`
- streaming Anthropic Messages via SSE
- `GET /api/v1/models`
- `GET /api/v1/models/meta`
- `GET /api/v1/pricing`

The `agent` subpackage provides the same bounded tool-calling loop as the TypeScript Agent SDK:

```bash
go get github.com/jd-opensource/joytoken-sdk-go/agent
```

```go
import (
    "context"
    "os"

    joytoken "github.com/jd-opensource/joytoken-sdk-go"
    "github.com/jd-opensource/joytoken-sdk-go/agent"
)

ctx := context.Background()
client := joytoken.NewClient(joytoken.WithAPIKey(os.Getenv("JOY_TOKEN_API_KEY")))
provider := agent.NewJoyTokenProvider(client)
runner := agent.New(agent.AgentOptions{
    Model: provider,
    Tools: []agent.AgentTool{{
        Name: "lookup",
        Execute: func(ctx context.Context, input any, execution agent.ToolExecutionContext) (any, error) {
            return "record:42", nil
        },
    }},
})
result, err := runner.Run(ctx, "Summarize record 42")
```

Every run has a hard eight-step limit by default. Use `RunWithOptions` with `MaxSteps: agent.Int(6)` or add `StepCountIs`, `MaxToolCalls`, and `MaxCost` conditions.

## Streaming

```go
stream, err := client.StreamChatCompletion(ctx, joytoken.ChatCompletionRequest{
    Model: "auto",
    Messages: []joytoken.ChatMessage{{Role: "user", Content: "Say hello"}},
})
if err != nil {
    return err
}
defer stream.Close()

for {
    chunk, err := stream.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        return err
    }
    fmt.Print(chunk.Choices[0].Delta["content"])
}
```

`StreamMessage` exposes the same iterator pattern for Anthropic Messages. Always close a stream when the consumer stops early.

## Errors

Authenticated model calls, model metadata and pricing requests return `joytoken.ErrMissingAPIKey` before sending a network request when no API key is configured. `ListModels` remains the unauthenticated catalog call.

HTTP failures are returned as `*joytoken.APIError`. Use `joytoken.IsAPIError(err)` or `errors.As` to inspect the status code, request ID, response headers, and parsed response body. The Agent package returns provider and tool errors to the caller without hiding them.

## Validate

```bash
go test ./...

cd example
go test ./...
```

## Live example

```bash
cd example
export JOY_TOKEN_API_KEY="..."
export JOY_TOKEN_MODEL="auto"
go run ./live
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
