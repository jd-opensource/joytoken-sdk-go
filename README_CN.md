# JoyToken Go SDK

[English](README.md) | 简体中文

JoyToken 公共开发者 API 的官方 Go 客户端。

本模块直接通过 GitHub 分发，无需另外发布到包注册中心。

```bash
go get github.com/jd-opensource/joytoken-sdk-go
```

默认服务地址为 `https://api.joytokens.ai`。如需使用其他环境，可设置 `JOY_TOKEN_API_BASE_URL`，或传入 `WithAPIBaseURL`。请求默认超时时间为 60 秒；传入 `WithTimeout(0)` 可禁用该限制。

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

OpenAI Responses：

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

OpenAI Images：

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

Anthropic Messages：

```go
message, err := client.CreateMessage(ctx, joytoken.MessageRequest{
    Model:     "auto",
    MaxTokens: 1024,
    Messages: []joytoken.MessageParam{
        {Role: "user", Content: "Say hello"},
    },
})
```

客户端支持：

- `POST /openai/v1/chat/completions`
- 基于 SSE 的流式 Chat Completions
- `POST /openai/v1/responses`
- 基于 SSE 的流式 Responses 文本事件
- `POST /openai/v1/images/generations`
- `POST /anthropic/v1/messages`
- 基于 SSE 的流式 Anthropic Messages
- `GET /api/v1/models`
- `GET /api/v1/models/meta`
- `GET /api/v1/pricing`

`agent` 子包提供与 TypeScript Agent SDK 一致的有界工具调用循环：

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

每次运行默认最多执行 8 个模型步骤。可以通过 `RunWithOptions` 设置 `MaxSteps: agent.Int(6)`，也可以添加 `StepCountIs`、`MaxToolCalls` 和 `MaxCost` 等停止条件。

## 流式调用

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

`StreamMessage` 为 Anthropic Messages 提供相同的迭代模式。调用方提前停止消费流时，应始终关闭流。

## 错误处理

调用需要鉴权的模型接口、模型元数据和价格接口时，如果未配置 API Key，SDK 会在发送网络请求前返回 `joytoken.ErrMissingAPIKey`。只有 `ListModels` 是无需鉴权的模型目录接口。

HTTP 请求失败时会返回 `*joytoken.APIError`。可以使用 `joytoken.IsAPIError(err)` 或 `errors.As` 获取状态码、请求 ID、响应头和解析后的响应体。Agent 包会将 Provider 和工具执行错误原样返回给调用方。

## 验证

```bash
go test ./...

cd example
go test ./...
```

## 在线示例

```bash
cd example
export JOY_TOKEN_API_KEY="..."
export JOY_TOKEN_MODEL="auto"
go run ./live
```

## 参与贡献

请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)、[SECURITY.md](SECURITY.md) 和 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
