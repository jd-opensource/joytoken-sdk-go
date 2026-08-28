# JoyToken SDK 调用链路总览

本文梳理 JoyToken Go SDK 从「调用方发起请求」到「模型 + 工具闭环执行」的完整链路,涵盖两条并列入口:

- **裸客户端层**:`joytoken.Client`(根包),直接面向 Chat Completions / Responses API。
- **Agent 工具层**:`agent` + `agent/toolkit`,在客户端之上封装多轮 Agent 循环与安全工具。

底层由 `tooldef` 提供工具抽象与默认工具实现,`tool` 提供执行原语。

---

## 1. 依赖方向(单向,无环)

```
agent/toolkit  ->  agent  ->  (root) joytoken.Client  ->  tool
                                        |
                                        v
                                     tooldef   (依赖图最底层, 被所有上层复用)
```

- `tooldef`:工具的单一真源。`Calculator()`、`DateTime()`、`EvalExpression()` 都在此定义,任何上层都复用它,避免重复实现。
- `joytoken.Client`:HTTP 通信 + 工具注入 + 闭环执行。
- `agent` / `agent/toolkit`:面向 Agent 场景的高层封装 + 安全工具(file/http/sql)。

---

## 2. 六个协议原语入口

`joytoken.Client` 为 Chat、Responses、Messages 各提供一个非流式和一个原始流式入口。工具来源按“请求级 > Client 注册集 > SDK 默认集”三档互斥解析：

| 入口 | API | 是否自动闭环 |
| --- | --- | --- |
| `CreateChatCompletion` | Chat Completions | 是(按执行归属自动闭环) |
| `StreamChatCompletion` | Chat Completions(SSE) | 否(单次原始流) |
| `CreateResponse` | Responses | 是 |
| `StreamResponse` | Responses(SSE) | 否 |
| `CreateMessage` | Anthropic Messages 本地适配 | 是 |
| `StreamMessage` | Anthropic Messages 本地适配(SSE) | 否 |

对应的显式闭环入口(opt-in、可回调):

- `RunChatCompletion` / `RunChatCompletionStream`
- `RunResponse` / `RunResponseStream`
- `RunMessage` / `RunMessageStream`

---

## 3. 默认工具注入链路

所有入口在发请求前经过统一解析器:

```
请求进入 -> c.chatTools(request) / c.responseTools(request)
              |
              v
        c.resolvedTools()             # 注册集与默认集整组二选一
              |
   defaultLocalTools 开关 == true ?    # WithDefaultLocalTools(false) 可关闭
              |
              v
   defaultLocalToolSet()             # calculator + datetime
   c.defaultFileTools()              # file_search + list_dir + file_read + file_write
   c.defaultShellTools()             # shell
```

- 请求显式 `Tools`（包括空切片）时原样使用请求集；否则若调用方注册过工具，则整组使用注册集；只有两处都没传时才使用默认集。
- 默认集顺序稳定，便于确定性列举；`file_write` 与 `shell` 始终声明，但必须通过各自权限回调才能执行。
- Responses 的 hosted built-in 默认关闭；可用 `WithDefaultBuiltinTools(true)` opt-in 注入 `web_search_preview`。用户显式传入的 hosted tool 始终原样透传；`file_search` 因需 `vector_store_ids` 不自动注入。

---

## 4. 非流式闭环时序(以 CreateChatCompletion 为例)

```
CreateChatCompletion(ctx, req)
  |
  +-- request.Tools 非 nil 或 Client 有注册工具
  |      `-> createChatCompletionOnce         # 单次透传，不执行调用方工具
  |
  `-- 两处都没有工具
         `-> runChatCompletion                # 注入并执行 SDK 默认兜底工具
                `-> createOnce -> execute -> 回填 -> 继续，直到收敛或 MaxSteps
```

`createChatCompletionOnce` 是私有单次原语,`RunChatCompletion` 复用它,避免递归回到自动闭环的 `CreateChatCompletion`。

---

## 5. 流式闭环时序(RunChatCompletionStream)

```
RunChatCompletionStream(ctx, req, opts)
  |
  v
循环 step < MaxSteps:
   streamOneChatTurn(ctx, stepReq, onTextDelta)     # 消费一轮 SSE
      |    - deltaText: 每个文本增量回调 OnTextDelta
      |    - toolCallAccumulator: 按 index 拼接分片的 tool_call(id/name/arguments)
      v
   重组 assistant 消息(text + tool_calls)
      |
      v
   executeToolCalls -> OnToolResult 回调 -> 结果作为 role=tool 消息回填
      |
      v
   若本轮无 tool_calls -> StoppedBy="stop", 结束
```

Responses 流式闭环 `RunResponseStream` 结构对称(function_call 输出项驱动)。
Messages 流式闭环 `RunMessageStream` 结构同样对称(tool_use / tool_result 驱动)。所有显式 `Run*` 在后续轮次失败时都会返回已完成的部分结果和错误。

---

## 6. Agent 工具层链路

```
toolkit.NewAgent(AgentOptions{ Model: NewJoyTokenProvider(client), ... })
  |
  v
runner.Run(ctx, message)
  |
  v
agent 循环: Provider(封装 client) 发起模型调用 -> 工具执行 -> 回填
  |
  v
toolkit 默认工具集: Calculator / DateTime(委托 tooldef)
                    + 安全工具 File / HTTP / SQL(需显式配置 + 权限)
```

### 权限中间件包裹

安全工具(FileWrite、HTTPFetch、SQL 写等)经权限中间件包裹:

```
tool.Execute
  -> PermissionMiddleware
       - PermissionAuto:直接放行
       - PermissionAsk:调用 Ask 回调征询;无回调则 fail-safe 拒绝
       - PermissionDeny:直接拒绝
```

### 安全工具边界

- **File**:`FileSandbox.resolve` 用 `filepath.Clean` + 前缀校验,拒绝绝对路径与 `../` 逃逸;`MaxBytes` 默认 1 MiB。
- **HTTP**:主机名 allowlist,空 allowlist 全拒(fail-safe);强制 http(s);`LimitReader` 限流。注意:仅按主机名校验,不解析 IP,不防内网重绑定(见 `http.go` 注释)。
- **SQL**:凭证经 `SQLConfig` 注入 `*sql.DB`,不到模型;`ReadOnly` 模式经 `isReadOnlyStatement` 剥离 `--` 与 `/* */` 注释后校验前导关键字,拒绝写语句。

---

## 7. 一图速览

```
调用方
  |                                            
  |-- joytoken.Client(裸客户端: 4 原语 + Run* 闭环)
  |       |-- chatTools/responseTools -> resolvedTools -> tooldef 默认工具
  |        |-- createChatCompletionOnce(单次) <- RunChatCompletion(闭环)
  |        `-- HTTP -> 网关 -> 模型
  |
  `-- agent/toolkit(Agent: NewAgent.Run 多轮)
           |-- Provider 封装 client
           |-- 权限中间件包裹安全工具
           `-- File / HTTP / SQL + tooldef 默认工具
```
