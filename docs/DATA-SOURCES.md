# api-ops · 数据源架构

> **状态**: 2026-06-15 同步 (本期 25+ commits 全部落地)
> **适用**: api-ops 全栈 (Go 1.22 + Gin + GORM + React 18 + Vite + Antd 5)
> **关联**: DESIGN.md §4 数据架构 + §6.1 错误率定义 + AGENTS.md 部署铁律

## 1. 3 个数据源 (铁律)

api-ops 严格只能从以下 3 个数据源读数据，**不允许第 4 源**（如一次性导入的影子表）。

| 数据源 | 路径 | 用途 | 实时性 | 配置 |
|---|---|---|---|---|
| **API** | newapi admin `/api/*` | 渠道/用户/Tokens 列表 + 准实时 stat | 实时 | `upstream_ADMIN_BASE_URL` + `API_OPS_ADMIN_TOKEN` |
| **直连 DB** (RoDB) | newapi RDS `new-api.logs` 等 | 178K 日志聚合 + 178万行 24h 扫描 | 实时 | `API_OPS_RO_DSN` |
| **本地缓存 DB** | api_ops 库 `cache_*` 表 | 1min/5min 聚合预计算 | 准实时 (1-5min 延迟) | 始终可用（`OPS_DB_DSN`） |

**反例（已被删）**：
- ❌ `cmd/import_real/` —— 一次性从 SQLite dump 灌数据 → api_ops.logs 影子表 (归档 archive/import-real)
- ❌ `cmd/seed/` (mock 套件) —— 写死的演示数据 (归档 archive/mock-suite)
- ❌ `internal/billing/upstream_pricing` —— v1 价目表 CSV 流程 (2026-06-14 23:43 下架, archive schema 保留 9 行)

**新代码如果需要"快照/测试数据"**：
1. 加到 `archive/` 目录
2. 文档说明**只用于单次测试，不进生产 schema**
3. 不在 handler 路径里直接读这个表

## 2. admin /api/log/stat 字段语义 (实测 2026-06-14)

admin stat 是 1 次 HTTP 返**单值 stat**, 但**字段语义不同**:

| 字段 | 语义 | 跟 start_timestamp/end_timestamp 关系 |
|---|---|---|
| `quota` | 时间范围内 type=2 消耗 SUM (内部单位, 1/500000 = USD) | ✅ 范围 SUM (00:00~now 拿真"今日累计") |
| `rpm` | 60s 滑窗内 type=2 请求数 | ❌ 跟 start/end 无关, 内部维护 |
| `tpm` | 60s 滑窗内 type=2 tokens (prompt + completion) | ❌ 跟 start/end 无关, 内部维护 |

**注意**: admin **不返 count** (今日调用次数). 实测新api 无全平台 count 端点:
- `/api/log/stat` 不给 count
- `/api/log/self` 给单用户 total (分页 1 页拿), admin token + New-Api-User=1 只能看 admin 自己
- `/api/log/search` **已废弃** (新api 升级后无替代)
- `/api/log/total` `/api/log/all` 等全部 404

**结论**: 全平台"今日调用次数"当前 admin API 拿不到, 要么砍卡片, 要么问 upstream 那边加个 stat 字段.

## 3. handler × 数据源对照表 (2026-06-15 全量)

> **本节是 AGENTS.md 对照表的 2026-06-15 全量扩展**, 包含本期所有新增端点. **所有活跃端点共 30 个**.

### 3.1 总览 Dashboard (Q-D1)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/dashboard/today` | **API** (admin /api/log/stat 1 次) | 3 字段: quota→revenue, rpm, tpm. 1 次 HTTP |
| `GET /api/dashboard/trend-7d` | **API** (admin /api/log/stat 1 轮 7 次 D-7~D-1) + **in-memory sync.Map** (`dashboard:trend7d` key, 5min TTL) | 7d 趋势曲线 (Q-C9). 含 `source_cached` 字段 |
| `GET /api/dashboard/trend` | ❌ **已删除** (2026-06-14) | admin API 不给按天趋势 |
| `GET /api/dashboard/top-customers` | ❌ **已禁用** (2026-06-14) | 路由注释, handler 返 503, 卡片不显示 (Q-D2) |
| `GET /api/dashboard/top-models` | ❌ **已禁用** (2026-06-14) | 同上. admin /api/data/ 表空 (DataExportEnabled=false) |
| `GET /api/dashboard/top-channels` | ❌ **已禁用** (2026-06-14) | 同上 |

### 3.2 渠道元数据

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/upstream/channels` | **API** (newapi `/api/channel/?p=N`) + **本地 cache** (`OPS`) | 5min sync |
| `GET /api/upstream/channels/:id` | **API** (newapi `/api/channel/:id`) | 实时 |
| `GET /api/channel-mappings` | **API** (upstream 49 渠道) + **OPS.channel_vendor_map** | 5min sync |

### 3.3 BILLING v2 客户对账 (2026-06-14, 6 端点)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/billing/v2/customer/current-month-overview` | **RoDB** (logs.type=2 当月聚合) | 27 客户当月 4 token 字段 (cache_tokens 来自 other JSONB) |
| `POST /api/billing/v2/customer/:uid/export-last-month` | **RoDB** + **cache** (billing_export_tasks) | 创建任务, 异步 worker 跑账单生成 |
| `GET /api/billing/v2/customer/:uid/tasks` | **cache** (billing_export_tasks WHERE user_id) | 单客户任务历史 |
| `GET /api/billing/v2/export-tasks` | **cache** (billing_export_tasks) | 全用户任务中心, SPA 5s 轮询 |
| `GET /api/billing/v2/export-tasks/:tid/download` | **本地 volume** (`/data/billing-exports/`) | 流式下载 ZIP |
| `POST /api/billing/v2/export-tasks/:tid/cancel` | **cache** (billing_export_tasks) | admin/finance 取消 running 任务 |

### 3.4 BILLING v3 上游对账 (2026-06-14, 5 端点)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/billing/v3/upstream/current-month-overview` | **RoDB** (logs.type=2 当月) + **OPS** (channel_vendor_map.discount) | 5 vendor / 39 channel 双层表 |
| `POST /api/billing/v3/upstream/export-last-month` | **RoDB** + **cache** (billing_export_tasks.kind=upstream) | vendor_code 在 body, 复用 v2 worker, kind=upstream 路由 |
| `GET /api/billing/v3/upstream/:vendor_code/tasks` | **cache** (billing_export_tasks.kind=upstream) | 单 vendor 任务历史 |
| `GET /api/billing/v3/export-tasks` | **cache** (billing_export_tasks) | 全 vendor 任务中心, 复用 v2 UI |
| `POST /api/billing/v2/export-tasks/:tid/cancel` | **cache** | 取消 running 任务 (复用 v2 端点 + kind 过滤) |

**复用**: `GET /api/billing/v2/export-tasks/:tid/download` (kind 过滤, v3 任务也走)

### 3.5 BILLING v4 利润分析 (2026-06-14, 1 端点)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/billing/v4/profit/overview?dimension=user\|channel\|model\|trend` | **RoDB** + **cache** (cache_logs_summary_by_model_5min / cache_logs_summary_by_channel_5min) | 4 维度汇总 |

### 3.6 BILLING v1 已下线 (2026-06-14 20:39)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/billing/customer/*/preview` | ❌ **已下线** | v1 客户对账, 改用 BILLING v2 `current-month-overview` |
| `POST /api/billing/customer/generate` | ❌ **已下线** | v1 客户对账, 改用 BILLING v2 `export-last-month` |
| `GET /api/billing/customer/statements` | ❌ **已下线** | v1 客户对账, 改用 BILLING v2 `customer/:uid/tasks` |
| `GET /api/billing/customer/statements/:id` | ❌ **已下线** | v1 客户对账, ZIP 走 BILLING v2 `download` |
| `POST /api/billing/customer/statements/:id/confirm` | ❌ **已下线** | v2 无 confirm 概念, 异步任务即生成 |
| `GET /api/billing/customer/statements/:id/export` | ❌ **已下线** | 改用 BILLING v2 `download` (ZIP 替代 CSV) |
| `GET /api/billing/customer/statements/:id/export.xlsx` | ❌ **已下线** | 改用 BILLING v2 `download` (含 XLSX 在 ZIP 里) |
| `GET /api/billing/customer/statements/:id/lines/export` | ❌ **已下线** | v2 不导原始 lines, 改 ZIP 内 XLSX 多 sheet |
| `POST /api/billing/upstream/generate` | ❌ **已下线** | v1 上游对账, v2 范围外, 由 v3 模块接管 |
| `GET /api/billing/upstream/statements` | ❌ **已下线** | 同上 |
| `GET /api/billing/upstream/statements/:id` | ❌ **已下线** | 同上 |
| `GET /api/billing/upstream/statements/:id/export` | ❌ **已下线** | 同上 |
| `GET /api/billing/profit/analysis` | ❌ **已下线** | v1 利润分析, v2 范围外, 由 v4 模块接管 |

### 3.7 upstream_pricing 价目表 已下架 (2026-06-14 23:43)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/upstream-pricing` | ❌ **已下架** | v1 价目表 CSV 流程, v3 反推公式完全弃用 |
| `POST /api/upstream-pricing/import` | ❌ **已下架** | 同上 |
| `GET /api/upstream-pricing/imports/:id` | ❌ **已下架** | 同上 |
| `DELETE /api/upstream-pricing/:id` | ❌ **已下架** | 同上 |

### 3.8 监控中心 - 渠道健康 (2026-06-15, 2 端点, Q-C10)

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/monitor/channels` | **cache** (`channel_health_5min`) + **OPS** (channel_vendor_map + upstream_vendors JOIN) | 44 渠道卡片数据 + 5 KPI |
| `GET /api/monitor/channels/:id/health?range=5min\|1h\|24h\|7d` | **cache** (`channel_health_5min` + `channel_health_1h`) | 单渠道趋势 |
| `POST /api/monitor/tick` | **RoDB** | 触发 5min 健康分重算 (admin 调试) |
| `GET /api/monitor/alerts` | ❌ **暂未开放** (Q-C10 决策: 告警模块暂缓) | rule / alert / ack / resolve / 飞书推送暂不开 SPA |
| `POST /api/monitor/alert-rules` | ❌ **暂未开放** | 同上 |
| `POST /api/monitor/alerts/:id/ack` | ❌ **暂未开放** | 同上 |

### 3.9 供应商管理 + 鉴权

| Handler | 数据源 | 说明 |
|---|---|---|
| `GET /api/vendors` | **cache** (`OPS`) | `upstream_vendors` |
| `GET /api/admin/users` | **cache** (`OPS`) | `ops_users` |
| `GET /api/auth/me` | **cache** (`OPS`) | `ops_users` |
| `POST /api/auth/login` | **cache** (`OPS`) | JWT 24h, 3 角色 RBAC |
| WebSocket `/api/ws/*` (5s tick) | **RoDB** | 实时聚合 (本矫正范围外) |

### 3.10 TopX 恢复路径 (任一, 见 handlers_stmt.go 注释)

1. **upstream 那边开 `DataExportEnabled`** → `/api/data/` 拿按 hour 聚合
2. **`cache_logs_summary_5min` 1min tick 扩展** `model_name`/`user_name`/`channel_name` 维度 (新表 + sync tick 写, 0 admin API, 1min 延迟)

## 4. 缓存聚合策略 (本期新增 / 已扩, 2026-06-14~15)

> 所有 tick 都通过 `scheduler.Run()` 在 main.go 启动 (**2026-06-15 修复 bug 之前, 这些表全是 0 数据**).

### 4.1 tick 表

| 表 | Tick | 写入方 | 读取方 | 用途 |
|---|---|---|---|---|
| `cache_logs_summary_5min` | **1min** | sync_logs_summary (RoDB → 聚合) | v2/v3/v4 SQL fallback | 27 user 1 SQL 拿 4 token + USD |
| `cache_logs_summary_by_model_5min` | **1min** | sync_logs_summary (RoDB → 聚合) | v3 CalcLogCost (按模型维度) | v3 利润分析按模型聚合 |
| `cache_logs_summary_by_channel_5min` | **1min** | sync_logs_summary (RoDB → 聚合) | v4 CalcProfitOverview (按渠道维度) | v4 利润分析按渠道聚合 |
| `channel_health_5min` | **1min** | monitor scheduler | monitor/channels + monitor/channels/:id/health | 渠道 5min 滑窗 (request/error/p50/p95/p99) |
| `channel_health_1h` | **5min** | monitor scheduler | monitor/channels 历史趋势 | 1h 聚合, 存 1 年 |
| `dashboard:trend7d` (in-memory sync.Map) | **5min** | `dashboardTrend7d` handler (1 轮 7 次 admin) | Dashboard SPA (5min tick) | 7d 趋势曲线, 不含今天 (Q-C9) |

### 4.2 admin 限流预算管理

admin `/api/log/stat` 限流 **18次/5min**, 当前预算分配:

| 端点 | 频次 | 5min 内调用 | 余额度 |
|------|------|------------|--------|
| `GET /api/dashboard/today` | 5s tick (SPA) | ~60次/5min | **超出 → 429** (SPA 需降频到 30s+) |
| `GET /api/dashboard/trend-7d` | 5min tick (SPA) | 1次/5min (含后端 cache 7 次 admin) = 7次 | 11 |
| 其他 admin 端点 (channels / users / ...) | 手动 | 偶发 | 11 |

**实际 SPA 行为**:
- Dashboard SPA 5s tick → today (高频, 触发限流)
- Dashboard SPA 5min tick → trend-7d (低频, 后端 cache 复用)
- 接受 today 5s tick 触发 429 → SPA 提示用户降频

### 4.3 sync 任务依赖 (main.go 启动)

| Sync 任务 | Tick | 依赖 | 没配时行为 |
|---|---|---|---|
| `upstream_sync` | 5min | API | 跳过 + 报错 |
| `logs_summary` | **1min** (本期扩 by_model/by_channel) | RoDB | 跳过 |
| `monitor_scheduler` | **1min** | RoDB | 跳过 (channel_health_5min 0 数据) |
| `ai_cluster` | 5min | cache | OK (offline 可) |
| `ai_daily_report` | cron 09:00 | cache | OK |
| `billing_prune` | cron 凌晨 | cache | OK (30 天清理) |

**main.go 启动修复 (2026-06-15 09:01)**:
- 修复前: monitor_scheduler / logs_summary 等 tick **不会自动启**, 全靠 lazy 触发
- 修复后: `scheduler.Run(rootCtx, cfg)` 在 main.go 顶部启 → 所有 tick 启动 5s 后跑

## 5. RoDB 未配时的行为

`API_OPS_RO_DSN` 未设时：

- `dal.RoDB()` 返回 `nil`
- `dal.HasRoDB()` 返回 `false`
- 所有走 RoDB 的 handler 返回 HTTP **500 + `dal.ErrNoRoDB`**
- **2026-06-14 变更**: 不再 fallback 到 OPS 影子表 (A 阶段清理后). 也不走 cache_logs_summary_5min.
- WebSocket 5s tick 返回空数据（不崩）

**当前 (2026-06-15)**:
- 总览 dashboard 4 个端点 0 个走 RoDB, 全部 admin API
- RoDB 缺失只影响 billing/monitor/WebSocket, 不影响 dashboard
- TopX 3 端点已禁用, 也不需要 RoDB
- v2/v3/v4 + 监控端点都依赖 RoDB, 缺配置 = 这些端点全 500

**`cache_logs_summary_5min` 当前 dashboard 不用**（admin stat 1 次拿 quota/rpm/tpm）, 但 sync tick 仍在 1min 跑 (供 monitor/billing 用).

## 6. 影子表 (已清空 / 已归档)

A 阶段 (2026-06-14) 已清空以下表（之前由 `import_real` 一次性灌入）：

| 表 | 原行数 | 处置 |
|---|---|---|
| `api_ops.logs` | 178,125 | **TRUNCATE** |
| `api_ops.users` | 27 | **TRUNCATE** |
| `api_ops.channels` | 49 | **TRUNCATE** |
| `api_ops.billing_statements` | 0 | **移到 archive schema** (2026-06-14 20:39 v1 下线) |
| `api_ops.billing_statement_lines` | 0 | **移到 archive schema** (同上) |
| `api_ops.upstream_pricing` | 9 | **移到 archive schema** (2026-06-14 23:43 价目表下架) |
| `api_ops.upstream_pricing_imports` | 0 | **移到 archive schema** (同上) |

**`import_real` 工具已删除**，归档到 `archive/import-real` 分支 + `v0.1-import-real-2026-06-14` tag.

**`cache_logs_summary_5min` 保留** (304 桶) —— 没害，**接 RoDB 后 1min tick 自动覆盖**.

## 7. 错误率新口径 (Q-C11, 2026-06-15)

> 监控中心 - 渠道健康模块的核心口径定义. **所有"渠道错误率" / "全平台错误率" 字段从此节取口径**.

### 7.1 业务定义

**错误率 = 独立错误数 / 业务请求数** (跨 24h 窗口).

### 7.2 分子分母 SQL

```sql
-- 业务请求 (分母): type=2 正常消耗 + type=5 错误 + type=6 重试 (排除登录/充值/管理操作)
SELECT COUNT(*) AS biz_requests
FROM newapi.logs
WHERE created_at >= NOW() - INTERVAL '24 hours'
  AND type IN (2, 5, 6);

-- 独立错误 (分子): type=5 且只用 1 个渠道 (排除被 retry 中间失败)
SELECT COUNT(*) AS independent_errors
FROM newapi.logs
WHERE created_at >= NOW() - INTERVAL '24 hours'
  AND type = 5
  AND jsonb_array_length(other::jsonb->'admin_info'->'use_channel') = 1;
```

### 7.3 实测案例 (ch_id=110, 2026-06-15)

| 来源 | 业务请求 | 成功 | 独立错误 | 错误率 |
|------|---------|------|---------|--------|
| RoDB 直查 SQL | 4,815 | — | 862 | 17.93% |
| API monitor/channels | 4,816 | 3,954 | 862 | 17.93% |
| **差** | **1** | — | 0 | — |

差 1 行根因: RoDB 实时 (NOW()) vs cache sync 1min 延迟. 在可接受范围内.

### 7.4 P95 / P50 / P99 口径

- **不**走 RoDB `percentile_cont()` (实测 178 万行扫描慢)
- **走** `channel_health_5min` 桶取 **MAX(对应分位数)**: 等价于"最新一桶的分位值"
- 24h 取最大 = 跨桶反映当前体感

### 7.5 性能与索引

- 24h 178 万行 logs → **7.3ms 扫描** (`idx_created_at_type` 复合索引完美命中)
- 不会拖垮 DB

### 7.6 与 v1 旧口径对比

| 维度 | v1 旧口径 (已废弃) | v2 新口径 (Q-C11 现行) |
|------|---------------------|-------------------------|
| 分母 | 全部 type=2 + type=5 | **业务请求** type IN (2, 5, 6) |
| 分子 | type=5 (含 retry 中间失败) | **独立错误** type=5 AND use_channel.length=1 |
| 数字 | 偏高 (retry 重复计) | **偏低且更准确** |
| P95 | RoDB percentile_cont (慢) | channel_health_5min 桶 MAX (快) |
| 24h 行数 | 178 万 | 同 (但 SQL 7.3ms 命中索引) |

## 8. 部署检查

```bash
# 1. 看 .env 配置
docker exec api-ops-api sh -c "cat /app/.env | grep -E 'DSN|TOKEN'"

# 2. 启动 log (验证 scheduler.Run 启动)
docker logs api-ops-api 2>&1 | grep -E "scheduler started|RO DSN|tick"

# 3. RoDB 健康
docker exec api-ops-api sh -c "wget -qO- http://127.0.0.1:8088/api/dashboard/today -H 'Authorization: Bearer ...'"
#   200: 有数据
#   503: {"error": "upstream RO DSN not configured"}

# 4. 监控 tick 跑起来验证
docker logs api-ops-api 2>&1 | grep -E "monitor.*tick"
#   期望: [monitor] tick: 5min_buckets=N 1h_buckets=0 alerts_fired=0 (每 1min)

# 5. 价目表已下架验证
curl -s -H "Authorization: Bearer ..." http://localhost:8088/api/upstream-pricing
#   期望: 404
```

## 9. 决策基线对齐 (本期)

> 本文档与 DESIGN.md v1.0 + Q-C7~C11 (本期 2026-06-14~15) 100% 对齐. 任一项变化需先升 DESIGN.md v1.1, 再改本文档.

| # | 决策 | DATA-SOURCES 落地位置 | 一致 |
|---|------|----------------------|------|
| Q1-Q14 | DESIGN.md v1.0 | §1-§6 + §8 | ✅ |
| Q-D1 | Dashboard 严格全 admin API | §3.1 | ✅ |
| Q-D2 | TopX 恢复路径暂不实现 | §3.1 + §3.10 | ✅ |
| Q-C7 | upstream_pricing 下架 | §3.7 + §6 | ✅ |
| Q-C8 | Dashboard TopX 永不再反转 | §3.1 | ✅ |
| Q-C9 | Dashboard 7d 趋势 | §3.1 + §4.1 + §4.2 | ✅ |
| Q-C10 | 监控中心 - 渠道健康 | §3.8 + §4.1 + §4.3 | ✅ |
| Q-C11 | 错误率新口径 | §7 (本章) | ✅ |

## 10. 变更日志 (本文档)

| 日期 | 变更 | 来源 commit |
|------|------|------------|
| 2026-06-14 | A 阶段清理 + dashboard 全 admin API | 4a0a65f + 4966916 |
| 2026-06-14 | BILLING v2 6 端点 + RoDB 聚合 | 5e464d4 → f6dceb0 |
| 2026-06-14 | BILLING v1 13 端点下线 | 72d82d4 |
| 2026-06-14 | BILLING v3 5 端点 + 反推公式 | df40966 → 4092f8b |
| 2026-06-14 | BILLING v4 1 端点 + 4 tab SPA | 7150dfb → 72d6ccd |
| 2026-06-14 | upstream_pricing 4 端点下架 | 141cd11 |
| 2026-06-15 | Dashboard 7d 趋势 + admin 限流预算 | 3f78f7d |
| 2026-06-15 | 全站 demo 风格升级 | 3ff38c5 |
| 2026-06-15 | 监控中心 - 渠道健康 + scheduler bug fix | 8546197 |
| 2026-06-15 | 渠道健康 3 规则 + 卡片化 | 4460965 |
| 2026-06-15 | 错误率新口径 + 红边加强 | 4edbc85 + 675dcc2 |
