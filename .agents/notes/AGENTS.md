# AGENTS.md — Agent Notes

Agent Note 是由 agent 撰写的 RFC：持久的提案与决策记录，保存论证、备选方案、后果与所需的验证。编写前遵循[文档标准](../../docs/AGENTS.md)与 [Agent Note 规则](README.md)。

**每个新 Agent Note 都必须触发取代检查（supersession check）。** 先在活跃目录树（`proposed/`、`implemented/`、`rejected/`）中搜索覆盖同一决策或机制的旧 Note；判定为完全取代的旧 Note 与新 Note 在同一个 PR 中交叉链接并归档处理，部分取代则保持双方活跃并互相链接。没有做取代检查的新 Note 不应被合入。
