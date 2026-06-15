# BILLING v2 RFC (2026-06-14)

> **作者**: Mavis
> **状态**: 草案 v1
> **评审者**: @user
> **目标**: 重新设计账单模块，按 5 条新需求 + 5 个 Q&A 决策
> **关联**: BILLING-RULES.md (v1.0) / MONTHLY-RECONCILIATION.md (v1.0) / AGENTS.md

---

## 状态 (2026-06-15)

| 维度 | 状态 |
|---|---|
| **上线端点** | ✅ **6 端点全部上线** (PR #1-#8, 2026-06-14 完成) |
| **公网验证** | ✅ 6 端点 200, 真实数据 user_alpha 5 月 1,002,849 调用 / $70,226.82 USD |
| **镜像** | `api-ops:latest` (commit `f6dceb0`) |
| **业务规则** | R1-R5 全复用 v1, 0 业务断点 |
| **后续演进** | ✅ v2 完成后, v3 上游对账复用 v2 worker (kind='upstream'), 0 改动 v2 代码 |
| **5 Q&A 决策** | Q1 ZIP 存 volume / Q2 30 天清理 / Q3 每用户 ≤ 2 running / Q4 UI 选格式 / Q5 v1 直接下线 (2026-06-14 20:39 决策变更, 原计划保留 6 月) |

### v2 已上线 6 端点

1. `GET /api/billing/v2/customer/current-month-overview` — 默认页: 27 客户当月 4 token + USD
2. `POST /api/billing/v2/customer/:uid/export-last-month` — 创建异步账单导出任务
3. `GET /api/billing/v2/customer/:uid/tasks` — 单用户任务历史
4. `GET /api/billing/v2/export-tasks` — 任务中心 (5s 轮询)
5. `GET /api/billing/v2/export-tasks/:tid/download` — 流式下载 ZIP (1h token 校验)
6. `POST /api/billing/v2/export-tasks/:tid/cancel` — admin/finance 取消 running 任务

### v2 部署清单关键事实

- **远端 volume**: `/data/billing-exports:/data/billing-exports`
- **chown**: 容器 app user (uid=100, gid=101) 写 zip 必须 chown 100:101
- **migration**: `migrations/2026-06-14-billing-v2-tables.sql` 创 `billing_export_tasks` + `billing_export_task_logs` 2 张表
- **验证报告**: `docs/test-reports/billing-v2-pr8-deploy-2026-06-14.md`

---

## v2 → v3 演进 (2026-06-14 21:00 决策)

**v2 客户对账 = 0 改动, v3 上游对账完全复用 v2 worker**:

| 维度 | v2 (客户对账) | v3 (上游对账, 复用 v2) |
|---|---|---|
| 端点数 | 6 | 5 |
| 任务表 | `billing_export_tasks` | **复用** v2 表, 加 `kind` + `vendor_code` 2 列 |
| Worker pool | 2 goroutine + 每用户 semaphore(2) | **复用** v2, 派发按 `kind` 路由 |
| ZIP 路径 | `/data/billing-exports/{task_id}.zip` | **复用** v2, 同路径 |
| 30 天清理 | `prune.go` 凌晨 cron | **复用** v2, 一起清 |
| 任务中心 UI | `/billing/exports` | **复用** v2, 加 1 列 `类型` |
| 业务规则 | R1-R5 (跟 v1 一致) | **复用** v2 R1-R5 + 新增 R6 缺渠道折扣 |
| 数据源 | RoDB (`newapi.logs`) | RoDB + OPS.channel_vendor_map (折扣) + log.other (group_ratio) |

**复用优势**: v3 不重写 worker / download / cancel / 任务中心, **省 3 PR 工作量** (≈ 3 天). v3 仅做"成本反推" + "上游对账 SPA 默认页" + "5 端点 + 1 SPA".

**对 v2 0 改动的事实**: v3 PR #1 migration 是 `ALTER TABLE ADD COLUMN IF NOT EXISTS`, 不破坏 v2 已有的 `billing_export_tasks` 表. v3 SPA 跟 v2 SPA 走不同路由 (`/billing/v3/upstream` vs `/billing/customers`), 无冲突. v3 overview 端点独立 (`/api/billing/v3/upstream/current-month-overview`), 不复用 v2 overview SQL.

---

## 0. 背景

api-ops 1.0 的账单模块（v1）有 **18 个端点 + 3 个 SPA 页面**，按"探索-生成-查询-改状态-导出"5 阶段拆开。**2026-06-14 用户给了 5 条新需求**，v1 不能满足：

1. 默认页 = 当月 27 客户累计消耗表格（v1 没有）
2. 字段 = 输入/输出/缓存创建/缓存命中 4 token + USD（v1 只算 quota 金额）
3. "生成上月账单"按钮 = 1 步生成 + Excel+HTML 双格式打包下载（v1 拆成 3 步 + 仅 CSV/XLSX）
4. 异步任务中心（v1 同步生成 ≤ 5s）
5. 直连 + 单账号 1 月 + 同时 ≤ 2 个 + 限流防 DB 崩

---

## 1. 决策表（5 个 Q&A）

| # | 决策 | 选项 | 你的选择 |
|---|---|---|---|
| Q1 | ZIP 存哪 | 容器本地 volume / Postgres bytea / OSS / NAS | **api 容器本地 volume** `/data/billing-exports/` |
| Q2 | 任务记录保留 | 30d / 90d / 永久 | **30 天后自动清** |
| Q3 | 同时任务数限制 | 全局 2 / 每用户 2 / 智能双层 | **每用户 ≤ 2 个** running（**注**：原话"全系统 2 个"，二次确认改"每用户 2 个"。在代码注释里标红防 6 个月后回看歧义）|
| Q4 | 账单格式 | 必选 / 用户选 | **UI 让用户选** (HTML / XLSX / 两者都要) |
| Q5 | v1 端点处理 | 保留 6m / 立刻 410 / 只读 | **v1 保留 6 个月** (2026-12-14 后 v1 返 410 Gone) |

---

## 2. API 设计 (6 端点)

### 2.1 端点列表

| 端点 | 用途 | 数据源 | 限流 | RBAC |
|---|---|---|---|---|
| `GET /api/billing/v2/customer/current-month-overview` | 默认页：27 客户当月累计 4 token + USD | RoDB GROUP BY username | 1 SQL/请求 | admin/finance/viewer |
| `POST /api/billing/v2/customer/:uid/export-last-month` | 创建异步账单导出任务，立即返 task_id | 异步 (worker) | **每用户 ≤ 2 running** | admin/finance |
| `GET /api/billing/v2/customer/:uid/tasks?status=&limit=` | 单用户历史任务列表 | OPS 表 | 无 | admin/finance/viewer |
| `GET /api/billing/v2/export-tasks?status=&limit=` | **任务中心** 列表 (所有用户) | OPS 表 | 无 | admin/finance/viewer |
| `GET /api/billing/v2/export-tasks/:task_id/download` | 下载 zip (成功后) | 本地文件 | 1h token 校验 | admin/finance |
| `POST /api/billing/v2/export-tasks/:task_id/cancel` | 取消 (仅 pending) | OPS 表更新 | — | admin/finance |

### 2.2 数据流

```
┌──────────────────────────────────────────────────────────────┐
│  SPA /billing/customers 默认页: 当前月 27 用户表               │
│  GET /v2/customer/current-month-overview                     │
│  → 1 SQL RoDB GROUP BY username (≈ 1s)                      │
└──────────────────────────────────────────────────────────────┘
       │ 点 "生成上月账单"
       ▼
┌──────────────────────────────────────────────────────────────┐
│  POST /v2/customer/:uid/export-last-month                     │
│  body: { formats: ["html"] } or ["xlsx"] or ["html","xlsx"]│
│  → 写 billing_export_tasks (status=pending)                  │
│  → 立即返 { task_id, status: "pending" }                    │
│  → 入队 worker queue                                        │
│  → 检查同 user_id 已 running ≤ 2, 超限返 429                │
└──────────────────────────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────────┐
│  Worker pool (2 goroutine 全局, 但每 user_id 信号量)          │
│  步骤:                                                       │
│    1) 改 status=running, started_at=now                      │
│    2) RoDB 查 user_id 上月 logs (5s timeout, LIMIT 100000)   │
│    3) 按天聚合 + 按模型聚合                                  │
│    4) 生成 HTML (text/template 模板) + XLSX (excelize)       │
│    5) zip 打包到 /data/billing-exports/{task_id}.zip         │
│    6) 改 status=success, file_size=..., finished_at=now     │
│  失败: status=failed, error_msg=...                          │
└──────────────────────────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────────┐
│  SPA /billing/exports 任务中心                                │
│  5s 轮询 GET /v2/export-tasks                                │
│  success → 显示 [下载] 按钮 (调 /download)                   │
│  failed → 显示错误信息 + [重试] 按钮                         │
└──────────────────────────────────────────────────────────────┘
```

### 2.3 字段定义 (4 token + USD)

| 字段 (账单 v2) | newapi logs 来源 | SQL |
|---|---|---|
| `prompt_tokens` | `prompt_tokens` 列 | `SUM(prompt_tokens)` |
| `completion_tokens` | `completion_tokens` 列 | `SUM(completion_tokens)` |
| **`cache_tokens`** | **`other->>'cache_tokens'` JSONB** | `SUM(COALESCE((other->>'cache_tokens')::bigint, 0))` |
| `revenue_usd` | `quota` (内部单位, 1 USD = 500000) | `SUM(quota) / 500000.0` |

**⚠️ 2026-06-14 PR #8 部署修正**:

RFC 原 v1 假设是 `cache_creation_tokens` + `cache_read_tokens` 拆开 2 列 (Anthropic prompt caching 标准字段),
但**实际 newapi logs 表只有 1 个 `other` JSONB 字段**, cache token 数存在 `other->>'cache_tokens'` 里.

RDS `information_schema.columns` 验证:
```sql
SELECT column_name FROM information_schema.columns
WHERE table_name = 'logs' AND column_name LIKE '%cache%';
-- (no rows) ← 没有 cache_creation_tokens / cache_read_tokens 列
```

账单 v2 合并为 1 个 `cache_tokens` 字段 (Anthropic 标准的 cache_creation + cache_read 之和),
**XLSX/HTML 模板也是单字段**, 跟 RFC 原假设的 2 列有差异, 实际部署后用单字段.

**4 字段都从 type=2 消费**里 SUM（不包含 type=3 退款, type=5 错误）。

---

## 3. 数据库 Schema (3 张表)

### 3.1 `billing_export_tasks` (主表)

```sql
CREATE TABLE billing_export_tasks (
  id              bigserial PRIMARY KEY,
  task_id         uuid UNIQUE NOT NULL DEFAULT gen_random_uuid(),  -- 暴露给前端
  user_id         integer NOT NULL,
  username        varchar(64) NOT NULL,  -- 冗余, 列表展示
  period          varchar(7) NOT NULL,    -- '2026-05' (上月)
  formats         varchar(32) NOT NULL,   -- 'html' / 'xlsx' / 'html,xlsx'
  status          varchar(16) NOT NULL DEFAULT 'pending',
    -- pending / running / success / failed / cancelled
  progress        integer DEFAULT 0,      -- 0-100
  file_path       text,                   -- /data/billing-exports/{task_id}.zip
  file_size       bigint,
  error_msg       text,
  started_at      timestamptz,
  finished_at     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  operator        varchar(64) NOT NULL    -- 谁发起的
);
CREATE INDEX idx_bet_status_created ON billing_export_tasks(status, created_at DESC);
CREATE INDEX idx_bet_user_period ON billing_export_tasks(user_id, period);
CREATE INDEX idx_bet_user_status ON billing_export_tasks(user_id, status);  -- 查同 user running 数
```

### 3.2 `billing_export_task_logs` (进度日志, 可选)

```sql
CREATE TABLE billing_export_task_logs (
  id bigserial PRIMARY KEY,
  task_id uuid REFERENCES billing_export_tasks(task_id) ON DELETE CASCADE,
  ts timestamptz DEFAULT now(),
  level varchar(8),  -- info/warn/error
  msg text
);
CREATE INDEX idx_betl_task ON billing_export_task_logs(task_id, ts);
```

### 3.3 自动清理 (30 天)

**用现有 `DAILY_STMT_CRON` 调度器** 加一个 job:
```go
// 每天 03:00 跑
func PruneExpiredExportTasks(ctx context.Context) {
    cutoff := time.Now().AddDate(0, 0, -30)
    // 物理文件
    filepath.Walk("/data/billing-exports/", func(path string, info os.FileInfo, err error) error {
        if info.ModTime().Before(cutoff) { os.Remove(path) }
        return nil
    })
    // DB 记录
    db.Where("created_at < ?", cutoff).Delete(&BillingExportTask{})
}
```

---

## 4. Worker Pool 设计

### 4.1 限流：每用户 ≤ 2

```go
// internal/billing/export_worker.go

var (
    userSemaphores = make(map[int]chan struct{})  // user_id → semaphore(2)
    semLock        sync.Mutex
)

// GetUserSem 获取某 user_id 的信号量
func GetUserSem(userID int) chan struct{} {
    semLock.Lock()
    defer semLock.Unlock()
    sem, ok := userSemaphores[userID]
    if !ok {
        sem = make(chan struct{}, 2)  // 2 个槽位
        userSemaphores[userID] = sem
    }
    return sem
}

// TryEnqueue 尝试入队, 满了返 false
func TryEnqueue(task *ExportTask) error {
    sem := GetUserSem(task.UserID)
    select {
    case sem <- struct{}{}:
        go func() {
            defer func() { <-sem }()  // 释放
            processExportTask(task)
        }()
        return nil
    default:
        return errors.New("user already has 2 running tasks")
    }
}
```

**`map[userID]chan struct{}` 无限增长问题**：30 天清理时同步清 map。

### 4.2 Worker 池

```go
var exportQueue = make(chan *ExportTask, 100)

func StartExportWorker(ctx context.Context) {
    for i := 0; i < 2; i++ {  // 2 个全局 worker
        go func(workerID int) {
            for task := range exportQueue {
                processExportTask(ctx, task, workerID)
            }
        }(i)
    }
}
```

**Q3 决策为"每用户 ≤ 2"，但全局 worker 是 2**——2 个 worker 够 27 账号 × 2 个并发 = 最多 54 个排队等不到 worker。**实际不会出现这个并发**，因为:
- 10 人内部用
- 月初 1-5 号高峰，财务大约 2-3 个同时点
- 27 账号轮流点，单账号不可能 1 个财务狂点 2+ 次

**如果真出现排队**：worker 串行处理，第 3+ 任务等 worker 空出来。

### 4.3 任务状态机

```
pending ──> running ──> success
   │           │
   │           └──> failed
   │
   └──> cancelled  (仅 pending 可取消)
```

---

## 5. 账单生成器

### 5.1 4 字段 × 2 维度

**维度 1: 按天聚合** (30 行)
```sql
SELECT 
  DATE(to_timestamp(created_at)) AS day,
  SUM(prompt_tokens) AS prompt,
  SUM(completion_tokens) AS completion,
  SUM(cache_creation_tokens) AS cache_creation,
  SUM(cache_read_tokens) AS cache_read,
  SUM(quota) / 500000.0 AS revenue_usd
FROM logs
WHERE user_id = ? AND created_at >= ? AND created_at < ?
  AND type = 2  -- 排除 type=3/5
GROUP BY day
ORDER BY day;
```

**维度 2: 按模型聚合** (N 个 model)
```sql
SELECT 
  model_name,
  SUM(prompt_tokens) AS prompt,
  SUM(completion_tokens) AS completion,
  SUM(cache_creation_tokens) AS cache_creation,
  SUM(cache_read_tokens) AS cache_read,
  SUM(quota) / 500000.0 AS revenue_usd
FROM logs
WHERE user_id = ? AND created_at >= ? AND created_at < ?
  AND type = 2
GROUP BY model_name
ORDER BY revenue_usd DESC;
```

### 5.2 HTML 模板

参考 RFC 草案 (`statement.html`):
- 头部: 客户名 / 周期 / 生成时间
- 汇总卡: 4 token 字段 + 合计 USD
- 按天明细表: 30 行
- 按模型明细表: N 行
- 业务规则标记: 0 output 兔单 / 图片生成

### 5.3 XLSX 多 Sheet

```
Sheet 1: 汇总 (4 token + USD)
Sheet 2: 按天明细
Sheet 3: 按模型明细
```

### 5.4 ZIP 打包

```go
import "archive/zip"

func PackZip(taskID string, html, xlsx []byte) (string, int64, error) {
    path := "/data/billing-exports/" + taskID + ".zip"
    f, _ := os.Create(path)
    defer f.Close()
    w := zip.NewWriter(f)
    
    if html != nil {
        fw, _ := w.Create("statement.html")
        fw.Write(html)
    }
    if xlsx != nil {
        fw, _ := w.Create("statement.xlsx")
        fw.Write(xlsx)
    }
    w.Close()
    
    info, _ := os.Stat(path)
    return path, info.Size(), nil
}
```

---

## 6. SPA 设计

### 6.1 客户对账单页 `/billing/customers` (改)

```
┌─────────────────────────────────────────────────────────────┐
│  [RangePicker: 本月 1 号 ~ 今天]  [刷新]                    │
│  KPI 卡: 27 客户 | 总收入 $X,XXX | 1.2M tokens              │
├─────────────────────────────────────────────────────────────┤
│  表格 (20 行/页, 排序):                                      │
│  用户名  │ 输入  │ 输出  │ 缓存创建 │ 缓存命中 │ USD │ 操作 │
│  user_alpha │ 1.2M  │ 320K  │ 0        │ 0        │ $338│[上月账单]│
│  upstream_001│ ... │ ...   │ ...      │ ...      │ ...  │[上月账单]│
└─────────────────────────────────────────────────────────────┘

点 "上月账单" → Modal:
  选格式: ☐ HTML ☐ XLSX  (默认都勾)
  [确认] → 调 POST /export-last-month → 弹 toast "任务已创建, 转到任务中心"
```

### 6.2 任务中心 `/billing/exports` (新页面)

```
┌─────────────────────────────────────────────────────────────┐
│  任务列表 (50 行/页):                                         │
│  TaskID │ 用户  │ 周期    │ 状态    │ 进度 │ 创建时间      │ 操作  │
│  abc123 │ user_alpha│ 2026-05 │ running │ 65%  │ 17:30        │ -     │
│  def456 │ upstream_001│ 2026-05 │ success │ 100%│ 17:25     │ [下载]│
│  ghi789 │ Seven  │ 2026-05 │ failed  │ 30%  │ 17:20        │ [重试]│
│  jkl012 │ user_alpha│ 2026-05 │ pending │ 0%   │ 17:35        │ [取消]│
└─────────────────────────────────────────────────────────────┘
```

**前端轮询**: `setInterval` 5s 拉一次, 只更新变化行。

---

## 7. v1 处理 (Q5 决策变更: 2026-06-14 20:39 v1 直接下线)

**RFC 原计划** (2026-06-14 PR #7 定稿):
| 端点 | 处理 |
|---|---|
| v1 18 端点 | 保留 6 个月, 不改 |
| 3 个 SPA 页面 | 保留 6 个月 |
| 2026-12-14 | v1 端点返 410 Gone, SPA 3 页面重定向 v2 |

**实际执行** (2026-06-14 20:39 用户决策变更新):
| 端点 | 处理 |
|---|---|
| v1 18 端点 | **2026-06-14 20:39 直接下线, 返 404** (不是 410) |
| 3 个 SPA 页面 | **2026-06-14 20:39 直接删除** |
| DB 表 | 移到 `archive` schema (只读, 不进业务逻辑) |
| 文档 | 移到 `archive/v1-docs/` |
| Go 业务代码 | 全部删除 (git history 保留) |
| scheduler | 删 `runDailyBilling` cron |

**变更原因**: v2 8 PR 全过, v1 数据 0 行, v1 业务规则 R1-R5 全被 v2 复用, 无业务断点.
具体处置: 见 `docs/CHANGELOG.md` 2026-06-14 (续 2) 节, `archive/v1-docs/README.md`.

**业务规则延续**: v1 5 条规则 (R1-R5: 零输出免单 / 图片标记 / 退款不计 / 错误不计 / 未匹配上游不计) 全在 v2 复用, 详见 `docs/BILLING-v2-RULES.md` §1.

---

## 8. 部署

### 8.1 部署步骤 (PR #8 完成, 2026-06-14)

1. 远端 ECS `/opt/api-ops/docker-compose.yml` 加 volume 挂载:
   ```yaml
   services:
     api:
       volumes:
         - /data/billing-exports:/data/billing-exports
   ```
2. 跑 migration: `psql -f migrations/2026-06-14-billing-v2-tables.sql` 创 2 张表 + 3 索引
3. `chown 100:101 /data/billing-exports` (容器 app user uid=100, gid=101)
4. 推镜像: `docker buildx build --platform linux/amd64 -t api-ops:latest --load .`
5. `docker save | ssh root@api-ops.example.com 'docker load + compose up -d --no-deps api'`
6. 公网 8091 验证 6 端点 (curl)
7. 创建 1 个测试任务 + 下载 zip + 解压校验 4 token 字段

### 8.2 部署发现 + 修复的 4 个 bug

| # | Bug | 修法 |
|---|-----|------|
| 1 | `cache_creation_tokens` 列不存在 (newapi 用 `other->>'cache_tokens'`) | SQL 改 JSONB 提取, struct/handler/SPA 同步合并为单字段 `cache_tokens` |
| 2 | HTML 模板 `template: statement.html:39:58: executing ... can't evaluate field CacheCreationTokens` | 删 2 个 `<dt>`, 合并 2 个 `<th>` 为单 `缓存 tokens` (PR #3 模板没同步 v2 schema) |
| 3 | XLSX headers 残留 `缓存创建 tokens` / `缓存命中 tokens` | `statement_format.go` 3 处 headers 同步合并 (汇总/按天/按模型) |
| 4 | Dockerfile 不 COPY 模板 + zip 写 `/data/billing-exports/` permission denied | Dockerfile 加 `COPY internal/billing/templates/ /app/internal/billing/templates/`; `findTemplatePath` 加 `/app/` 候选; chown 100:101 `/data/billing-exports` |

### 8.3 部署关键经验 (后续 PR 参考)

1. **新api 字段一定先打 SQL 验证** — `information_schema.columns` 查存在性, 不要从字段名猜
2. **容器内读 `other` JSONB** — `other->>'字段'` (text) 然后 `::bigint`, 不是 `other->'字段'->>'...'`
3. **Dockerfile COPY 覆盖** — 模板、静态资源、SQL migration files、CA certs 所有代码运行时读的路径
4. **容器外目录 mount 进容器** — 用容器内 user 写, 一定 chown
5. **多阶段 build cache 复用** — `/tmp/gocache` 挂载做 cache 目录
6. **buildx `--platform linux/amd64 --load`** — mac arm 64 build 不能直接推 linux
7. **macOS Docker Desktop 8088 占用** — 远程端口用 8091, 本地映射 8088
8. **测试清理 `defer os.RemoveAll` 顺序** — 必须在读 zip 之后调, 不然删早了

### 8.4 Dockerfile 模板挂载

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

### 8.5 公网验证 (PR #8 完成)

6 端点全过, 真实数据 (user_alpha 5 月 2026-05):
- `/api/dashboard/today` HTTP 200
- `/api/billing/v2/customer/current-month-overview` 真实数据
- 任务 6 秒内 success, progress 100, file_size 11.6KB
- ZIP 解压: README + statement.html (9.9KB) + statement.xlsx (9.5KB OOXML)
- XLSX sharedStrings 校验: 7 列表头单字段 `缓存 tokens`
- HTML 合计行: 1,002,849 调用 / 101.7亿输入 / 126.2亿输出 / 12.5亿 cache / **$70,226.82 USD**

详细报告: `docs/test-reports/billing-v2-pr8-deploy-2026-06-14.md`

---

## 9. 工作量 + PR 切分 (全部完成 ✅)

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| **#1** | RFC 文档 (本文件) + DB schema migration | `5e464d4` | 0.5 天 |
| **#2** | Worker pool + semaphore + 任务表 CRUD (Repo 层) | `8e032e6` | 1 天 |
| **#3** | 账单生成器 (HTML 模板 + XLSX 多 sheet) + ZIP 打包 | `c63fccb` | 1.5 天 |
| **#4** | API 6 端点 (含 RBAC + 限流 + 下载流) | `d5206c8` | 1.5 天 |
| **#5** | SPA `/billing/customers` 默认页 (改 1 个) | `0329a2d` | 1 天 |
| **#6** | SPA `/billing/exports` 任务中心 (新页面) | `d6f7460` | 1 天 |
| **#7** | 单测 (worker 限流 / HTML 渲染 / ZIP 打包) + 文档同步 | `9ee7e65` | 1 天 |
| **#8** | **远端部署 (挂 volume + 跑 migration) + 公网验证** | **`f6dceb0`** | **0.5 天** |
| **总计** | | | **8 天** |

---

## 10. 测试用例

### 10.1 单元测试

- `TestTryEnqueue_SameUser_2Tasks`：同 user 入队 2 个成功，第 3 个返 "user already has 2 running"
- `TestTryEnqueue_DifferentUsers`：不同 user 互不干扰
- `TestProcessExportTask_Pending2Running2Success`：完整状态机跑通
- `TestProcessExportTask_LongQuery_Timeout`：5s RoDB timeout 触发，status=failed
- `TestPackZip_HTMLOnly`：仅 HTML 模式，zip 内 1 文件
- `TestPackZip_HTMLAndXLSX`：两者都要，zip 内 2 文件

### 10.2 集成测试

- `TestEnd2End_Generateuser_alphaLastMonth`：模拟 1 个用户 1 月 logs，生成 → 成功 → 下载 → 解压 → 校验 4 字段
- `TestConcurrent_3SameUser_2Running`：3 个同 user 并发，1 个 429 拒绝
- `TestAutoPrune_30Days`：30 天前任务被删

---

## 11. 风险

| 风险 | 概率 | 兜底 |
|---|---|---|
| **HTML 模板里注入 logs 内容**（XSS）| 中 | `text/template` 走自动 escape；用 `text/template` 而非 `html/template` 防止 XSS |
| **RoDB 5s timeout 不够** | 低 | 1 月数据 5w 行 × 50ms = 2.5s 够。极端情况可改 10s |
| **ZIP 文件名冲突** | 低 | `task_id` uuid 命名, 概率 0 |
| **30 天清理误删** | 低 | 留 1 周宽限 (35 天) |
| **下载 token 校验** | 中 | 1h JWT, 跟现有 token 体系对齐 |

---

## 12. 跟 v1 的关系

| 维度 | v1 | v2 |
|---|---|---|
| 端点数 | 18 | 6 |
| SPA 页面 | 3 | 2 (1 改 1 新) |
| 数据源 | RoDB (但缺 4 token 字段详细分布) | RoDB (全 4 字段 + 4 维度) |
| 同步/异步 | 同步 | **异步** (任务中心) |
| 输出格式 | CSV / XLSX | HTML / XLSX / ZIP |
| 并发限制 | 无 | **每用户 ≤ 2** |
| 业务规则 | 5 条全在代码 | 5 条同 v1, **不动** |

---

## 13. 评审检查清单

请你评审前过一下：

- [ ] Q1-Q5 决策我理解对了没
- [ ] 4 字段映射 (newapi logs) 对没
- [ ] DB schema 3 张表字段够没
- [ ] Worker 限流 (每用户 2) 粒度对没
- [ ] HTML / XLSX / ZIP 内容设计
- [ ] SPA 2 个页面布局
- [ ] v1 保留 6 个月时间窗口 (2026-12-14)
- [ ] 8 个 PR 切分粒度对没

**如有改动, 直接说, 我重写 RFC**。**确认后开 PR #2 (worker pool + semaphore + 任务表 CRUD)**。

---

## 附: RFC 评审邮件模板 (可选)

```
@user RFC 评审 BILLING v2: docs/BILLING-v2-RFC.md

5 个 Q&A 决策已记录。
8 个 PR 切分 (8 天工作量)。
v1 保留 6 个月 (2026-12-14 后 410)。

请评审: docs/BILLING-v2-RFC.md
如确认开 PR #2 (worker + 任务表 CRUD)。
```

---

## BILLING 累计成果 (跨 v2 / v3 / v4, 2026-06-15)

> **共享章节**: v2/v3/v4 RFC 同步包含, 财务/运营看 1 处即可了解全貌.

| 模块 | 状态 | 端点 | SPA | 任务中心 | 公网验证 |
|---|---|---|---|---|---|
| v1 (已下线) | ❌ 2026-06-14 20:39 | 0 (原 18) | 0 (原 3) | — | 全 404 |
| **v2 客户对账** | ✅ 2026-06-14 (PR #1-#8) | **6** | **1** (`/billing/customers`) | **1** (`/billing/exports`, 复用) | 真实 27 用户 + 11.6KB ZIP |
| **v3 上游对账** | ✅ 2026-06-14 (PR #1-#7) | **5** | **1** (`/billing/v3/upstream`) | 复用 v2 | 真实 5 vendor 39 channel |
| **v4 利润分析** | ✅ 2026-06-14 (6 PR 合并 4 commit) | **1** | **1** (`/billing/v4/profit`, 4 tab) | — | 21 user $5090 rev $4168 cost $921 profit |
| upstream_pricing (已下架) | ❌ 2026-06-14 23:43 | 0 (原 4) | 0 (原 1) | — | 全 404 |
| **合计活跃** | | **30** | **6** | **1** | — |

### BILLING 累计数据点

- **30 个活跃端点**: v2 (6) + v3 (5) + v4 (1) + 18 其他模块 (dashboard 4 + monitor 1 + auth 2 + users 1 + vendors 4 + channel-mappings 1 + ops 杂项) = 30
- **6 个 SPA 页面**: 总览 Dashboard + 监控中心 ChannelHealth + 对账中心 (v2 customers / v2 exports / v3 upstream / v4 profit) + 供应商管理 (Vendors / VendorManagement) + 登录页 = 6
- **1 个任务中心**: `/billing/exports` 复用 v2/v3 两种 kind (customer/upstream), 5s 轮询 + 下载 + 取消 + 重试

### 累计 PR + commit 时间线

| 模块 | PR 数 | Commit 范围 | 工作量 |
|---|---|---|---|
| v1 (已下线) | — | 2026-06-14 `72d82d4` 下线 | — |
| v2 客户对账 | 8 | `5e464d4` ~ `f6dceb0` | 8 天 |
| v3 上游对账 | 7 | `df40966` ~ 待 `PR #7` | 7 天 |
| v4 利润分析 | 6 (合并 4 commit) | `7150dfb` + `a6b2b8d` | 3.9 天 |
| upstream_pricing (已下架) | 1 | 2026-06-14 23:43 | 0.5 天 |
| **BILLING 总投入** | | | **≈ 19.4 天** |

### 业务规则累计 (v1 → v4 一脉相承)

- **R1 零输出免单** / **R2 图片生成标记** / **R3 退款不计** / **R4 错误不计** / **R5 未匹配上游不计** — v1 制定, v2/v3/v4 复用
- **R6 缺渠道折扣** — v3 新增 (channel_vendor_map.discount 缺失), v4 复用
- **5 业务规则在 4 模块零断点** (BILLING 业务连续性最强)

### 财务月初工作流 (1-5 号标准流程)

```
1. 打开 v4 利润分析, 看汇总卡 (本月总利润 / 毛利率)
2. 切到 v4 客户 tab (哪些客户亏钱 / 按毛利排名)
3. 切到 v4 上游 tab (哪些 vendor 亏钱)
4. 切到 v2 生成 27 客户对账 ZIP (HTML + XLSX)
5. 切到 v3 生成 5 vendor 对账 ZIP (HTML + XLSX)
6. 用 ZIP 精确数算成本 + 付款上游 + 发账单给客户
```

4 模块一起跑, 财务月度对账工作量从"1 周"压到"1 天" (v3 反推公式覆盖率 100% + v2 异步 ZIP 不阻塞 + v4 汇总卡秒看趋势).
