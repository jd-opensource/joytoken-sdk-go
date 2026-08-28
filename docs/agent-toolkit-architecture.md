# JoyToken Agent 工具能力架构设计方案（提案 · 前后端一体）

> 面向对象：管理层 / 架构评审 / 接入方
> 文档性质：**改造设计方案（提案，尚未实现）**。本文描述"我们打算怎么改、为什么这么改、分几步落地"，不是已完成功能的验收报告。文中的接口名、参数、默认值均为**拟定设计**，最终以实现与评审结论为准。
> 一句话目标：**在"让 AI 知道有哪些工具"之外，补齐"安全地替 AI 把工具跑起来、出错能自愈、越权有护栏"的完整闭环；让 Go 与 TypeScript 两套 SDK 架构对齐，并刻意把工具执行放在 agent 层而非客户端层，守住分层与安全边界。**

---

## 〇、现状基线（改造前我们有什么）

在动工之前，先如实说明当前已具备的能力，避免把"要做的"当成"已做的"。

| 能力 | 当前状态 | 说明 |
| --- | --- | --- |
| 客户端 SDK 传输层 | ✅ 已具备 | Go 根包 / TS `@joytoken/client-sdk-ts` 已能发请求、收响应（OpenAI / Anthropic / Responses 三协议） |
| 基础 agent 循环 | ✅ 已具备 | 已有基础的"模型↔工具"多轮循环骨架 |
| 工具集（calculator/datetime/file/http/sql） | ⏳ 待改造 | 本方案新增，尚未实现 |
| 权限三档（Auto/Ask/Deny） | ⏳ 待改造 | 本方案新增 |
| 中间件（Timeout/Audit） | ⏳ 待改造 | 本方案新增 |
| 错误/panic 回喂自愈、参数强转、输入上限 | ⏳ 待改造 | 本方案新增 |
| 厂商托管工具透传（web_search / file_search） | ✅ 网关已支持转发 | 走透传路线，SDK 侧只需在 `ResponseTool` 声明字段，不本地实现（详见第二部分） |

> 因此：下文所有工具、权限、中间件、健壮性能力，除特别标注外，**均为本次要新建的设计**，请以"提案"视角阅读。

---

## 一、总体架构：两套 SDK，同一套分层（目标形态）

目标是让 Go SDK 与 TypeScript SDK 采用**一致的三层分层**，接入方在任一语言的心智模型可直接迁移。

| 层 | Go 包 | TS 包 / 目录 | 职责 | 现状 → 目标 |
| --- | --- | --- | --- | --- |
| 客户端 SDK | `joytoken`（根包） | `@joytoken/client-sdk-ts` | 发请求、收响应（OpenAI / Anthropic / Responses 三协议） | ✅ 已具备（无状态传输层） |
| Agent SDK | `agent` | `@joytoken/agent-sdk-ts`（`src/agent`） | 多轮"模型↔工具"循环编排、停止条件、错误回喂 | 🟡 有基础循环，待补错误回喂/停止条件 |
| Toolkit | `agent/toolkit` | `@joytoken/agent-sdk-ts`（`src/toolkit`，子路径 `./toolkit`） | 开箱即用的可执行工具 + 权限 + 中间件 | ⏳ 待新建（本方案核心） |

拟定的依赖方向单向：**`toolkit → agent → client`**。只想调模型的人拿轻量客户端，要工具的人再叠加 agent + toolkit，各取所需。

```
graph TB
    Client[客户端 SDK 传话] --> Agent[Agent SDK 编排循环]
    Agent --> Toolkit[Toolkit 执行工具]
 Toolkit --> Tools[工具集 + 权限 + 中间件]
```

---

## 二、拟提供的工具集：两大分组

> **先厘清一个常见疑问：web_search / file_search 为什么不打算放进下面的清单？**
>
> 工具其实有**两个来源**，本方案的清单只覆盖其中一类：
>
> - **① 厂商托管工具（走透传路线，SDK 不本地实现）**：如 Responses API 的 `web_search`（兼容 type=`web_search_preview`）、`file_search`。这类工具由**模型厂商侧执行**——宿主把工具声明发送到原生 `/openai/v1/responses` → JoyToken 网关选择并调用支持该能力的厂商 → 结果随响应回来。SDK 只需在 `types.go` 的 `ResponseTool` 里声明字段，**无需也不应本地实现**，所以不纳入本方案的"工具集"。Hosted 默认仍关闭；用户可显式声明，或对 `web_search_preview` 启用 `WithDefaultBuiltinTools(true)`。
> - **② 本地工具（本方案要新建的清单）**：calculator / datetime / file / http / sql，拟由 SDK 在本地进程内执行，作为"网关未透传对应能力时"的安全网。
>
> 换句话说：**web 搜索优先用厂商托管能力**（能力更强、无需宿主维护白名单/凭证）；本地的 `http_fetch` 只是网关不透传抓取能力时的兜底。二者不冲突，是互补关系。

拟按"是否需要宿主提供配置"分为两组。**只有第一组进入 Default 自动注入；第二组必须宿主显式注册。**（注意：本节只列本地工具，厂商托管工具见上方说明。）

### 分组一：零配置默认工具（拟自动注入、只读、无凭证、无网络）

| 工具 | 名称 | 参数 | 权限档位 | 拟定处理机制 | 是否默认注入 |
| --- | --- | --- | --- | --- | --- |
| 计算器 | `calculator` | `expression: string` | Auto | 递归下降解析器求值（+−×÷%、括号、一元正负），除零/模零/空表达式/多余字符结构化报错 | ✅ |
| 时间 | `datetime` | `timezone?: string` | Auto | 按时区格式化当前时间，返回 `datetime/timezone/unix`；无效时区报错 | ✅ |

这两个工具无副作用、不触网、不需凭证，因此拟可安全自动注入，构成 `Default` 集合。

### 分组二：宿主配置的本地兜底工具（拟需显式注册 + 宿主提供配置）

> 这些是"网关未透传对应能力时"的本地安全网。若网关已把 `http_fetch` 等转发给厂商，优先用厂商能力。

| 工具 | 名称 | 参数 | 建议权限 | 宿主须提供 | 拟定安全处理机制 | 默认注入 |
| --- | --- | --- | --- | --- | --- | --- |
| 读文件 | `file_read` | `path: string` | Auto | 沙箱根 `Root` | 相对路径解析进沙箱；防绝对路径/`..` 逃逸；单次读 ≤1 MiB；拒目录 | ❌ |
| 写文件 | `file_write` | `path, content` | **Ask** | 沙箱根 `Root` | 同上路径护栏；自动建父目录；单次写 ≤1 MiB；有副作用需审批 | ❌ |
| HTTP 抓取 | `http_fetch` | `url: string` | Auto(可信白名单)/Ask | `AllowedHosts` 白名单 | 仅 http/https；host **精确白名单**（空=全拒，防 SSRF）；超时默认 15s；响应体 ≤1 MiB 截断 | ❌ |
| SQL 查询 | `sql_query` | `sql: string` | Auto(只读)/Ask(可写) | `*sql.DB` 句柄 | 凭证只在宿主侧，模型只出 SQL 文本；`ReadOnly` 校验拒非 SELECT/WITH/EXPLAIN/SHOW/PRAGMA；结果 ≤200 行截断 | ❌ |

**为什么不打算进 Default**：它们需要沙箱根、白名单或数据库句柄才安全，SDK 无法替所有人预设默认值——一旦默认放开即全局隐患。宿主显式配置 + 授权，风险才可控、可审计。

---

## 三、每个工具"拟如何处理"

### 3.1 计算器 `calculator`
- 拟自实现递归下降解析器（`eval.go` / `eval.ts`），文法：`expr = term {(+|-) term}`、`term = factor {(*|/|%) factor}`、`factor = ±factor | (expr) | number`。
- 除零 / 模零 / 空表达式 / 尾部多余字符均抛结构化错误，错误经 agent 回喂模型自我纠错。

### 3.2 时间 `datetime`
- Go 拟用 `time.LoadLocation` 加载时区；TS 拟用 `Intl.DateTimeFormat("sv-SE", {timeZone})` 对齐同一行为。
- 缺省 UTC；无效时区报错。

### 3.3 文件 `file_read` / `file_write`（沙箱化）
- 拟定 `FileSandbox{Root, MaxBytes}`：所有模型给的路径都相对 `Root` 解析。
- `resolve()` 三重防逃逸：拒绝绝对路径 → `filepath.Clean` 归一 → 校验结果仍以 `Root` 为前缀（防 `..` 穿越）。
- 读拒目录、超限（默认 1 MiB）拒绝；写自动建父目录、覆盖同名文件。
- 读只读无副作用 → Auto；写有副作用 → **Ask**（宿主逐次审批）。

### 3.4 HTTP `http_fetch`（防 SSRF）
- 仅允许 `http`/`https`；host 转小写做**精确白名单**匹配。
- **空白名单 = 全部拒绝**（默认安全，SSRF 防护基线）。
- 单请求超时（默认 15s）、响应体 `LimitReader` 截断（默认 1 MiB，返回 `truncated` 标记）。

### 3.5 SQL `sql_query`（凭证隔离 + 只读约束）
- 宿主在配置期用自己的 DSN/密码 `sql.Open` 建 `*sql.DB` 注入；**模型全程只产出 SQL 文本，永远看不到连接信息**。
- `ReadOnly=true` 时，剥离前导注释后检查首关键字，仅放行 SELECT/WITH/EXPLAIN/SHOW/PRAGMA，其余拒绝 → 配 Auto。
- 允许写时 `ReadOnly=false` + Ask 逐次审批；结果按 `MaxRows`（默认 200）截断，`[]byte` 归一为字符串以干净序列化。

### 3.6 TS 侧三工具的拟定配置与调用（与 Go 一一对应）

三个宿主工具在 TS 侧的注册方式拟与 Go 保持同构，把 Go 的 struct 配置换成 TS 的对象字面量（以下为**目标 API 草案**）：

```ts
import { createAgent } from "@joytoken/agent-sdk-ts";
import {
  fileRead, fileWrite, httpFetch, sqlQuery,
  type SQLDatabase,
} from "@joytoken/agent-sdk-ts/toolkit";

// 文件沙箱：所有模型给的路径都相对 root 解析，越界即报错
const read = fileRead({ root: "/data/workspace" });          // Auto
const write = fileWrite({ root: "/data/workspace" });        // 建议配 Ask

// HTTP 抓取：host 精确白名单，空数组=全拒（防 SSRF）
const fetchTool = httpFetch({
  allowedHosts: ["api.internal"],
  timeoutMs: 15000,   // 默认 15s，拟用 fetch + AbortController 实现
  maxBytes: 1 << 20,  // 默认 1 MiB，超出截断并标记 truncated
});

// SQL：宿主注入 SQLDatabase 适配器，只读模式配 Auto
const query = sqlQuery({ db, readOnly: true });
```

**拟定的宿主 `SQLDatabase` 适配器**——把任意驱动包在最小接口后面，凭证只留在宿主侧：

```ts
// 以 better-sqlite3 为例（mysql2 / pg 同理，query/exec 换成对应 API 即可）
import Database from "better-sqlite3";

function makeSqliteAdapter(file: string): SQLDatabase {
  const conn = new Database(file);
  return {
    // 只读查询：返回 { columns, rows }
    query(sql: string) {
      const stmt = conn.prepare(sql);
      const rows = stmt.all() as Array<Record<string, unknown>>;
      const columns = stmt.columns().map((c) => c.name);
      return { columns, rows };
    },
    // 可选写入：仅在 readOnly=false 时需要
    exec(sql: string) {
      const info = conn.prepare(sql).run();
      return { rowsAffected: info.changes, lastInsertId: Number(info.lastInsertRowid) };
    },
  };
}
```

> 设计要点：`query`/`exec` 既可同步返回也可返回 `Promise`（如 mysql2 的 `pool.query`），工具内部统一 `await`。**连接串与密码只出现在这段宿主代码里，模型永远只看到 SQL 文本**——与 Go 侧 `sql.Open` 注入 `*sql.DB` 的安全边界拟保持一致。

---

## 四、拟统一的权限与中间件（两套 SDK 对齐）

### 权限三档（fail-safe，拟新建）
| 档位 | 行为 | 适用 |
| --- | --- | --- |
| Auto | 直接放行 | 只读、无副作用（计算、时间、只读查询） |
| Ask | 回调宿主审批，**未配处理器则拒绝** | 写文件、改数据库等副作用操作 |
| Deny | 直接拦截 | 明令禁止的工具 |

### 中间件链（拟新建）
- **Timeout**：每次调用限时。Go 拟用 goroutine + `select ctx.Done()` 并 `recover` panic；TS 拟用 `Promise.race` + `setTimeout`。
- **Audit**：调用前后回调，对接日志/监控。
- 拟定包装顺序：`middleware（外，先注册者最外）→ permission → tool.Execute（内）`。

### 拟补齐的生产级健壮性（四个缺口）
| 缺口 | 现状（改造前） | 目标（改造后） |
| --- | --- | --- |
| 工具报错 | 直接中断会话 | 转"观察结果"**回喂模型**，AI 自我纠错继续 |
| 工具 panic | 击穿进程 | `recover` 捕获（含超时 goroutine），转错误回喂 |
| 参数类型 | AI 传数字/布尔即报错 | 宽松转换（数字/布尔/JSON number → 字符串） |
| 超大输入 | 无上限 | 单参数 1 MiB 上限校验 |

---

## 五、分阶段实施计划

> 以下为拟定的落地步骤，示例代码为**目标 API 草案**，非已存在接口。

### 阶段 1：搭建 toolkit 骨架 + 默认工具（零配置）
- 新建 `toolkit` 包 / 目录，实现 `calculator` + `datetime` 与 `Default` 自动注入。
- 目标使用方式：
  - Go：`a := toolkit.NewAgent(agent.AgentOptions{Model: provider})`
  - TS：`const a = createAgent({ model: provider })`
- 效果：自动带上 `calculator` + `datetime`，Auto 权限，无需任何额外配置。

### 阶段 2：新增权限/中间件 + 高权限工具（宿主提供配置）
```go
tk := toolkit.New(
    toolkit.WithPermission(toolkit.Permission{Mode: toolkit.PermissionAsk, Ask: askHandler}),
    toolkit.WithMiddleware(toolkit.Timeout(15*time.Second), toolkit.Audit(logFn)),
).Register(
    toolkit.Calculator(), toolkit.DateTime(),
    toolkit.FileRead(toolkit.FileSandbox{Root: "/data/workspace"}),
    toolkit.HTTPFetch(toolkit.HTTPFetchConfig{AllowedHosts: []string{"api.internal"}}),
    toolkit.SQLQuery(toolkit.SQLConfig{DB: db, ReadOnly: true}),
)
a := agent.New(agent.AgentOptions{Model: provider, Tools: tk.Tools()})
```

### 阶段 3：权限回调对接宿主 UI
- 实现 `PermissionFunc`（Go）/ `PermissionFunc`（TS）：收到 `PermissionRequest{ToolName, Input, Step}` → 弹窗/审批流 → 返回 allow。
- **未配回调时 Ask 一律拒绝**，永不静默放行。

拟定审批交互落在宿主，SDK 不渲染任何 UI。两种典型形态：

**形态 A · CLI（读 stdin y/n）** —— 适合本地脚本 / CLI 工具：
```go
ask := func(_ context.Context, req toolkit.PermissionRequest) (bool, error) {
    fmt.Printf("工具 %q 请求执行(step %d)，参数：%v\n是否允许？[y/N] ", req.ToolName, req.Step, req.Input)
    line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
    a := strings.ToLower(strings.TrimSpace(line))
    return a == "y" || a == "yes", nil
}
```

**形态 B · GUI/Web（推送前端 + 等确认）** —— 适合 GUI / Web 服务：
```go
ask := func(ctx context.Context, req toolkit.PermissionRequest) (bool, error) {
    reply := make(chan bool, 1)
    select { // 1) 推给前端(WebSocket/SSE/MQ)
    case pending <- approvalRequest{req: req, reply: reply}:
    case <-ctx.Done(): return false, ctx.Err()
    }
    select { // 2) 等前端回传用户决定，全程尊重 ctx 不卡死
    case allow := <-reply: return allow, nil
    case <-ctx.Done(): return false, ctx.Err()
    }
}
```
> 设计要点：Web 形态**必须尊重 `ctx`**——用户迟迟不点或请求被取消时及时返回，而不是阻塞住整个 agent 循环。

### 阶段 4：观测与限时
- 注册 `Timeout` 防卡死、`Audit` 打点，对接现有日志/监控体系。

### 透传路线约束（设计红线，不可破坏）
- `WithDefaults` / `withDefaults` **仅在 `Tools == undefined/nil` 时注入默认工具**。
- 显式空数组 `[]` 保留"无工具"意图；自定义数组原样透传，SDK 不干预。

---

## 六、前后端一致性目标与验收标准

> 下表是本方案要达成的**目标对等状态**（当前均未实现），以及各能力的验收判据。

| 能力 | Go SDK 目标 | TS SDK 目标 | 验收判据 |
| --- | --- | --- | --- |
| 分层 / 依赖方向 | 🎯 | 🎯 | 依赖单向 toolkit→agent→client，无反向引用 |
| calculator / datetime | 🎯 | 🎯 | 递归下降解析器、时区处理、参数强转、1 MiB 上限行为一致 |
| 三档权限 fail-safe | 🎯 | 🎯 | Auto 放行 / Ask 无 handler 即拒 / Deny 拦截 |
| Timeout / Audit 中间件 | 🎯 | 🎯 | TS 用 Promise.race 等价实现，超时可触发 |
| 错误/panic 回喂自愈 | 🎯 | 🎯 | 工具报错转观察结果回喂，panic 被 recover |
| **file_read / file_write** | 🎯 | 🎯 | 沙箱三重防逃逸、1 MiB 上限、file_write 走 Ask |
| **http_fetch** | 🎯 | 🎯 | host 精确白名单防 SSRF、超时、响应体截断（TS 用 `fetch`+`AbortController`） |
| **sql_query** | 🎯 | 🎯 | 只读校验、行数截断、凭证隔离（TS 由宿主注入 `SQLDatabase` 适配器） |

> **TS 端 SQL 适配说明**：Node 无 `database/sql` 标准库，故 TS 侧 `sqlQuery` 拟由宿主实现一个最小 `SQLDatabase` 接口（`query(sql)` 读、可选 `exec(sql)` 写）注入，把自己的驱动（mysql2 / pg / better-sqlite3 等）包在后面。凭证与连接细节始终只在宿主侧，模型只产出 SQL 文本——与 Go 侧 `*sql.DB` 注入的安全边界拟保持一致。

### 计划中的测试覆盖（验收清单）

改造完成后，两套 SDK 的安全护栏均应有单元测试守护，用例语义一一对应。计划覆盖：

| 工具 / 能力 | 计划核心用例（Go 与 TS 语义对齐） |
| --- | --- |
| file_read / file_write | 写后读回、拒 `..` 逃逸、拒绝对路径、超限(≤上限)拒绝、缺文件报错、未配沙箱根报错 |
| http_fetch | 白名单内正常抓取、拒白名单外 host、空白名单全拒、拒非 http/https、大响应体截断(truncated) |
| sql_query | 只读语句分类(表驱动)、只读模式拒写、返回行、无 DB 报错、空语句报错、`maxRows` 截断、写走 exec |
| calculator / datetime / 权限 / 中间件 | 求值/时区/三档权限 fail-safe/超时 |

> 计划的测试替身：HTTP 用注入 `fakeFetch` / `httptest`、SQL 用内存 fake 适配器（无第三方依赖）、文件用系统临时目录。目的是让安全用例无真实副作用、可重复。

---

## 七、设计决策：为什么不把工具集放进客户端 SDK（直白版）

**核心：客户端和工具集是两件不同的事，混在一起会更乱、更不安全。**

1. **客户端只管"传话"，工具集要管"干活"。** 客户端像快递员——送请求、拿回信，一趟结束，不记事、不做决定。工具集要真去读文件、连库、跑多轮，是有状态、有风险的"干活"。把干活塞进快递员，等于让快递员顺便进你家开冰箱——职责越界。
2. **安全责任必须留在业务方手里。** 能读哪个目录、连哪个库、允不允许发外网，是各接入方各不相同的安全决策。SDK 不能替所有人预设，否则默认放开即全局隐患。放 agent 层由宿主显式授权，风险可控、可审计。
3. **保持业界一致。** OpenAI、Anthropic 官方 SDK 都这么分：客户端只收工具调用意图，绝不替你执行。保持一致，用户零学习成本。
4. **保护"轻量客户端"这条路。** 只想调模型的人不必背上工具循环的状态与依赖。

**一句话**：不是"客户端不能有"，而是"放进去会打破边界、放大风险、还违背行业惯例"。我们主张的一致是——**传话归客户端，干活归 agent**。

## 八、默认带 Tools + 可降级（推荐用法）

前面讲了"为什么不把执行塞进客户端"。但很多接入方希望**开箱即用**：什么都不配就自带工具、自动执行；只在需要时才关掉。我们支持这种体验，且**无需把执行引擎塞进 client**——关键是让"开关决定用哪个包/传什么参数"，而不是让某个包内部行为随一个 bool 分裂。

### 8.1 现有雏形

`agent/toolkit` 的 `WithDefaults` 已经是"默认开"的实现：当 `AgentOptions.Tools == nil` 时自动注入 calculator/datetime 等零配置默认工具。用户什么都不传就有工具；想关掉，显式传空工具集即可。入口在 `NewAgent`，不在 client。

### 8.2 "前端可关闭"的落地分支

开关只控制两件事：**是否注入工具**、**是否走执行循环**。据此分两条路径：

- **开关 = 开（默认）**：宿主用 `agent` + `WithDefaults` → 带默认工具、自动多轮执行。
- **开关 = 关**：宿主改用裸 `joytoken.Client`（或给 agent 传空 Tools）→ 退化成纯对话，一趟结束。

```
graph TB
    A[前端 启用Tools 开关] --> B{开关状态}
    B -->|开 默认| C[agent + WithDefaults]
    C --> D[带默认工具 自动多轮执行]
    B -->|关| E[裸 client 或 空Tools agent]
    E --> F[纯对话 一趟结束]
```

### 8.3 设计红线

开关活在**宿主/前端配置**里,不能做成 client 内部的 bool。否则 client 会重新退化成 mini-agent，破坏"传话归客户端、干活归 agent"的边界(见第七部分)。正确做法：**选包 / 选参数**，而非**选运行时 flag**。
