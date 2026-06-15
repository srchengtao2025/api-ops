## Feature request / 功能请求

### 痛点 / Problem

你遇到了什么问题？什么场景下？

### 提案方案 / Proposed solution

API 形态 / SQL 草稿 / UI 截图 / 伪代码，越具体越好。

### 替代方案 / Alternatives considered

你考虑过什么方案，放弃原因是什么？

### 影响面 / Impact

- 影响哪些端点 / 哪些 SPA 页面？
- 是否需要 database migration？
- 是否影响 [DESIGN.md 21 项决策基线]？

### 数据源 / Data source

[DESIGN.md 3 数据源铁律] 要求新端点从 3 源之一取数：

- [ ] upstream admin API
- [ ] 直连 upstream DB (RoDB)
- [ ] 本地 cache_* 表 (5min tick)

你倾向哪个？为什么？

### 优先级建议 / Priority

- [ ] P0 — 阻塞生产
- [ ] P1 — 重要
- [ ] P2 — 锦上添花
- [ ] P3 — 想法阶段

### 自己写 PR？/ Will you submit a PR?

- [ ] 我愿意自己写 PR
- [ ] 我愿意写 PR 但需要 maintainer 协助
- [ ] 我没空写，请 maintainer 评估

### 关联 / Links

相关 issue / RFC / 文档。
