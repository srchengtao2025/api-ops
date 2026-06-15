# BILLING v2 业务规则 v2.0 (2026-06-14)

> **作者**: Mavis
> **生成时间**: 2026-06-14
> **依据**: 用户 2026-06-14 5 条新需求 + 5 个 Q&A 决策
> **关联**: [BILLING-v2-RFC.md](BILLING-v2-RFC.md) / [MONTHLY-RECONCILIATION.md](MONTHLY-RECONCILIATION.md) / [BILLING-RULES.md](BILLING-RULES.md) v1.0

> **v1.0 业务规则保持不变**（零输出免单 / 图片标记 / 退款 / 错误 / 未匹配上游）
> v2.0 增量仅在 **导出格式**、**字段粒度**、**异步任务**、**v1/v2 并行** 上扩展

---

## 0. v1 → v2 变更总览

| 维度 | v1 (18 端点 + 3 SPA) | v2 (6 端点 + 2 SPA) |
|---|---|---|
| **数据源** | RoDB 直连 | RoDB 直连 (不变) |
| **同步/异步** | 同步生成 | **异步任务 + 任务中心** |
| **字段** | prompt_tokens / completion_tokens / cache_tokens (单字段) + USD | **4 字段拆开** (prompt / completion / cache_creation / cache_read) + USD |
| **输出格式** | CSV / XLSX | **HTML / XLSX / ZIP** (UI 选) |
| **默认页** | 客户/上游/利润 3 页面 | **1 默认页: 当前月 27 客户累计** + 任务中心 |
| **并发控制** | 无 | **每用户 ≤ 2 个 running** (Q3) |
| **任务保留** | 永久 | **30 天自动清** (Q2) |
| **v1 状态** | — | **2026-06-14 20:39 已下线** (Q5 决策变更新, 不再保留) |

---

## 1. 5 条业务规则（v1.0 不变 + v2.0 适配）

### R1-R5 保持 v1.0 行为

| # | 规则 | v1 行为 | v2 行为 |
|---|---|---|---|
| **R1** | 零输出免单 | revenue=0, cost 照算 | **同** + 账单 4 字段累加时仍按"实际 quota"算 USD（不受兔）|
| **R2** | 图片标记 | is_image_generation=true | **同** + 账单 HTML/XLSX 加 [图片] tag |
| **R3** | 退款扣减 | type=3, revenue=-quota | **同** + 4 字段扣减仍按实际 quota |
| **R4** | 错误不计 | type=5, revenue=0 cost=0 | **同** + 4 字段不计入 |
| **R5** | 未匹配上游 | 加 unmatched_reason | **同** + HTML 顶部加 ⚠️ 提示 |

**v2 不在 calc 层重写规则**，复用 v1 的 `CalcLogCost` + `CalcRevenue` 等函数。

---

## 2. v2 新增：4 token 字段定义

### 2.1 字段映射 (newapi logs 表)

| 字段 (账单 v2) | 字段名 (newapi logs) | SQL 聚合 |
|---|---|---|
| `prompt_tokens` | `prompt_tokens` | `SUM(prompt_tokens)` |
| `completion_tokens` | `completion_tokens` | `SUM(completion_tokens)` |
| **`cache_tokens`** | `other->>'cache_tokens'` (JSONB) | `SUM(COALESCE((other->>'cache_tokens')::bigint, 0))` |
| `revenue_usd` | `quota` (内部单位) | `SUM(quota) / 500000.0` |

**⚠️ 2026-06-14 PR #8 部署修正**:

v1 假设是 `cache_creation_tokens` + `cache_read_tokens` 拆开 2 列（Anthropic prompt caching 标准字段）,
但**实际 newapi logs 表只有 1 个 `other` JSONB 字段**, cache token 数存在 `other->>'cache_tokens'` 里.

RDS `information_schema.columns` 验证:
```sql
SELECT column_name FROM information_schema.columns
WHERE table_name = 'logs' AND column_name LIKE '%cache%';
-- (no rows) ←  没有 cache_creation_tokens / cache_read_tokens 列
```

账单 v2 合并为 1 个 `cache_tokens` 字段 (Anthropic 标准的 cache_creation + cache_read 之和),
**XLSX/HTML 模板也是单字段**, 跟 v1 假设的 2 列对账需要:
- 客户内部对账时, 报"cache tokens" = 创建 + 命中合计, 不区分读写
- 如果未来要拆开, 需 newapi 升级加列后我们同步改

**说明**:
- 4 字段**都从 type=2 消费**里 SUM（不包含 type=3 退款, type=5 错误）
- 1 USD = 500000 quota (newapi 内部换算, 跟 newapi 官方汇率同步)

### 2.2 业务语义

- **`cache_tokens`** = Anthropic prompt caching 写入 + 命中合计; 启用 cache 的模型 (Claude 全系) 才有值, 其他模型为 0
- **`revenue_usd` = 客户应付**（按 1 USD = 500000 quota 换算）
- **`cost_usd`**（上游成本）v2 暂不算（v1 才有），客户账单只算 customer-side
- 4 字段都按 type=2 过滤 (type=1 充值不计, type=3 退款不计, type=5 错误不计)

---

## 3. v2 新增：异步任务系统

### 3.1 任务状态机

```
pending ──> running ──> success
   │           │
   │           └──> failed
   │
   └──> cancelled  (仅 pending 可取消, 调 cancel 端点)
```

### 3.2 任务生命周期 (worker pool 内部)

```
1. POST /api/billing/v2/customer/:uid/export-last-month
   → 写 billing_export_tasks (status=pending)
   → 查 CountBillingExportTasksRunningByUser
     - 若 user_id 已 running ≥ 2 → 返 429
     - 否则入队
   → 返 { task_id, status: "pending" }

2. worker pool (2 goroutine 全局) 拉任务
   → 占该 user_id 的信号量 (sem<2)
   → 改 status=running, started_at=now
   → 调 generateStatement:
     a) PeriodBounds('2026-05') → [start, end)
     b) QueryStatement (3 SQL: 汇总 + 按天 + 按模型)
     c) RenderHTML + RenderXLSX (按 formats 拆)
     d) PackZip → /data/billing-exports/{task_id}.zip
   → 改 status=success, file_size=..., finished_at=now
   → 释放信号量
```

### 3.3 输出格式 (UI 让用户选)

| 格式 | 文件 | 用途 |
|---|---|---|
| `html` | `statement.html` (单文件) | 浏览器打开, 人类可读 |
| `xlsx` | `statement.xlsx` (3 sheet: 汇总+按天+按模型) | 财务/Excel 处理 |
| `html,xlsx` | 2 文件 + `README.txt` | 打包 ZIP 下载 |

### 3.4 限流 (Q3 决策)

- **每用户 ≤ 2 个 running** (注意原话"全系统 2 个"二次确认改"每用户 2 个")
- 全局 worker pool 2 goroutine
- 限流在 2 层实现:
  - **入队前**: 查 DB count, 满了返 429
  - **运行时**: semaphore(2) 限流, 第 3+ 任务阻塞等槽

---

## 4. v2 新增：ZIP 文件保留期 (Q2 决策)

- **任务表 + 物理文件 30 天后自动清**
- 清理触发: `StartPruneLoop` 24h tick 跑一次（生产环境默认 03:00 跑）
- 清理内容:
  - DB: 硬删 `created_at < now-30d` 的 `billing_export_tasks` 记录
  - 物理文件: 删 `/data/billing-exports/*.zip` 中 `mtime < now-30d` 的文件
  - 内存: `user_sems` map 清理空 semaphore (30 天时跑 `CleanupUserSem`)

**测试覆盖**:
- `TestIntegration_TaskLifecycle` 模拟文件 mtime 改 31 天前, 跑 PruneExportRetentionDir, 验证文件被删

---

## 5. v2 SPA 行为

### 5.1 默认页 `/billing/v2/customer`

| 元素 | 数据 | 行为 |
|---|---|---|
| **顶部 KPI** | 用户数 / 本月总收入 / 本月总 tokens | 3 个 Card |
| **表格** | 27 用户当月 4 token + USD | 20 行/页, 排序 |
| **行内按钮** | "生成上月账单" | 调 API 创建任务, 跳任务中心 |
| **Modal** | 多选格式 (HTML/XLSX) | 创建任务前确认 |

### 5.2 任务中心 `/billing/exports`

| 元素 | 行为 |
|---|---|
| **表格列** | TaskID / 用户 / 周期 / 格式 / 状态 / 进度 / 文件大小 / 操作人 / 时间 / 错误 / 操作 |
| **轮询** | 5s `setInterval`, 仅 running/pending 时轮询 |
| **下载按钮** | success 状态可见, 调 a[download] 触发 |
| **取消按钮** | pending 状态可见, admin/finance 可点 |
| **状态过滤** | 5 状态 (排队/生成中/完成/失败/已取消) |
| **进度条** | running=active, success=100%绿, failed=exception, cancelled=normal |

---

## 6. v1 / v2 并行期 (Q5 决策已变更为 v1 直接下线, 2026-06-14 20:39)

| 期间 | v1 状态 | v2 状态 |
|---|---|---|
| **2026-06-14 20:39 前** | 18 端点 + 3 SPA 正常运行 (历史期) | 6 端点 + 2 SPA 正常运行 |
| **2026-06-14 20:39 后** | **❌ 全部 404** (路由 + handler + SPA + DB 表全删/归档) | 6 端点 + 2 SPA 唯一入口 |

**v1 18 端点 (已下线, 返 404)**:
- 客户 (8): `preview / generate / statements / :id / confirm / exported / export / lines/export`
- 上游 (4): `generate / statements / :id / export`
- 利润 (1): `profit/analysis`

**v1 处置明细**:
- 后端: 13 handler 全删 (`internal/api/handlers_stmt.go` 重写), 13 路由全删 (`server.go`)
- 前端: 3 页面全删 (`CustomerStatements.tsx` / `UpstreamStatements.tsx` / `ProfitAnalysis.tsx`), 9 个 API 客户端方法 + 1 interface 全删
- DB: 2 表 + 2 sequence 移到 `archive` schema (`migrations/2026-06-14-billing-v1-archive.sql`)
- 文档: 2 文档移到 `archive/v1-docs/` (BILLING-RULES.md + MONTHLY-RECONCILIATION.md)
- Go 业务: 4 文件全删 (`customer_statement.go` + `upstream_statement.go` + `exporter.go` + `integration_test.go`)
- scheduler: 删 `runDailyBilling` cron 每日 02:00 自动跑昨日对账
- 业务规则: 5 条 R1-R5 全在 v2 复用, 业务无断点

**v2 6 端点 (唯一入口)**:
- `GET /api/billing/v2/customer/current-month-overview` (替代: 27 客户当月聚合)
- `POST /api/billing/v2/customer/:uid/export-last-month` (替代: 单/批量 generate)
- `GET /api/billing/v2/customer/:uid/tasks` (替代: 单客户历史 statements)
- `GET /api/billing/v2/export-tasks` (新: 任务中心)
- `GET /api/billing/v2/export-tasks/:task_id/download` (替代: export 3 端点)
- `POST /api/billing/v2/export-tasks/:task_id/cancel` (新: 取消)

**v1 的"趋势 / 月份切换 / 跨月对比"等 v2 暂不替代**——v3 模块接管.

---

## 7. 5 个 Q&A 决策记录

| # | 决策 | 选项 | 你的选择 |
|---|---|---|---|
| Q1 | ZIP 存哪 | 容器本地 / PG bytea / OSS / NAS | **api 容器本地 volume** `/data/billing-exports/` |
| Q2 | 任务保留 | 30d / 90d / 永久 | **30 天后自动清** |
| Q3 | 同时任务数 | 全局 2 / 每用户 2 / 智能 | **每用户 ≤ 2 个** running (二次确认) |
| Q4 | 格式 | 必选 / 用户选 | **UI 让用户选** (HTML / XLSX / 两者) |
| Q5 | v1 处理 | 保留 6m / 410 / 只读 | **⚠️ 2026-06-14 20:39 决策变更: v1 直接下线 (不保留 6 个月)** |

**Q3 特别说明**:
- 原话: "同时进行中的生成任务不能超过2个"
- 二次确认: 选了"每用户 ≤ 2 个"
- **这是 27 账号 × 2 = 54 个同时 (远高于原话 2 个)**
- RFC §1 标红, export_worker.go 头注释也标红
- 6 个月后回看: 如确认要改回"全局 2 个", 改 `MaxConcurrentPerUser` 常量 + Repo SQL 即可

---

## 8. 测试覆盖

### 8.1 单元测试 (16 个, 全过)

**PR #2 (7 个)**:
- `TestGetUserSem_Singleton` 同 user 返同 chan
- `TestUserSem_CapIs2` cap = 2
- `TestUserSem_2SlotsEnforced` 第 3 个阻塞
- `TestUserSem_ConcurrentEnforce` 10 并发抢 2 槽, 峰值 = 2
- `TestNewTaskID_Unique` 1000 次不重复
- `TestNewTaskID_Length32` 32 字符
- `TestCleanupUserSem_OnlyWhenEmpty` 仅空 sem 才清

**PR #3 (9 个)**:
- `TestRenderHTML_NotEmpty` 字段替换正确
- `TestRenderHTML_EmptyByDay` 空数据 placeholder
- `TestRenderXLSX_3Sheets` PK magic 验证
- `TestPackZip_HTMLOnly/HTMLAndXLSX/XLSXOnly` 3 格式
- `TestPeriodBounds_May/Invalid` 边界
- `TestSplitFormats` 4 case

### 8.2 集成测 (4 个, PR #7)

- `TestIntegration_PeriodBounds` 全月 + 跨年 + 闰月 (6 case)
- `TestIntegration_TaskLifecycle` 状态机 + 30 天清理 (改 mtime + 跑 Prune)
- `TestIntegration_RoundTripData` 中文 + 数字千分位
- `TestIntegration_FormatsCombination` 4 格式组合

### 8.3 公网端到端 (PR #8 完成, 2026-06-14)

- **真实 RoDB 端到端**: user_alpha (uid=47) 2026-05 → 任务成功 → 下载 zip → 解压校验 ✅
  - HTML 合计行: 1,002,849 调用 / 101.7亿输入 / 126.2亿输出 / 12.5亿 cache / **$70,226.82 USD**
  - XLSX sharedStrings: 汇总表头 7 列单字段 `缓存 tokens` ✅
  - 任务 6 秒内 success, progress 100, file_size 11.6KB
- 5s 轮询压测: 略 (后续)
- 30 天清理 cron 跨年场景: 略 (后续)

---

## 9. 部署清单 (PR #8 完成, 2026-06-14)

### 9.1 部署步骤

1. 远端 ECS `/opt/api-ops/docker-compose.yml` 加 volume 挂载:
   ```yaml
   services:
     api:
       volumes:
         - /data/billing-exports:/data/billing-exports
   ```
2. 跑 migration: `psql -f migrations/2026-06-14-billing-v2-tables.sql`
3. `chown 100:101 /data/billing-exports` (容器 app user uid=100, gid=101)
4. 推镜像: `docker buildx build --platform linux/amd64 -t api-ops:latest --load .`
5. `docker save | ssh root@api-ops.example.com 'docker load + compose up -d --no-deps api'`
6. 公网 8091 验证 6 端点 (curl)
7. 创建 1 个测试任务 + 下载 zip + 解压校验 4 token 字段

### 9.2 部署发现 + 修复的 4 个 bug

| # | Bug | 修法 |
|---|-----|------|
| 1 | `cache_creation_tokens` 列不存在 (newapi 用 `other->>'cache_tokens'`) | SQL 改 JSONB 提取, struct/handler/SPA 同步合并为单字段 `cache_tokens` |
| 2 | HTML 模板 `template: statement.html:39:58: executing ... can't evaluate field CacheCreationTokens` | 删 2 个 `<dt>`, 合并 2 个 `<th>` 为单 `缓存 tokens` (PR #3 模板没同步 v2 schema) |
| 3 | XLSX headers 残留 `缓存创建 tokens` / `缓存命中 tokens` | `statement_format.go` 3 处 headers 同步合并 (汇总/按天/按模型) |
| 4 | Dockerfile 不 COPY 模板 + zip 写 `/data/billing-exports/` permission denied | Dockerfile 加 `COPY internal/billing/templates/ /app/internal/billing/templates/`; `findTemplatePath` 加 `/app/` 候选; chown 100:101 `/data/billing-exports` |

### 9.3 部署关键经验 (后续 PR 参考)

1. **新api 字段一定先打 SQL 验证** — `information_schema.columns` 查存在性, 不要从字段名猜
2. **容器内读 `other` JSONB** — `other->>'字段'` (text) 然后 `::bigint`, 不是 `other->'字段'->>'...'`
3. **Dockerfile COPY 覆盖** — 模板、静态资源、SQL migration files、CA certs 所有代码运行时读的路径
4. **容器外目录 mount 进容器** — 用容器内 user 写, 一定 chown
5. **多阶段 build cache 复用** — `/tmp/gocache` 挂载做 cache 目录
6. **buildx `--platform linux/amd64 --load`** — mac arm 64 build 不能直接推 linux
7. **macOS Docker Desktop 8088 占用** — 远程端口用 8091, 本地映射 8088
8. **测试清理 `defer os.RemoveAll` 顺序** — 必须在读 zip 之后调, 不然删早了

### 9.4 Dockerfile 模板挂载

```dockerfile
# Dockerfile (PR #8 加的)
COPY --from=builder /out/api-ops-server /app/api-ops-server
COPY web/dist/ /app/web/dist/
# BILLING v2 账单 HTML 模板 (PR #8 / 8, 2026-06-14)
COPY internal/billing/templates/ /app/internal/billing/templates/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
```

容器内读路径: `findTemplatePath` 候选列表要包含 `/app/internal/billing/templates/`:
```go
candidates := []string{
    "internal/billing/templates/" + name,         // 开发
    "./internal/billing/templates/" + name,       // 开发
    "/app/internal/billing/templates/" + name,    // 部署 ← PR #8 加
    "templates/" + name,                           // 旧版兼容
    "./templates/" + name,
}
```

---

## 10. 业务规则 vs 代码实现的对应

| 规则 | v1 文件 | v2 文件 | 状态 |
|---|---|---|---|
| R1 零输出免单 | internal/billing/customer_statement.go:CalcRevenue | 不动 (复用) | ✅ v1 已有, v2 复用 |
| R2 图片标记 | internal/billing/customer_statement.go:IsImageGeneration | 不动 | ✅ v1 已有 |
| R3 退款 | CalcRevenue case 3 | 不动 | ✅ v1 已有 |
| R4 错误 | CalcRevenue case 5 | 不动 | ✅ v1 已有 |
| R5 未匹配上游 | model_breakdown JSONB | 不动 | ✅ v1 已有 |
| **4 token 字段** (新) | — | internal/billing/statement_query.go | ✅ v2 新增 |
| **异步任务** (新) | — | internal/billing/export_worker.go | ✅ v2 新增 |
| **30 天清理** (新) | — | internal/billing/prune.go | ✅ v2 新增 |
| **ZIP 打包** (新) | — | internal/billing/statement_format.go:PackZip | ✅ v2 新增 |

---

## 11. 后续优化（不在本次范围）

1. **图片类 model 关键词移到 system_config** (运营可配置)
2. **Q3 决策回看**: 6 个月后用户是否要改回"全局 2 个"
3. **v1 真正的 410 下架**: 2026-12-14
4. **AI 自动解释 0 output 占比**: P3, 客户成功自动回复
5. **多任务并行 worker pool 扩到 4**: 月初 1-5 号高峰

---

## 12. FAQ

### Q1: 4 token 字段哪 4 个?
A: prompt_tokens (输入) / completion_tokens (输出) / cache_tokens (缓存合计) / revenue_usd (应付 USD). **2026-06-14 PR #8 修正**: cache 字段实际是 `cache_creation + cache_read` 合计, 存 newapi `other->>'cache_tokens'` JSONB. 启用 Anthropic prompt caching 的模型 (Claude 全系) cache_tokens > 0, 其他模型为 0.

### Q2: 任务中心能看到其他用户的任务吗?
A: 任务中心 (`/billing/exports`) 是**所有用户**的任务列表, viewer 角色也能看. 但**取消**操作需 admin/finance.

### Q3: 任务 30 天后被清, 还能重新生成吗?
A: 可以. 调 POST `/api/billing/v2/customer/:uid/export-last-month` 重新创建. ZIP 会写到新 task_id, 不冲突.

### Q4: ZIP 文件名格式?
A: `{username}_{period}_statement.zip`, 例 `user_alpha_2026-05_statement.zip`. period 是 'YYYY-MM'.

### Q5: admin 看所有用户, 怎么只导自己?
A: v2 暂不支持单用户切换. admin 用自己的 uid 调 export, task operator 字段会记是谁导的.

### Q6: v1 端点 2026-12-14 之后怎样?
A: 返 HTTP 410 Gone. SPA 3 页面重定向到 v2 入口 (`/billing/v2/customer`, `/billing/exports`). 历史对账数据**保留在 v1 表里只读**, 财务可以查.

### Q7: 任务 running 卡住怎么办?
A: 当前没有超时 (worker 永不取消 running 任务). 后续 PR 加 `force cancel` 端点 (admin) + 5min 自动超时. **本 PR 不做**.

---
