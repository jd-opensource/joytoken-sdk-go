# Client SDK Tools 能力改造：调用方式与链路

本文说明 JoyToken client SDK 在引入「本地工具执行闭环」后的完整 API、调用方式变化、请求链路调整，以及「兜底工具 vs 用户传入工具」的判定规则。

> **最新规则**：Gateway 正式提供 `/openai/v1/chat/completions` 与 `/openai/v1/responses`；Anthropic Messages 仍是 SDK 上层协议适配器。请求级 `Tools` 非 nil 时原样透传；否则 `WithTools` 注册集替代默认集；仅当两处都没传工具时才注入 SDK 默认工具。

## 1. Client SDK 完整 API 一览

### 1.1 构造与 Option

| Option | 作用 | 是否本次新增 |
| --- | --- | --- |
| `NewClient(opts ...Option) *Client` | 从环境变量 + Option 构造客户端 | 否 |
| `WithAPIKey(apiKey)` | 配置 API Key | 否 |
| `WithAPIBaseURL / WithOpenAIBaseURL` | 配置唯一 Gateway BaseURL | 否 |
| `WithAnthropicBaseURL / WithAnthropicVersion` | 已废弃的源码兼容 no-op | 否 |
| `WithHTTPClient(hc)` | 自定义 HTTP 传输 | 否 |
| `WithTimeout(d)` | 请求超时 | 否 |
| `WithHeader(k, v)` | 附加请求头 | 否 |
| **`WithTools(tools ...Tool)`** | **注册可执行工具（供 RunChatCompletion 使用）** | **是** |
| **`WithToolHandler(name, desc, params, execute)`** | **注册单个可执行工具的便捷封装** | **是** |

### 1.2 AI 调用方法（请求原语）

工具选择是互斥的三档规则：`request.Tools != nil` 时完全采用请求工具；否则若调用了 `WithTools`，只采用用户注册集；仅当两者都不存在时才注入默认集。显式空切片表示“不要工具”。

用户拥有工具时，`CreateXxx`/`StreamXxx` 只做协议转换和透传，不执行 handler；需要 SDK 执行用户工具时显式调用对应 `RunXxx`。完全缺省时，非流式 `CreateXxx` 会自动执行 SDK 默认工具闭环。

| 方法 | 说明 | 是否注入默认工具 schema | 是否执行工具 | 状态 |
| --- | --- | --- | --- | --- |
| `CreateChatCompletion` | Chat 补全 | 仅完全缺省时 | 用户工具透传；默认工具自动闭环 | 保留 |
| `StreamChatCompletion` | 流式 Chat 补全 | 仅完全缺省时 | 否 | 保留 |
| `CreateResponse` | 原生 Responses 请求 | 仅完全缺省时 | 用户工具透传；默认工具自动闭环 | 保留 |
| `StreamResponse` | 原生 Responses SSE | 仅完全缺省时 | 否 | 保留 |
| `GenerateImage` | 图像生成 | 否 | 否 | 保留 |
| `CreateMessage` / `StreamMessage` | Anthropic Messages 兼容适配 | 仅完全缺省时 | 用户工具透传；原始流不执行 | 保留 |
| `ListModels` / `ListModelsWithOptions` | 模型列表 | 否 | 否 | 保留 |
| `GetModelMeta` / `GetPricing` | 模型元数据/定价 | 否 | 否 | 保留 |

#### 执行归属：谁来执行 tool_call

**是否真的执行某个 `tool_call`，不是 SDK 一刀切决定的，而是按「用户是否提供实现 + 我们是否内置该能力」自然判定**。核心原则：用户传了就按用户的来；用户没实现但我们有本地实现就兜底；两者都没有就管不了、按正常流程透传还给用户。不搞一棍子打死。

对模型产出的每个 `tool_call`，闭环入口（1.3）按下表判定：

| 场景 | 用户注册了该工具的 handler？ | 我们内置有本地实现？ | 处理方式 |
| --- | --- | --- | --- |
| A | 是 | — | 原语透传；显式 `RunXxx` 执行用户 handler |
| B | 用户完全未提供工具 | 是 | 注入并执行 SDK 默认实现 |
| C | 用户仅在请求声明 schema | 不参与 | 原语透传，不回退到同名默认实现 |
| D（built-in） | — | 不适用（不在本地跑） | `web_search` / `file_search` 等网关 built-in：用户显式传入时原样透传；也可显式开启 hosted 默认声明，执行发生在 AI/网关侧，SDK 不代跑 |

能力边界（哪些算「我们内置有本地实现」）：

- **默认本地工具**：`calculator` / `datetime` / `file_search` / `list_dir` / `file_read` / `file_write` / `shell`。前五个可在工作区沙箱内直接执行；`file_write` 与 `shell` 虽会声明给模型，但执行时必须分别通过 `WithFilePermission` / `WithShellPermission` 授权，未配置回调时安全拒绝。
- **需宿主显式注册的扩展工具**：`sql_query` / `http_fetch` 等。它们需要数据库连接、网络允许清单等宿主状态，不能作为零配置默认兜底，须宿主显式 `Register`。
- **网关 built-in**：`web_search` / `file_search`。**不是本地可执行的函数**，执行在 AI/网关侧。用户显式声明时 SDK 原样透传；`web_search_preview` 可通过 `WithDefaultBuiltinTools(true)` 开启默认声明，`file_search` 因需 `vector_store_ids` 始终由调用方显式配置。Hosted 默认关闭，避免单一 Chat Gateway 路由到不支持该工具的上游时让普通 Responses 请求失败。

我们的定位：在 API KEY 裸调用之上，**补齐常用本地 Tools 的必要执行能力**，而不是替用户决定「要不要执行」。

### 1.3 工具执行闭环入口

`RunXxx` 入口提供显式、可控的闭环（自定义 `MaxSteps`、读取分步 `Steps`/`ToolResults`），并共用同一套工具归属规则：

| 请求原语 | 对应显式闭环入口 | 协议 | 流式 |
| --- | --- | --- | --- |
| `CreateChatCompletion` | **`RunChatCompletion(ctx, req, RunChatOptions)`** | Chat Completions | 否 |
| `StreamChatCompletion` | **`RunChatCompletionStream(ctx, req, RunChatStreamOptions)`** | Chat Completions | 是（`OnTextDelta` 回调） |
| `CreateResponse` | **`RunResponse(ctx, req, RunResponseOptions)`** | Responses API | 否 |
| `StreamResponse` | **`RunResponseStream(ctx, req, RunResponseStreamOptions)`** | Responses API | 是（`OnTextDelta` 回调） |
| `CreateMessage` | **`RunMessage(ctx, req, RunMessageOptions)`** | Anthropic Messages | 否 |
| `StreamMessage` | **`RunMessageStream(ctx, req, RunMessageStreamOptions)`** | Anthropic Messages | 是（`OnTextDelta` 回调） |

## 2. 之前的调用方式（改造前）

client 只是「包装 API」，工具能力靠调用方自己接管：

```go
c := joytoken.NewClient(joytoken.WithAPIKey(key))

req := joytoken.ChatCompletionRequest{
    Model:    joytoken.ModelAuto,
    Messages: []joytoken.ChatMessage{{Role: "user", Content: "北京天气?"}},
    Tools:    []joytoken.ChatTool{ /* 只声明 schema */ },
}
resp, _ := c.CreateChatCompletion(ctx, req)

// 模型返回 tool_calls 后，全部由调用方自己解析、执行、回填、再次请求
if resp != nil && len(resp.Choices) > 0 {
    for _, call := range resp.Choices[0].Message.ToolCalls {
        //用户自己写：查函数 -> 执行 -> 组 tool 消息 -> 再调 CreateChatCompletion
    }
}
```

特点：client 不持有任何工具状态，不执行；`tool_calls` 原样返回，多轮循环由用户手写。

## 3. 新的调用方式（改造后，opt-in）

### 3.1 注册工具 + 一次调用跑完闭环

```go
c := joytoken.NewClient(
    joytoken.WithAPIKey(key),
    joytoken.WithToolHandler(
        "get_weather",
        "查询城市天气",
        map[string]any{"type": "object", "properties": map[string]any{
            "city": map[string]any{"type": "string"},
        }},
        func(ctx context.Context, input any, _ joytoken.ToolExecutionContext) (any, error) {
            m := input.(map[string]any)
            return "晴 26℃ @ " + m["city"].(string), nil
        },
    ),
)

result, _ := c.RunChatCompletion(ctx, joytoken.ChatCompletionRequest{
    Model:    joytoken.ModelAuto,
    Messages: []joytoken.ChatMessage{{Role: "user", Content: "北京天气?"}},
}, joytoken.RunChatOptions{MaxSteps: 8})

fmt.Println(result.FinalText) // 已自动执行工具并把结果回填给模型后的最终答复
```

client 内部自动完成：请求 → 模型返回 tool_calls → 按名查已注册 handler → 执行 → 回填 `role=tool` 消息 → 再次请求 → 直到模型无 tool_calls 或到达 MaxSteps。

### 3.2 与 agent 的关系

`RunChatCompletion` 是 client 层轻量闭环，复用与 agent 相同的共享抽象（`Tool` / `ToolExecuteFunc` / `ToolExecutionContext`）与执行辅助。需要停止条件、中间件、权限、多 provider 时仍用 `agent` 包；只需「返回后能自动执行」时用 client 这条入口即可。

### 3.3 为什么不直接把执行塞进 `CreateChatCompletion`（保留两个方法的原因）

分「原始原语」与「自动编排」两类入口，不是功能冗余，而是**语义边界**：

| | `CreateChatCompletion`（原语） | `RunChatCompletion`（编排） |
| --- | --- | --- |
| 语义 | 发一次请求，拿一次响应 | 请求→执行工具→回填→再请求…直到收敛 |
| 返回 | `ChatCompletionResponse` | `RunChatResult`（多轮） |
| 副作用 | 无 | 会真的调用你的 Go 函数 |
| 网络 | 1 次 | N 次 |

若把执行闭环并进 `CreateChatCompletion`，会破坏它「只发一次、不执行任何本地函数」的既有契约——所有老调用方一旦注册了工具就会行为突变（隐式多轮 + 隐式执行），有安全与兼容风险。因此保留原语不动，新增编排入口。

**体验上的补偿**：`RunChatCompletion` 在「未注册任何工具且请求里也无工具」时会自然退化——模型不返回 tool_calls，循环第一轮即停止，等价于一次 `CreateChatCompletion`。所以简单场景只记一个入口也够用。

### 3.4 流式闭环 `RunChatCompletionStream`

一边逐 token 输出、一边自动执行工具：

```go
result, _ := c.RunChatCompletionStream(ctx, req, joytoken.RunChatStreamOptions{
    OnTextDelta:  func(delta string) { fmt.Print(delta) },       // 实时渲染
    OnToolResult: func(r joytoken.ToolCallResult) { /* 展示工具活动 */ },
})
```

每个模型轮次以 SSE 消费：SDK 累积文本（通过 `OnTextDelta` 透传）与增量 `tool_calls`；流结束后执行工具、回填、再开新流，直到某轮无 tool_calls 或到达 MaxSteps。`StreamChatCompletion` 原始流式语义完全不变。

原始 Chat SSE 可能包含只有 `metadata` / `usage`、没有 `choices` 的事件。
SDK 保留该事件并把 `Choices` 标准化为空切片；调用方应使用 `range` 或先检查
`len(chunk.Choices)`，不要无条件访问 `chunk.Choices[0]`。当协议层 usage
缺失时，SDK 从 `metadata.billing.input_tokens/output_tokens` 回填；Messages
和 Responses 适配复用同一兜底。请求 ID 使用 `chunk.RequestID()`、
`response.RequestID()` 或 `RequestIDFromMetadata` 获取。

> 可见「流式」和「工具执行」并不互斥：原语 `StreamChatCompletion` 不执行工具，流式闭环 `RunChatCompletionStream` 则边流式边执行。
>
> Responses API 同样对称：原语 `StreamResponse` 不执行工具，流式闭环 `RunResponseStream(ctx, req, RunResponseStreamOptions)` 边流式边执行——每轮 SSE 抓 `response.output_text.delta` 逐 token 透传、`response.completed` 取完整 `Response` 提取 `function_call` 执行回填，逻辑与 Chat 流式同构。
>
> Anthropic Messages 兼容层也提供 `RunMessageStream(ctx, req, RunMessageStreamOptions)`；它把 Chat SSE 转换为 Messages 事件语义，执行 `tool_use`，并把 `tool_result` 追加到下一轮。

如果任一显式 `Run*` 在后续模型轮次返回错误，SDK 会同时返回非 nil 的部分结果。已完成的 `Steps`、`ToolResults` 及累积 transcript 不会丢失，调用方应先记录或持久化这些信息，再处理错误。

### 3.5 Responses API 闭环 `RunResponse`

```go
result, _ := c.RunResponse(ctx, joytoken.ResponseRequest{
    Model: joytoken.ModelAuto,
    Input: "北京天气?",
}, joytoken.RunResponseOptions{})

fmt.Println(result.FinalText)
```

Responses 协议直连正式 `/openai/v1/responses`：模型请求以 `output` 里的 `function_call` item 表达；SDK 保留原生 output items，并把执行结果作为 `function_call_output` item 追加进下一轮 `input`。注册工具会以 Responses 的扁平 `function` tool 形式并入，且**保留用户请求里的内建工具**（`web_search`/`web_search_preview`/`file_search`）不动。当显式启用 `WithDefaultBuiltinTools(true)` 时，SDK 会自动注入零配置的 `web_search_preview`；该选项默认关闭。`file_search` 不默认注入，因为需要调用方提供 `vector_store_ids`。

## 4. 链路调整对比

| 环节 | 改造前 | 改造后（RunChatCompletion） |
| --- | --- | --- |
| 工具声明 | 用户手填 `request.Tools` | 用户可填，也可 `WithTools` 注册后自动并入 |
| tool_calls 处理 | 用户手写解析/执行/回填 | client 自动执行已注册 handler 并回填 |
| 多轮循环 | 用户手写 | client 内建（MaxSteps 兜底，默认 8） |
| panic 防护 | 用户自理 | `safeExecuteTool` 统一 recover |
| `CreateChatCompletion` | 单次不执行 | 用户工具仍为原语透传；完全缺省时可自动执行 SDK 默认工具闭环 |

请求组装遵循“请求级 > 注册集 > 默认集”的整组选择，不再混合用户工具和默认工具。

## 5. 判定：本身兜底工具 vs 用户传入工具

两个判定发生在不同阶段，规则如下。

### 5.1 请求下发阶段（哪些 tool schema 发给模型）

`chatTools`、`responseTools`、`messageTools` 使用相同规则：
- `request.Tools != nil`：原样复制请求级工具，默认工具不参与；
- 请求未提供 Tools、但 `WithTools` 已注册：只发送注册集；
- 两者都没有：发送 SDK 默认集；
- 显式空切片：发送空工具集。

### 5.2 执行阶段（tool_call 由谁执行）

请求级工具永远不会回退到同名 SDK 默认 handler。只有 `WithTools` 中同名注册的 handler 才能在显式 `RunXxx` 中执行；完全缺省时才使用默认 handler。

> 各闭环入口共用同一执行器；Chat 用 `role=tool` 并发送到 Chat Completions，Responses 用原生 `function_call_output` 并发送到 Responses，Anthropic 用 `tool_result` 经本地适配后发送到 Chat Gateway。

### 5.3 三条「不覆盖用户」保证

1. **整组用户优先**：一旦用户提供工具，默认集完全退出。
2. **原语只透传用户工具**：`CreateXxx`/`StreamXxx` 不抢执行用户 handler。
3. **执行显式化**：用户工具仅在 `RunXxx` 中执行；请求级同名工具不会命中默认 handler。

## 6. 兼容性小结

- 网关路由：Chat 调用 `/openai/v1/chat/completions`，Responses 调用正式 `/openai/v1/responses`；Anthropic Messages 是 SDK 上层兼容适配器，转换后调用 Chat Gateway。
- 默认兜底：仅当请求级 Tools 与 `WithTools` 都不存在时注入；用户工具集与默认集不混合。
- 原语方法：用户工具只透传；默认工具可在非流式 `CreateXxx` 中自动闭环。显式 `RunXxx` 用于执行用户注册 handler、控制 `MaxSteps` 并获取步骤信息。
- 依赖方向：`tooldef` 位于依赖图最底层（不 import 模块内任何包），根包/`tool/`/`agent`/`agent/toolkit` 单向依赖它，无循环依赖。
