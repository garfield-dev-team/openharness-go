# 调研报告：pi-coding-agent / oh-my-pi / pi packages 可落地"黑科技"

> 调研日期：2026-08-22
> 来源：
> - pi（上游框架）：github.com/earendil-works/pi/blob/main/packages/coding-agent
> - omp（oh-my-pi，pi 的深度强化 fork）：github.com/can1357/oh-my-pi
> - pi packages 生态（5300+ 包）：pi.dev/packages
>
> 视角：只看 agent 本体能力（工具、上下文、会话、多模型协同），不含 TUI 增强。
> 每条标注在 openharness-go 中的落地点（基于此前 P0 分析的代码结论）。

## 背景

- **pi** = 极简核心（默认只有 read/write/edit/bash 四个工具），一切能力通过 TypeScript extension API 外挂；session 是带 id/parentId 的 JSONL 树。
- **omp** = 在 pi 之上把每个工具做 harness 级重做（自测：弱模型编辑一次成功率 6.7%→68.3%，Grok 4 Fast 输出 token −61%）。
- **pi packages** = 证明 extension API + 包管理能形成生态：MCP 适配器、subagents、memory、plan mode 全是第三方包而非内核功能。

对 openharness-go 的启示：我们缺的不是功能，而是「可注入外部行为的骨架」和「针对弱模型优化的工具协议」。

---

## P0 清单

### 1. Hash-anchored 编辑（hashline）：内容哈希锚点替代字符串匹配 ⭐ 性价比最高

来源：omp #11。

- read 时每行附加稳定 3 字符内容哈希锚点；edit 时模型只给「锚点 → 新内容」，无需重抄旧文本。文件已变化则锚点失配 → 写盘前直接拒绝。
- 实测：Grok Code Fast 编辑成功率 6.7%→68.3%；Grok 4 Fast 输出 token −61%（消灭 string-not-found 重试循环）。对弱模型提升最狠，直接改变模型选型成本曲线。

落地：重做 `pkg/tools/builtin/file_edit.go` 与 `file_read.go`。纯 Go 可实现，无外部依赖。

### 2. Session 持久化为 JSONL 树：branching/fork 是免费副产品

来源：pi README「Sessions」。

- 每条消息一条 JSONL record，带 id + parentId → 单文件天然成树。/tree 跳回任意历史节点继续（分支切换），/fork 从任意用户消息复制新会话，--fork CLI 直达。
- 关键联动：compaction 有损，但完整历史永远留在 JSONL 里——压缩不再等于丢数据。

**上下文压缩增强**（来源：pi docs/compaction.md，与 #10 强相关）：

- **Cut point 识别**：从最新消息向前回溯累积 token，直到 keepRecentTokens（默认 20k）预算用尽即为切点。切点有硬规则——只能在 user / assistant / 自定义消息处切割；**tool result 永远不与它的 tool call 分离**。单个 turn 超预算时做 split turn：切点落在 turn 中间的 assistant 消息上，对 turn 前缀单独生成第二份摘要再合并（我们的 compact.go 完全没有这些边界保护）。
- **结构化摘要提示词**：固定 schema——Goal / Constraints & Preferences / Progress(Done·In Progress·Blocked) / Key Decisions / Next Steps / Critical Context，外加 `<read-files>`、`<modified-files>` 标签。文件清单跨多次压缩**增量累积**（从上一份 compaction entry 的 details 里继承再合并），保证"读过/改过哪些文件"永不失忆。
- **增量压缩（迭代式摘要）**：每次压缩把上一份 summary 作为输入传给 LLM（iterative context），且下一次的汇总区间从上一次的 firstKeptEntryId 开始（而非从 summary 条目开始），幸存消息会再次进入下一轮汇总。CompactionEntry 作为一等公民存进 JSONL 树（含 summary + firstKeptEntryId + tokensBefore + usage），重建上下文 = summary + 从 firstKeptEntryId 起的消息。
- **触发与恢复语义**：阈值 = contextWindow − reserveTokens(16k)；三种触发原因 manual/threshold/overflow 分开上报；overflow 触发时压缩后自动重试被中止的那个 turn。摘要请求用全新 session id 且禁写 prompt cache（一次性请求不值得污染缓存）。序列化时 tool result 截断到 2000 字符控制摘要请求本身的 token 量。
- **分支摘要（branch summarization）**：/tree 切分支时，从旧叶子回溯到公共祖先收集条目、按 token 预算取最新、生成摘要挂到新分支叶子上——换分支不丢上下文。配套 extension 钩子 session_before_compact / session_compact_failed 可整体替换压缩实现。

落地：P0 报告中 --resume/--continue 未实现、sessionID 无处持久化的正解。不要做线性 append 文件，直接上树形 JSONL。压缩侧按上述五点重造 `pkg/services/compact.go`：我们现有的 L1-L5 管道只有字符级截断/剪枝，缺 cut point 边界保护、结构化摘要、增量语义和 CompactionEntry 持久化四样核心机制。

### 3. Steering/Follow-up 消息队列：并发问题的正统解法

来源：pi README「Message Queue」。

- agent 运行中用户仍可输入：steering 消息在当前 turn 的工具执行完后立刻投递（边跑边纠偏）；follow-up 在全部工作结束后投递；abort 时队列退回编辑器。
- pi 处理运行中输入的唯一方式是严格串行 + 队列，而不是开 goroutine 并发跑 loop。

落地：替换 JSONLines 模式里 `go func(line){ rt.HandleLine }` 的并发提交（P0-4a 数据竞争根源），同时解决 REPL Ctrl-C 变砖问题（P0-4b）。

### 4. Time-traveling Stream Rules：流式中途规则注入 + 原点重试

来源：omp #04。

- 正则规则平时零上下文占用；输出流命中规则时中途 abort → 规则作为 system reminder 注入 → 从断点重试。注入内容可在 compaction 中存活。
- 本质：「规则常驻系统提示词」（每轮付 token 税）与「事后纠错」（太晚）之间的第三条路——只在违规瞬间付费。

落地：给 engine 加流拦截层，复用 pkg/hooks（现为死代码，见 P0 报告 P0-3），让 hooks 从摆设变成核心差异化能力。

### 5. Advisor：第二个模型旁听每一轮

来源：omp #06。

- 配置独立 reviewer 模型（自己的上下文、自己的模型），旁听主 agent 每一步，以内联 note 插入，级别分 aside / concern / hard blocker。主 agent 看到后纠偏或说明理由。
- doer 与 reviewer 分离，catch 主模型赶进度犯的错；advisor 可用便宜模型。

落地：在 `RunQuery` turn 循环加异步旁路调用（复用 apiClientAdapter + 第二模型角色）。工程量小，多智能体里 ROI 最高的一种。

### 6. Subagent 结构化输出 + git worktree 隔离

来源：omp #05。

- task 扇出子代理到各自隔离的 git worktree，互不踩踏工作区；最终产物是 JSON Schema 校验过的 typed 对象，父代理直接读字段，不解析散文。
- 配套 Agent Hub：实时查看每个子代理 transcript、发 steering、kill 卡死 worker。

落地：升级 `pkg/tasks`——现在 SubAgentExecutor 只返回文本。两步走：(a) 子代理输出强制过 schema 校验，失败重试；(b) TaskCreate 支持 isolate 选项用 `git worktree add` 隔离工作区，解决并行改同一目录的冲突。

### 7. Agent 自主记忆：retain / learn / recall + session 压缩成 mental model

来源：omp #13；pi packages 上 pi-memory、pi-blackhole 等高下载包验证了需求。

- agent 运行中主动写记忆：retain（存事实）、learn（沉淀经验，可晋升为 skill）、recall（检索）；每次会话结束压缩成 mental model，下次会话首轮自动加载。项目作用域隔离，后端可插拔。
- 关键差异：记忆由 agent 自己策展，而非启动时静态拼接 MEMORY.md。

落地：`pkg/memory` 目前只有 LoadMemoryPrompt 静态读取。最小可行版：3 个新 builtin 工具读写本地记忆库 + 会话结束时触发压缩 hook。

### 8. Provider 韧性三件套：fallback chain / 凭证轮换 / prompt-cache 友好

来源：omp「Four knobs」「/fresh」；pi 的 PI_CACHE_RETENTION。

- fallback chains：per-role/per-model 链，429/quota 时自动切下一个模型接完当前 turn，冷却后切回。
- round-robin credentials：同 provider 多 API key 轮换，带 session affinity（保 prompt cache 命中）与 per-key backoff。
- prompt cache 是一等公民：PI_CACHE_RETENTION=long（Anthropic 1h / OpenAI 24h）；/fresh 重置僵死 cache 状态；UI 直接显示 cache hit rate。

落地：`pkg/api/client.go/openai_client.go` 目前零重试零降级。先做按错误类型分流的重试 + 可配置 fallback 链；审计 system prompt / tools schema 的顺序稳定性以保 cache 前缀不变；顺带补 input_tokens 与 cache 字段统计（对应 P0 报告 P0-5b）。

### 9. Extension API + 项目信任门控：把「死接线」问题连根拔掉

来源：pi「Extensions」「Project Trust」；pi packages 生态本身。

- pi 内核极小，subagents/plan mode/MCP/权限弹窗全部经 extension API 实现：registerTool / registerCommand / on("tool_call") 事件钩子（可拦截、改参、拒绝）/ 甚至自定义 compaction。包经 npm/git 安装，5394 个包的生态由此而来。
- 安全配套 Project Trust：项目本地 extensions/settings/skills 首次加载前必须显式信任（trust.json 记录），未信任只加载全局资源——第三方代码不能静默执行。

落地：把我们的 ToolRegistry + hooks 升级为统一的事件总线式 extension 接口（Go 里可用接口注册 + Go plugin 或脚本解释器），同时给 `BuildRuntime` 加载 skills/plugins 的路径补上信任门控（当前任意 cwd 下 plugins/ 目录会被无条件加载执行，本身就是安全隐患）。

### 10. Checkpoint/Rewind + 有损压缩的可逆化

来源：omp 工具表（checkpoint、rewind）；pi compaction 文档。

- checkpoint：在对话中标记状态点；rewind：把探索性上下文整段剪掉、只保留一份简明报告——探索的 token 成本从上下文里消失但结论留下。
- 与 #2 联动：被剪掉的内容在 session JSONL 树里永久可回溯。

落地：作为我们 compaction 管道（pkg/services/compact.go）的高层原语补充：新增两个 builtin 工具 + 引擎支持消息区间折叠。修复 P0-5a no-op 后，这是 compaction 方向的下一步演进。

---

## 落地优先级建议

| 批次 | 条目 | 理由 |
|---|---|---|
| 第一批 | #2 #3 | 先修会话层地基（持久化 + 串行队列），P0 报告 P0-4/P0-5 一并解决 |
| 第二批 | #1 #8 | 工具协议升级与 API 韧性，直接提升所有模型的成功率与成本 |
| 第三批 | #4 #5 #7 | engine 能力扩展，依赖第一批的地基 |
| 第四批 | #6 #10 #9 | 多代理与生态化，工程量最大 |

## 未入选但值得知道

- **URL scheme 统一文件系统**（omp）：pr:// issue:// conflict:// agent:// 在所有 FS 型工具内透明解析，一个 read 工具覆盖 GitHub PR/issue/合并冲突——工具数量做减法的思路。
- **Magic keywords**（omp）：ultrathink/orchestrate/workflowz 三个小写关键词触发专门行为模式，仅在散文中匹配（跳过代码块/路径），实现极便宜。
- **Model roles 路由**（omp/pi）：default/smol/slow/plan/commit/advisor 十个角色按意图路由不同模型，commit message 用便宜模型的思路可低成本借鉴。
- **eval 持久内核**（omp #01）：Python/Bun 内核通过 loopback bridge 回调 agent 自身工具——数据分析场景大杀器，但工程量大。
- **LSP/DAP 集成**（omp #02/#03）：14 个 LSP op + 28 个 DAP op，rename 走 workspace/willRenameFiles；依赖 Rust native 层，Go 侧可考虑仅接 gopls LSP。
