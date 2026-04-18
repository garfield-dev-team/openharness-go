# OpenHarness Go

OpenHarness-Go 是一个基于 Go 语言开发的高性能、高可扩展的 AI Agent Harness 项目。该项目实现了一个强大的多轮智能体引擎，不仅原生支持挂载文件操作、Shell 执行等工具，还完美支持了 **MCP (Model Context Protocol)**、**Progressive Disclosure Skills (渐进式披露技能)** 以及 **Task 并行子智能体系统**。

它旨在提供一个受控、安全且具有强大上下文管理能力的执行环境，适用于自动化代码开发、代码审查、系统管理以及复杂的并行任务编排。

## 🌟 核心特性

- **多轮会话引擎 (`engine`)**: 支持状态维护、SSE 流式解析，并在终端提供 Hacker 风格的彩色日志输出（包含工具参数展示、`⏳ Thinking...` 状态符）。
- **动态上下文压缩 (`compaction`)**: 内置基于 Claude Code 理念的 5 阶段上下文压缩管道（L1 结果截断、L2 Snip、L3 微压缩、L4 坍缩、L5 LLM 总结），确保即使进行数百轮工具调用也不会导致 Token 溢出，并提供实时的 `[🧠 Brain Capacity]` 监控。
- **高维插件与技能系统 (`plugins & skills`)**: 独创的**分层索引（Hierarchical Lazy Loading）机制**。支持挂载成百上千个 Markdown 定义的 Agent 技能，在初始时仅暴露插件命名空间索引，由大模型按需下钻加载，彻底解决上下文爆炸和注意力稀释问题。
- **并行任务与子智能体 (`tasks`)**: 从串行 Todo 模式升级为并行的 Task 架构。大模型可以通过 `TaskCreate` 工具派生多个运行在**工具白名单沙箱**（如 Explore 模式只读）中的子 Agent，实现复杂任务的并行处理和结果汇总。
- **MCP 客户端集成 (`mcp`)**: 实现了对 JSON-RPC 2.0 协议（Stdio 传输层）的无缝支持，一键接入海量外部 MCP 服务端扩展能力。
- **深度思考模型支持 (`reasoning`)**: 深度兼容 OpenAI API 规范，完美支持带有 `reasoning_content` (思维链) 的深度思考模型（如 DeepSeek-R1）。

## 🏗️ 系统架构

```text
┌─────────────────────────────────────────────────────────────┐
│                      TUI / REPL Layer                       │
│  (ANSI Colors, 🧠 Brain Capacity, ⏳ Thinking, Tool Logs)     │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                    Agent Engine (RunQuery)                  │
│                                                             │
│  ┌─────────────────┐ ┌─────────────────┐ ┌───────────────┐  │
│  │ Context Compact │ │ OpenAI / Stream │ │ Task Registry │  │
│  │  (L1~L5 Stages) │ │ (Reasoning Fix) │ │ (Sub-Agents)  │  │
│  └─────────────────┘ └─────────────────┘ └───────────────┘  │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                       Tool Registry                         │
│                                                             │
│  ┌──────────────┐ ┌───────────────┐ ┌────────────────────┐  │
│  │ Built-in I/O │ │ MCP Connectors│ │ Skills & Plugins   │  │
│  │ (Bash, Read) │ │ (JSON-RPC 2.0)│ │ (Lazy Namespace)   │  │
│  └──────────────┘ └───────────────┘ └────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## 🚀 快速开始

```bash
# 1. 构建项目
go build -o openharness ./cmd/openharness/main.go

# 2. 运行交互式 REPL
./openharness

# 3. 运行单次指令
./openharness --prompt "帮我列出当前目录的文件并用 karpathy-guidelines 技能进行审查"
```

## ⚙️ 配置文件

在用户目录下创建配置文件：`~/.openharness/settings.json`

```json
{
  "provider": "openai-compatible",
  "base_url": "https://openrouter.ai/api/v1",
  "api_key": "sk-xxxx",
  "model": "meta-llama/llama-3.3-70b-instruct:free"
}
```

如何接入免费模型：

- [OpenRouter.ai](https://openrouter.ai/models) 提供的免费模型接入
- [NVIDIA AI](https://build.nvidia.com/models) 提供的免费模型接入

## 📚 内置插件与技能 (Plugins & Skills)

本项目内置了高维度的插件系统，你可以将各种 `.md` 格式的技能定义放到 `plugins/<plugin-name>/skills/` 目录下。

目前系统已内置以下高级插件集合：

### 1. `anthropic` (Anthropic 官方优质插件)
- `anthropic:code-review`: 并行代码审查，支持自动读取 PR 详情、提取变更。
- `anthropic:code-simplifier`: 代码精简专家，重构复杂代码的专项技能。
- `anthropic:mcp-builder`: MCP 服务生成器，教 Agent 如何快速编写一个标准的 MCP Server。
- `anthropic:skill-creator`: 技能生成器，指导 Agent 如何为你编写新的 `SKILL.md`（元技能）。
- `anthropic:frontend-design`: 前端设计辅助，包含 React/UI 最佳实践。
- `anthropic:webapp-testing`: Web 应用测试专家。
- `anthropic:pptx`: 幻灯片制作与排版技能。
- `anthropic:ralph-loop`: 自动化测试与反馈循环机制脚本。

### 2. `superpowers` (社区增强技能包)
包含系统性 Debug、测试驱动开发、子智能体规划等数十个强大指令。
- `superpowers:systematic-debugging`: 深度 Bug 分析与排查框架。
- `superpowers:writing-plans`: 任务拆解与子智能体 (SubAgent) 计划编写。
- `superpowers:test-driven-development`: TDD 开发模式指导。
- *(以及更多社区沉淀技能...)*

### 3. `karpathy` (大师级代码准则)
- `karpathy:karpathy-guidelines`: Andrej Karpathy 总结的代码开发黄金准则（保持极简、手术刀式修改、明确的思考与验收条件）。

**如何使用？**
大模型在启动时只会看到插件包的索引名称。当大模型需要特定能力时，它会主动调用 `Skill` 工具进行**按需下钻加载**，彻底避免了初始上下文爆炸。

## 架构与组件

- `cmd/openharness`: 命令行入口。
- `pkg/engine`: LLM 交互、上下文压缩、工具分发的引擎核心。
- `pkg/tasks`: 并行任务注册表与子智能体（SubAgent）执行器。
- `pkg/api`: SSE 流式解析器与 OpenAI/Anthropic 协议适配层。
- `pkg/skills`: 渐进式披露技能的 Markdown 解析与动态加载器。
- `pkg/tools`: 内置工具定义（文件读写、Task 管理等）。
- `pkg/mcp`: MCP 协议的客户端实现。

---

## ⚠️ 后续迭代规划

本项目正处于高速迭代中，目前已修复了初版中存在的工具并发数据竞态、静默崩溃等重大 Bug。接下来的迭代重点：

### 1. 安全性与权限沙箱 (高优先级)
- **严格的目录穿越防护**: 增强 `FileWrite`/`FileEdit` 的路径沙箱（`filepath.Clean`），防止大模型越权操作。
- **指令执行权限控制**: 为 `Bash` 工具引入基于规则的 `PermissionChecker`（如拦截 `rm -rf /` 等高危命令的二次确认机制）。

### 2. 记忆检索与存储 (中优先级)
- **向量化语义检索**: 将目前基础的字符串匹配 `memory` 升级为真正的向量嵌入（Embeddings）模型检索，支持长期的跨项目知识沉淀。

### 3. MCP 协议完善 (低优先级)
- 实现除 `stdio` 之外的其他传输协议（如 SSE）。
- 支持 JSON-RPC 2.0 的异步通知和 ID 多路复用。

* 增加日志模块，记录Tools、Skills调用详情
* 完善记忆系统的实现（分层记忆，项目级别、用户偏好等）
* Agent Team/Coordinator Mode 的实现
