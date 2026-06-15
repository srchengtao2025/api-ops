# 贡献指南 / Contributing to api-ops

感谢你愿意一起让 api-ops 变得更好。  
本指南说明本项目的**开发流程、提交规范与社区公约**。请花 5 分钟读一遍，能帮我们少走很多弯路。

> Thanks for your interest in contributing to api-ops! This guide covers dev workflow,
> commit conventions, and community norms. Please read it once before opening an issue or PR.

---

## 目录 / Contents

- [行为准则 / Code of Conduct](#行为准则--code-of-conduct)
- [我能贡献什么 / What can I contribute](#我能贡献什么--what-can-i-contribute)
- [开发流程 / Development workflow](#开发流程--development-workflow)
- [本地搭建 / Local setup](#本地搭建--local-setup)
- [提交规范 / Commit conventions](#提交规范--commit-conventions)
- [Pull Request 规范 / PR guidelines](#pull-request-规范--pr-guidelines)
- [Bug 报告 / Reporting bugs](#bug-报告--reporting-bugs)
- [功能请求 / Feature requests](#功能请求--feature-requests)
- [安全漏洞 / Security issues](#安全漏洞--security-issues)
- [设计基线 / Design baseline](#设计基线--design-baseline)
- [三大数据源铁律 / 3 data-source rule](#三大数据源铁律--3-data-source-rule)

---

## 行为准则 / Code of Conduct

本项目采用一份简洁的"互相尊重"公约：

- **善意推定**：对方一定在尽力解决问题，先讨论再判断
- **聚焦技术**：分歧拿数据 / RFC / 测试用例说话，不搞人身攻击
- **接受不完美的 PR**：审稿人给建议，作者有最终决定权（"我的仓库我做主"）
- **隐私优先**：永远不要在 issue / PR / 截图里贴真 token / 真 IP / 真实业务数据

This project follows a simple "mutual respect" code of conduct. Be kind, assume good faith,
argue with data not personalities, and **never** post real tokens, IPs, or business data
in public issues/PRs/screenshots.

---

## 我能贡献什么 / What can I contribute

| 类别 | 例子 |
|---|---|
| 🐛 Bug fix | 修编译错误、内存泄漏、SQL 慢查询、并发竞争 |
| ✨ Feature | 新的 BILLING 端点 / 新的 SPA 图表 / 新的告警规则 |
| 📚 Docs | 完善 README、RFC、AGENTS.md、test-reports |
| 🌐 i18n | 翻译 README / 国际化 SPA 文案 |
| ⚡ Perf | 5min tick 提速、SQL 加索引、Redis 缓存命中率优化 |
| 🧪 Test | 补 unit test / integration test / e2e Playwright |
| 🐳 Infra | Dockerfile 瘦身、CI workflow、Helm chart |
| 🔌 Adapter | 新的 upstream provider 接入层、新的 webhook 通道 |

---

## 开发流程 / Development workflow

```bash
# 1. Fork + 克隆
git clone https://github.com/<your-username>/api-ops.git
cd api-ops

# 2. 拉一条 feature 分支（不要直接动 main）
git checkout -b feat/<scope>/<short-desc>
# 例: feat/billing/v5-quarterly-export

# 3. 改代码 + 跑测试
#    详见 "本地搭建" 一节
go build ./...
docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine \
  sh -c "go test ./internal/..."

# 4. 提交
git add -p
git commit -m "feat(billing/v5): 季度对账导出 (含 5min tick + Redis cache)"
git push -u origin feat/billing/v5-quarterly-export

# 5. 开 PR, 走 PR 模板
gh pr create --title "feat(billing/v5): 季度对账导出" \
  --body-file .github/PULL_REQUEST_TEMPLATE.md
```

> **注意**：本项目 [AGENTS.md](./AGENTS.md) 列出 15 条项目铁律（含命名、部署、错误率口径、3 数据源），
> 违反铁律的 PR 会被直接 close，无论代码多漂亮。读一遍再动手，胜过 review 来回 5 轮。

---

## 本地搭建 / Local setup

### 前置条件 / Prerequisites

- **Go 1.22+** （macOS 1.22.5 有 LC_UUID bug，**go test 必用容器跑**，见下）
- **Node 20+** / npm
- **PostgreSQL 15**（docker 一键起）
- **Redis 7**（可选）
- **Docker + docker compose**

### 一键起 demo / Quick start

```bash
# 1. 复制 .env 模板
cp .env.example .env
cp .env.production.example .env.production

# 2. 填 3 个必填
#    - REPLACE_WITH_UPSTREAM_API_TOKEN
#    - REPLACE_WITH_DB_PASSWORD
#    - REPLACE_WITH_ADMIN_PASSWORD

# 3. 起服务
docker compose up -d

# 4. 访问 http://localhost:8088/
```

### macOS 上跑 go test / Run go test on macOS

```bash
# 容器内跑，避开 LC_UUID 段错误
docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine \
  sh -c "go test ./internal/..."
```

### 跨平台编译 / Cross-platform build (macOS arm64 → linux/amd64)

```bash
docker buildx build --platform linux/amd64 -t api-ops:dev . --load
```

详见 [AGENTS.md §部署铁律](./AGENTS.md)。

---

## 提交规范 / Commit conventions

本项目采用 **Conventional Commits**（轻量版，scope 用括号）：

```
<type>(<scope>): <subject>

[optional body]

[optional footer]
```

### type 取值 / Types

| type | 用途 | 例 |
|---|---|---|
| `feat` | 新功能 | `feat(billing/v5): 季度对账导出` |
| `fix` | 修 bug | `fix(monitor): P95 走 cache 而非 percentile_cont 慢查询` |
| `refactor` | 重构（无功能变化） | `refactor(api): handler 拆 billing v2/v3 独立文件` |
| `perf` | 性能优化 | `perf(sync): 1min tick 批量 insert 取代 1 by 1` |
| `docs` | 文档 | `docs(DESIGN): 补 Q-C11 错误率新口径决策记录` |
| `test` | 测试 | `test(billing/v3): 补 round-robin tick 单元测试` |
| `chore` | 构建/工具 | `chore(deps): 升级 gin v1.10.0` |
| `ci` | CI/CD | `ci(github): 加 golangci-lint workflow` |
| `revert` | 回滚 | `revert: feat(billing/v4): 利润分析 v0.1` |

### scope 取值 / Scopes

尽量用以下值，方便日后 `git log --oneline --grep=...`：

- `web` / `v4` / `v3` / `v2` — 前端 / BILLING v4/v3/v2
- `monitor` / `ai` / `sync` / `scheduler` — 后端模块
- `api` / `dal` / `realtime` / `auth` / `audit` — 后端包
- `docs` / `ci` / `deps` — 杂项

### subject 规范

- 中文 / 英文都允许（**同一 PR 内保持一致**）
- ≤ 60 字符
- 不加句号
- 用动词原形（"新增" / "add"，不用 "增加了" / "added"）

---

## Pull Request 规范 / PR guidelines

### PR 标题

跟 commit subject 保持一致：

```
feat(billing/v5): 季度对账导出 + Redis cache 5min tick
```

### PR 描述（必填）

```markdown
## 背景 / Context
为什么需要这个改动？贴 issue 链接、用户反馈、Q-decision 引用。

## 改动 / Changes
- 改了什么 / 新增什么
- 关联 RFC / AGENTS 铁律引用

## 数据源 / Data source
从以下 3 源之一取数（**3 数据源铁律**）：
- [ ] upstream admin API
- [ ] 直连 upstream DB (RoDB)
- [ ] 本地 cache_* 表 (5min tick)

## 测试 / Testing
- [ ] 单测已加
- [ ] 集成测试已加
- [ ] 公网环境验证通过（贴时间戳 + commit hash）

## 影响 / Impact
- 性能影响（SQL 加索引？Redis 加 key？）
- 兼容性（数据库 migration？API breaking change？）
- 文档（哪些 .md 要同步更新？）

## Checklist
- [ ] 我读了 [AGENTS.md](./AGENTS.md)
- [ ] 我没在代码 / 注释 / 截图里贴真 token / IP / 业务数据
- [ ] go build / go test / npm build 全过
- [ ] 改了 SQL 我查过 information_schema.columns
```

### Review 流程

1. 维护者会在 1-3 工作日内 review
2. CI 跑 golangci-lint + go test + npm build
3. 至少 1 人 approve 才能 merge
4. squash merge（保持 main 干净）
5. 大改动（>500 行 or 数据库 migration）需在 Discord/issue 里先 RFC

---

## Bug 报告 / Reporting bugs

**用 [Bug 报告模板](./.github/ISSUE_TEMPLATE/bug_report.md)** 开 issue。

请提供：

- api-ops 版本（`git rev-parse HEAD`）
- Go / Node / PG / OS 版本
- 完整复现步骤（从 docker compose up 开始）
- 期望行为 vs 实际行为
- 错误日志（`docker logs api-ops-api-1 --tail 200`）
- 截图（**先涂掉 token / IP / 业务数据**）

> 缺关键信息的 bug 报告会被先打上 `needs-info` 标签，不会被处理。

---

## 功能请求 / Feature requests

**用 [Feature request 模板](./.github/ISSUE_TEMPLATE/feature_request.md)** 开 issue。

请说清楚：

- 你想解决什么问题 / 痛点
- 你的使用场景（什么角色、多大用量、频次）
- 提案方案（API 形态、SQL 草稿、UI 截图）
- 替代方案（你考虑过什么，放弃原因）
- 是否愿意自己写 PR（**我们最爱这种**）

---

## 安全漏洞 / Security issues

**不要在 public issue 讨论安全漏洞**。

请发邮件到 **security@api-ops.dev**（占位邮箱，待维护者绑定）：

- 标题前缀 `[SECURITY]`
- 详细描述漏洞 + 复现步骤
- 你的修复建议（可选）

我们会在 48 小时内回复，严重漏洞 7 天内修。

> **不要在 PR / commit / 截图里贴真实 token / 密码 / 业务数据**。
> 即使是 "我自己的内网测试数据"，脱敏后再贴。详见 [AGENTS.md §隐私铁律](./AGENTS.md)。

---

## 设计基线 / Design baseline

本项目已锁定 **21 项决策**（Q1-Q14 + Q-C4/Q-C6 + Q-D1/D2 + Q-C7~C11）于 [docs/DESIGN.md](./docs/DESIGN.md)。

任何**重大改动**（架构、新模块、breaking change）必须：

1. 先开 issue / discussion 讨论
2. 写一份 RFC 进 `docs/<topic>-RFC.md`（参考 `BILLING-v3-RFC.md` 模板）
3. RFC 加 "## 状态" 段，PR merge 后改为 ✅
4. 同步更新 `docs/CHANGELOG.md` + `docs/DESIGN.md`

**没有 RFC 的 PR 会被要求补 RFC，不会直接 close**。

---

## 三大数据源铁律 / 3 data-source rule

> **api-ops 严格只能有 3 个数据源**，"第 4 源"必须砍掉或归档到 `archive/`。

| 数据源 | 路径 | 实时性 | 何时用 |
|---|---|---|---|
| upstream API | upstream admin `/api/*` | 实时 | 列表/详情（≤ 1000 行） |
| 直连 DB (RoDB) | upstream RDS `*.logs` | 实时 | 大量聚合 SQL |
| 本地缓存 DB | 自有库 `cache_*` 表 | 准实时 | dashboard 实时面板 |

新加端点 / handler / 字段前，**先看 [docs/DATA-SOURCES.md](./docs/DATA-SOURCES.md)** 选源。

违反铁律的 PR（影子表 / mock / 临时 import）会被直接 close + 标 `stale`。

---

## 风格 / Style

- **Go**：跟 `internal/` 看，GORM + Gin + zap；导入顺序 stdlib / 3rd / local
- **TypeScript / React**：Antd 5 + ECharts 5；函数组件 + hooks；**JSX `{xxx}` 永远求值**（描述文字用 `<task_id>` 或 `$(task_id)`）
- **SQL**：gorm tag + raw query 都行，但 EXPLAIN 必走
- **注释**：中文 / 英文都行，但**接口 / 字段注释必加**（gorm + json tag）
- **error**：用 `fmt.Errorf("xxx: %w", err)` 包装，不要丢弃

---

## 沟通渠道 / Community

- **GitHub Issues** — bug、feature、RFC 讨论
- **GitHub Discussions** — Q&A、最佳实践、show & tell
- **Discord** — 实时聊天（链接待补）
- **邮件列表** — 重要公告（订阅方式待补）

---

## 致谢 / Acknowledgments

- 灵感来自 [upstream 项目](https://github.com/songquanpeng/one-api) 和 LLM API 代理生态
- 核心贡献者见 [AUTHORS.md](./AUTHORS.md)（待补）

Happy hacking! 🎉
