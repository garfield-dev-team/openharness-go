# 上下文压缩与前缀缓存策略 (Context Compaction & Prompt Caching)

在大语言模型（LLM）的智能体（Agent）工程化实践中，随着工具调用的增多和对话轮次的累积，上下文窗口（Context Window）的爆炸是一个核心痛点。本文档梳理了业界标杆（如 Claude Code）在解决**长文本上下文压缩**与**大模型前缀缓存（KV Cache）失效冲突**时的核心机制与最佳实践，并为当前 `openharness-go` 项目提供后续迭代的参考。

---

## 一、五级渐进式上下文压缩流水线 (Progressive Compaction Pipeline)

Claude Code 采用了分层、渐进式的策略（从无损低成本到有损高成本），在每次调用 LLM 前运行流水线，确保上下文健康：

### L1: 工具结果预算截断 (Tool Result Budget)
- **策略**: 原地截断过大的工具调用结果（如大文件读取、超长的报错日志）。
- **做法**: 设定阈值（如 50,000 字符）。若超出，**保留尾部 (tail)**，将头部替换为 `[tool result truncated: X chars removed]...`。
- **依据**: 终端命令或报错日志的尾部通常包含最终的 Exit Code、报错栈底或执行完成信息，对 Agent 继续决策最有价值。

### L2: 旧消息裁剪 (Snip Compact)
- **策略**: 针对不在“保留区”（最近 N 条对话）的老旧消息，进行“掏空中间，保留头尾”的裁剪。
- **做法**: 若消息长度超出阈值（如 8,000 字符），只保留开头和结尾各一半的预算字符，中间替换为 `[snipped X chars]`。
- **依据**: 能够保留文件结构、函数签名（通常在头部）以及最终结论（通常在尾部），剥离冗长的实现细节。

### L3: 微压缩 / 去除冗余空白 (Microcompact)
- **策略**: 遍历所有消息的文本，移除多余的空行。
- **做法**: 将连续的多个空行压缩为单一空行。
- **依据**: 一种无损的 Token 节约手段，主要针对某些工具输出的大量格式化空白。

### L4: 上下文坍缩 (Context Collapse)
- **策略**: 一种“二段回收”防线机制。
- **做法**: 在正常的引擎循环中，老旧消息会被移入“暂存区”。在 L4 阶段，如果 Token 还是超标，系统会直接清空这个暂存区（彻底永久丢弃这部分上下文）。

### L5: 自动总结 (Auto-compact / LLM Summary)
- **策略**: 最激进、最智能的压缩方式，调用 LLM 重新生成历史上下文的结构化摘要。
- **做法**: 将历史消息截断格式化后，交由 LLM 总结，强制输出 XML 结构的 `<summary>`，包括：项目范围 (`<scope>`)、工具使用 (`<tools_used>`)、关键文件 (`<key_files>`)、当前进度 (`<current_work>`)、后续计划 (`<pending_work>`)、关键决策 (`<key_decisions>`) 和代码上下文 (`<code_context>`)。最后将历史消息替换为这条总结，拼上最近几条消息。
- **依据**: 避免简单总结导致关键细节（如文件路径、代码片段）丢失而引发 Agent 幻觉。

---

## 二、上下文压缩与 KV Cache 的天然冲突

大模型厂商（如 Anthropic 的 Prompt Caching）的缓存匹配机制基于**绝对前缀匹配 (Prefix Matching)**。
**冲突点**：一旦我们在历史消息的中间进行了哪怕是一个字符的修改（例如 L2 裁剪了中间的文字，或 L3 删除了一个空格），从修改点开始往后的所有 KV Cache 都会瞬间失效，导致模型必须重新计算（Re-compute）Token，不仅速度变慢，还会产生高昂的 Input Token 费用。

---

## 三、Claude Code 的破局思路

为了在“控制上下文长度”与“最大化缓存命中率”之间取得平衡，Claude Code 采取了以下策略：

### 1. 结构上的“动静分离”设计
请求体被严格划分为两部分：
- **静态长文本区**: 包含 System Prompt、Tools Schema 定义、加载的长篇项目文档（如 `CLAUDE.md`）或记忆等。这部分通常占据数千甚至数万 Token。
- **动态对话区**: 用户与 Assistant 的多轮 `Messages` 数组及工具调用结果。

**缓存锚点 (Cache Anchor)**：
Claude API 允许在请求体的任意 Block 打上 `cache_control: {"type": "ephemeral"}` 标记。Claude Code 会**在“静态长文本区”的最后一个元素（例如 System Prompt 的末尾或最后一个 Tool）打上 Cache 标记**。
**效果**：无论后面的动态对话区（`Messages`）如何堆积，或因压缩被修改，前面占据 Token 大头的静态长文本区的 KV Cache **永远不会失效**。

### 2. 高阈值与低频触发
- 触发 L1~L5 压缩的 Token 阈值被设置得非常高（例如 40,000 Tokens）。
- **日常阶段**: 在前 40k Tokens 的交互中，上下文仅做正常的 Append，前缀完全不变，KV Cache **完美命中**。
- **干预阶段**: 只有对话长度即将触及红线时，才会触发一次集中式的压缩。此时确实会导致 `Messages` 部分的缓存 Miss 产生计算成本，但这属于“断臂求生”，避免了整体 Token 爆仓，且让后续请求变短、变便宜。

### 3. L5 总结带来的“重生” (New Session)
当 L5 级别的 LLM 总结发生时，旧的 `Messages` 历史被彻底清空并替换为单条总结消息。
- 从缓存的视角看，这相当于开启了一个**全新的会话**。
- Context Window 可能会从 80,000 Token 断崖式下降到 5,000 Token。
- 随后的交互将基于这个新的短前缀，重新开始积累并享受 KV Cache 的加速红利。

---

## 四、OpenHarness-Go 后续迭代建议

当前 `openharness-go` 已经实现了完整的五级压缩流水线（见 `pkg/services/compact.go`）。但在缓存利用率上，为了对齐工业级标准，建议在后续版本进行以下改造：

1. **底层结构支持**: 修改 `types.ContentBlock` 和 `types.ConversationMessage`，增加对 Anthropic API `cache_control` 字段的支持。
2. **打标策略**: 在 `pkg/engine/query.go` 构建请求参数（`LLMRequestParams`）时，识别出最庞大的静态前缀（例如组合好的 System Prompt），并为其末尾打上 `ephemeral` 缓存标记。
3. **隔离压缩影响**: 确保 `RunPipeline` 在对 `Messages` 进行原地的 L1~L4 裁剪或 L5 替换时，完全不干扰已打好标记的静态前缀区。这样即实现了无缝压缩，又保住了最大头的缓存费用。
