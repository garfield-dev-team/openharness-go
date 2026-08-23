# AGENTS.md

本文件是 agent 在本仓库工作的常设指令。文档层级与写作规范见 [docs/AGENTS.md](docs/AGENTS.md)；决策记录规则见 [.agents/notes/README.md](.agents/notes/README.md)。

## 构建与验证

- 构建：`go build ./...`；静态检查：`go vet ./...`；测试：`go test ./...`。提交前三者必须全部通过。
- 仓库目前几乎没有测试覆盖：为可测行为补测试时，优先放在被测包内（`package foo_test`）。

## 决策与文档

- 每个非平凡变更在同一 PR 中至少新增或更新一个 Agent Note，路径 `.agents/notes/{lifecycle}/{class}/yyyy-mm-dd-topic.md`（[规则](.agents/notes/README.md)）；新 Note 前先做取代检查（[指令](.agents/notes/AGENTS.md)）。
- 文档描述当前状态而非历史；仓库内引用一律用可解析的相对链接。
