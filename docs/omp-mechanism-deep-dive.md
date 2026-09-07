# 调研报告：omp 机制深读 —— 可迁移「黑科技」清单（P0/P1/P2）

> 调研日期：2026-08-30
> 来源：omp.sh/docs 全站目录 + github.com/can1357/oh-my-pi 的 docs/ 全部 130 篇工程文档，另加 25 篇关键增量（packages/snapcompact、hashline、wire 的 README，packages/coding-agent/src/prompts/ 下的提示词源文件，.omp/skills/ 下的三个内部技能）。
> 方法：核心机制文档（compaction/session/memory/TTSR/rulebook/handoff/retry/advisor/hooks/approval 等 ~30 篇）逐篇精读；provider 层、工具层、平台层由三路并行子代理全量通读后交叉核对。
> 与既有研究的关系：[pi-omp-research.md](pi-omp-research.md)（2026-08-22）已覆盖 omp 的功能面（hashline、会话树、steering、TTSR 概念、advisor 概念、记忆、provider 韧性、extension API、checkpoint）；[harness-v2-research.md](harness-v2-research.md)（2026-08-29）覆盖执行模型（durable operations）。本文不重复这两轮的结论，只收录**该轮未覆盖或只提到概念、而本文补齐了机制细节**的内容；条目末尾标注与既有清单的关系。落地均须遵守 AGENTS.md 的接线纪律（进 BuildRuntime、带反拆除测试）。

---

## P0 —— 修地基：直接对准 p0-tech-debt 与 API 韧性

### 1. 三层工具审批模型（修复 P0-1 的现成设计）
来源：docs/approval-mode.md、docs/settings.md。

omp 的审批不是一维开关，而是三个输入的解析序：
- **工具声明 tier**：`read` / `write` / `exec`（exec 是兜底默认，未知自定义工具一律 exec——安全默认）。
- **参数级判定**：工具可实现 `approval(args)`，按参数返回 tier 或对象 `{tier, reason, override, policy}`——bash 用它实现 critical 模式强制弹窗（`rm -rf /`、fork bomb、远程拉取执行、写 /etc/passwd），lsp 用它把只读动作降为 read。
- **用户 per-tool policy**：`tools.approval.<tool>: allow|deny|prompt`，任何模式下都生效但压不过工具自身的 deny。
- **bash.patterns 的不对称匹配**（关键细节）：`deny`/`prompt` 匹配整条命令**或任一 compound 段**（按 `&&`/`||`/`;`/`|`/子shell/换行切分，`rm -rf *` 抓得住 `cd /tmp && rm -rf build`）；`allow` 必须匹配**整条命令**且不下探 compound（`git *` 放行不了 `git status && rm -rf /`）。deny 连 yolo 都拦。
- **子代理**：headless 强制 yolo，父级 task 工具调用本身是授权边界；用户 `prompt` 在 headless 下拒绝而非静默放行。

落地：`pkg/permissions/checker.go`（已实现未接线）扩成三层模型 + `pkg/tools/base.go` 让 `execCtx.AskPermission` 真正被 builtin 工具调用 + bash.go 实现 compound 切分与 patterns。子 agent 继承父策略（替换 `pkg/tasks/executor.go:108` 的 AllowAll）。

### 2. Capability 注册表：统一发现管道 + 被遮蔽项可观测（治「死接线」根因）
来源：docs/config-usage.md、docs/hooks.md、docs/rulebook-matching-pipeline.md。

omp 把 skills/slash commands/hooks/tools/prompts/rules/extensions/settings/MCP 全部收敛到一个 `loadCapability(id)` 管道：每个发现源是数字优先级的 provider（native 100 > 插件 90 > claude 80 > … > 内置默认 1），每类 capability 定义 `key(item)`（如 hooks 的 `${type}:${tool}:${name}`），**同名 first-wins 去重，但低优先级重复项不丢弃**——保留在 `result.all` 并标 `_shadowed`，`/extensions` 面板可见"谁被谁遮蔽"。这是排障能力的核心设计：配置没生效时能回答"为什么"。

落地：在 `pkg/config`（或新 `pkg/capability`）实现统一的发现+去重管道，`BuildRuntime` 只消费注册表产物；配套 wiring 自检（关键依赖 nil 即 fail-fast），P0-2（MCP 配置被丢弃）、P0-3（hooks 未注入）从根因上不再复发。

### 3. Replay-safe 自动重试 + fallback 链 + retryRecovery 标记
来源：docs/non-compaction-retry-policy.md、docs/provider-streaming-internals.md。

omp 的 TurnRecovery 与 pi-omp-research #8 的差别在三个机制细节：
- **Replay-safety 门**：只有当流尚未产生"replay-unsafe 输出"（非空可见文本、图片、tool call、server-tool 块）才允许重试；thinking-only / 空白 partial 可安全丢弃。`withReplaySafeStreamRetry` 对空补全做 2 次指数退避重试并缓冲 pre-output 事件——**可见副作用一旦出现，重试策略必须避免重复输出**（provider-endpoint-constraints 的成文原则）。
- **分类与退避**：错误经结构化分类（transient/usage/overflow 三分流，overflow 排除出重试、交给压缩路径），`min(500ms·2^(n-1), 8000ms) × 75–100% jitter`，retry-after 头解析可延长；usage 限制触发凭证轮换或模型 fallback（fallback 拿全新重试预算），`retry.maxRetries` 默认 10、`maxDelayMs` 5 分钟。
- **retryRecovery 台账**：重试链中被剥离的错误 entry 留在会话史上，链成功后逐个标记 `recovered`（kind/attempt/note/supersededBy），重建 LLM 上下文时剔除、展示转录保留为暗色一行注记。成本与恢复过程可审计。

另有 provider 流式层的配套件（pkg/api 直接相关）：256 字节增长阈值节流的 partial-JSON 重解析（防每 delta 二次方开销）+ RelaxedJson 修复式解析兜底；首事件/idle watchdog（`trackLocalWork` 标记服务器请求的本地工作防误判停摆）；Google 侧 ThinkingLoopDetector（逐字尾重复 + trigram Jaccard ≥0.8 + 词汇新颖度停滞 → 合成可重试错误）。

落地：`pkg/api/client.go`（当前零重试）+ `pkg/engine/query.go` 的错误分流。与 durability 提案 Phase 2 的持久 attempt 计数互补：先进程内落地，Phase 2 把 attempt 写进记录。

### 4. Handoff：cache 对齐的就地压缩摘要
来源：docs/handoff-generation-pipeline.md、docs/compaction.md。

pi 原版的「新 session 开头注入摘要」会全部缓存失效；omp 的 handoff 是**同一前缀的 side request**：系统提示、工具数组、真实消息历史与活轮完全同管线构建（同 promptCacheKey），handoff 提示词作为**最后一条 user 消息**追加（唯一分歧点），`toolChoice:"none"` 强制纯文本（provider 拒绝时降级 `"auto"` 重试一次，工具块丢弃只取文本）。产物作为普通 `CompactionEntry` 提交到**当前会话**：session id、文件、转录、缓存键全部不变，`firstKeptEntryId` 起的近期历史原样保留。异步化时（`compaction.asyncEnabled`）在 pre-threshold 预备带就后台生成，跨阈值瞬间提交——摘要延迟被完全隐藏。

落地：`pkg/services/compact.go` 摘要请求改造 + durability Phase 1 的 KindCompaction 写入方。这是 pi-omp-research #2 压缩设计在「缓存经济学」上的关键升级。

### 5. Shake：无 LLM 的本地机械压缩 + methodOrder 方法级联
来源：docs/compaction.md。

omp 的压缩不是单一算法而是**方法瀑布** `compaction.methodOrder = ["remote", "snapcompact", "handoff", "shake", "soft"]`，前一个不够就降级到下一个。`shake` 是纯本地的一级：把可牺牲的老 tool 结果和大 fenced/XML 块替换成可恢复的 `artifact://` 引用，规则包括——保护最近 40k tool-output token；总节省 ≥20k token 才动手；永不空掉 <50 token 的结果（占位符本身 ~8 token，省不出正数还白搅缓存）；skill 结果与活动 plan 引用文件永不剪。**溢出恢复（overflow）也可以走它**：无模型调用、无 key 依赖，天然安全。配合 prune 的 cache-aware 时机（只有候选之后的后缀 ≤~8k token、或会话已闲置超过 provider 缓存寿命时才动历史）。

落地：`pkg/services/compact.go` 的新一级（与 L1-L5 管线整合为 methodOrder），溢出路径（P0-5a 修复后）优先走 shake。

---

## P1 —— 能力强化：把已有概念工程化

### 6. 上下文经济学三件套：useless 擦除、缓存断点滚动窗、date/cwd 出 system prompt
来源：docs/compaction.md、provider-quirks（Anthropic 节）、system-prompt-customization.md。

- **useless/superseded 原位擦除**：工具结果可标 `useless`（零命中搜索、空信箱 drain），被 `[Uneventful result elided]` 原位替换（不改历史结构、不破坏 tool call/result 配对与 provider 重放）；同一文件的旧 read（superseded reads）同理。摘要序列化时整个 pair 直接丢弃。
- **Anthropic 缓存断点滚动窗**：cache_control 断点挂在**最近两消息的尾部窗口**滚动推进（跳过 thinking/redacted 块），pad `Continue.` 时锚定前一条真实 assistant——而不是每请求重打断点。
- **date/cwd 移出 system prompt**：放进首轮 `<system-reminder>`，跨午夜会话可刷新日期且开放权重 provider 的前缀缓存得以保留。

落地：`pkg/tools` 结果元数据加 useless 标志、`pkg/services` 擦除时机、`pkg/api` Anthropic 断点策略、`pkg/ui/runtime.go` 系统提示拆分。三者都是小改动大收益的 cache 纪律补丁（与已落地的 cache-stable tool schema 同族）。

### 7. TTSR 完整生命周期 + rulebook 多源规则发现
来源：docs/ttsr-injection-lifecycle.md、docs/rulebook-matching-pipeline.md。

pi-omp-research #4 只有概念；omp 的工程化细节值得整建制照抄：
- 规则元数据：`condition`（正则）、`astCondition`（ast-grep 结构模式，只在 edit/write 工具流上、按文件扩展名推语言、对**重构出的源快照**匹配）、`scope`（`text|thinking|tool|tool:<name>(<glob>)`，默认监听正文+全部工具参数、不含 thinking）、`interruptMode`（always/prose-only/tool-only/never）、`repeatMode`（once / after-gap N 个完成 turn）。
- 三种交付路径：中断（立即 abort → 50ms 后注入 `<system-interrupt>` + 重试，contextMode discard 丢半截输出）；非中断 tool 源（`afterToolCall` 把 `<system-reminder>` **前插**进工具结果内容，不中止流）；非中断 prose 源（成功消息后经 followUp 队列注入隐藏消息）。
- 持久化：`ttsr_injection` entry 记录已注入规则名，resume 时恢复抑制状态。
- 规则发现本身是多源 capability：native `.omp/rules`、`.agents/rules`、cursor、windsurf、cline、GitHub copilot instructions 全部归一化，优先级去重后分桶（TTSR > always-apply > rulebook），`rule://<name>` 按需读取。

落地：`pkg/hooks`（AGENTS.md 已指明 stream rules 落点）+ `pkg/engine` 流拦截层。内嵌 builtin 规则（omp 的 `discovery/builtin-rules/` 有 25 条 Go/Rust/TS 代码卫生规则可直接移植成 Go 版）。

### 8. Advisor 2.0：roster、emission guard、交付路由
来源：docs/advisor-watchdog.md。

pi-omp-research #5 只提了"第二模型旁听"；omp 的完整机制：
- **多 advisor roster**（`WATCHDOG.yml`）：每个条目独立模型/工具授权/专项指令；`WATCHDOG.md` 是 advisor 专属审查要点（不进主 agent 上下文）。
- **增量评审**：advisor 只收上次以来的转录 delta（含 reasoning 与工具意图），经 secret 混淆器；自己已注入的 advice 被过滤防止递归自审；转录重写（压缩/切分支）时 advisor 重置。
- **advise 工具 + severity 交付路由**：`nit` → 步边界批量旁注；`concern` → steering 打断（尾部已是终止答案时降级为可见卡片，避免为已完成轮次唤醒 agent）；`blocker` → 永远 steering。plan mode 一律卡片化。
- **Emission guard**（防噪音洪水）：NFKC 归一化 + 空洞短语过滤（`stop`/`lgtm`…）+ 会话级精确去重（FIFO 4096）+ 每次更新至多采纳一条；`immuneTurns`（默认 3）冷却后续降级为旁注。
- **隔离与审计**：advisor 有独立 ToolSession 与审批面；异常输出（非授权工具请求、破坏性指令）整轮隔离，两次连续隔离重置上下文；转录持久化为 `<session>/__advisor[.<slug>].jsonl`，成本独立核算。

落地：`pkg/engine` turn 循环旁路 + `pkg/tasks` 复用 executor。工程量比"旁听"大但每一段机制都是独立可发布的。

### 9. MCP 运行时质量包：快速启动门 + 重连状态机 + 确定性命名
来源：docs/mcp-runtime-lifecycle.md、docs/mcp-protocol-transports.md、docs/mcp-server-tool-authoring.md。

给复活的 MCP 栈（P0-2）配齐运行时语义：
- **250ms 快速启动门**：`connectServers` 与 250ms 竞速；到点后已完成的成为 live 工具、失败的记 per-server error、仍在飞的**有缓存则发 DeferredMCPTool**（调用时等连接），无缓存则后台完成后经 onToolsChanged 补注册——慢 server 不阻塞会话启动。
- **重连职责分层**：transport 永不自愈（fail-fast），manager 负责退避重连（500/1000/2000/4000ms）+ **crash-storm 熔断**（30s 内 >5 次挂起自动重连）+ epoch 计数（`disconnectAll` 后迟到的重连无法复活旧连接）。无轮询健康检查，全事件驱动。
- **确定性工具命名**：`mcp__<server>_<tool>` 归一化 + >64 字符加 hash 后缀；同名冲突按 origin key 字典序裁决，**重连/发现顺序无法改变归属**（provider-facing determinism 的直接延伸）。
- 秘密解析两级（`${VAR}` 发现期展开、连接前 `!cmd`/env 求值）与 per-URL OAuth 凭据绑定可后置。

落地：`pkg/mcp`（439 行 client.go 已有基础）+ `BuildRuntime` 接线。

### 10. 上下文文件多源发现与 shadowing 规范
来源：docs/context-files.md。

直接修复 p0 报告次级问题「ClaudeMD 发现了多条路径却只读第一条」：
- **深度去重**：每目录深度只保留一个项目上下文文件，同深度高优先级 provider 遮蔽低优先级（native `.omp` > claude > agents/codex > gemini > opencode > github > 独立 AGENTS.md）；跨深度多文件共存，**注入顺序远祖先在前、近者在后**（越近越显著）。
- **`@` 导入**：相对导入基于导入文件自身目录，`~/` 基于家目录；代码块/行内代码内的 `@` 不展开；`git@`/邮箱形 token 不算导入；递归 5 层、环跳过、缺失保留原文本。
- **sticky 规则**：顶层 `RULES.md` 走 always-apply 通道（每轮贴近当前 turn 重新附着），与一次性的 `AGENTS.md` 分工明确。
- 单文件禁用 id（`context-file:<level>:<basename>`）与被遮蔽项可见性（同 #2）。

落地：`pkg/ui/runtime.go` 的 ClaudeMD 发现 + `pkg/prompts`。字节相同文件折叠取最靠近 cwd 者，是幂等且确定性的（cache 友好）。

---

## P2 —— 前沿：长周期高差异性

### 11. 会话操作四件套
来源：docs/session-operations-export-share-fork-resume.md、docs/session-switching-and-recent-listing.md。
- `/fresh`：只轮换 provider 侧会话状态（重铸 session id、失效 append-only 上下文、重连记忆 key），**本地转录一个字不动**——治"僵死缓存/服务端会话漂移"而不丢对话。
- `/clear` = 持久 `reset_boundary` entry：磁盘全史保留，重建的模型上下文与折叠转录从边界开始。
- `/fork` 继承 `providerPromptCacheKey`（除非路由/提示形状变了）——fork 后第一轮就可能命中源会话缓存。
- `--continue` 走**终端级 breadcrumb 文件**（TTY/tmux/kitty id），而非全局 mtime 最新；会话列表只读 4KiB 前缀 + 32KiB 尾（lifecycle 状态），不碰全文件。
落地：`pkg/session/store.go`（reset_boundary 与 fork 键继承）+ `cmd/openharness`（--resume/--continue 已声明未实现）。

### 12. Snapcompact 位图归档压缩
来源：docs/compaction.md、packages/snapcompact/README.md。
被丢弃历史不用 LLM 摘要，而是序列化后渲染成**像素字体 PNG 帧**让视觉模型直接读回：全本地、确定性、零 API 调用（overflow 恢复也安全）。shape 表按读取模型的计费实测调优（Claude `11on16-bw` 1932px / Gemini `8on22-bw` 2048px 固定每图 token 预算 / OpenAI 同形 `detail:"original"`）；CJK 自动切换 silver16 字体；preserveData 保存有界源文本，后续压缩**重渲染**而非带旧 PNG 前滚，中间帧 HQ/LQ/HQ 中心凹视。Go 侧完全可实现（freetype + png），是 harness 差异化最"黑科技"的一件。

### 13. Eval 持久内核（code mode）
来源：tools/eval.md、python-repl.md、notebook-tool-runtime.md（子代理通读）。
py/js/rb/jl 各自持久 kernel：自研 NDJSON 子进程协议（无 Jupyter 依赖）；取消分级 SIGINT→5s 升级→SIGKILL 且下次调用重建 kernel；**宿主桥调用（agent()/parallel()）期间 cell 超时按引用计数暂停**；matplotlib `MPLBACKEND=Agg` 每 cell 自动出 PNG 作为 image 输出；环境变量白名单 + 常见 API key denylist 剥离；`.ipynb` 只是编辑视图转换（执行走 eval），两条路径干净分离。数据分析/实验场景的大杀器。

### 14. Prewalk 模型接力
来源：docs/prewalk.md。
一次性武装的跨模型接力：强模型负责规划，注入 planning nudge；todo 工具的首次成功调用（含只读 view）打开闸门，**首次真正的 edit/write 后切换到目标模型**（默认 `@smol` 角色），切换后 prewalk 自行解除。省钱的编排原语——贵模型只付规划的钱，重活交给便宜模型。依赖 model roles 路由（pi-omp-research「未入选」清单里已有 modelRoles 条目），是它的第一个高价值用例。

### 15. 自主记忆管线 2.0
来源：docs/memory.md、docs/mnemosyne-memory-backend.md（pi-omp-research #7 的机制升级）。
- **本地两阶段管线**：Phase 1 每会话抽取（`default` 角色，并发 8、lease 120s）；Phase 2 跨会话归并（`smol` 角色，lease + 30s heartbeat 防多进程双跑）产出 MEMORY.md + 注入摘要 + **可复用 skills/**——记忆直接晋升为技能。
- **Mnemopi 引擎**：本地 SQLite，FTS + 本地 embedding；polyphonic recall = 向量/图/事实/时间四路召回 + 倒数排名融合；工作记忆 → 情景记忆的 sleep 归并；retain 频率 4 turn，注入预算 5000 token。
- **协议细节**：`memory://<id>` 读全行（召回预览截断 500 字符，**编辑前必须读全行**否则把截断内容写回）；retain 失败只告警用户不告知模型；退出时 1.5s drain 预算。
最小可行版仍是 pi-omp-research #7 的三工具方案，本条提供的是后端与归并管线的完整蓝图。

---

## Provider 层速览（pkg/api 直接相关的其余机制）

三路子代理之 provider 组的完整报告已在调研过程中产出，除 #3 已收编的项外，值得留档的还有：

- **Anthropic 严格排序与降级链**：assistant 轮稳定分区（non_tool_use 块后接 tool_use 块）防 400；strict 工具 400 降级按 `${provider}:${baseUrl}:${model}` 记忆避免每轮重付；OAuth 下工具名 `_` 前缀转义。
- **OpenAI Responses stateful chaining**：`store:true` + `previous_response_id` 增量，历史/选项/缓存断点变化即回退全量重放；stale-ID 连续 3 次熔断禁用链式；ZDR 错误立即禁用。
- **reasoning 重放不变量体系**：`requires/allowsSyntheticReasoningContentForToolCalls`、`replayReasoningContent`（本地后端重放保 KV-cache）、`reasoningDisableMode` 六种关 thinking 编码——compat 元数据驱动请求塑形而非 provider 名分支。
- **SoftToolRequirement**：硬 tool_choice 会搅动 prompt cache——先注入提醒保持 auto，模型不配合才在下一轮硬钉一次。
- **Harmony 泄露检测**（ERRATA-GPT5-HARMONY.md）：args 区 logit mask 压制控制 token 导致"无括号明文影子格式"级联自增强；检测需共信号而非裸 marker，`edit` patch-DSL 可截断恢复。
- **流方言扫描器家族**（toolconv/*.md）：DeepSeek/Kimi/GLM/Harmony/Hermes/MiniMax/Pythonic-Gemini 等 12 种文本工具调用方言的流式解析、跨 chunk 标记缓冲、伪造结果防护——多 provider 支持的长期路线图。

## 落选但值得知道

- **非交互子进程环境加固清单**（bash-tool-runtime.md）：`PAGER=cat`、`GIT_PAGER=cat`、`GIT_EDITOR=true`、`GIT_TERMINAL_PROMPT=0`、`SSH_ASKPASS=/usr/bin/false`、`NO_COLOR=1`、`CI=true` + 包管理器自动化 flags，垫在 caller env 之下——做 Bash 工具时的必抄清单（可并入 P0-1 的 bash 改造）。
- **fs-scan-cache**：TTL 1000ms + 空结果二次校验（防缓存窗口漏新建文件）+ 写/删/改名后显式失效；key = 规范根 + 完整遍历选项。
- **bashInterceptor**：`cat→read`、`sed -i→edit`、重定向→write 的能力路由（仅当目标工具在场，明说不是安全边界）。
- **startup 相位标记**：`PI_DEBUG_STARTUP` 向 stderr 同步写 `[startup] <phase>:start/:done`，最后一行即卡死点——存活性诊断的极简方案。
- **TUI history/viewport 双通道**：history 批次带单调 id + ack 握手（重试合并渲染安全）、viewport 逐行 diff；"finality 是应用决策"而非渲染器猜测。
- **统一取消契约**（natives-rust-task-cancellation.md）：timeoutMs + signal → 统一 token；阻塞任务靠 heartbeat 协作检查；取消语义按 API 族二选一（abort-as-error / typed result）并成文——Go 语境即统一 context 桥接规范。
- **语义压缩技能**（.omp/skills/semantic-compression）：把「重编码而非删词」「密度门 <10% 停手」「scar tissue 保留（git blame 后再砍）」写成可执行规范，适合直接吸收进 oh-doc-standards / 工具提示词 review 标准；配套 tool-prompt-optimization 的 schema/probe 重叠实测法。
- **Collab E2E 共享**（collab.md + packages/wire）：view-only（32B key）/full（+16B write token）双强度链接、AES-256-GCM、content-blind relay——pkg/protocol 的远期扩展方向。

## 建议落地顺序

| 批次 | 条目 | 依赖 |
|---|---|---|
| 1 | P0-1 审批模型、P0-2 capability 注册表（连带 MCP 一行接线 + hooks 注入） | 无，直接修债 |
| 2 | P0-3 重试/fallback、P1-6 上下文经济学 | pkg/api 现状即可启动 |
| 3 | P0-4 handoff、P0-5 shake、P1-6 配套 | durability Phase 1 的 KindCompaction 写入方 |
| 4 | P1-7 TTSR、P1-8 advisor、P1-9 MCP 质量包、P1-10 上下文发现 | 批次 1 的注册表 |
| 5 | P2 按需 | 各自独立 |
