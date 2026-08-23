# Agent Notes

本目录存放唯一一类设计文档：**Agent Note**。它记录影响本代码库的决策或提案——*为什么这么做、放弃了什么*，即代码和普通文档承载不了的部分。本文件定义 Agent Note 的存放位置、编写时机与[文件内格式](#文件内格式)。规范参考 [deepseek-harness/.agents/notes](https://github.com/deepseek-ai/deepseek-harness/blob/master/.agents/notes/README.md)。

## 目录与命名

每个 Agent Note 有两个轴，都编码在**路径**里：`{lifecycle}/{class}/yyyy-mm-dd-topic-title.md`。

- **Lifecycle**（顶层目录）是状态，文件随状态在目录间移动：
  - **`proposed/`** — 实施前评审的提案；尚未构建（或只构建了一部分）。
  - **`implemented/`** — 决策已落地。文件记录决策内容与被否决的备选方案，并**与实际交付保持同步**：代码后续移动文件、重命名包、修改关键默认值时，同一次变更中更新 Note 的事实性内容（路径、名称、结构），但不改决策本身。
  - **`rejected/`** — 提案被考虑后否决。只要它的论证还能防止一个有诱惑力的实质性错误就保留；否则连同关联文件一起删除。
- **Class**（二级目录）是决策的类型，见下方[分类](#分类)。

文件名中的日期是议题**首次提出**的日期（依 git 历史）。Note 之间的交叉引用一律使用相对 markdown 链接（`[主题](../../implemented/architecture/2026-…-….md)`），不用裸文字或编号——保证链接可机械校验、且能在目录移动中存活。不建集中式 INDEX.md，直接浏览 lifecycle/class 目录或在仓库内搜索。

## 分类

| Class | 覆盖范围 |
|---|---|
| `feature` | 新的用户侧或模型侧能力。 |
| `bug-fix` | 修正缺陷，或关闭复盘暴露的缺口。 |
| `simplification` | 移除代码、行为或表面积，不新增能力。 |
| `architecture` | 关于**已交付源码**的结构性决策——包如何关联、运行时词汇是什么。 |
| `process` | 围绕代码的工具链、策略或工作流——门禁、依赖管理、脚本——而非运行时行为。 |
| `testing` | 测试基础设施与测试策略。 |

`architecture` / `process` 的分界线：**architecture** 关于我们交付的源码本身；**process** 关于周边工具与流程。（刻意不设 `refactor`——它与 `simplification` 重叠，后者的判别标准"可观测行为是否变化"已覆盖。）

## 编写时机

每个非平凡变更必须在同一个 PR 中新增或更新至少一个 Agent Note。非平凡指：改变行为、架构、跨文件/包共享的契约、流程或工具、测试策略、磁盘/线上/配置格式，或任何维护者可能合理重新审视的决策。面向未来的大量工作从 `proposed/` 起步；已定的决策直接进 `implemented/`。已拥有该决策的 Note 更新即可，不得重复建 Note。纯机械性局部编辑（不涉及行为、契约、结构、流程、论证）豁免。

Agent Note 永远不会被编辑成**另一个决策**：替代它用新 Note 取代，并保持新旧交叉链接。lifecycle 迁移规则：`proposed/` → `implemented/` 时把 `## Proposal` 改写为现在时态的 `## Decision`，将 Acceptance criteria / Risks 并入 `## Consequences`；`proposed/` → `rejected/` 时仅在 Status 行追加原因并冻结正文。

## 文件内格式

### Header block

每个 Agent Note 的前三行必须严格是：

```markdown
# Agent Note: <title>

Status: <status>
```

后跟一个空行。`Status:` 取三种形式之一，且必须与所在 lifecycle 目录一致：

- `Status: proposed`
- `Status: implemented`
- `Status: rejected — <一行原因>`

Header 两行 token（`# Agent Note: ` 与 `Status:` 行）**保持英文原文**；正文语言默认中文，如需英文版本以 `.en.md` 镜像文件提供，结构与中文版逐节对应。

### 正文骨架

- `proposed/`：

  ```markdown
  ## Problem
  ## Proposal
  …自定义技术章节…
  ## Alternatives considered
  ## Acceptance criteria
  ## Risks
  ```

- `implemented/`：

  ```markdown
  ## Problem
  ## Decision
  …自定义技术章节…
  ## Alternatives considered
  ## Consequences
  ```

  `## Decision` 用现在时描述已交付的现实；spec 语气的章节名（`## Proposal`、`## Plan`、`## Migration plan`、`## Acceptance criteria`）不允许出现在 implemented 中。`## Testing`、`## Deferred`、`## Related` 可在陈述现时事实时使用。

- `rejected/`：保留提案时的骨架原样冻结，结论写在 `Status:` 行。

### Alternatives considered — 强制

每个 Agent Note 必须有 `## Alternatives considered` 章节：每个真实备选方案及它为何落败，每个方案一段加粗导语，争议大的用 `### Why not <X>?` 小节。没有记录"打败了谁"的决策会招致反复重新争论——这正是 Agent Note 要防止的失败。备选方案只记录、不虚构。
