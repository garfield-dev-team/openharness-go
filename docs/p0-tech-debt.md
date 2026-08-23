# P0 技术债分析报告

> 分析日期：2026-08-22
> 方法：以代码为唯一事实来源（code as truth），逐层核查 CLI 入口 → UI 运行时装配 → engine → 各子系统之间的实际接线情况，不依赖 README 描述。
> 范围：重点覆盖 CLI 层（`cmd/openharness/main.go`）与 REPL/会话层（`pkg/ui/`、`pkg/hitl/`、`pkg/engine/`）。

---

## TL;DR

项目分层清晰，但**"造好的零件大量没有接线"**：权限系统、Hooks、MCP 配置三个子系统均已实现，却在运行时装配（`pkg/ui/runtime.go`）中被静默丢弃。另有并发生命周期缺陷和 compaction no-op 两类正确性问题。以下 5 个问题按严重程度排序。

---

## P0-1 权限系统完全未接线 —— 危险工具零确认执行（安全问题）

**这是全仓库最严重的问题。**

代码证据：

- `pkg/engine/query_engine.go:111` — engine 默认 `AllowAllPermissions{}`，即放行一切。
- `pkg/permissions/checker.go` — 完整实现了 mode 判定、path 规则、denied commands、`RequiresConfirmation` 的 `PermissionChecker.Evaluate()`，但**全仓库无任何调用方**（grep 验证：仅定义处出现）。
- `pkg/ui/runtime.go:52-192` — `BuildRuntime` 从未调用 `engine.WithPermissionChecker(...)`；`settings.Permission.Mode` 与 CLI 的 `--permission-mode` 参数被读入 settings 后**再无下文**。
- `pkg/tools/base.go:26` — `execCtx.AskPermission` 回调被注入到每个工具的执行上下文，但 grep 全部 builtin tools 后确认：**没有任何一个工具调用过它**（只有 `ask_user_question.go:70` 用了 `AskUser`）。包括 `BashTool`（`pkg/tools/builtin/bash.go:56-98`）——任意命令直接 `exec.CommandContext` 执行。
- `pkg/tasks/executor.go:108` — 子 agent 显式写死 `AllowAllPermissions{}`。

**影响**：
- `--permission-mode plan/default/full_auto` 三种模式行为完全相同 = full_auto。
- README 宣称的 "拦截 `rm -rf /` 等高危命令的二次确认机制" 在代码中不存在。
- 模型一旦被诱导（prompt injection），可直接执行任意 shell 命令、写任意文件，无任何闸门。

**修复建议**：在 `BuildRuntime` 中用 `permissions.NewPermissionChecker(settings.Permission)` 构造 checker，适配成 `engine.PermissionChecker` 接口注入；`RequiresConfirmation=true` 时走已接好的 `askPermission` 回调；子 agent 继承父级策略而非写死 AllowAll。

---

## P0-2 MCP 配置被静默丢弃 —— `openharness mcp add` 是假功能

代码证据：

- `pkg/ui/runtime.go:99-100`：

  ```go
  mcpConfigs := make(map[string]mcp.McpServerConfig)  // 永远是空 map
  mcpMgr := mcp.NewMcpClientManager(mcpConfigs)
  ```

  `settings.McpServers`（`pkg/config/settings.go:114`）从未传入。`cmd/openharness/main.go:188-231` 的 `mcp add/remove/list` 子命令正常读写配置文件，但运行时永远连接零个服务器。

**影响**：用户通过 CLI 添加的 MCP server 静默失效，无任何报错——比报错更糟的是用户以为配置成功了。`pkg/mcp/client.go`（439 行，仓库最大文件）整体成为死代码路径。

**修复建议**：一行接线 `mcpConfigs := settings.McpServers` + 类型转换即可让整个 MCP 栈复活；同时给连接失败加显式 stderr 告警。

---

## P0-3 Hooks 已构建但从未注入 engine —— 整个 hooks 包是死的

代码证据：

- `pkg/ui/runtime.go:102-107` 创建了 `hookReg` / `hookExecCtx` / `hookExec`，并存入 RuntimeBundle（line 187）。
- 但 `runtime.go:170-178` 构造 `NewQueryEngine(...)` 时，`engineOpts` 里只可能有 `WithAskUser`/`WithAskPermission`，**从未 append `engine.WithHookExecutor(hookExec)`**。
- 因此 `QueryContext.HookExecutor` 恒为 nil，`pkg/engine/query.go:291-295, 316-318` 的 PreToolUse/PostToolUse 分支永不触发。

**影响**：`pkg/hooks/` 下 executor(335 行)+loader+schemas 共约 800 行代码完全不生效；任何基于 hooks 的扩展（审计日志、工具结果改写等）都无法工作。且由于 P0-1 权限系统同样未接线，说明 `BuildRuntime` 这个装配点缺乏系统性的"装配完整性"校验。

**修复建议**：`engineOpts = append(engineOpts, engine.WithHookExecutor(hookExec))` 一行修复；更根本地，建议为 RuntimeBundle 增加 wiring 自检（如关键依赖 nil 即 fail-fast），避免同类"造好没插电"问题再次累积。

---

## P0-4 会话层并发与生命周期缺陷 —— JSONLines 数据竞争 + Ctrl-C 把 REPL 变砖

### 4a. JSONLines 模式并发提交导致历史丢失与输出乱序

- `pkg/ui/app.go:227-229`：每条 `FRSubmitLine` 都 `go func(line) { rt.HandleLine(ctx, line) }`，多个 agent loop 可同时运行。
- `pkg/engine/query_engine.go:125-205`：`SubmitMessage` 各自复制同一份 `qe.Messages` 快照跑 loop，结束时无条件写回 `qe.Messages = msgs`（line 200）→ **后完成者覆盖先完成者，对话历史丢失**；两个 loop 还会对同一 LLM 产生交叉的工具调用序列。
- 同时多 goroutine 直接 `fmt.Print` 写同一 stdout（`HandleLine` 内），流式输出必然交错乱码。

### 4b. 一次 Ctrl-C 之后整个 REPL 永久不可用

- `cmd/openharness/main.go:55`：`signal.NotifyContext(ctx, os.Interrupt)` 的 ctx 贯穿整个会话。
- 用户在 agent 运行中按 Ctrl-C（最自然的"打断"操作）→ ctx 被 cancel 且**无法恢复** → 此后所有 `SubmitMessage`/compaction 立即返回 `context.Canceled`，REPL 变砖，只能杀进程退出。
- 正确形态应是：Ctrl-C 仅取消当前查询（派生 ctx），长按/二次 Ctrl-C 才退出会话。

### 4c. 附带发现

- `RunJSONLinesMode`（`pkg/ui/app.go:192`，TUI/IDE 的协议入口）在 main.go 中没有任何 flag/subcommand 可达 —— 死入口，意味着 JSONLines 协议栈（protocol + hitl.Manager + adapter）从未被端到端验证过，4a 的 race 正因此未被发现。

**修复建议**：引擎内加 per-session 串行化（前一查询未结束则拒绝/排队新提交）；REPL 为每次查询派生 `queryCtx, cancel := context.WithCancel(ctx)` 并处理 Ctrl-C 语义；给 `RunJSONLinesMode` 加 CLI 入口使协议栈可测试。

---

## P0-5 Compaction 中途压缩是 no-op + token 统计系统性失真

### 5a. turn 循环内的 compaction 结果被丢弃

- `pkg/engine/query.go:159-160`：

  ```go
  if services.ShouldCompact(*messages, config) {
      services.RunPipeline(ctx, *messages, config, nil, nil) // 返回值未接收！
  }
  ```

  `RunPipeline` 内部先 `cloneMessages`（compact.go:85）在副本上操作——返回值一丢，这次压缩**对真实消息列表毫无作用**。注释宣称的 "prevent token blowout mid-loop" 完全无效：长 agent loop 中上下文仍会无限膨胀直至超限。

### 5b. Usage 只统计 OutputTokens，成本数据失真

- `pkg/api/client.go:323-334`：Anthropic SSE 解析结构体只声明了 `output_tokens`，`input_tokens` 被丢弃；`pkg/ui/adapter.go:47-49` 也只透传 OutputTokens。
- 后果：`CostTracker.InputTokens`（query_engine.go:31）恒为 0。而对话类负载中 input 通常占 token 消耗的 90%+ —— `/cost` 命令与 REPL 每轮打印的 "[🧠 Brain Capacity]" 数字严重偏低，用户会在错误的安全感下撑爆上下文窗口。

**修复建议**：5a 改为 `*messages, _ = services.RunPipeline(...)`（并考虑把 mid-loop 压缩结果同步回 engine）；5b 在 Anthropic client 解析 `input_tokens`（含 cache_read/cache_creation 如 API 提供），adapter 完整透传。

---

## 次级问题（非 P0，记录备查）

| 问题 | 位置 |
|---|---|
| `--resume`/`--continue` 直接返回 not implemented；sessionID 生成后无处持久化 | main.go:70-72, runtime.go:180 |
| 渲染逻辑重复：`HandleLine` 与 `printText` 各维护一份几乎相同的事件渲染 | runtime.go:222-283 vs app.go:63-90 |
| `bufio.Scanner` 默认 64KB 行上限，粘贴大文本进 REPL 会报 `token too long` | app.go:143 |
| `readStdin` 吞掉所有 read 错误 | main.go:146-159 |
| ClaudeMD 发现了多条路径却只读第一条 | runtime.go:126-132 |
| 全仓库仅 1 个测试文件（`pkg/skills/loader_test.go`） | — |
| `stream-json` 输出丢失 tool_use/tool_result 结构，只打 type/text，下游 TUI 无法还原事件 | app.go:108-120 |

---

## 建议的处理顺序

1. **P0-1**（安全，阻塞一切对外发布）
2. **P0-5a/5b**（正确性，长会话必现）
3. **P0-4**（稳定性，JSONLines 模式不可用 + Ctrl-C 变砖）
4. **P0-2 / P0-3**（功能复活，各约一行接线 + 补自检）
5. 为 `BuildRuntime` 增加 wiring 完整性校验与最小 e2e 测试，防止"死接线"模式复发
