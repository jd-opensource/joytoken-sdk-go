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
    Model: joytoken.ModelAuto,
    Messages: []joytoken.ChatMessage{
        {Role: "user", Content: "Say hello"},
    },
})
// completion is *ChatCompletionResponse (single-shot).
// Use RunChatCompletion to run the tool loop and read the RunChatResult instead.
```

OpenAI Responses:

`CreateResponse` uses the gateway's native Responses endpoint, preserving
Responses tools, output items, usage, annotations, and streaming events.

```go
result, err := client.CreateResponse(ctx, joytoken.ResponseRequest{
    Model: joytoken.ModelAuto,
    Input: "Say hello",
})
if err != nil {
    return err
}
fmt.Println(result.OutputText())
```

OpenAI Images:

```go
image, err := client.GenerateImage(ctx, joytoken.ImageGenerationRequest{
    Model:  joytoken.ModelAuto,
    Prompt: "A neon JoyToken logo on a black background",
    Size:   "1024x1024",
})
if err != nil {
    return err
}
fmt.Println(image.Data[0].URL)
```

Anthropic Messages:

`CreateMessage` is also a local protocol adapter over the single Chat
Completions gateway endpoint.

```go
message, err := client.CreateMessage(ctx, joytoken.MessageRequest{
    Model:     joytoken.ModelAuto,
    MaxTokens: 1024,
    Messages: []joytoken.MessageParam{
        {Role: "user", Content: "Say hello"},
    },
})
```

The client supports:

- `POST /openai/v1/chat/completions`
- streaming chat completions via SSE
- `POST /openai/v1/responses` and native Responses SSE
- `POST /openai/v1/images/generations`
- Anthropic Messages-compatible request/response and streaming adapters
- `GET /api/v1/models`
- `GET /api/v1/models/meta`
- `GET /api/v1/pricing`

All model requests require `joytoken.ModelAuto`; concrete model IDs are not accepted.

Model descriptions can be localized with `ListModelsWithOptions`. `Locale`
accepts `joytoken.ModelLocaleZH` or `joytoken.ModelLocaleEN`; when omitted, the
API defaults to English.

```go
models, err := client.ListModelsWithOptions(ctx, joytoken.ListModelsOptions{
    Locale: joytoken.ModelLocaleZH,
})
```

The SDK preserves the API response envelope; catalog entries are available at `models.Data.Models`.

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

Prefer a ready-made, safe tool set with built-in permissions and middleware? Use
`toolkit.NewAgent(...)` for zero-config defaults, or register host-configured
tools (file/HTTP/SQL) with an approval callback. See [`agent/README.md`](agent/README.md#built-in-toolkit-optional).

## Streaming

Streaming supports tool execution too. There are two levels:

- `StreamChatCompletion`, `StreamResponse`, and `StreamMessage` are raw streams that never execute caller-owned tools.
- `RunChatCompletionStream`, `RunResponseStream`, and `RunMessageStream` stream text via `OnTextDelta` **and** execute matching tools between turns until the loop stops.

Raw stream primitive (no tool execution):

```go
stream, err := client.StreamChatCompletion(ctx, joytoken.ChatCompletionRequest{
    Model: joytoken.ModelAuto,
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
	for _, choice := range chunk.Choices {
		if text, ok := choice.Delta["content"].(string); ok {
			fmt.Print(text)
		}
	}
}
```

The Gateway may emit a metadata/usage-only SSE event with no choices. The SDK
preserves that event and normalizes `chunk.Choices` to an empty slice, so range
and `len(chunk.Choices)` are safe. Never index `chunk.Choices[0]` without a
length check. When protocol-level usage is absent, the SDK derives token counts
from `metadata.billing`; explicit protocol usage remains authoritative.

Use `response.RequestID()`, `chunk.RequestID()`, or
`joytoken.RequestIDFromMetadata(metadata)` to read the request ID. These helpers
prefer response metadata and also accept a successful HTTP request-ID header
when one is available.

`StreamMessage` exposes the same raw iterator pattern for Anthropic Messages; use `RunMessageStream` for its streaming tool loop. Always close a raw stream when the consumer stops early.

## Errors

Authenticated model calls, model metadata and pricing requests return `joytoken.ErrMissingAPIKey` before sending a network request when no API key is configured. `ListModels` remains the unauthenticated catalog call.

HTTP failures are returned as `*joytoken.APIError`. Use `joytoken.IsAPIError(err)` or `errors.As` to inspect the status code, request ID, response headers, and parsed response body. The Agent package returns provider and tool errors to the caller without hiding them.

Explicit `Run*` methods return the partial result together with an error when a later model turn fails. Inspect `Steps` and the accumulated `Messages` or `Input` to retain already completed tool work and diagnostics.

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
go run ./live
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
