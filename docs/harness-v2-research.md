# 调研报告：Pi Harness V2 —— 可恢复运行时（durable operations / lanes / recovery）

> 调研日期：2026-08-29
> 来源：
> - 设计规格全文：https://github.com/earendil-works/pi/blob/harness-v2/j4/packages/agent/docs/harness-v2.md（3446 行，session v4 格式 + AgentHarness 运行时规格；文末 work-package 分解显示 R0–R2、J0–J3、F0 等包已完成，实施进行中）
> - 开发笔记（锐评转载）：https://x.com/MaxForAI/status/2091056022551187579
> - 定位文：https://earendil.com/posts/what-is-a-harness/
> 视角：只看 harness 执行语义——持久化、恢复、并行、扩展边界。与 [pi-omp-research.md](pi-omp-research.md)（2026-08-22，pi v3 能力面）互补不重复：那一轮吸收的是「功能」，这一轮是「执行模型」；压缩机制细节（cut point、结构化摘要、增量语义）仍以该文及其配套设计文档为准。落地决策见配套提案 note：`.agents/notes/proposed/architecture/2026-08-29-harness-v2-durability-core.md`；本文只承载调研事实与机制细节。

## 1. 三份材料各自在说什么

### 1.1 开发笔记：三个观察（动机层）

**DSH 的上下文管理：剪枝 vs 剪枝+落盘。** DSH 对超长工具结果做永久有损剪枝；Pi 主张 prune+spill——剪掉的内容全文落盘保留可检索路径（path + offset），模型需要时自己 grep 回来。笔记引用的实测：19 个真实会话上 context 占用 −26%~−35%，uncached prefill −72%~−88%。差别不在省多少 token，而在「被剪掉的信息是否还存在于宇宙中」。

**Claude Code 的 schema 协同进化。** 较新的 Claude 模型在第三方 harness 上工具调用质量下降：幻觉出只有 Claude Code schema 里才有的参数（如 `requireUnique`）。模型与特定 harness 协同进化成一体——Claude+Claude Code、DeepSeek+DSH、GPT+Codex 各自成为整合品；脱离 harness 单独评测模型越来越没有意义。

**50 小时问题。** 对 DSH、Claude Code、OpenCode 一类方案的共同批评：harness 的本质仍是「while 循环 + 模型 + 工具 + 上下文塞满就 summarize + 祈祷」。竞争维度已从功能数量转向执行语义——一个连续跑 50 小时的 agent 是否还知道自己在干什么；Pi V2 的答案是把一次被接受的 prompt 当作数据库事务来执行。

### 1.2 What is a Harness（定位层）

harness 的四要素：系统提示、工具、agentic loop、翻译层（模型 token I/O ↔ 真实世界副作用）。Earendil 的立场：中立的开源 harness 是用户主动权的工具——pi 走极简内核 + 扩展生态路线，能力长在边界上而不是内核里。

### 1.3 harness-v2.md 的位置

会话格式从 v3 升到 v4，兼容策略只有一条：旧 v3 文件必须能打开并恢复为 idle；其余格式与 API 一律允许破坏，不写迁移、不做 schema 版本化。规格本身是可直接开工的工程文档：每个公共方法标明归属包、崩溃点全目录化、测试分三层、work package 带依赖与合并顺序（串行运行时轨 H0→…→N1→O1→…，保证 `agent-harness.ts` 单写者）。

## 2. 核心机制

### 2.1 持久性规则与 provisioned id

> 效果之前：写一条意图记录，写明将发生什么、将产生哪些 id。效果之后：以恰好这些 id 追加结果 entry。

不存在也不需要多记录原子性：每条记录、每个 entry 各自持久。崩溃落在中间 → 恢复按意图类型决定补全、重试、或以合成结果关闭。「意图被满足」⟺「持有其 provisioned id 的 entry 存在」；provisioned id 已存在但内容不同 = 腐败。推论：任何时刻「这件事发生了没有」都是可判定的，判定只需一次按 id 的点查。

### 2.2 会话 = 四部分状态

1. **树**——对话本身，被动、只追加、属于任何 lane：消息、模型/思考等级/工具激活变更、压缩摘要、分支摘要、自定义 entry。entry 的父链永不变改。
2. **lane**——活跃状态的载体：命名 + leaf 指针 + 自己的队列 + 自己的配置视图。
3. **lane 操作日志**——持久性的实现：operation started / step attempted / tool started / queue enqueued / operation finished 等扁平时序记录，正常运行期间没有任何代码读它，只为崩溃后的新进程服务。
4. **全局 facts**——会话名、entry 标签等最新写胜出的值，保留为追加历史。

全部写入共享一个单调 `seq`。不变量：操作日志记录永不影响树、永不进入模型上下文——删光全部操作日志，剩下的仍是一棵完整合法的对话树；「对话」与「执行状态」在存储层就是分离的。

### 2.3 lane：并行的单位

lane 是树上的命名位置，最近的类比是 git worktree 里 checkout 的分支：名字锚定一个 entry，新工作推进它，可跳到任意历史 entry，不可被 checkout 两次。每条 lane 同时至多一个开放操作；lane 之间并行，互不协调。`main` 之外按外部身份建 lane（Slack thread id、邮件 thread id）；模型、思考等级、激活工具的配置是 leaf 路径上的 entry，因此两条 lane 可以各跑各的模型互不知晓。子代理工具可以直接跑在父会话的第二条 lane 上。关键工程事实：**pi 自己的交互模式只用一条 lane，且 UI 不展示该概念**——数据模型可以先落地而多 lane UX 完全可以后置。

### 2.4 操作、turn、步与重试

操作（operation）是 lane 上持久工作的单位，三型：run（一次被接受的 prompt 连同全部自动延续）、compaction、navigation。操作先接受后执行，接受即持久；结局 completed / failed / aborted / declined（结构型操作被 hook 否决）。

run 由 turn 组成；turn = 一次 assistant 步 + 其请求的完整工具批。步（step）是可重试单位：产生 assistant 消息、压缩摘要或分支摘要；失败的 attempt 重试同一步，**attempt 计数是持久的**——崩溃-重启循环无法重置重试上限。每个发起效果的 tool call 也是一步：`tool_started` 打开它，工具结果 entry 关闭它。

### 2.5 队列与延迟写

两种向运行中的 lane 输入的机制，按 abort 行为区分：

- **队列**承载对话意图：steer 纠正当前工作，followUp 在模型将停时加活，nextRun 播种下一次 run。abort 时 steer/followUp 死亡（payload 返还调用方），nextRun 存活。
- **延迟写**承载事实：步执行期间请求的 entry 与配置变更，abort 也要应用，在检查点落地。

两者都在**接受时**持久（记录携带完整 payload 与 provisioned id），树 entry 在消费点才写——接受与落地之间崩溃，恢复读取记录补写。「已接受的输入永不丢失」由这条两段式写入保证，另配 `queue_cancelled` 记录实现可持久撤销（没有它，崩溃会让已撤销的输入复活）。

### 2.6 检查点与 append-only context

turn 之间 lane 通过一个检查点：应用延迟写 → 消费 steering → 需要则压缩。背后的硬不变量：

> 一条 lane 的相邻请求之间，provider 上下文只在尾部增长。在上一次请求的尾部之前插入任何内容，都会使 KV cache 从插入点起全部失效。

mid-turn 的写推迟到检查点就是为了保证只追加；压缩是唯一被批准的例外（一次全失效换更小上下文）。这与我们「provider-facing determinism」纪律同源：对 cache 稳定性的要求从 tool schema 排序扩展到了上下文本身。

### 2.7 overflow 分类与一次性恢复

`length` 是歧义的：生成停在某个输出边界，但那可能是意图的输出上限（压缩无济于事），也可能是更小的上下文/供应商限制（压缩有用）。分类方法：拿实际输出 usage（含 reasoning tokens）对比**意图输出上限**（调用方 maxTokens，否则模型的 maxTokens，取钳制前的值）——达到意图上限 = 真停；低于 = 可恢复。没有任何上下文百分比启发式。

可恢复响应**整体丢弃**：不成为 entry，因此重试时（无论 live 还是崩溃后）无需从上下文里清洗它；其成本已由 usage 记录保住。随后压缩、重试；**每次对话输入至多恢复一次**——overflow 压缩记录新于本 run 最新被消费的对话消息时，再遇可恢复响应直接追加 give-up 错误 entry 并失败，循环有界。

### 2.8 工具执行：三阶段、replay 声明、崩溃矩阵

每次调用三阶段：prepare（查找/校验/before_tool 可改参或阻止，无效果）→ execute（效果本体）→ finalize（after_tool 逐字段 patch）。意图记录 `tool_started` 写在 1、2 之间，携带 after-hook 的 effective args、provisioned 结果 id 和工具声明的 `replay: "never" | "safe"`。

崩溃矩阵（恢复按调用逐个处理，保持源顺序）：

| 崩溃点 | 持久状态 | 恢复 |
|---|---|---|
| before_tool 前后、无记录 | 无 | 走完整正常路径（before_tool 再跑） |
| tool_started 后、结果前 | 意图无结果 | 记录与当前声明都为 safe 才重放（用持久化的 args）；否则合成 "interrupted" 结果 |
| 结果 entry 已存在 | 完成 | 跳过 |

被阻止/非法的调用不写 tool_started（无效果则无需意图），以 isError 结果 entry 持久。并行批的次序：phase 1 与意图写入按源序串行，phase 2 并发，phase 3 与结果落盘按源序。全批结果都持久化 `terminate: true` 时抑制自动续 turn。

### 2.9 恢复：状态 = 记录的归约

restore 只读、不追加、不启动效果。索引化发现代替全量扫描：`findOpenOperations(lane, limit 2)` → 0 = idle，1 = suspended，2 = 腐败；suspended lane 只做两次有界读（该 lane 自 `operation_started` 起的记录；leaf 回溯到操作锚点的自有 entry）。恢复永不新建操作，只继续开放的操作；恢复追加跳过已存在的 provisioned id——**恢复可重入**（半途再崩，重跑恢复永远安全）。

设计核心：`LaneState`（未完成步、attempt 数、工具批状态、待决队列/延迟写、overflow 恢复已用否、终态失败标记）被**定义**为记录 + 自有 entry 的归约函数的输出。live 执行与 restore 重算跑同一套规则，二者不可能分歧；每次 resume 结束后重算比对（fixed-point self-check），分歧即腐败并故障整个 harness。有效性检查覆盖：双开操作、attempt 不连续、cancel 无对应 enqueue、意图 id 内容不符等。

### 2.10 events / hooks / telemetry 三分

- **events** 被动观察：快照 + live 流，一次一步、有序、不持久不重放，重连 = 新快照；报告的是 hook 处理后的最终值。`watch()` 单步完成取快照与开始缓冲，`start()` 原子切换，无注册竞态；lane 级 watch 看到本 lane 的 transcript 与操作状态，`watchSession` 只看 inventory + 全量事件。
- **hooks** 拦截执行、可改变执行：before_run / before_resume / before_run_end / transform_context / before_request / before_payload / after_response / before_tool / after_tool / before_compaction / before_navigation 共 11 个插入点。凡进入持久状态的 hook 输出随记录持久化（before_run 输出进 operation_started、effective args 进 tool_started）；hook 自发的副作用崩溃可重放，需要幂等（按操作 id 键控）。before_tool fail-closed：抛异常的处理器 = 阻止该工具。
- **telemetry** 被动诊断：显式上下文传播（不用环境上下文），默认属性只含 schema 声明的标识符、计数、时延、stop reason、usage，永不携带 prompt、补全、工具参数与输出。

### 2.11 effects 边界、drive 模式与三层测试

每个效果——持久写、provider 请求、工具执行、hook、计时器——都经过一个注入的 `Effects` 句柄；过程代码只拿得到 `fx`，拿不到 session/models/tools。`drive: "manual"` 在每个效果前停靠，`peekAction()/executeAction()/runToCompletion()` 逐步驱动：**生产与测试跑同一套过程，drive 模式只控制边界**。崩溃模拟 = 在选定边界 close()，重开 backend，resume()。

- **Tier A（归约与恢复）**：用公共 API 预填某崩溃状态的记录与 entry，开 harness、resume、断言持久结果；覆盖 X1–X5 全部工具状态、半途恢复跑两遍等。
- **Tier B（写入一致性）**：仪器化的 Session 录下每个 E/R/L/G/H，对照规格中的 trace 逐条断言精确顺序——专抓「效果先于意图记录」这类回归；并**可执行地**断言 append-only context：一个 run 内每个请求的消息列表是前一请求的严格前缀扩展（压缩边界除外）。
- **Tier C（确定性交错）**：manual drive 驱动真实 harness；崩溃点从 trace 机械派生——每个 executeAction 后快照 backend、每个快照重开恢复；race 目录 12 行每行断言两种顺序都合法。

### 2.12 usage 台账

每次 provider 请求落定即写 usage 记录，**先于任何分类、重试或丢弃决策**——成本持久性不依赖结果持久性：被丢弃的响应、重试耗尽的 attempt，其花费都不随结果一起消失。三层分离：entry 上的 `usage` 字段是不可变展示快照；entry 的有效成本是按 entryId 汇总记录的读时查询；会话成本是全部记录之和。安全重放诚实计两次。

### 2.13 fork 与子代理

fork 只复制 entry（不复制记录与队列，派生即 idle；成本留在原会话）。子代理子会话 id 由 `(parentSessionId, toolCallId)` 确定性派生——safe replay 重新挂到同一子会话而不是孵出双胞胎，崩溃吞掉工具结果后子会话仍可从父会话发现。策略分界：与父共享历史的平台线程 = lane；需要隔离的（子代理、导出、克隆）= fork。

## 3. 对照 openharness-go 现状

| 维度 | Harness V2 | 现状（已核实） | 差距 |
|---|---|---|---|
| 持久性合同 | 接受即持久，意图先行 | `persist` 尽力而为、错误吞掉、内存权威（pkg/engine/query_engine.go `QueryEngine.persist`） | 根本性：崩溃 = 本次 run 全部丢失 |
| 操作记录 | 9 类记录 + provisioned id | 会话 entry 仅 message/compaction/branch_summary/stream_rule 四类；**KindCompaction/KindBranchSummary 全仓库无写入方**（pkg/session/store.go 定义未用） | 无执行状态可恢复 |
| 步与重试 | attempt 持久、跨重启、引擎可见 | HTTP 层重试存在但进程内且引擎不可见（pkg/api/client.go `retryableStatusCodes`）；流中断 = 引擎直接 EventError 终止（pkg/engine/query.go `RunQuery`），无步概念 | 中断即弃，attempt 不存在 |
| 压缩落盘 | CompactionEntry 自包含检查点（含 retainedTail） | L1–L5 管线只改内存消息，压缩结果不落树；重启后旧历史整体回归、再触发一次压缩（pkg/services/compact.go、pkg/engine/query_engine.go `executeRun`） | 压缩 = 会话级临时效果 |
| KV cache 纪律 | 只追加不变量 + 可执行测试 | 每个 turn 内 `ShouldCompact` 触发管线改写中段历史（截断/剪枝），失效频繁且无测试锁定 | 与 V2 不变量直接冲突 |
| 工具意图 | tool_started + replay 声明 + X1–X5 矩阵 | 并发 goroutine 直接执行，无意图记录、无 replay 概念（pkg/engine/query.go `executeToolCall`） | 取消/崩溃时的工具状态未定义 |
| abort | 持久 abort_requested + 和解（合成结果、收尾消息、outcome=aborted） | Cancel 丢弃部分 turn，steering 返还（EventUndelivered），树里不留痕迹（pkg/engine/query_engine.go `Cancel`） | abort 是遗忘而非收尾 |
| lane | 命名 leaf + 每 lane 队列与配置 | 单 leaf；单飞 + steering/followUp 交付点（pkg/engine/query_engine.go） | 现有单飞队列已是单 lane 特例，扩展点现成 |
| usage 台账 | 每请求记录、先于决策 | CostTracker 进程内存（pkg/engine/query_engine.go） | 跨会话成本不可审计 |
| 观察/拦截 | 快照协议 + 11 hook 点 + telemetry 三分 | HookExecutor 仅 Pre/PostToolUse（pkg/engine/query.go）；UI 走 StreamEvent 流，无快照+补发协议 | 扩展权力与观察通道都窄 |
| 测试 | effects 边界 + drive manual + Tier A/B/C | 常规包内测试 | 崩溃语义不可测 |

**已对齐的部分**（8/22 那轮的遗产，方向与 V2 一致）：会话树 JSONL + 撕裂尾截断 + NavigateTo/ForkTo（pkg/session/store.go，与 V2 树语义同构）；单飞 + steering/followUp 交付点（V2 检查点的雏形）；hashline 编辑与 cache-stable tool schema（同一 cache 纪律在工具协议层）；流规则注入。

## 4. 独立值得记录的观点

- **边界即架构**：V2 最大的贡献不是某个功能，而是把「什么是对话（树）、什么是执行状态（记录）、什么是 UI（快照+events）、什么是扩展权力（hooks）」在存储与 API 层划清。这与 [p0-tech-debt.md](p0-tech-debt.md) 记录的「实现了但没接线」失效模式互为表里：接线乱，是因为边界从未被定义。
- **模型×harness 协同进化**是选型与评测问题：我们支持多 provider，工具 schema 应保守、归一化，评测要带着 harness 一起测，不能只测裸模型。
- **prune+spill 的硬数据**（context −26%~−35%，uncached prefill −72%~−88%）说明上下文管理对成本曲线的影响大于多数功能投入。
- **竞争维度迁移**：hashline、压缩这些「单次成功」侧的优化有天花板；50 小时语义——崩溃、恢复、成本可审计——是下一块阵地，且 pi 已用 work-package + 三层测试把这块阵地工程化了。
- **工程方法可借鉴**：把设计写成「状态 = 记录的归约」+ 崩溃点全目录 + 机械派生的测试矩阵，使并行分包与合并顺序成为可能；这比任何单个机制更值得学。
