# 调研报告：Dynamic Workflow（PTC 模式）与 bash-native harness

> 调研日期：2026-08-27
> 来源：
> - deepseek-ai/deepseek-harness `packages/workflow`（master@b150a551，4 子包全源码 + 测试 + 2 份 implemented Agent Note）：PTC 模式的完整参照实现
> - laude-institute/headlong `bin/`（main 分支，16 个脚本约 10K 行纯 bash）：bash-native harness 的机制样本
> - dop251/goja（master 源码核实）：Go 内嵌 JS 的宿主绑定机制与 goroutine 约束
>
> 配套决策记录：[Agent Note: Dynamic Workflow (PTC)](../.agents/notes/proposed/architecture/2026-08-27-dynamic-workflow-ptc.md)——本文件只承载调研事实与机制细节，不做决策。

## 目录

- [Part 1：DeepSeek Harness workflow 包](#part-1deepseek-harness-workflow-包)
- [Part 2：Headlong 纯 bash harness](#part-2headlong-纯-bash-harness)
- [Part 3：goja 宿主绑定机制（JS↔Go 通信）](#part-3goja-宿主绑定机制jsgo-通信)

---

## Part 1：DeepSeek Harness workflow 包

### 1.1 机制本质

四句话概括 PTC（Programmatic Tool Calling）模式：

1. **谁写代码**：LLM 写。模型在一次普通 tool_use 中提交两样东西——JSON 格式的 `meta`（身份块）和一段**纯 JavaScript 脚本体**（支持 top-level `await`，以 `return <json值>` 结尾）。循环、分支、fan-out 全部由代码承载，不需要每步回一次模型。
2. **在哪执行**：每 run 一个 Node worker thread，脚本跑在 worker 内的 `node:vm` context 里（文档明确叫 "escapable vm context"，即**不是**安全沙箱）。
3. **"programmatic tool calling" 的确切含义**：模型用一段程序连续发起 N 次 `agent()` 调用（每次调用在宿主侧展开为一个完整的子代理 run），中间结果保存在脚本变量里而不是父会话上下文中。逐轮编排的对比："every intermediate result lands in the parent context…coordination costs a model round-trip per step"。
4. **工具桥接方式**：worker 内脚本的 `agent()` 不是本地函数调用，而是一次跨线程 RPC——宿主收到 `child-start` 消息后通过 SubagentRuntime 启动真正的子代理 agent loop；子代理内部的多轮模型循环留在宿主进程，脚本只拿到最终结果投影。

效果：tool-use 次数从 O(编排步数) 降为 O(1)；token 成本从"父上下文携带全部中间结果"变为"N 个独立子上下文 + 一个最终 JSON 返回值"。该项目明确对标 Claude Code dynamic workflows（其 Agent Note 原文："Claude Code ships this capability as dynamic workflows"；`WorkflowMeta` 注释："The field vocabulary matches the Claude Code dynamic-workflows meta block"）。

### 1.2 包结构与分层

```
packages/workflow/
├── workflow/                 # Service Definition（seam）：ctx.workflowEngine 抽象 + workflow/* 事件 + 类型词表
├── workflow-worker-thread/   # Service Provider（引擎实现）：每 run 一个 worker thread
│   ├── index.ts              # 引擎插件（start 校验、spawn 决策）
│   ├── host.ts               # 宿主侧 WorkerRun（消息仲裁、子代理账本、取消/终止）
│   ├── runtime.ts            # worker 侧 WorkflowExecution（vm context、hooks、并发、上限）
│   ├── session.ts            # worker 侧 MessagePort ↔ Execution 接线（ready/go 握手）——与持久化无关
│   ├── protocol.ts           # host⇄worker 线协议（tag enum + payload map）
│   ├── realm.ts              # 值边界：materializeFromRealm + renderThrown
│   ├── meta.ts               # meta 数据校验（绝不 eval）
│   └── worker.ts             # 进程入口
├── tool-workflow/            # Consumer：模型可见的通用 workflow 工具
└── tool-ralph/               # Consumer：固定脚本的 ralph 工具（fresh-agent 循环）
```

依赖方向：`tool-*` → `workflowEngine`(seam) ← 引擎实现可整体替换（"A future process or container engine can replace the implementation without changing the tool."）。每个 context 只允许一个 workflowEngine——部署期替换而非并存。

### 1.3 领域模型（关键类型全文）

```ts
type WorkflowRunId = Branded<'WorkflowRunId'>          // 引擎 mint UUID

interface WorkflowPhase {                               // meta.phases 元素，纯进度标注
  title: string; detail?: string; provider?: string; model?: string
}

interface WorkflowMeta {                                // 字段词表 = Claude Code meta block
  name: string             // kebab-case 显示名 + 持久化 key
  description: string
  whenToUse?: string
  phases?: WorkflowPhase[] // phase() 调用按 title 精确匹配；无执行语义
}

type WorkflowStopReason = 'completed' | 'cancelled' | 'error'   // 封闭联合

interface WorkflowResult {
  value: unknown           // 脚本 return 值物化后的宿主 JSON 数据；undefined→null
  stopReason: WorkflowStopReason
  error?: string           // 仅非 completed 时存在
  agentsStarted: number    // 优雅结算=脚本侧计数（含排队中）；终止路径=宿主观测计数
}

interface WorkflowStartRequest {
  script: string                     // 纯 JS 体（top-level await；return <json>）
  meta: WorkflowMeta                 // 纯数据，引擎 shape 校验
  args?: unknown                     // 原样暴露给脚本作 args 全局
  subagentProvider?: string          // 本 run 全部子代理的 provider 覆盖（脚本不可见）
  maxTotalAgents?: number            // 本 run 子代总数上限（只能降不能升 ceiling）
  parent: Agent                      // 必须：所有子代理归于此活 Agent
  signal?: AbortSignal               // abort 即取消整个 run
}

interface WorkflowRun {
  readonly id: WorkflowRunId
  readonly meta: WorkflowMeta        // body 执行前即可用（已验证副本）
  readonly result: Promise<WorkflowResult>   // 永不 reject！
  cancel(reason?: string): void      // 幂等，第一个 reason 赢
  dispose(): Promise<void>           // cancel + 有界结算 + 子代 quiescence
}
```

`agentsStarted` 双口径是刻意设计：grace 强制结算或 worker 死亡时，被终止脚本内部排队的调用不可知，只能退化为宿主观测计数（types.ts:79-86 注释）。

### 1.4 两个工具的模型可见 API 面

**workflow 工具入参**：`script`（必填，禁止 `export const meta` 语句）、`meta`（必填纯 JSON）、`args?`（顶层裸数组须包一个字段）。出参 `{runId, agentsStarted, result}`。

脚本内 hooks（工具描述文本即模型-facing 规范）：

```js
await agent(prompt, opts?)   // opts: {label?, phase?, schema?, provider?, model?}
                             // 无 schema → resolve 为子代理最终文本拼接；有 schema → 已验证对象
                             // 子代理普通失败 → resolve null（模型用 .filter(Boolean) 过滤）
                             // 未认识的 option → 抛 UNSUPPORTED_OPTION，杀掉整个脚本
await parallel(thunks)       // thunks: (() => any)[]；barrier；thunk throw → 该项 null
await pipeline(items, ...stages) // 每 item 独立穿 stages；无跨 stage 栅栏；
                                 // 普通 stage throw → 该 item null 并跳过剩余 stages
phase(title); log(message)   // 观察者叙事
args                         // 全局常量
// 约束：无 filesystem/network/timers/Node API 注入；前台同步收集，整脚本跑完才返回工具结果
```

JSON Schema 支持子集（`assertObjectJsonSchema`）：仅 `type/properties/required/additionalProperties/items/enum/const/oneOf`；越界抛 `UNSUPPORTED_SCHEMA`。

**ralph 工具**（固定策略编排器，模型只交数据不改程序）：`ralph({objective, maxRounds?=256})`。内置固定脚本每轮起一个 fresh 结构化输出子代理，prompt 只含不可变 objective、轮次/cap、workspace-as-authority 指令、上一轮 handoff JSON。报告 schema 固定为 `{status, summary, evidence[], nextSteps[], blocker}` 且键集精确匹配校验（`Object.keys(value).sort().join(',')`）；handoff 序列化长度超限报错而非截断。provider 硬性要求：必须存在、`capabilities.outputSchema=true`、`inheritsParentContext=false`。路由参数（provider/maxTotalAgents=maxRounds）走 StartRequest 传递，脚本无法看到或篡改。

### 1.5 端到端执行流程

```
阶段一：同步校验（宿主主线程）
 tool_use{script,meta,args} → exec.agent 缺失检查
 → validateMeta（shape 校验，violation 全部命名后抛 META_INVALID）
 → assertBodyParses：用与 worker 完全相同的 wrapper `(async () => {\n${body}\n})()`
   做 host 侧 parse-only 编译，保证 SCRIPT_PARSE 同步抛出；
   body 含 export const meta 给定向报错（模型最易犯的错）
 → resolveSubagentProvider / resolveMaxTotalAgents（≤ceiling 否则 INVALID_ARGUMENT）
 → WorkflowRunId(randomUUID)；limits 解析（maxConcurrentAgents===0 时自动 min(16,max(1,cores-2))）
 → new Worker(entry, {workerData, env:{}, execArgv:[]})   ← 凭据不跨线程
 → emit 'workflow/start'；返回 WorkflowRun

阶段二：worker 启动握手
 worker: ChildRpcBridge(callId 自增+pending 表) → new WorkflowExecution
   · 构造器第一步 new vm.Script(wrapper)（防御 Node 版本 skew）
   · vm.createContext({}, {name:`workflow:${meta.name}`})
   · 注入五个冻结 hook + args（均经 contain() 包装挂 rejection consumer，
     防脚本丢弃 promise 导致 unhandled rejection 杀死 worker）
 port.on('message')：Go→gate.resolve；Cancel→cancel(reason)+gate.resolve
   （已取消则脚本体一行都不执行）
 post('ready') … 宿主收 ready → post('go') → drive():
   compiled.runInContext(context, {timeout: syncTimeoutMs})

阶段三：每个 agent() 调用的旅程（runtime.ts:250-345）
 ① throwIfCancelled（边界=下一个 HOOK，不只是 agent()）
 ② prompt 非空字符串校验 → INVALID_ARGUMENT
 ③ readAgentOptions：materializeFromRealm(rawOpts)；未知 key → UNSUPPORTED_OPTION
 ④ started >= maxTotalAgents → AGENT_CAP
 ⑤ acquireSlot：activeSlots < max ? 直接进 : FIFO slotWaiters 排队（cancel reject 全体 waiter）
 ⑥ 再次 throwIfCancelled
 ⑦ children.startAgent：post('child-start',{callId,request}) → await started promise
 宿主 onChildStart：
 ⑧ childAdmissionFailure？cancelled/死亡/terminalClaimed → 拒绝（绝不在已 abort 信号上起子代理）
 ⑨ startChild：subagents.start(provider,{prompt,parent,signal,outputSchema,…})
    · 先挂 result 转发回调再 post('child-started')（保证乱序结果也按序到达）
    · 结果投影 snapshotJsonValue → 无损 JSON 才 child-settled；否则 child-failed
 ⑩ observer.agentStart({seq,label,phase,childId}) → 宿主 liveAgents 记账
 ⑪ await run.result：
    reject（基础设施故障）→ agent-end(failed) 后抛 AGENT_RESULT(fatal)
      ——"a broken provider must not read as a failed child"
    completed+schema → structured 缺失 = 子代理自身失败 → return null
    非 completed：isCancelled → 抛 CANCELLED 杀死脚本；否则 return null
 ⑫ finally: dispose（memoized，post('child-dispose') 往返）
 ⑬ releaseSlot → FIFO 唤醒下一个 waiter

阶段四：结算
 return <value> → undefined→null → materializeFromRealm
   （violation → RESULT_UNSERIALIZABLE）→ post('result')
 宿主 onResult：
 → terminalClaimed 已设？丢弃（重复 Result 零副作用）
 → cancellationWasRequested？completed 也改写为 cancelled（竞态裁定权在外部取消）
 → reapChildren（abort 共享信号 + 处置已注册子代——在对外 settle 之前）
 → settleResult → fire 'workflow/end'（payload 刻意去掉 value，防观察者拿到可变别名）
 工具 finally：removeEventListener(abort) → await run.dispose() →
   非 completed reason 映射为 isError；成功返回 {runId, agentsStarted, result:value}
```

dispose() 细节：幂等；claim 公共事务 → detach 外部 signal → cancel → **立即** reapChildren（wedged worker 无法转发 dispose RPC，宿主必须自己驱动）→ `Promise.race([quiescence, sleep(disposeGraceMs)])` → `worker.terminate()` 无条件 → 最后再 sweep 幸存者。quiescence 条件 = `children.size===0 && pendingStarts.size===0`。

### 1.6 协议规范（protocol.ts 全量）

worker→宿主（8 tag）：`ready{}`、`phase{title}`、`log{message}`、`agent-start{info}`、`agent-end{info:含outcome}`、`child-start{callId,request}`、`child-dispose{callId}`、`result{result}`。

宿主→worker（7 tag）：`go{}`、`cancel{reason}`、`child-started{callId,childId}`、`child-start-error{callId,rendered}`、`child-settled{callId,result:{output,structured?,stopReason}}`、`child-failed{callId,rendered}`、`child-disposed{callId}`。

工程纪律：两个方向都是封闭协议，接收端 switch+`assertNever`（添加新消息类型=编译错误）；对未知 callId 必须容忍（teardown race 有专项测试）。

### 1.7 沙箱边界与超时层析

官方定性（README 原文）："**node:vm inside a worker is an API-shaping mechanism, not a security boundary.**" 与模型 bash 权限同级的信任前提。逃逸路径甚至被写成测试夹具常量：`globalThis.constructor.constructor('return process')()`。真实 containment 收益来自：worker `env:{}` 孵化（逃逸后读 `proc.env.WORKFLOW_ENV_CANARY` 得 null）、structured clone 序列化边界、CPU 自旋离开宿主 event loop、terminate() 作为真终点。

超时/取消的五层层析：

| 层 | 机制 | 覆盖范围 |
|---|---|---|
| vm timeout | `runInContext(ctx, {timeout})` | 只有首个同步切片；`while(true){}` 在 50ms 配置下被杀 |
| hook 边界取消 | cancel 设标志 → slotWaiters.reject + 每个 hook 入口 throwIfCancelled | 死于"下一个 await/hook 边界"；catch 后继续 phase/log 会再次抛 |
| 遗弃承诺 | contain() 附 no-op rejection consumer | 防 unhandled rejection 杀 worker |
| 不肯死的脚本 | grace 定时器到期 → terminalClaimed → settle(cancelled) → terminate | `await new Promise(()=>{})` 在 grace=50ms 内被强制结算 |
| 孤儿清理 | 共享 AbortController，cancel/dispose/reap/settle 各路径都 abortChildren | 每个 pending start + published child |

死亡屏障：首个 error/messageerror/exit 设 `workerDeathObserved=true`，其后排队消息一律丢弃（Node 可能按 error→queued message→exit 顺序派发）；`liveAgents` ledger 保证每个转发的 agent-start 恰好配对一个 agent-end（缺端合成 outcome:'cancelled'，先于 workflow/end 发出）。

### 1.8 值边界（realm.ts 行为表）

`materializeFromRealm(value)` 把离开脚本的值深拷贝为宿主 plain JSON。接受：boolean/string/有限 number/null/plain-object（prototype 链长 ≤2，跨 realm 无法恒等比较所以用链长判定）/dense array/string keys。拒绝并给出**路径限定**报错：bigint/function/symbol/nested undefined/NaN±Infinity/循环引用/稀疏数组/带非索引属性的数组/symbol 键属性/exotic prototype（Date/Map/class instance）/读取时抛错的 getter（包装为 MaterializeError）。

三个安全细节：① `__proto__` 键经 `Object.defineProperty` 写为自有数据属性，不会污染宿主原型（有测试）；② 属性选择=`Object.keys`（own enumerable string keys），与非枚举属性的处理恰好对齐 JSON.stringify；③ getter 会正常求值（信任前提），throwing getter 失败是响亮的 MaterializeError。

跨 realm instanceof 陷阱：hooks 抛的 WorkflowError 是宿主 realm 类实例，脚本里 `instanceof Error` 会失败——作者须按 `name`/`code` 字段分支。反向同理：fatal 识别靠宿主类 instanceof，脚本伪造 `{name:'WorkflowError', fatal:true}` 的 plain object 不能骗过组合器（有测试验证 forged 对象被置 null）。进入脚本方向的值不经净化（信任前提内的作者；真实跨线程克隆已在 workerData 完成）。

### 1.9 错误分层与稳定文案

11 个封闭 code：SCRIPT_PARSE、META_INVALID（start 前同步抛）、INVALID_ARGUMENT、UNSUPPORTED_OPTION、UNSUPPORTED_SCHEMA（hook 违约）、AGENT_CAP、ITEM_CAP（安全上限）、AGENT_START（async start 失败）、AGENT_RESULT（result-reject 基础设施故障）、RESULT_UNSERIALIZABLE、CANCELLED。

组合器纪律是核心设计（对 Claude Code 的故意偏离，strictness divergence）：**hook 误用/上限/provider 故障=fatal→parallel/pipeline re-throw 杀整脚本；普通子代理失败=in-stage 抛错→该项 null**。"typo'd option 不得溶成看起来像子代理失败的 null"。`fatal` 默认 true，`isFatalWorkflowError` 用宿主 instanceof 判定——不可伪造。

稳定错误文案族（engine README 明列，模型依赖这些措辞自我修正）：`workflow script does not parse: <err>`、`invalid meta: <violations>`、`agent() requires a non-empty prompt string`、`agent() could not start a child: <err>`、`child agent run failed: <err>`、`this run reached its total agent cap (N) — ...raise the applicable maxTotalAgents limit...` 等。

Observer 隔离：emitWorkflowEvent 逐 listener try/catch + promise containment，毒听众既不能传播也不能饿死后继听众（连 toString 都炸的 thrown 值都有 fixed label 兜底）。

### 1.10 持久化立场：log-only，resume 是 documented non-goal

`session.ts` 与持久化无关（是 worker 侧消息会话接线，为覆盖率可测而从 worker.ts 拆出）。真实持久记录只有两处：

1. **父 Session 的事件投影**（tool-workflow recorder）：`tool-workflow/run-start|agent-start|agent-end|run-end` 追加进 Session；首次 append 失败即禁用该 run 后续记录+一条 warn，留下合法连续前缀，绝不改变工具结果。
2. **对话历史本身**：完整 script/meta/args 留在 assistant tool_call 里（"the tool-call event already records the script durably"）。

README 原文："No journaling or resume — scripts, child progress, and intermediate values are not checkpointed, so a process restart cannot continue a run." 引入 journaling 会连带引入 Claude Code 的 determinism 禁令，而现在脚本可以读时钟/随机数。持久日志尾部缺 terminal suffix 是合法中断证据而非腐坏。

### 1.11 为什么 worker thread + RPC（而非进程内 vm）

其 Agent Note 逐一否决替代方案的三条理由：① 进程内 vm 的 `start()` 会阻塞调用方整整一个初始同步切片；② vm timeout 只罩第一片——首个 await 之后如果脚本同步自旋，进程内无法击杀（worker 上仍有此洞，但有 grace+terminate 兜底）；③ dispose 只能在宿主 loop 上"放弃"不肯结算的脚本，worker 让 terminate 成为真实的最终停止手段。附带收益：per-run 新 worker 天然获得序列化边界与环境清洗。代价：每 run 冷启动、无池化、每 agent() 两次 RPC 往返、结果是拷贝不是引用。

### 1.12 invariant 校验体系

全局 dsh-invariants 服务，同时挂 cold load（历史回放）与 live append（提交前钩子）两条路径，fail loud。workflow 家族规则举例：run id 不重复；之后任何事件的 meta 快照必须与 start 相同（JSON 比较）；agent-start 的 seq 正且唯一；agent-end 必须匹配在册 start 且身份一致；workflow/end 时不得有未闭合 agent；`agentsStarted ≥ trace.starts`；error 字段恰好在非 completed 时存在。tool-workflow 另有持久日志折叠状态机（一 run 一 start；成员 seq 必须恰 end 一次；run-end 后不得再有该 run 事件）。

### 1.13 测试揭示的关键边界行为

- 异步 provider 启动事务：早到的 result 必须等到 publish 后才转发（顺序断言 `['start:1','end:completed','run-end']`）。
- 取消竞态：completed 结果与外部 cancel 竞争时被改写为 cancelled；cancel 后叙事消息宿主侧抑制但 agent-end 保留（配对可见性）。
- parked-on-unowned-promise（`await new Promise(()=>{})`）：grace 强制结算 + terminate + workflow/end 照发。
- wedged worker 下 dispose()：宿主主动驱动子代处置并在 grace 内返回（<1200ms）。
- ghost callId 全套应答被容忍（teardown race 不崩溃）。
- holder-owned run 在引擎卸载后仍能完成（句柄捕获设计）。
- ralph integration：两个 distinct 空 seed 子会话、共享 cwd、parent history marker 绝不入子请求、结束后子代理全部消失。

### 1.14 重实现核对单（按依赖序）

seam 接口（start/never-reject result/cancel/dispose + 六 observe 事件 + 封闭 error code）→ 元校验（纯数据白名单，violation 全列）→ 脚本编译契约（统一 wrapper 保行号，host 预 parse）→ 执行体（空 context+冻结 hooks、containment、首片 timeout、FIFO 并发槽、cap×2、选项白名单、schema 子集断言）→ RPC 桥（封闭 tag 表、callId 三 Promise pending、ghost 容忍）→ 宿主管账（children map memoized disposal、pendingStarts、liveAgents 缺端合成、单一共享 AbortController、terminalClaim 三源 first-wins、grace timer、survivor sweep）→ 值边界（行为表照抄，路径限定错误、total renderer）→ 工具壳（schema 同文嵌 description、sync-collect execute、非 completed→isError、maxResultChars 截断通知、Session 记录器前缀语义）。

---

## Part 2：Headlong 纯 bash harness

### 2.1 总体架构

```
                              ┌──────────────────────────────────────────────┐
   人/Slack/Telegram          │  identity (~/.headlong/.identities/<name>/)  │
   ada hello ─────┐           │  ├ persona md   ├ memories/ (mem)            │
                  ▼           │  ├ skills/      ├ thinkers/ (step+subs)      │
            ┌─────────┐       │  └ trajectories/<uuid>-slug/trajectory.jsonl│
            │ chat    │──append "message" step──►┐                          │
            └─────────┘                          │  append-only JSONL DAG   │
                                                 ▼                          │
        ┌────────────────── thinkers dispatcher (常驻进程) ─────────────────┴─┐
        │ tail -F 每个 traj 文件 → tagged 行 → FIFO → 按 subscriptions 匹配    │
        │ 唤醒 thinker 的 step 脚本（并发上限 THINKERS_MAX_CONCURRENT=20）      │
        │ tick(1s)：pending 队列 / watchdog 合成 idle / wake_at 定时自醒       │
        └──────┬─────────────────────────────┬───────────────────────────────┘
               ▼                             ▼
     monolith/step（内在生命）        responder/step（聊天回复）
     每次 wakeup = 一个 shellm run    单次 llm 调用 + chat reply
               │                             │
               ▼                             ▼
  ┌──────────────── shellm（RLM 引擎, 2862 行）─────────────────┐
  │ loop: context(traj→messages) → llm → extract ```bash → 执行 │
  │       (Docker sandbox / local) → 输出 append 回 traj → 循环 │
  │  终止: FINAL="..."/FINAL_FILE= 或无代码块                   │
  │  嵌套: 生成的代码直接调 shellm → 子 traj fork/merge         │
  └──────┬───────────────┬──────────────┬──────────────────────┘
         ▼               ▼              ▼
     llm(多provider   context(traj    traj(append/fork/
     curl+SSE流式)    渲染messages)    merge/blob溢出)
                mem / skills / focus / glob / view / put / sub
                （生成的代码作为普通 PATH 命令调用）
```

组合接口只有三种：管道/stdout、文件系统（JSONL 轨迹 + markdown 记忆）、环境变量（`TRAJ_DIR`/`TRAJ_ID`/`SHELLM_RUN_STEP_ID`/`_SHELLM_PARENT_TRAJ_ID`…）。没有任何 RPC、socket API 或内部协议——除 dispatcher 用了一个 FIFO（本质还是文本行）。

各脚本职责一句话：**shellm** RLM 引擎核心；**llm** 多 provider LLM CLI（curl 直发 SSE，stdout=正文/stderr=thinking）；**traj** 轨迹存储引擎（append-only JSONL 的 new/append/fork/merge/show/tail/search/list，blob 溢出与跨进程锁）；**context** 把轨迹投影成 LLM messages 数组的策略层；**thinkers** 心跳调度器；**recap** 生命日志摘要（episodes/themes + tiered staircase）；**chat** 消息传输层（落地为轨迹上的 message step）；**focus** 目标管理（memories frontmatter CRUD）；**mem** 文件记忆库（YAML frontmatter .md 一条一文件）；**skills** SKILL.md 技能包管理器；**glob/view/put/sub** 四个"钝化"文件工具（git-aware 查找/带行号读取/原子写入/精确串替换）；**shellm-docker** 受限 docker 门面。

"Persistent agency"：agent 从不休眠——人的一句话只是 chat send 追加的一条 message step，dispatcher tail -F 当成唤醒事件投递；空闲时 monolith 通过 `.wake_at` 文件自排下一次自发思考（指数退避到 300s 上限，EXIT trap 必然武装——退出即布防）。"Recursive LMs"：LLM 不走 JSON tool-calling 而是**生成 bash 代码在真实 shell 里跑**，输出回灌下一轮输入；递归双关——模型可以在自己的输出代码里再调 shellm（LLM calling LLM）。

### 2.2 核心 agent loop（bin/shellm run_loop，2187 行起）

```bash
while [[ -z "$MAX_ITERATIONS" || "$iteration" -lt "$MAX_ITERATIONS" ]]; do
    # ① 轨迹 → messages（策略在 context 里，head/tail/pins 三段选择）
    messages_json=$(context --traj_dir … --assistant-types reasoning,final \
                          --user-types prompt,shell-output,feedback \
                          --exclude-types shellm-run,run-summary)
    # ② 调 llm；空响应重试把 thinking 回灌为 assistant 消息让模型续写
    response=$(call_llm "$system_prompt" "$messages_json" "$thinking_file")
    # ③ awk 做 heredoc-aware 围栏提取；无代码块=最终答案，记 final step 返回
    code=$(extract_code "$response")
    [[ -z "$code" ]] && { … printf '{type:"final",…}' | traj append …; return 0; }
    # ④ 包装用户代码：set -x 回显 + 捕获 FINAL/FINAL_FILE 到 sentinel 文件
    wrapped_code="set -x
$code
set +x
__shellm_rc=\$?
[ -n \"\${FINAL+x}\" ] && printf '%s' \"\$FINAL\" > \"$final_path\"
[ -n \"\${FINAL_FILE+x}\" ] && cat \"\$FINAL_FILE\" > \"$final_path\"
exit \$__shellm_rc"
    # ⑤ 后台执行 + 双阈值看门狗（输出字节零增长30s杀；嵌套beacon每5s刷新但900s硬上限仍杀）
    execute_code "$workdir" "$wrapped_code" "${env_vars[@]}" >> "$output_file" 2>&1 &
    # ⑥ reasoning step(assistant角色)/shell-output step(user角色) 追加轨迹
    # ⑦ $final_path 存在 ⇒ 写 final step、向父轨迹 merge、返回
    # ⑧ spin/stall 保护：连续10次失败或同命令重复失败3次 → 放弃
done
```

值得展开的机制：

- **完成信号走文件不走 stdout**：模型设 `FINAL="..."`，包装器写 `$rundir/final`（Docker 模式 bind-mount 可见），最终答案与其他输出严格分离，stdout 可以继续当管道用。
- **执行模型**：子进程 `bash -e -c` + stdin 接 `/dev/null`（交互提示立即 EOF，提示词教模型一律 `-y/--non-interactive`）；Docker 模式换 `docker exec`。
- **kill 原因走 marker 文件**：inactivity kill 产出结构化 feedback step，能对模型说清"你在等交互输入"还是"你的 sub-run 卡死了"，甚至检测疑似确认提示词并建议具体 flag。
- **行为矫正无处不在**：多个代码块时第二块起丢弃并给 `[rest truncated...]` 提示；无围栏整段 prose 当代码跑（剥 `[cmd]`/`[thought]` 标记）；专门处理 grok 把 ``` 附在句尾的毛病。
- 开跑前：有父轨迹则预生成 fork step_id → 子轨迹首行带 parent 反向链接 → 父轨迹记 fork step → 写 shellm-run 头 step（command/workdir/model/env 全记录）→ 后台异步跑便宜模型给 run 打 tldr。

### 2.3 llm 脚本工程细节

- payload 由 jq 过滤器拼接后**写临时文件**再 `-d @file` 发送——argv 单参上限 128KB，长 prompt 直接爆 "Argument list too long"。
- provider 发现靠模型名前缀 case 语句（claude-*/gpt-*/gemini-*/含 `/`=OpenRouter/opencode-*），派发完全靠动态函数名 `build_payload_$P`/`stream_$P`/`extract_text_$P`——provider 就是五组同形函数零抽象框架。
- 流式逐行读 SSE `data:` 行内嵌 jq 解析 delta；thinking 打 stderr 正文打 stdout。两个 trick：`_chunk=$(printf '%s' "$json" | jq -j '.choices[0].delta.content // empty'; printf X); _chunk="${_chunk%X}"`（尾缀 X 防 command substitution 吃掉换行）；热路径先字符串 glob 预判 `[[ "$json" == *'"finish_reason"'* ]]` 再 fork jq。
- 重试不变量：**只在没有部分输出前重试**——首个 delta 用 marker 文件标记 emitted，之后不再 retry（防止半截流当新回复）。另处理"provider 保持 200 但流以空白结束"判失败。
- 自己无状态：多轮靠 `-M '[{role,content},...]'` 传入；stdout=text/stderr=thinking/exit 0 的合同使它能当纯函数嵌 `$( )` 用。usage 写全局 ledger（ts/provider/model/identity/run_id）。
- 网络护栏映射 curl flags：connect 10s / max-time 600s / `--speed-limit 100 --speed-time 60`（100B/s 持续 60s 判卡死）。

### 2.4 traj：轨迹 DAG 与并发正确性

布局：`<hex8>-<slug>/trajectory.jsonl`，首行必为 `{type:"trajectory", step_id:<uuid>, ts, parent_traj_ref?}`。大字段溢出三件套：截断版内联 + `blobs/<step_id>-<n>.stdout` 引用 + 原始字节数。

append 的 mkdir 锁（macOS 无 flock 的可移植原子原语）：

```bash
local lock_dir="${traj_file}.lock"
until mkdir "$lock_dir" 2>/dev/null; do
    # every ~0.5s steal locks older than 5s — the holder was killed mid-append
    ...
done
printf '%s\n' "$compact" >> "$traj_file"
rmdir "$lock_dir"
```

自动盖章：env 有 `SHELLM_RUN_STEP_ID` 时 append 自动补 run_id——"嵌套代码写的 step 自动归属当前 run" 的全部实现就这一行 env 约定。DAG 就是两条相对路径链接：fork 在父轨迹追加 `{type:"fork", child_ref(相对路径), step_id}`、子轨迹首行反向指回；merge 读子轨迹末 step_id，父轨迹追加 `{type:"merge", from_step, content}`（shellm 收尾自动做，把子 run final answer 提升进父时间线；stall abort 也 merge `(stalled:...)`）。

**bin/sub 不是子代理**——它是 exact-string substitution 编辑工具（awk literal 计数，出现 0 次或多于 1 次拒绝除非 --replace-all；temp+mv 原子写；保留权限和尾换行）。真正的子代理就是 shellm 递归本身：模型写 `shellm "task..." > out.txt &`，env 注入 `_SHELLM_PARENT_TRAJ_ID` 自动建 fork 链接，结束时自动 merge 回。**并发控制=shell 本身的 job control**（系统提示直接教 `&` + `wait`），没有 orchestrator、没有深度上限——"a spend-capped API key and shellm's inactivity timeouts are the guard rails"。子 run 必须 stdout 重定向文件（继承的管道随启动它的 step 结束而消失）；beacon 文件让父 watchdog 不误杀等待中的思考。

### 2.5 上下文工程五件套

- **context**（500 行）：轨迹→prompt 的投影层。自我定位 "Policy layer"：只解析 head、tail、pins 三段，两次流式字节扫描加一次 jq，成本对轨迹长度 O(1)。两个通用算法值得偷：blob prepass（先渲染一遍只收集选中 step 引用的 blob 路径）；预算二分（给定 --max-bytes 对单步截断上限 binary search 直到塞进预算——比启发式 chunking 干净得多）。
- **recap**（817 行）：两种压缩。默认 map-reduce：窗口切分优先按 ≥30min 时间空隙（episodes 对齐自然工作 session）其次 100 步/60KB；缓存增量化只摘要新步。`--context` 是层级楼梯：按 fanout=10 逐级 rollup（tier1 每 10 步一块，tier2 由 10 个 tier1 归并……），惰性/增量/幂等的 sealed block 文件（存在即跳过），且只向前构建。R（verbatim 尾窗）从 token 预算推导：40% 给 recency，auto cap 4000 tokens。人格化提示一阶视角："You are the AI agent whose life log this is. Summarize... say 'I', never 'the agent'." 关键立场：**压缩从不破坏原文件**——原始 JSONL 永在，层级只是索引/缓存（"Context is a projection of the trajectory. Nothing is compacted away in place."）。
- **mem**（478 行）：持久记忆=一个 markdown 目录（`<ts>_<hex4>_<slug>.md` YAML frontmatter）；search 朴素地把所有记忆 cat 成大字符串 pipe 给便宜模型做语义匹配——没有向量库，inspectable/greppable 就是特性。
- **focus**（325 行）：goal 文件的 frontmatter CRUD，monolith prompt "{{goals}}" 段的数据源——目标不是状态机而是文本。
- **skills**（1490 行）：SKILL.md 目录的包管理器（GitHub install、requires 检查、promote 到 kernel 常驻系统提示，预算 8000 tokens）。核心洞察：**工具发现即 prompt injection，而不是 function schema 注册**——`skills prompt` 把 CLI 用法+kernel 全文+技能清单拼成系统提示段落。

**schema 即 prompt**：不存在注册表。系统 prompt 文字清单列出 view/sub/put/glob + spawn 能力；shellm 把自己的 usage_text 整个拼进 system prompt 的 "ShellLM reference" 一节——**工具的 man page 就是它的 contract**。安全边界：四小工具全是防御性设计（put 拒绝覆盖需 --force、sub 拒绝非唯一匹配、view 二进制探测、glob 尊重 .gitignore），真正隔离靠 Docker broker 白名单（拒绝 privileged/bind mount/socket）。是否天然 PTC？"模型不是从菜单点菜的 dispatcher，而是坐在终端前的 operator"；`count_links_by_domain` 不需要定义——`curl | grep | wc -l` 即组合涌现，且比典型 PTC 更激进：**组合表达式直接就是 OS 进程**，分支(&/wait)、子代理(递归)、长任务(tmux)全部复用 shell 语义，orchestration framework 就是 process model。

### 2.6 设计洞察与反面教材

简单到令人惊讶的机制清单：dispatcher=`tail -F | FIFO | read -t 1` 事件总线（背压用定宽 epoch 文件名队列使 glob 序=到达序；liveness 由 1s tick 内联 housekeeping 保证）；LCG 式心智节律只是"下次唤醒 epoch 写进 .wake_at + EXIT trap 必然武装"；mkdir 当分布式锁、token 文件当租约、epoch 文件当 cron、marker 文件传 kill 原因——**每个同步原语都退化成文件系统的一个姿势**；watchdog/beacon 协议只是每 5s 被 touch 的临时文件路径；salience injection——打断运行中 agent 的方式是往它下一个 context 里插一行 feedback step，让它任务中途也能顺带回一句；replay-based idempotency 连"No reply 也是决定"都记录防抖。

bash 的优势：LLM 对齐（预训练语料最大的行动语法）；一切状态可 grep/cat；组件天然可替换（换记忆=改 MEM_DIR）；审计友好；修改实验成本低（10K 行 vs 传统 harness）。

反面教材（他们的工程债自供状态）：SIGPIPE under pipefail 在 macOS 杀掉每次流式运行的注释（shellm:1798）；bash 3.2 `read -t` 返回码歧义；zombie/PGID/cgroup 层层的 kill 盲区（thinkers 大段事故考古注释）；生产部署实际绑定 systemd unit + 专门 killall 工具；性能悬崖（traj cat O(file)，532MB 生命日志迫使所有读取改造成 tail-window + 容错解析）；**无 schema/type 安全——契约散落在 env 名、JSONL 字段名和相对路径约定里，错误静默**（AGENTS.md 明言记录的 coupling 都 fail silently）；测试即金样 diff 无法保护跨脚本隐式协议。这最后一条反过来支持我们把协议做成封闭枚举。

---

## Part 3：goja 宿主绑定机制（JS↔Go 通信）

以下事实均已对照 dop251/goja master 源码核实。核心结论：**"通信"分两个方向，且只有一个 goroutine 被允许触碰 VM**——不需要任何 IPC。

### 3.1 方向一 JS→Go：注入函数就是普通调用

goja 支持把任意 Go 函数注入为 JS 全局（`vm.Set("agent", goFn)`）。当 JS 执行 `await agent(prompt, opts)` 时，goja 在 VM 所属 goroutine（owning goroutine）上直接调用这个 Go 函数——普通函数调用，零 IPC。硬规则来自 Runtime 非线程安全：**JS 执行和注入函数永远只发生在这一个 goroutine 上**。

### 3.2 agent() 必须立刻返回 Promise，不能阻塞

若 hookAgent 等子代理跑完再返回值，fan-out 全串行、VM 卡死、`Interrupt()` 失去意义。正确形态（签名对照 builtin_promise.go:625）：

```go
func (x *Execution) hookAgent(c goja.FunctionCall) goja.Value {
    throwIfCancelled(x)                                   // 边界=下一个 hook
    prompt := assertNonEmptyString(x.vm, c.Arguments, 0)  // 路径限定 INVALID_ARGUMENT
    opts := readAgentOptions(x, c.Arguments)              // 白名单外 fatal
    x.gates.agentAdmitOrThrow()                           // caps → AGENT_CAP(fatal)
    p, resolve, reject := x.vm.NewPromise()               // *Promise + resolve/reject 回调
    x.pending[callID] = &pendingCall{resolve: resolve}
    x.cmdCh <- cmde{cmdChildStart, callID, req}           // 非阻塞投递给 host goroutine
    return x.vm.ToValue(p)                                // 挂起态 Promise 交给 JS
}
```

JS 拿到 Promise，`await` 挂起，控制权回到外层 drive() 循环。

### 3.3 方向二 Go→JS：resolve 必须回到属主协程的安全窗口

子代理跑在别的 goroutine 上（host 收 child-start 后嵌套 RunQuery）。而 goja 源码明确警告（builtin_promise.go:603 注释原文）：

> WARNING: The returned values are not goroutine-safe and must not be called in parallel with VM running. In order to make use of this method you need an event loop…

所以 `resolve()`/`reject()` 绝不能从 host goroutine 直接调。结果经 channel 送回属主协程，由它在驱动间隙（VM 不在执行时）完成 resolve：

```go
for p.State() == goja.PromiseStatePending {
    select {
    case ev := <-x.evtCh:
        pc := x.pending[ev.CallID]
        pc.resolve(ev.Projection)        // ✓ 安全窗口：此刻 VM 空闲
        delete(x.pending, ev.CallID)
    case <-x.cancelCh:
        x.vm.Interrupt(errCancelled)     // 唯一例外：Interrupt 是原子 flag（vm.go:685），专为外部设
    case <-time.After(...):              // watchdog
        x.vm.Interrupt(errSyncTimeout)
    }
    x.drainJobs()
}
```

### 3.4 JS 续体什么时候真正恢复：jobQueue 与 leave()

`resolve()` 按 Promises/A+ 规范只是把 reaction job 入队，不会立即执行 JS。goja 内部 `jobQueue []func()`（runtime.go:199）**没有公开的单步排空 API**（master 无 PerformJobLoop/ExecutePendingJobs 导出方法）；队列唯一冲刷点是 `leave()`（runtime.go:2836-2843）——"called when the top level function returns normally (i.e. control is passed outside the Runtime)"。因此驱动器每次处理完事件要重新进入一次 runtime 把队列刷掉：

```go
func (x *Execution) drainJobs() {
    x.vm.RunString("0") // 平凡语句：进入 → leave() 自动冲刷全部排队 job → 出来
}
```

drainJobs 期间被执行的就是被唤醒的 JS 续体——继续往下跑 parallel/pipeline 组合逻辑、发起新的 agent()、或走到最终 return。drive 循环再次阻塞 select，如此往复直到外层 Promise settle。（成熟替代：引入 dop251/goja_nodejs 的 EventLoop 管理 job 调度；代价是多一层抽象。）

另一处相关约束（builtin_promise.go:56）："Instances of Promise are not goroutine-safe"——pendingCall 表里的 resolve/reject 只能在属主协程触达。

### 3.5 全景图

```
G_VM（唯属主协程）                    G_HOST                     G_CHILD goroutines
─────────────────────────────       ────────────────────       ──────────────────
JS: await agent(p)
 └→ hookAgent()【本协程同步调用】
     pending[42]=promise
     cmdCh ◄──────────────────────►  收 child-start#42
     return Promise                        ├→ SpawnChild ─────► engine.RunQuery
                                           │                       （真 LLM 流式）
JS 挂起…                                   │◄── result ─────────┘
drive(): select {                          ▼ 投影为纯 JSON
  case evtCh ◄────────────────────  evtChildSettled#42
    resolve(proj)   ← 安全窗口: VM空闲
  }
  drainJobs():  ← JS 续体真正执行的时机
  … 直到外层 Promise settle
```

反模式提醒：技术上可以注一个阻塞式 `agentSync(prompt)`（hook 里 `<-settled chan`），顺序脚本更好写——但它使 parallel() 失效，且违反 3.3 的并行约束前提（该路必须为零）。另外 goja 无 heap cap，脚本可造巨大字符串耗内存——这是已知缺口（caps+depth 兜底），记录于提案 note 的 Risks。
