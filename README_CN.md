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
    Model: joytoken.ModelAuto,
    Messages: []joytoken.ChatMessage{
        {Role: "user", Content: "Say hello"},
    },
})
// completion 为 *ChatCompletionResponse(单次调用)。
// 使用 RunChatCompletion 可跑工具闭环, 改读 RunChatResult。
```

OpenAI Responses：

`CreateResponse` 直接调用 Gateway 的原生 Responses 入口，完整保留 Responses
工具、输出项、用量、标注和流式事件。

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

OpenAI Images：

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

编辑已有图片（单个 URL/base64 data URI，或它们的数组）：

```go
edited, err := client.EditImage(ctx, joytoken.ImageEditRequest{
    Model:  joytoken.ModelAuto,
    Prompt: "将背景替换为星空并保持主体不变",
    Image:  "https://picsum.photos/512/512",
})
if err != nil {
    return err
}
fmt.Println(edited.Data[0].B64JSON)
```

Anthropic Messages：

`CreateMessage` 同样是基于唯一 Chat Completions Gateway 入口的协议适配层。

```go
message, err := client.CreateMessage(ctx, joytoken.MessageRequest{
    Model:     joytoken.ModelAuto,
    MaxTokens: 1024,
    Messages: []joytoken.MessageParam{
        {Role: "user", Content: "Say hello"},
    },
})
```

客户端支持：

- `POST /openai/v1/chat/completions`
- 基于 SSE 的流式 Chat Completions
- `POST /openai/v1/responses` 及原生 Responses SSE
- `POST /openai/v1/images/generations`
- `POST /openai/v1/images/edits`
- Anthropic Messages 兼容请求、响应及流式事件适配
- `GET /api/v1/models`
- `GET /api/v1/models/meta`
- `GET /api/v1/pricing`

所有模型请求都必须使用 `joytoken.ModelAuto`，不支持传入具体模型 ID。

模型列表描述语言按请求设置，不是客户端全局配置。使用 `ListModelsWithOptions` 传入 `joytoken.ModelLocaleZH` 或 `joytoken.ModelLocaleEN`；不传 `Locale` 时接口默认返回英文描述。

```go
models, err := client.ListModelsWithOptions(ctx, joytoken.ListModelsOptions{
    Locale: joytoken.ModelLocaleZH,
})
```

SDK 保留接口原始响应层级，模型目录项位于 `models.Data.Models`。

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

想直接用一套带权限与中间件、开箱即用的安全工具集？可用 `toolkit.NewAgent(...)`
获得零配置默认工具，或显式注册宿主配置类工具（文件/HTTP/SQL）并配上审批回调。
详见 [`agent/README.md`](agent/README.md#built-in-toolkit-optional)。

## 流式调用

流式同样支持工具执行，分两个层次：

- `StreamChatCompletion`、`StreamResponse`、`StreamMessage` 是原始流，绝不执行调用方工具。
- `RunChatCompletionStream`、`RunResponseStream`、`RunMessageStream` 通过 `OnTextDelta` 流式输出文本，并在轮次间执行同名工具直到闭环停止。

模型返回的厂商扩展工具元数据保存在 `ToolCall.ExtraContent` 中，并由所有
`Run*` 闭环和 Messages / Responses 适配原样带入续轮。例如 Gemini 的
`extra_content.google.thought_signature` 不会在本地工具执行后丢失。手写循环时
也应回填完整的 `ToolCall`，不要只复制 `id` 和 `function`。

原始流原语(不执行工具):

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

Gateway 可能发送只有 metadata/usage、没有 choices 的 SSE 事件。SDK 会保留该
事件并将 `chunk.Choices` 标准化为空切片，因此 `range` 和
`len(chunk.Choices)` 都是安全的；不要在未检查长度时直接访问
`chunk.Choices[0]`。协议层 usage 缺失时，SDK 会从 `metadata.billing`
回填 token 数；如果协议层明确提供 usage，则始终以协议值为准。

可以使用 `response.RequestID()`、`chunk.RequestID()` 或
`joytoken.RequestIDFromMetadata(metadata)` 获取请求 ID。SDK 优先保留响应
metadata 中的 ID，也会在成功响应 header 存在 ID 时将其作为兜底。

`StreamMessage` 为 Anthropic Messages 提供原始事件迭代；需要流式工具闭环时使用 `RunMessageStream`。调用方提前停止消费原始流时，应始终关闭流。

## 错误处理

调用需要鉴权的模型接口、模型元数据和价格接口时，如果未配置 API Key，SDK 会在发送网络请求前返回 `joytoken.ErrMissingAPIKey`。只有 `ListModels` 是无需鉴权的模型目录接口。

HTTP 请求失败时会返回 `*joytoken.APIError`。可以使用 `joytoken.IsAPIError(err)` 或 `errors.As` 获取状态码、请求 ID、响应头和解析后的响应体。Agent 包会将 Provider 和工具执行错误原样返回给调用方。

显式 `Run*` 方法如果模型请求或本地执行失败，会同时返回错误和部分结果，并设置 `StoppedBy == "error"`。调用方可以从 `Steps` 以及已累积的 `Messages` 或 `Input` 中保留已经完成的工具工作和诊断信息。只有真正耗尽配置的步骤上限时才会返回 `StoppedBy == "max_steps"`；Chat 工具循环在错误路径还会将 `FinishReason` 设置为 `"error"`。

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
go run ./live
```

## 参与贡献

请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)、[SECURITY.md](SECURITY.md) 和 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
