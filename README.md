# OpenHarness Go

OpenHarness-Go 是一个基于 Go 语言开发的 AI Agent Harness 项目。该项目实现了一个可以挂载多种工具、MCP (Model Context Protocol) 服务和执行钩子（Hooks）的智能体引擎。它允许 LLM 在受控（或半受控）的环境下进行多轮交互、规划和工具调用，适用于自动化开发、文件操作和系统管理。

## 核心特性

- **多轮会话引擎 (`engine`)**: 支持状态维护、Token 消费统计以及并发/顺序工具调用的核心执行循环。
- **工具挂载与执行 (`tools`)**: 内置 `Bash`、`FileEdit`、`FileRead`、`FileWrite` 等基础能力。
- **MCP 客户端集成 (`mcp`)**: 实现了对 JSON-RPC 2.0 协议（Stdio 传输层）的支持，允许连接外部 MCP 服务端进行能力扩展。
- **记忆与检索 (`memory`)**: 提供基础的项目级别记忆文件读写与简单的文本匹配检索。
- **事件与钩子 (`hooks`)**: 支持在工具执行前后或特定事件触发时，通过 HTTP、Command、Prompt 或 Agent 模式执行拦截和校验逻辑。
- **交互界面 (`ui`)**: 提供 REPL 交互模式和单次查询的打印模式。

## 快速开始

```bash
# 构建项目
go build -o openharness ./cmd/openharness/main.go

# 运行交互式 REPL
./openharness

# 运行单次指令
./openharness --prompt "帮我列出当前目录的文件"
```

## 架构与组件

- `cmd/openharness`: 命令行入口。
- `pkg/engine`: LLM 交互和工具分发的引擎核心。
- `pkg/mcp`: MCP 协议的客户端实现。
- `pkg/tools`: 各种基础工具（如文件操作、Bash 执行）的定义。
- `pkg/hooks`: 钩子系统的注册和执行器。
- `pkg/memory`: 记忆系统的本地文件存储。

---

## ⚠️ 已知问题与后续迭代规划

根据当前代码库的扫描和分析，项目在安全性、功能完备性和健壮性上存在部分未实现的模块和潜在风险。以下是后续版本的核心迭代规划：

### 1. 核心逻辑的潜在风险与破坏性行为修复（高优先级）
* **修复工具调用的数据竞态 (Race Condition)**: 
  * 当前 `pkg/engine/query.go` 中，引擎对于 LLM 返回的多个 ToolUses 采用了 `go func()` 简单并发执行。这极易导致文件读写冲突。计划改为串行执行或引入文件级读写锁。
* **修复目录穿越漏洞 (Directory Traversal)**: 
  * `pkg/tools/builtin/file_write.go` 和 `file_edit.go` 仅使用简单的路径拼接。需要增加严格的目录沙箱校验（如 `filepath.Clean` 和 `strings.HasPrefix`），防止 LLM 越权读取或覆盖工作区外的敏感文件。
* **收紧危险命令执行的权限**: 
  * 当前 `pkg/tools/builtin/bash.go` 没有任何沙箱约束。结合 `AllowAllPermissions` 的 Dummy 实现，Agent 存在极大的 RCE（远程命令执行）风险。计划实现完整的 `PermissionChecker`，并对高危命令提供二次确认机制或运行环境沙箱化。

### 2. 未实现的模块与降级逻辑补全（中优先级）
* **完善 MCP 协议实现**:
  * 当前 `mcp/client.go` 的 JSON-RPC 读取仅假设了请求/响应是严格串行的（`sequential request/response`）。计划引入通过 ID 进行多路复用（Demultiplexing）的机制，以支持乱序返回和异步通知。
  * 实现除 `stdio` 之外的其他传输协议（当前标记为 `transport not yet implemented`）。
* **升级记忆检索系统**:
  * 当前 `memory/manager.go` 中的 `FindRelevantMemories` 仅实现了基础的字符串子串匹配（Fallback 实现）。计划接入真实的向量嵌入（Embeddings）模型以支持语义检索。
* **完善 Hook 默认配置逻辑**:
  * 补全 `pkg/hooks/loader.go` 中 `applyDefaults` 的占位逻辑，使其对齐更复杂的默认行为设定。

### 3. 代码质量与健壮性提升（低优先级）
* **清理无用代码**: 移除静态检查发现的冗余代码（如 `pkg/engine/query.go` 中的 `nowMillis()` 函数）。
* **增强错误处理**: 补全 `mcp/client.go` 中忽略的底层错误（如写入 `notifications/initialized` 时未处理断开异常）。
