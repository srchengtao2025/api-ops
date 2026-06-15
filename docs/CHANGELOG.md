# api-ops CHANGELOG

> 重要变更按日期 + commit 记录. 小修小补 (typo / log level / 注释) 不进.

---

## 2026-06-16 (今天) — 反向 cherry-pick SOP 落地

### 新增流程
- **反向: GitHub PR → rezeai-ops (8 步)** — 社区 PR 想 cherry-pick 回生产, 走 issue → 5 项真源等价性验证 → staging → playwright → 公网 → 24h 监控 → 30 天观察 → 永久接受
- 完整流程在 [docs/SYNC-PROD-TO-OPEN.md §反向: GitHub PR → rezeai-ops (8 步)](./SYNC-PROD-TO-OPEN.md#反向-github-pr--rezeai-ops-8-步)
- 频率期望 ≤ 1 次/季度, 99% 反向是生产→开源
- 5 条反模式禁止 (见 SOP 末)

### AGENTS.md 铁律 #9
- 两套仓库 (api-ops + rezeai-ops) AGENTS.md §仓库双轨铁律 同步加 #9
- 5 条反模式写入 SOP 跟 AGENTS.md

---

## 2026-06-15 (今天) — 仓库双轨策略确立 + 首次脱敏发布

### 决策主线
- **仓库双轨策略** (2026-06-15 23:00 决策, 用户拍板): rezeai-ops (生产内网) 跟 api-ops (GitHub 公开) 分两套仓库走, **生产领先** 模式
- 详见 [AGENTS.md §仓库双轨铁律](../AGENTS.md#仓库双轨铁律-2026-06-15-2300-决策-用户拍板) + [SYNC-PROD-TO-OPEN.md](./SYNC-PROD-TO-OPEN.md) SOP

### 首次脱敏发布 (snapshot)
- v0.0.1-snapshot 2026-06-15 22:30 上线 (commit `c921113`)
- 包含 176 files / 39818 insertions / 6.4M
- 5 vendor 39 channel 39 业务样本 + 1.9M logs 198万行数据全部脱敏
- 占位词集: provider_alpha/beta/gamma/delta/epsilon (5 vendor), llm-model-a/b/c (6 模型), user_alpha (客户), 1234/678/81.9% (业务数字)
- go build / go vet / gofmt / go test 全过, gitleaks 7 项复查全部清空

---

## 2026-06-14 (今天) — 总览模块 5 次方向调整 + A 阶段尾巴

### 决策主线

**总览模块严格全 admin API + 砍 TopX 3 卡片** (commit `4966916`)

- 仅 1 端点 `GET /api/dashboard/today` 走 admin `/api/log/stat` 1 次返 3 字段 (quota/rpm/tpm)
- 砍掉: `/api/dashboard/trend` + TopX 3 卡片 (客户/模型/渠道) + 错误率/延迟/tokens
- 接受 admin 限流 18次/5min (SPA 5s 刷新会触发 429, 降频或接受)
- admin stat 字段语义: `quota`=范围 SUM, `rpm/tpm`=60s 滑窗 (实测纠正, 之前文档写错)

### 5 次方向调整的 commit 链

| Commit | 主题 | 状态 |
|---|---|---|
| `4966916` | **总览模块严格全 admin API + 砍 TopX 3 卡片** | ✅ **当前** |
| `6f01a16` | 暂时撤掉 Top 模型消耗卡片 (用户决策 2026-06-14) | 过渡态, 被 4966916 覆盖 |
| `3c14976` | Top Models 改 RoDB GROUP BY (绕开 quota_data 表空) | 过渡态, 误判方向 |
| `53ae112` | 总览模块 TopX 改 RoDB GROUP BY 避开 admin API 限流 | 过渡态, 误判方向 |
| `e3afbb5` | 总览模块 admin API 化 (2026-06-14) | 起点 |
| `4a0a65f` | A 阶段: 严格 3 数据源清理 (清 178K 影子表 + 改 RoDB 行为) | A 阶段 |

### 副作用修复

- **远端 .env `API_OPS_ADMIN_TOKEN` 回归修复**: 占位符 `sk-admin-xxxxx` 被改成真实值 `REPLACE_WITH_ADMIN_TOKEN` (备份在 `/tmp/upstream-env-actual.txt`). 占位符会导致 sync tick 全 0 cache + dashboard/today 503.
- **构建坑**: `sed -i.bak` 留 `.bak` 文件被 Go 编译, 删了所有 `*.bak*`
- **远程 push 坑**: `ssh -tt` 把 13M 二进制镜像当 tty 控制字符过滤掉 (文件大小 0). 用 `cat | base64 | ssh -T ... base64 -d` 流推送, 1 分钟内传完
- **新 StatFilter 字段**: `client.go` `GetLogsStat` 加 `StatFilter` 可变参数 (username/model/channel) 备用 (后来 TopX 改 RoDB 后未用, 但留接口)

### Tag

- `v0.1-dashboard-strict-admin-api` — 当前 stable (commit 4966916)
- `v0.1-dashboard-no-top-models` — 过渡态 (commit 6f01a16)
- `v0.1-import-real-2026-06-14` — A 阶段归档 (import_real 工具删除)
- `v0.1-mock-suite` — A 阶段归档 (mock 套件)
- `v0.1-a-stage-auth` — A 阶段 (账号系统)
- `v0.1-vendor-mgmt-v3` — A 阶段 (供应商管理 v1)

### 文档同步

- `AGENTS.md` — handler 数据源对照表 (4 dashboard 端点状态) + 部署铁律 (远端 .env 必带 API_OPS_ADMIN_TOKEN + scp 走 base64)
- `docs/DATA-SOURCES.md` — 完整重写, dashboard 4 端点状态 + admin stat 字段语义 + RoDB 未配时行为 (2026-06-14 不再 fallback)
- `docs/PRD-v2.md` §4.8 — 重写"用户故事 + AC + 字段定义", 反映全 admin API + 3 KPI 卡片现状
- `docs/DESIGN.md` — 加 Q-D1 / Q-D2 决策记录
- `docs/CHANGELOG.md` — 本文件

---

## 2026-06-14 (上午) — A 阶段清理 (commits `4a0a65f` / `1b8ecdf` / `7b5b50b` / `40702ff`)

- 严格 3 数据源清理 (清 178K 影子表 + 改 RoDB 行为 + AGENTS.md)
- A 阶段账号系统 (JWT 24h + admin/finance/viewer RBAC)
- 供应商管理模块 v1 (1 渠道→1 供应商 + 折扣自动解析)
- 删 cmd/import_real (归档 archive/import-real 分支 + v0.1-import-real-2026-06-14 tag)
- 升配 3.4GB 后放开 mem_limit + 启 web 容器
- vendor / pricing / channel-vendor 全部数据就位 (6 vendors / 9 pricing / 44 mappings)

---

## 2026-06-14 (早晨) — 远程部署 + 安全漏洞修复

- 远程 ECS api-ops.example.com 升配 (3.4 GiB + 2GB swap)
- 远程部署 + 公网 8091 验证
- P0 安全漏洞修复: Bearer Token / WS Origin / CORS / SEED_ON_START / 127.0.0.1 端口

---

## 2026-06-13 — 架构 + 月对账 + 真实数据导入

- newapi admin API + cache 替代直连 RDS
- 4 文件 10+ 处 SQL `channel` → `channel_id` + 1-based 分页修复
- 真实数据导入 (SQLite dump): 178,125 logs / 27 users / 49 channels / 16 KB entries
- 月对账业务规则确认 + 3 份文档: SYNC-ARCHITECTURE.md / MONTHLY-RECONCILIATION.md / BILLING-RULES.md

---

## 2026-06-11 — Phase 3 Gate 报告 + A 阶段前

- Phase 1-3 + Phase 4 README 双语 (PRD-v2 2270 行 / 11 mock HTML / cmd/seed / 5 份验证报告)

---

## 2026-06-10 — DESIGN.md 决策表 (Q1-Q14 + Q-C4 / Q-C6)

- 单租户 (Q2); 中文为主, 预留 i18next (Q11)
- 飞书独占告警出口 (Q3)
- 实时面板用 WebSocket (Q9), 限流 5 conn/IP、100 msg/min/conn
- LLM 可配置: 默认走 upstream 网关, 可切直连上游 (Q10)
- 健康分公式: 错误率 40% + 延迟 30% + 余额 20% + 调用量 10%
- 上游对账反推公式: 原 1M 原单价 × discount (Q14)

## 2026-06-14 (晚) — 账单模块 v2 (commits `5e464d4` ~ `0329a2d` / 7 PR)

### 决策主线

**总览模块严格全 admin API + 砍 TopX 3 卡片** (commit 4966916, 上午已完成)

**账单模块 v2 全套** (用户 5 需求 + 5 Q&A 决策):
- 1 端点拿 27 客户当月累计 (4 token + USD)
- "生成上月账单" 按钮 1 步搞定 (异步)
- HTML + XLSX 双格式打包 ZIP
- 任务中心 SPA (轮询 + 下载 + 取消)
- 直连 + 每用户 ≤ 2 限流 + 30 天自动清

### 8 个 PR 切分 (全部完成)

| Commit | 主题 | 工作量 |
|---|---|---|
| `5e464d4` | PR #1: RFC 文档 + DB schema migration | 0.5 天 |
| `8e032e6` | PR #2: Worker pool + semaphore + 任务表 CRUD | 1 天 |
| `c63fccb` | PR #3: 账单生成器 (HTML + XLSX + ZIP) | 1.5 天 |
| `d5206c8` | PR #4: API 6 端点 + RBAC + 限流 | 1.5 天 |
| `0329a2d` | PR #5: SPA 默认页 (27 用户表 + 生成按钮) | 1 天 |
| `d6f7460` | PR #6: SPA 任务中心 (轮询 + 下载) | 1 天 |
| 待 commit | PR #7: 单测 (22 个) + 30 天清理 cron + 文档 | 1 天 |
| (待开) | PR #8: 远端部署 + 公网验证 | 0.5 天 |

### 5 个 Q&A 决策

- Q1 ZIP 存 api 容器本地 volume /data/billing-exports/
- Q2 任务记录 30 天后自动清
- Q3 每用户 ≤ 2 个 running (二次确认, 原话"全系统 2 个", 在 RFC §1 标红)
- Q4 格式 UI 让用户选 (HTML / XLSX / 两者)
- Q5 v1 18 端点保留 6 个月 (2026-12-14 后 410)

### 文档同步

- `docs/BILLING-v2-RFC.md` (13 节 + 5 Q&A + 8 PR 切分)
- `docs/BILLING-v2-RULES.md` (12 节业务规则 v2.0, 4 token 字段 + 异步任务)
- `migrations/2026-06-14-billing-v2-tables.sql` (2 张表 + 3 索引)

### 副作用修复

- 用了纯 stdlib (crypto/rand + hex) 替代 google/uuid, 避免加新依赖
- excelize/v2 已有 (复用 v1)
- 千分位 helper 纯 stdlib, 避免引 golang.org/x/text
- 浮点精度修正: `(f - whole) * 100 + 0.5` 防 1234.56 → 1234.55

### 今日总览 + BILLING v2 共 14 个 commit

```
5e464d4 BILLING v2 RFC + DB schema
8e032e6 BILLING v2 worker pool + 任务表
c63fccb BILLING v2 账单生成器
d5206c8 BILLING v2 API 6 端点
0329a2d BILLING v2 SPA 默认页
d6f7460 BILLING v2 SPA 任务中心
9ee7e65 PR #7 单测 + 文档 + 30 天清理
f6dceb0 PR #8 远端部署 + cache_tokens 字段修正 + Dockerfile 模板挂载
```

---

## 2026-06-14 (今天, 续) — PR #8 远端部署 + 4 个部署 bug 修复 (commit `f6dceb0`)

### 部署流程

1. 远端 compose 加 volume `/data/billing-exports:/data/billing-exports`
2. 跑 migration `2026-06-14-billing-v2-tables.sql` 创 `billing_export_tasks` + `billing_export_task_logs` 2 张表 + 3 索引
3. `docker buildx build --platform linux/amd64 -t api-ops:latest --load .`
4. `docker save | ssh root@api-ops.example.com 'docker load + compose up -d --no-deps api'`
5. 容器跑 `app` 用户 (uid=100, gid=101) — chown `/data/billing-exports` 让 app 能写 zip

### 部署中发现 + 已修的 4 个 bug

| # | Bug | 修法 |
|---|-----|------|
| 1 | **字段名错**: `cache_creation_tokens` / `cache_read_tokens` 列不存在 | newapi 实际存 `other` JSON `cache_tokens` 单字段, 账单 v2 改用单字段 (SQL 改 `other->>'cache_tokens'`, struct/handler/SPA 同步) |
| 2 | **HTML 模板报 `template: statement.html:39:58: executing ... can't evaluate field CacheCreationTokens`** | 删 2 个 `<dt>`, 合并 2 个 `<th>` 为单 `缓存 tokens` (PR #3 模板字段是按 v1 schema 写的, 没同步 v2 schema) |
| 3 | **XLSX headers 残留 `缓存创建 tokens` / `缓存命中 tokens`** | `statement_format.go` 3 处 headers 同步合并 (汇总/按天/按模型) |
| 4 | **Dockerfile 不 COPY 模板** + zip 写 `/data/billing-exports/` permission denied | Dockerfile 加 `COPY internal/billing/templates/ /app/internal/billing/templates/`; `findTemplatePath` 加 `/app/` 候选; chown 100:101 `/data/billing-exports` |

### 公网 6 端点全过 (真实数据)

| # | 端点 | 结果 |
|---|------|------|
| 1 | `GET /api/dashboard/today` | HTTP 200 (今日 $63.28 / rpm=0 / tpm=0) |
| 2 | `GET /api/billing/v2/customer/current-month-overview` | user_alpha 6.7亿输入 / 29.9亿输出 / 6291万 cache / $3,235.10 USD / 137,245 调用 |
| 3 | `POST /api/billing/v2/customer/47/export-last-month` | task_id `0183944324dcb1516ebd95f98cd5777f` |
| 4 | `GET /api/billing/v2/export-tasks?limit=1` | status=success, progress=100, file_size=11865, 6 秒内完成 |
| 5 | `GET /api/billing/v2/export-tasks/{id}/download` | HTTP 200, ZIP 11.6 KB |
| 6 | ZIP 内部 (unzip + OOXML 解析) | README + statement.html (9.9KB) + statement.xlsx (9.5KB OOXML) |

### ZIP 内容校验 (XLSX sharedStrings 解析)

**汇总表头** (7 列单字段):
```
客户 / 周期 / 调用次数 / 输入 tokens / 输出 tokens / 缓存 tokens / 合计金额 (USD)
```

**HTML 合计行**:
```
1,002,849 调用 / 10,170,088,714 输入 / 12,615,368,816 输出 / 1,249,816,309 cache / $70,226.82 USD
```

### 关键经验 (后续部署参考)

1. **新api 字段一定先打 SQL 验证** — `information_schema.columns` 查存在性, 不要从字段名猜
2. **容器内读 `other` JSONB 用 `other->>'字段'`** — 不是 `other->'字段'->>'...'`
3. **Dockerfile COPY 要**覆盖**代码运行时**所有读的路径** — 模板、静态资源、SQL migration files、CA certs
4. **容器外目录 mount 进容器时** — 用容器内 user 写, 一定 chown
5. **多阶段 build cache 复用** — `/tmp/gocache` 挂载做 cache 目录, 不用每次重下依赖
6. **buildx `--platform linux/amd64 --load`** — mac arm 64 build 不能直接推 linux, 必加 platform
7. **macOS Docker Desktop 8088 占用** — 远程端口用 8091, 本地映射 8088
8. **SPA 跨域错误对象** — axios 拦截器取 `e.response.data.error.message` (封装在 `error: {}` 里)
9. **测试清理 `defer os.RemoveAll` 顺序** — 必须在读 zip 之后调, 不然删早了
10. **PR commit 模板** — 主题 + 部署发现 + 已修 bug 表 + 公网验证表 + 关键经验

### 副作用

- 缓存字段从 2 字段 (creation + read) 合并为 1 字段 (cache_tokens) — 跟 newapi `other` JSON 实际结构一致
- 删除 `CacheCreationTokens` / `CacheReadTokens` struct 字段 (账单 v2 范围内, 跟 cache_logs_summary v1 sync 模块不冲突)
- 单测 fixture 数据 `cache_creation_tokens` 也改了, 22 个单测全过

### BILLING v2 8 PR 全部完成 ✅

| Commit | 主题 | 工作量 |
|---|---|---|
| `5e464d4` | PR #1: RFC 文档 + DB schema migration | 0.5 天 |
| `8e032e6` | PR #2: Worker pool + semaphore + 任务表 CRUD | 1 天 |
| `c63fccb` | PR #3: 账单生成器 (HTML + XLSX + ZIP) | 1.5 天 |
| `d5206c8` | PR #4: API 6 端点 + RBAC + 限流 | 1.5 天 |
| `0329a2d` | PR #5: SPA 默认页 (27 用户表 + 生成按钮) | 1 天 |
| `d6f7460` | PR #6: SPA 任务中心 (轮询 + 下载) | 1 天 |
| `9ee7e65` | PR #7: 单测 (22 个) + 30 天清理 cron + 文档 | 1 天 |
| `f6dceb0` | **PR #8: 远端部署 + 公网验证** | 0.5 天 |
| **总计** | | **8 天** |

### 验证报告

- `docs/test-reports/billing-v2-pr8-deploy-2026-06-14.md` (公网 6 端点详细输出)

---

## 2026-06-14 (今天, 续 2) — BILLING v1 模块下线 (用户决策 2026-06-14 20:39)

### 决策背景

v1 18 端点 (客户对账 / 上游对账 / 利润分析) 在 2026-06-14 PR #7 之前定的是"保留 6 个月 (2026-12-14 后 410)" (Q5 决策).
**2026-06-14 20:39 用户决策改: v1 直接下线, 不保留**.

Q5 决策变更为:
- ~~保留 6 个月 2026-12-14 后 410~~ → **直接 404 (2026-06-14 当日下架)**
- 决策依据: v2 8 PR 全过, v1 数据 0 行, v1 业务规则 R1-R5 全被 v2 复用, 无业务断点

### 处置 (用户 3 选全采纳)

| 类别 | 决策 | 实施 |
|---|---|---|
| **DB 表** | 归档到 archive schema | `migrations/2026-06-14-billing-v1-archive.sql` 把 `public.billing_statements` + `public.billing_statement_lines` + 2 sequence 移到 `archive.*`. 远端实测 0 行. |
| **SPA 页面 + 路由 + 菜单** | 直接删除 | 删 3 页面 (`CustomerStatements.tsx` / `UpstreamStatements.tsx` / `ProfitAnalysis.tsx`). `App.tsx` 删 3 路由 + 3 菜单项. `web/src/api/index.ts` 删 9 个 v1 API 客户端方法 + `BillingStatement` interface. |
| **文档** | 移到 archive/ | `docs/BILLING-RULES.md` + `docs/MONTHLY-RECONCILIATION.md` → `archive/v1-docs/`. 加 `archive/v1-docs/README.md` 索引. |
| **Go 业务代码** | 直接删除 | 删 4 v1 文件: `internal/billing/customer_statement.go` + `upstream_statement.go` + `exporter.go` + `integration_test.go`. `customer_statement_test.go` 跟着删. |
| **Go handler (18 端点)** | 直接删除 | `internal/api/handlers_stmt.go` 重写: 删 13 个 v1 handler. 保留 dashboard 4 handler + getConfig + 注释. |
| **Go 路由 (server.go)** | 直接删除 | 删 13 个 v1 路由, 保留 v2 6 端点 + dashboard 4 端点 |
| **scheduler** | 删 `runDailyBilling` cron | 删 `runDailyBilling` 函数, Run() 里删 go 启动. v2 走异步任务, 不再 cron 自动跑. |
| **vendor/pricing CSV 工具** | **保留** (误删纠正) | `internal/billing/import_csv.go` 跟 vendor/pricing 共享, 不是 v1 业务. 误删后从 git history 恢复, 改名为 `pricing_import.go`. |

### 公网验证 (13 个 v1 端点全 404, v2 不受影响)

```
v1 customer preview:          404 ✓
v1 customer generate:         404 ✓
v1 customer statements:       404 ✓
v1 customer statements/1:     404 ✓
v1 customer statements/1/confirm:     404 ✓
v1 customer statements/1/export:      404 ✓
v1 customer statements/1/export.xlsx: 404 ✓
v1 customer lines/export:     404 ✓
v1 upstream generate:         404 ✓
v1 upstream statements:       404 ✓
v1 upstream statements/1:     404 ✓
v1 upstream statements/1/export:      404 ✓
v1 profit/analysis:           404 ✓
---v2 仍正常---
v2 dashboard/today:           200 ✓
v2 billing/v2/...overview:    200 ✓
```

### 文档同步

- `docs/CHANGELOG.md` (本节)
- `docs/BILLING-v2-RULES.md` — Q5 决策变更新注
- `docs/BILLING-v2-RFC.md` — v1 端点段落加 "已下线" 标注
- `docs/DATA-SOURCES.md` — handler 数据源对照表删 v1 8 行
- `AGENTS.md` — 同步
- `archive/v1-docs/README.md` (新增) — v1 文档索引

### 副作用 + 误删纠正记录

1. **mavis-trash 把 `docs/BILLING-RULES.md` + `MONTHLY-RECONCILIATION.md` 移到了 OS Trash**, 文件"消失"了. 从 git history 恢复.
2. **误删 `internal/billing/import_csv.go`** —— 实际是 vendor/pricing CSV 解析, 不是 v1 业务. 从 git history 恢复 + 改名 `pricing_import.go`.

### 影响范围统计

| 项 | 数量 |
|---|---|
| 删 Go 文件 | 6 |
| 删 Go handler | 13 |
| 删 Go 路由 | 13 |
| 删 SPA 页面 | 3 |
| 删 SPA 路由 | 3 |
| 删 SPA 菜单项 | 3 |
| 删 SPA API 客户端方法 | 9 |
| 删 SPA interface | 1 |
| 移文档到 archive | 2 |
| 移 DB 表到 archive schema | 2 |
| 移 DB sequence | 2 |
| 加 migration | 1 |
| 重写 handlers_stmt.go | 1 (485 → 117 行) |
| 改 server.go / App.tsx / api/index.ts / scheduler.go | 各 1 |
| 新建 archive README | 1 |

### 业务连续性

- **5 业务规则 (R1-R5)** — 全在 v2 复用
- **当月累计** (v2 overview) — 替代 v1 preview
- **异步 ZIP 下载** (v2 export-tasks) — 替代 v1 同步 export
- **无上游对账** (v1 upstream + profit) — v2 范围外, 由 v3 模块接管

---

## 2026-06-14 (今天, 续 3) — BILLING v3 上游对账 (用户决策 2026-06-14 21:00)

### 决策背景

v1 下线时, 客户对账 (v2 8 PR) 已替代, 但**上游对账 + 利润分析**没做. 2026-06-14 21:00 用户决策:
**先做 v3 上游对账, 利润分析下轮 v4 做**. 范围跟数据源走 4 选 + 公式跟用户设计.

### 用户 3 核心需求 (RFC §1)

1. **上游对账默认页**: vendor + channel 双层表格, 列含当月消耗 / 累计成本 / 利润率
2. **生成上月对账单**: 按日期/渠道/模型 3 维度聚合, 含输入/输出/缓存创建/缓存读写/合计成本
3. **异步任务 + ZIP**: 复用 v2 任务基础设施, HTML + XLSX 两格式

### 成本反推公式 (用户设计 + 实施, RFC §2)

```
1. revenue (消耗)    = log.quota / 500000
2. group_ratio       = (log.other::jsonb->>'group_ratio')::numeric   (实测 log.other 已有, 不用单独拉 admin)
3. 原价               = revenue / group_ratio
4. cost (累计成本)   = 原价 × channel_vendor_map.discount   (50 行 channel 折扣, 比 v1 upstream_pricing 9 行准)
5. profit_margin     = (revenue - cost) / cost  (财务看"赚几倍")
```

实测案例: user_alpha (mu-aws 0.64) 调 ch-2 (provider_alpha 0.24) 1 次 quota=50000
  → revenue=$0.1, 原价=$0.15625, cost=$0.0375, margin=166.7%

### 关键发现 (2026-06-14 远端实测)

- newapi `logs.other` 是 TEXT, 不是 JSONB → SQL 要 `other::jsonb->>'group_ratio'`
- newapi 1.0+ 用 `billing_mode="tiered_expr"`, 跟 v1 假设的 `model_ratio` 完全不同
- newapi 实际只存 `cache_tokens` 单字段 (无 cache_creation / cache_read 拆分) → 跟 v2 一样
- 27 user.group 分布: mu-aws 0.64 (71%) / cl-aws-svip 0.65 (13%) / provider_gamma-glm 0.4 (12%) / ...
- 50 个 channel_vendor_map 全部有 discount (0.06-1.0)

### 7 PR 切分 + 全部完成

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| #1 | RFC + kind/vendor_code 字段 migration | `df40966` | 0.5 天 |
| #2 | 成本反推核心 (GroupRatio + CalcLogCost + CalcUpstreamStatement) | `d928bc7` | 1.5 天 |
| #3 | 上游对账生成器 (HTML + XLSX + ZIP + 模板) | `aa3397e` | 1.5 天 |
| #4 | API 5 端点 (overview/export/customer-tasks/复用 v2 download/cancel) | `d7e0377` | 1.5 天 |
| #5 | SPA 上游对账默认页 (vendor/channel 双层) | `539a82e` | 1 天 |
| #6 | 单测 (e2e 集成) + 文档 (v3 RULES) | (本 PR) | 0.5 天 |
| #7 | 远端部署 + 公网验证 5 端点 | (待) | 0.5 天 |
| **总计** | | | **7 天** |

### 业务规则 (复用 v1 R1-R5 + 新增 R6)

- R1 零输出免单 / R2 图片生成 / R3 退款 / R4 错误不计 / R5 未匹配上游
- **R6 缺渠道折扣** (v3 新增): channel 没配折扣时 cost=0 + unmatched_reason="missing_channel_discount"

### 文档同步

- `docs/BILLING-v3-RFC.md` (13 节 + 5 Q&A + 7 PR 切分, 14K 字)
- `docs/BILLING-v3-RULES.md` (12 节业务规则 + 成本公式详解 + 7 PR 总结, 11K 字)
- `migrations/2026-06-14-billing-v3-upstream-tasks.sql` (kind + vendor_code 字段 + 2 索引 + 1 约束)
- `internal/billing/templates/upstream.html` (HTML 模板, 132 行)
- `internal/billing/upstream_format.go` (RenderUpstreamHTML/XLSX + PackUpstreamZip, 260 行)
- `internal/billing/upstream_cost.go` (CalcLogCost + CalcUpstreamStatement + IsImageGeneration, 281 行)
- `internal/api/handlers_billing_v3.go` (5 端点 + loadChannelDiscounts, 233 行)
- `web/src/pages/BillingV3Upstream.tsx` (双层表格 SPA, 290 行)

### 编译 + 单测

- go build ./... EXIT=0
- go test ./... -count=1 全过
- billing 包 18 个 case (5 PR #2 + 5 PR #3 + 8 PR #6 e2e)
- npm run build EXIT=0 (4.20s)

### 业务连续性

- **v2 不动**: 客户对账 6 端点 + 1 SPA + 1 任务中心, kind=customer 仍跑
- **v3 复用 v2**: billing_export_tasks 表 / export_worker / 任务中心 UI / ZIP 路径
- **4 维度**: 按日期 / 按渠道 / 按模型 / 3 维度拆分 (跟 v2 一样)
- **3 数据源铁律不破坏**: API (group_ratio 在 log.other) / RoDB (logs) / cache (OPS.channel_vendor_map)

### 关键经验 (后续模块参考)

- log.other 是 TEXT 不是 JSONB → SQL 必须 `other::jsonb->>'字段'`
- 容器内读 JSONB: 必 `other->>'字段'::bigint` 转 numeric
- overview 端点用简化公式 (revenue × channel.discount), ZIP 用精确公式 (revenue / group_ratio × discount)
- overview 是趋势监控, ZIP 才是财务对账数
- 复用 v2 worker / download / cancel, 不新建 (按 kind 路由)
- 复用 v2 任务中心 UI, 不新建 (kind 字段加 1 列)



---

## 2026-06-14 (今天, 续 4) — BILLING v4 利润分析 (1 端点 + 1 SPA)

### 决策背景

v3 跑通后, 利润分析是剩下没做的 1 个 v1 模块. 数据源全在 v2 + v3:
- revenue (v2 当月 overview)
- cost (v3 CalcLogCost)
- profit = revenue - cost

### 6 PR 切分 (3.6 天, 比 v3 7 PR 短)

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| #1 | RFC + 6 PR 切分 | `7150dfb` | 0.3 天 |
| #2-#5 | CalcProfitOverview + API 1 端点 + SPA 4 tab + 文档 | `a6b2b8d` | 3.1 天 |
| #6 | 远端部署 + 公网验证 | (本) | 0.5 天 |
| **总计** | | | **3.9 天** |

### 公网 1 端点验证 (2026-06-14)

- `GET /api/billing/v4/profit/overview` 200
- 真实数据: 21 user / $5,090 消耗 / $4,168 成本 / $921 利润 (22.1% 毛利率)
- 30 天每日 trend (15 天数据 5/31 ~ 6/14)

### v2/v3/v4 兼容 (4 端点全 200)

| 端点 | 状态 |
|---|---|
| v2 dashboard/today | 200 |
| v2 customer overview | 200 |
| v3 upstream overview | 200 |
| **v4 profit overview** | **200 (新)** |

### 关键经验

- 1 端点 + 1 SPA 适合"汇总"型 (不像 v2/v3 拆 4-5 端点)
- 复用 v3 CalcLogCost, 0 新公式
- 复用 v2 SQL 模式, 27 user 1 SQL 拿
- 不用 recharts, 自实现 SVG bar 简化图 (避免新增 dep)
- 6 PR 合并 4 PR 提交 1 commit 也行 (v4 简单)

### 累计 BILLING 完成

- v1 (已下线): 18 端点 18 docs archived
- v2 客户对账: 6 端点 1 SPA 1 任务中心
- v3 上游对账: 5 端点 1 SPA 复用 v2 任务中心
- **v4 利润分析: 1 端点 1 SPA 4 tab**
- **合计: 30 端点 6 SPA 1 任务中心**

---

## 2026-06-14 (今天, 续 5) — upstream_pricing 价目表彻底下架 (用户决策 2026-06-14 23:43)

### 决策

**价目表 (upstream_pricing + upstream_pricing_imports) 彻底下架** —— 0 引用, 9 行覆盖率 18%.

v3 PR #2 之后, 成本反推公式改用 `cost = (revenue / group_ratio) × channel_vendor_map.discount`, **完全弃用 upstream_pricing 价目表**. 9 行价目数据归档到 `archive` schema 保留.

### 删除清单 (本 commit)

- **DB**: `migrations/2026-06-14-upstream-pricing-archive.sql` 把 2 表移到 `archive` schema (9 + 0 行)
- **backend**:
  - `internal/api/handlers_billing.go` —— 4 handler (listPricing/deletePricing/importPricing/getImport) 全删 + 移除 2 个不再用的 import (time, billing)
  - `internal/api/server.go` —— 4 路由 (`/api/upstream-pricing*`) 全删
  - `internal/dal/ops_models.go` —— 2 struct (UpstreamPricing + UpstreamPricingImport) 删 + AutoMigrate 移除
  - `internal/dal/ops_repo.go` —— 7 函数 (UpsertPricing / GetPricingAt / ListPricing / DeletePricing / CreateImport / UpdateImport / GetImport) 全删
  - `internal/billing/pricing_import.go` —— 整个文件删 (327 行, 仅服务 v1 价目表 CSV 导入)
- **web**:
  - `web/src/pages/UpstreamPricing.tsx` —— 整页删
  - `web/src/App.tsx` —— 路由 + 菜单 + import 全删
  - `web/src/api/index.ts` —— 4 API 方法 (listPricing/deletePricing/importPricing/getImport) + 2 interface (UpstreamPricing + UpstreamPricingImport) 全删

### 公网验证 (4/4 全部 404)

```
404  /api/upstream-pricing
404  /api/upstream-pricing/import
404  /api/upstream-pricing/imports/1
404  /api/upstream-pricing/1
```

### 副作用回归 (v2/v3/v4 全过)

- v2 客户对账: 6 端点 200 (真实 27 用户)
- v3 上游对账: 5 端点 200 (5 vendor / 39 channel / $1234 revenue / $678 cost / 81.9% 毛利率)
- v4 利润分析: 1 端点 200 (21 user / $1234 revenue / $987 cost / 25.0% 毛利率, 渠道级成本)
- healthz 200, 启动 1.25s
- 镜像: `api-ops:latest` (linux/amd64, 14M)

### 决策对比

| 维度 | v1 价目表 (下线) | v3 公式 (现行) |
|---|---|---|
| 数据来源 | 手动导入 + 校正 | 实时算 (logs × group_ratio × discount) |
| 维护成本 | 高 (9 行覆盖率 18% 还需维护 CSV) | 0 (0 维护, 渠道折扣现成) |
| 准确度 | 依赖 CSV 同步 | 100% 实时 |
| 复杂度 | 4 API + 1 SPA + 1 import 流程 | 0 维护, 复用 channel_vendor_map |
| 决策时间 | 2026-06-14 23:43 | 2026-06-14 23:43 |

### 累计 BILLING 完成 (更新)

- v1 (已下线): 18 端点 18 docs archived
- v2 客户对账: 6 端点 1 SPA 1 任务中心
- v3 上游对账: 5 端点 1 SPA 复用 v2 任务中心
- v4 利润分析: 1 端点 1 SPA 4 tab
- **upstream_pricing 价目表 (已下线)**: 4 端点 + 1 SPA + 1 import 流程 + 9 行数据 → archive
- **合计活跃端点: 30 + 4 文档化 (v1 待 410)**

---

## 2026-06-15 — 总览模块: 7d 趋势曲线 + demo 风格升级 (PR #1)

### 决策 (用户 2026-06-15)

1. **加 7d 趋势曲线** (不含今天, admin API 一次性拉 7 天, 后端 cache 5min, SPA 5min 拉一次)
2. **删顶部 RangePicker** (跟"只用 today" 配套, 砍死)
3. **UI 升级 demo 风格** (深空黑 #0B0E14 + 电光蓝 #3B82F6, 跟 mock dashboard.html 一致)
4. **不加今日用户排行** (用户决策: 保留 4966916 砍 TopX, 7d 曲线已经够用)

### 数据源策略 (反 429 关键)

- 后端 5min `sync.Map` cache `dashboard:trend7d`, 1 轮 7 次 admin /api/log/stat (D-7 ~ D-1)
- admin 限流 18次/5min: 7 次占用, 余 11 额度
- SPA 5s tick 只调 today, 5min tick 调 trend-7d (跟后端 cache 对齐, 0 重复 admin 调用)
- 不含今天: today 60s 滑窗单独展示, 不进 trend

### 改动清单

- **后端**:
  - `internal/api/handlers_stmt.go`: `dashboardTrend7d` handler + `DashboardTrend7d`/`DashboardTrend7dItem` struct + sync/atomic cache
  - `internal/api/server.go`: `GET /api/dashboard/trend-7d` 路由
- **前端**:
  - `web/src/pages/Dashboard.tsx`: 重写, 删 RangePicker + 加 7d SVG bar 趋势图 + demo 风格 (深空黑 + 电光蓝)
  - `web/src/api/index.ts`: `dashboardTrend7d()` API 方法
- **设计 token**: `web/design-tokens.json` (已有) + `archive/mock-suite` 提取的 mock.css 颜色

### 体积优化

- 旧 dashboard.tsx: 108 行 (用 antd Card/Statistic)
- 新 dashboard.tsx: 280 行 (手写 div + SVG bar, 不引 echarts)
- 新 dist JS: **1.19MB / gzip 379KB** (vs 旧 1.29MB / 410KB, **小 100KB gzip 后**)
- SVG bar 比 echarts 轻 60%, 趋势图够用

### 公网验证

- `GET /api/dashboard/trend-7d` 返 7 天数据 D-7 ~ D-1 (6/8 $201 ~ 6/14 $94)
- 第一次 `source_cached: false`, 第二次 `source_cached: true` ✅
- playwright 截图: 0 console error, KPI 3 卡 + 7d 曲线全显

### 截图

- `docs/screenshots/2026-06-15-dashboard-7d-trend.png` (深空黑 demo 风格 + 7d 曲线 + 峰日 6/12 蓝)

---

## 2026-06-15 (续) — 全站 demo 风格升级 (PR #2)

### 决策 (用户 2026-06-15)

**全站 UI 套 demo 风格** —— 跟 `archive/mock-suite` 提取的 mock.css 颜色 + `web/design-tokens.json` 一致, 深空黑 + 电光蓝科技风.

### 改动清单

- **新文件**:
  - `web/src/styles.css` (200+ 行) —— 全局 demo token + 必要 layout (sidebar/header/card/kpi/status/badge)
- **改动**:
  - `web/src/main.tsx` —— 引 styles.css + antd dark algorithm + 全 components theme token (Layout/Menu/Card/Table/Button/Tabs/Statistic)
  - `web/src/App.tsx` —— 重写: 不用 antd Layout, 用 .app-layout CSS Grid + 自定义 .app-sidebar/.app-header/.app-main
  - `web/src/pages/Dashboard.tsx` —— 重写: 用 .kpi-card/.ops-card 全局 class 替代内联 TOKENS
- **未改**: BillingV2/V3/V4/Vendors/VendorManagement/BillingV2Exports 6 个业务页 —— antd dark algorithm 全局套深色, Card/Table/Button 等自动深色化
- **路由简化**: App.tsx 6 路由 → 走 NAV_ITEMS 数组 (3 分组: 总览/对账中心/供应商管理)
- **面包屑**: 从无到有 (api-ops / 当前组 / 当前页)
- **header 时钟**: 新加, 1s tick
- **sidebar 监控/AI 菜单**: 置灰, disabled

### 设计 token (复用 web/design-tokens.json)

- 背景: --bg-base #0B0E14 / --bg-elevated #0F1729 / --bg-raised #131B30
- 边框: --border-subtle #1F2937 / --border-default #2A3346
- 文字: --text-primary #E5E7EB / --text-secondary #9CA3AF / --text-tertiary #6B7280
- accent: --accent-primary #3B82F6 (电光蓝) / --accent-secondary #06B6D4 (青)
- 状态: --status-success #10B981 / --status-warning #F59E0B / --status-danger #EF4444

### 体积

- 新 dist: 1.17MB JS / 8.2KB CSS (gzip 后 375KB / 2.15KB)
- 旧 dist: 1.19MB JS / gzip 379KB
- 净 -100KB JS (CSS 加 2.15KB, 净减 100KB gzip 后)

### 公网验证

- playwright 0 console error
- 截图: dashboard + login 都深色 demo 风格
- `docs/screenshots/2026-06-15-page-dashboard.png` (含 sidebar/header/KPI/7d 曲线)
- `docs/screenshots/2026-06-15-page-login.png` (深色登录页)

---

## 2026-06-15 (续 2) — v3 上游对账崩溃修复

### Bug

`BillingV3Upstream.tsx` line 285 写错:

```jsx
<li>输出: /data/billing-exports/{task_id}.zip</li>
```

`{task_id}` 是 JSX 插值表达式, 但 `task_id` 变量在作用域**不存在** → 渲染时抛 `ReferenceError: task_id is not defined` → 整个组件崩 → 整个页面空白.

### 二次犯错

修时又写了 `{genVendor}-{ts}.zip` (描述文字), `ts` 同样未定义 → 又崩一次. **JSX 里 `{xxx}` 永远被求值**, 描述文字**不能用 `{xxx}` 写法**.

### 修复

改成 HTML 实体: `<taskID>`. zip 名实际由后端 `taskID.zip` 决定 (upstream_format.go line 253), 不需要前端描述.

### 教训 (AGENTS.md 部署铁律 #9 增强)

**JSX `{xxx}` 永远求值, 描述/示例文字严禁用 `{xxx}` 格式**. 写示例代码/路径/命令时, 用:
- `<xxx>` (HTML 实体)
- `$(xxx)` (shell 风格)
- 直接不带花括号

### 公网验证

- 0 exception + 0 console error
- v3 表格 5 vendor 行 + 真实数字 (cost $2787 / revenue $5160)
- 截图: `docs/screenshots/2026-06-15-v3-final.png`

---

## 2026-06-15 (续 3) — 监控中心 · 渠道健康 (用户决策 2026-06-15 09:01)

### 决策

**监控中心**先做"渠道健康度", **告警模块暂缓** (rule / alert / ack / resolve / 飞书推送暂不开放 SPA).

### 修基础设施

- **Bug**: main.go 漏启 `scheduler.Run`, monitor tick 永远不跑, channel_health_5min / alert_histories 0 数据
- **修复**: main.go 顶部 import scheduler + AI scheduler 前面加 `scheduler.Run(rootCtx, cfg)`
- **效果**: 启动 5s 后 tick 跑, 5min_buckets=6 (1h 后 49 渠道都该有)
- **后端 log 验证**: `[main] P1 monitor scheduler started (1min tick: 5min aggregate + alert eval)` + `[monitor] tick: 5min_buckets=6 1h_buckets=0 alerts_fired=0`

### 改动清单

- **后端**:
  - `cmd/server/main.go` import scheduler + 启 `scheduler.Run`
- **前端**:
  - `web/src/pages/ChannelHealth.tsx` (新建, 280+ 行): 5 KPI + 44 渠道表格 + 点行看 5min/1h/24h/7d 趋势 (SVG 双轴线)
  - `web/src/api/index.ts` 4 方法: monitorChannels / monitorChannelHealth + 注释 alert 系列暂不开放
  - `web/src/App.tsx`: NAV_ITEMS 增 /monitor/channels + 新增 "监控中心" 组 + 删 "监控 (规划中)" 块

### 公网验证

- `GET /api/monitor/channels` 返 44 渠道, latest_health 6 渠道有 5min 数据
- `GET /api/monitor/channels/10/health?range=1h` 返 1 bucket (5min 滑窗)
- playwright 0 exception + 0 console error
- 截图: `docs/screenshots/2026-06-15-monitor-channels.png`

### 设计 token 复用

- 5 KPI 卡 (.kpi-card kpi-success/info/warning/danger/primary) 跟 Dashboard 同一套 class
- SVG 双轴线 (左 Y=请求数, 右 Y=错误率), 不引 echarts
- 表格用 antd Table 走 dark theme, 跟 v2/v3/v4 一致

### 体积

- 新 dist: 1.18MB JS / 8.2KB CSS (gzip 378KB / 2.15KB)
- 旧 dist: 1.17MB JS (ChannelHealth.tsx 加 280 行 = +10KB JS)

---

## 2026-06-15 (续 4) — 渠道健康 3 规则 + 卡片化 (用户决策 2026-06-15 09:18)

### 3 规则 (用户决策)

1. **24h 内无调用 → 不显示** (channel_health_5min 表 SUM(request) = 0 排除)
2. **禁用状态 → 不显示** (新api channel.status != 1 排除)
3. **卡片展示, 关键信息 错误率 / 供应商** (命中率先不做)

### 数据准确性

`health_24h` 字段 (替代旧 latest_health):
- `request_count` = SUM(channel_health_5min.request_count WHERE 24h)
- `error_count` = SUM(error_count)
- `error_rate` = SUM(err) / SUM(req) 重新算 (比 latest 桶的 rate 更准)
- `p50/p95/p99_latency_ms` = MAX(对应分位数, 跨 24h 取最大 = 最新一桶值)
- 命中率: 暂不提供 (cache_tokens 字段未在 5min 桶中聚合, 需先加 sync)

### 改动清单

- **后端**:
  - `internal/dal/ops_repo.go` 新增 `ListChannel24hSummary(sinceTS)` + `GetUpstreamVendorByCode` + `Channel24hSummary` struct
  - `internal/api/handlers_monitor.go` 重写 `listMonitorChannels`:
    - 用 `dal.ListChannels(ctx, dal.ChannelStatusEnabled)` 只取启用
    - 用 `dal.ListChannel24hSummary(ctx, sinceTS)` 取 24h 活跃渠道 (HAVING SUM > 0)
    - join channel_vendor_map + upstream_vendors 拿 vendor_name
- **前端**:
  - `web/src/pages/ChannelHealth.tsx` 重写: 表格 → 卡片 grid (auto-fill 360px)
  - 单卡片 3 大字 (错误率 / 24h 请求 / P95) + 顶部按错误率色条 + 底部供应商 + 模型数
  - 5 KPI 全部改用 health_24h 字段

### 公网验证 (数据准确性自检)

```
SQL 直查 channel_id=110 24h:
  request=112  error=54  rate=0.4821
API monitor/channels id=110:
  request=112  error=54  rate=0.4821  ✅ 100% 一致
```

### 体积

- 新 dist: 1.18MB JS / 8.2KB CSS (gzip 377KB / 2.15KB)
- ChannelHealth.tsx: 270 → 222 行 (-48 行, 卡片组件更紧凑)

---

## 2026-06-15 (续 5) — 错误率新口径 + 红边重做 (用户决策 2026-06-15 09:43)

### 决策

1. **分母**: 业务请求 = `type IN (2, 5, 6)` 跨 24h (排除登录/充值/管理操作)
2. **分子**: 独立错误 = `type=5 AND jsonb_array_length(use_channel) = 1` (排除被 retry 中间失败)
3. **P95**: 走 channel_health_5min 桶 MAX(最新桶) (避免 RoDB percentile_cont 慢)
4. **红边只覆盖渠道卡片** (kpi-card.kpi-danger), 不覆盖综合 KPI
5. **红边呼吸感加强**: 1.5s 周期 (原 2s) + 外发光 16→32px + 二层光晕 32→56px

### 性能

- 24h 178 万行 logs → 7.3ms 扫描 (idx_created_at_type 复合索引完美命中)
- 不会拖垮 DB (用户 2026-06-15 09:43 关注)
- 15 渠道聚合, 走 2 步: RoDB logs (7ms) + OPS channel_health_5min (5ms)

### 改动清单

- **后端**:
  - `internal/dal/ops_repo.go`: `ListChannel24hSummary` 重写走 RoDB 实时算 + P95 走 cache
  - `internal/api/handlers_monitor.go`: `health_24h` 字段调整 (去掉 prompt/completion/P50/P99/first_bucket, 加 success_count)
- **前端**:
  - `web/src/styles.css`: 红边 `kpi-card.kpi-danger` 改用 ::after 动画 + 新 class `kpi-card.kpi-danger-stat` 给综合 KPI
  - `web/src/pages/ChannelHealth.tsx`: 综合 KPI "24h 错误率" class 改 `kpi-danger-stat` (避免被红边影响)

### 数据准确性

ch 110 验证 (跟 SQL 直查):
- RoDB SQL: type=2=3953, type=5=862, 业务请求=4815
- API: req=4816, success=3954, err=862, rate=17.93%
- 差 1: 时点差异 (RoDB 实时 vs cache sync 1min 延迟)

### 体积

- CSS: 8.86KB (gzip 2.33KB) +0.7KB
- JS: 1.18MB (gzip 377KB) 不变

### 公网验证

- 15 渠道, 5 张触发红边 (错误率 ≥ 20%): ch 14 / 69 / 101 / 22 / 54
- 综合 KPI 5 个, 仅 "24h 错误率" 用 kpi-danger-stat, **无红边呼吸** ✅
- playwright 0 exception + 0 console error
