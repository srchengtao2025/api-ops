# api-ops 数据架构方案（最终版）

> **生成时间**: 2026-06-14
> **作者**: Mavis
> **目的**: 落定"每个数据走哪条路径"——三角（admin API / RDS 直连 / 本地 cache）的最终分工

---

## 0. 铁律（Hard Constraints）

1. **logs 表全量走 RDS 直连**——`SELECT ... FROM logs WHERE ... LIMIT N`
2. **admin API 永远不拉 logs**（`ItemsPerPage=10` 硬编码 + 无增量游标 = 不能用）
3. **元数据全量走 admin API 拉取**——5min 一次全量翻页，18 次请求 / 7 秒
4. **W3 admin API 走 leader election**——多实例只 1 个跑，其它只读 cache
5. **所有 handler 默认走本地 cache，零外部依赖**

---

## 1. 三角路径总览

| 路径 | 工具 | 限流 | 实时性 | 占比 |
|------|------|------|--------|------|
| **RDS 直连** | PG 协议 SELECT 聚合 | 业务直连，无应用层限流 | ≤1min | 主路径（≥80% 流量）|
| **admin API** | HTTPS + Bearer + New-Api-User | 480/3min 全局共享 | ≤5min | 辅助（≤15% 流量）|
| **本地 cache** | OPS DB 表 + GORM | 0 外部依赖 | 实时 | handler 100% 走 cache |

---

## 2. 数据路径详细分工（穷举所有业务数据）

### 类别 A：**实时监控类**（L1，≤1min 必须有）

| 数据 | 走哪 | 怎么取 | 频率 | 写哪个 cache |
|------|------|--------|------|---------------|
| **logs 1min 桶（global）** | RDS 直连 | `SELECT count(*), sum(quota), ... FROM logs WHERE created_at >= ? AND created_at < ?` | 1min tick | `cache_logs_summary_5min` (channel_id=0) |
| **logs 1min 桶（per channel）** | RDS 直连 | `SELECT channel_id, count(*), sum(quota), percentile_cont ... FROM logs WHERE created_at >= ? AND created_at < ? AND channel_id > 0 GROUP BY channel_id` | 1min tick | `cache_logs_summary_5min` (channel_id=N) |
| **logs 1min 桶（per model）** | RDS 直连 | `SELECT model_name, count(*), sum(quota), sum(tokens) FROM logs WHERE ... GROUP BY model_name` | 1min tick | `cache_logs_summary_by_model` |

**handler 走 cache**：
- `/api/dashboard/today` → `cache_logs_summary_by_model` 累加当日
- `/api/monitor/channels` → `cache_logs_summary_5min` 5 桶求和
- `/api/monitor/channels/:id/health` → `cache_logs_summary_5min` 12 桶（5min/1h）求和 + 健康分公式
- `/api/ai/cluster/tick` → 直读 RDS 最近 1h 做聚类

**RDS 实际开销**：1min tick × 3 个 SQL × 50ms = **0.25% CPU 占用**（1.6GB 内存小实例无压力）

---

### 类别 B：**元数据类**（L3，≤5min）

| 数据 | 走哪 | 怎么取 | 频率 | 写哪个 cache |
|------|------|--------|------|---------------|
| **channels 元数据** | **admin API** | `GET /api/channel/?p=0..5` 翻页（5 次）| 5min tick | `upstream_channel_cache` |
| **users 元数据** | **admin API** | `GET /api/user/?p=0..12` 翻页（12 次）| 5min tick | `upstream_user_cache` |
| **tokens 元数据** | **admin API** | `GET /api/token/?p=0` 翻页（1 次）| 5min tick | `upstream_token_cache` |
| **options 系统配置** | **admin API** | `GET /api/option/` | 5min tick | `upstream_options_cache`（新增表）|
| **vendors 上游供应商** | RDS 直连 | 业务元数据，不在 newapi | 仅初始化 | 静态 DDL（不需 cache）|

**leader election**：
- key: `api_ops:sync:metadata:leader`
- SETNX 5min TTL，多实例只 1 个跑
- 单实例 18 次/5min = 2.25% 限流配额；5 实例仍是 2.25%（**这是关键收益**）

**RDS 不背元数据**：因为 channels/users/tokens 表**只有 schema 没 SELECT 权限**（已实测 `permission denied`）。这是**功能上不能**，不是技术选型上"应该"。

---

### 类别 C：**客户对账 + 上游对账**（L2，≤1h）

| 数据 | 走哪 | 怎么取 | 频率 | 写哪个 cache |
|------|------|--------|------|---------------|
| **logs 按 user×day 汇总** | RDS 直连 | `SELECT user_id, username, to_char(to_timestamp(created_at),'YYYY-MM-DD') as d, count(*), sum(quota), sum(prompt_tokens), sum(completion_tokens) FROM logs WHERE ... GROUP BY 1,2,3` | 5min tick | `cache_logs_daily_user`（新增表）|
| **logs 按 channel×day 汇总** | RDS 直连 | `SELECT channel_id, channel_name, to_char(...) as d, count(*), sum(quota) FROM logs WHERE ... GROUP BY 1,2,3` | 5min tick | `cache_logs_daily_channel`（新增表）|
| **upstream_pricing（上游 1M 单价）** | 自有 DB | 业务自己维护 | 启动期 | 静态表（已有）|

**handler 走 cache**：
- `/api/billing/customer/:user_id/preview` → `cache_logs_daily_user` WHERE user_id
- `/api/billing/customer/generate` → `cache_logs_daily_user` 范围聚合
- `/api/billing/upstream/generate` → `cache_logs_daily_channel` + `upstream_pricing` JOIN

**RDS 实际开销**：5min tick × 2 SQL × 200ms = **0.7% CPU 占用**

**为什么不实时算**：客户对账业务用户月初看（offline），5min 延迟无感；实时算要扫 1.98M 行，5min 聚合 cache 才 5000 行，**压 400 倍**。

---

### 类别 D：**历史明细回溯**（L4，10s 内按需）

| 数据 | 走哪 | 怎么取 | 频率 |
|------|------|--------|------|
| **单条 log 明细（按 request_id / user_id / channel_id）** | RDS 直连 | `SELECT * FROM logs WHERE ... LIMIT 1000` | 业务低频 |

**不 cache 明细**：1.98M 行全量 cache 翻 100 倍 disk，且 99% 明细永远没人查。**直连 RDS `LIMIT 1000` 50ms 内返回**。

**handler**：
- `/api/billing/customer/statements/:id/lines` → RoDB 直查（按 statement_id 查明细）
- `/api/audit/logs` → RoDB 直查
- `/api/ai/diagnose` → RoDB 直查（异常 100 条样本）

---

### 类别 E：**审计 / AI / 告警**（L1 派生，0 外部依赖）

| 数据 | 走哪 | 说明 |
|------|------|------|
| **audit_logs** | 自有 DB 写 | 4xx/5xx 不入，业务变更入（Gin middleware）|
| **ai_diagnoses / ai_error_clusters / ai_reports** | 自有 DB 写 | 5min cluster tick + 09:00 daily report |
| **alert_rules / alert_histories / alert_actions** | 自有 DB 写 | 1min tick 读 L1 cache 算分，写本表 + 飞书 |
| **error_kb_entries** | 静态 YAML → 启动时导入 | 16 条 |
| **system_config** | 自有 DB 写 | 飞书 webhook / ai_provider 等运行时配置 |
| **tier_threshold** | 自有 DB 写 | 客户分层阈值（svip/vip/default）|

**完全不依赖外部**：handler 0 外部依赖，崩了也只影响 L1 实时数据。

---

## 3. 6 张 cache 表 DDL

```sql
-- 1. 实时监控：1min 桶聚合
CREATE TABLE cache_logs_summary_5min (
    channel_id       BIGINT NOT NULL,         -- 0 = global
    bucket_ts        BIGINT NOT NULL,         -- 分钟级时间戳 (已 /60*60)
    request_count    BIGINT NOT NULL DEFAULT 0,
    success_count    BIGINT NOT NULL DEFAULT 0,
    error_count      BIGINT NOT NULL DEFAULT 0,
    quota            BIGINT NOT NULL DEFAULT 0,  -- type=2|6 的 quota 总和
    prompt_tokens    BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    p50_latency_ms   INT NOT NULL DEFAULT 0,
    p95_latency_ms   INT NOT NULL DEFAULT 0,
    p99_latency_ms   INT NOT NULL DEFAULT 0,
    avg_latency_ms   INT NOT NULL DEFAULT 0,
    error_rate       REAL NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, bucket_ts)
);
CREATE INDEX idx_cache_summary_5min_ts ON cache_logs_summary_5min(bucket_ts);
-- 7 天滚动删除

-- 2. 实时 KPI：按 model
CREATE TABLE cache_logs_summary_by_model (
    bucket_ts        BIGINT NOT NULL,
    model_name       TEXT NOT NULL,
    request_count    BIGINT NOT NULL DEFAULT 0,
    success_count    BIGINT NOT NULL DEFAULT 0,
    error_count      BIGINT NOT NULL DEFAULT 0,
    quota            BIGINT NOT NULL DEFAULT 0,
    prompt_tokens    BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_ts, model_name)
);
CREATE INDEX idx_cache_model_ts ON cache_logs_summary_by_model(bucket_ts);
-- 7 天滚动删除

-- 3. 客户对账：按 user×day
CREATE TABLE cache_logs_daily_user (
    day              DATE NOT NULL,
    user_id          BIGINT NOT NULL,
    username         TEXT NOT NULL,
    request_count    BIGINT NOT NULL DEFAULT 0,
    success_count    BIGINT NOT NULL DEFAULT 0,
    error_count      BIGINT NOT NULL DEFAULT 0,
    quota            BIGINT NOT NULL DEFAULT 0,
    prompt_tokens    BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    model_breakdown  JSONB NOT NULL DEFAULT '{}'::jsonb,  -- {"llm-model-b-large": 1234, ...}
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (day, user_id)
);
CREATE INDEX idx_cache_daily_user_user ON cache_logs_daily_user(user_id);
-- 30 天滚动删除

-- 4. 上游对账：按 channel×day
CREATE TABLE cache_logs_daily_channel (
    day              DATE NOT NULL,
    channel_id       BIGINT NOT NULL,
    channel_name     TEXT,
    request_count    BIGINT NOT NULL DEFAULT 0,
    success_count    BIGINT NOT NULL DEFAULT 0,
    error_count      BIGINT NOT NULL DEFAULT 0,
    quota            BIGINT NOT NULL DEFAULT 0,
    prompt_tokens    BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    model_breakdown  JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (day, channel_id)
);
CREATE INDEX idx_cache_daily_channel_ch ON cache_logs_daily_channel(channel_id);
-- 30 天滚动删除

-- 5-7. 元数据 cache（admin API 拉取）
-- upstream_channel_cache / upstream_user_cache / upstream_token_cache 已存在
-- 复用现有 schema

-- 8. system_config（已有）
-- options cache（新增）
CREATE TABLE upstream_options_cache (
    key              TEXT PRIMARY KEY,
    value            TEXT,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**总磁盘占用估算**：
- `cache_logs_summary_5min`: 49 channel × 288 桶/天 × 7 天 = **98,784 行** × 200 bytes = 20 MB
- `cache_logs_summary_by_model`: 30 model × 288 × 7 = 60,480 行 × 150 bytes = 9 MB
- `cache_logs_daily_user`: 27 user × 30 day = 810 行 × 500 bytes = 400 KB
- `cache_logs_daily_channel`: 49 ch × 30 day = 1,470 行 × 500 bytes = 700 KB
- 元数据 cache: 几百行 × 1KB = 几百 KB

**合计 ~30 MB**，**完全可忽略**。

---

## 4. 4 个 worker 详细定义

### W1: rds_logs_summary_sync（1min tick）
```go
func LogsSummaryLoop(ctx) {
    t := time.NewTicker(60s)
    for {
        select {
        case <-t.C:
            // 1. 全局桶
            row := rds.Query(`
                SELECT count(*), sum(quota), sum(prompt_tokens), sum(completion_tokens),
                       percentile_cont(0.5) WITHIN GROUP (ORDER BY use_time)*1000 as p50, ...
                FROM logs
                WHERE created_at >= $1 AND created_at < $2
            `, now-1min, now)
            ops.Upsert(channel_id=0, bucket_ts=now/60*60, row)
            
            // 2. per channel
            rows := rds.Query(`
                SELECT channel_id, count(*), ...
                FROM logs
                WHERE created_at >= $1 AND created_at < $2 AND channel_id > 0
                GROUP BY channel_id
            `, now-1min, now)
            for each row: ops.Upsert(channel_id=N, ...)
            
            // 3. per model
            ...
            
            // 4. 7 天外删除
            ops.Exec(`DELETE FROM cache_logs_summary_5min WHERE bucket_ts < $1`, now-7day)
        }
    }
}
```

### W2: rds_logs_daily_sync（5min tick）
```go
func LogsDailyLoop(ctx) {
    t := time.NewTicker(300s)
    for {
        select {
        case <-t.C:
            // 1. 按 user×day 累加（用 ON CONFLICT 增量更新当日）
            rows := rds.Query(`
                SELECT user_id, username,
                       to_char(to_timestamp(created_at), 'YYYY-MM-DD') as d,
                       count(*), sum(quota), sum(prompt_tokens), sum(completion_tokens),
                       jsonb_object_agg(model_name, ...) as model_breakdown
                FROM logs
                WHERE created_at >= $1 AND created_at < $2
                GROUP BY 1, 2, 3
            `, now-1day, now)  // 拉过去 24h 就够（24h 滚动覆盖）
            for each row: ops.Upsert(day, user_id, ...)
            
            // 2. 按 channel×day
            ...
            
            // 3. 30 天外删除
            ops.Exec(`DELETE FROM cache_logs_daily_user WHERE day < $1`, now-30day)
        }
    }
}
```

### W3: admin_api_metadata_sync（5min tick，**leader election**）
```go
func AdminMetadataLoop(ctx) {
    t := time.NewTicker(300s)
    for {
        select {
        case <-t.C:
            // 0. leader election
            got := redis.SETNX("api_ops:sync:metadata:leader", os.Hostname(), 5*time.Minute)
            if !got { continue }  // 我不是 leader, 跳过
            
            // 1. channels
            for p := 0; ; p++ {
                resp := http.GET("/api/channel/?p="+p, Bearer)
                if resp.status == 429 {
                    feishu.Alert("admin API 限流, 暂停 W3 5min")
                    sleep 5min
                    break
                }
                ops.BulkUpsert("upstream_channel_cache", resp.items)
                if len(items) < 10 { break }
            }
            
            // 2. users（同样）
            // 3. tokens（1 页就够）
            // 4. options
        }
    }
}
```

### W0: rds_logs_backfill（启动时一次性，30 天历史）
```go
func BackfillOnce(ctx) {
    // backfill cache_logs_daily_user 和 cache_logs_daily_channel 30 天
    // 用 30 个 1day 桶，每个桶 1 个 SQL 聚合
    // 1.98M / 30 = 6.6 万行/天 × 30 天 = 30 SQL，约 30s 完成
    for day := 0; day < 30; day++ {
        ts := now.AddDate(0, 0, -day)
        rds.Query(`SELECT user_id, ..., to_char(to_timestamp(created_at), 'YYYY-MM-DD'), count(*), sum(quota), ... FROM logs WHERE created_at >= $1 AND created_at < $2 GROUP BY ...`, ts, ts+24h)
        ops.Upsert(...)
    }
}
```

---

## 5. 限流防御

| 防御点 | 实现 | 触发 |
|--------|------|------|
| **429 detection** | HTTP client 看到 429 → 立即 stop W3，sleep 5min，飞书告警 | 第一次 429 |
| **滑动窗 self-throttle** | client 维护 "过去 3min 调了多少次" map，>100/3min 自降速 | 配额 70% |
| **401 detection** | 401 → 立即 stop W3，飞书告警 "admin token 失效" | 第一次 401 |
| **leader election** | Redis SETNX | 多实例部署时 |
| **连接池限制** | RDS 直连 pgxpool max conns=2 | 启动期 |

---

## 6. handler ↔ cache 一一对应

| handler | cache 命中 | 兜底（cache 空时）|
|---------|-----------|------------------|
| GET /api/dashboard/today | cache_logs_summary_by_model 累加 | RDS 直连（最近 24h 聚合）|
| GET /api/dashboard/trend | cache_logs_summary_by_model 30d 桶 | RDS 直连 |
| GET /api/dashboard/top-customers | cache_logs_daily_user 7d | RDS 直连 |
| GET /api/dashboard/top-models | cache_logs_summary_by_model 7d 累加 | RDS 直连 |
| GET /api/dashboard/top-channels | cache_logs_summary_5min 24h 桶 | RDS 直连 |
| GET /api/monitor/channels | cache_logs_summary_5min 5 桶 | RDS 直连 5min 窗 |
| GET /api/monitor/channels/:id/health | cache_logs_summary_5min 12 桶 | RDS 直连 1h 窗 |
| GET /api/monitor/alerts | alert_histories（自有 DB）| — |
| GET /api/billing/customer/:id/preview | cache_logs_daily_user 30d | RDS 直连 |
| GET /api/billing/upstream/generate | cache_logs_daily_channel 30d | RDS 直连 |
| GET /api/billing/profit/analysis | 内存 JOIN cache + pricing | — |
| GET /api/upstream/channels | upstream_channel_cache | RoDB 直连（拒访问则空）|
| GET /api/upstream/users | upstream_user_cache | RoDB 直连 |
| GET /api/upstream/channels/:id | upstream_channel_cache | RoDB 直连 |
| GET /api/audit/logs | audit_logs（自有 DB）| — |
| POST /api/ai/diagnose | RoDB 直查 + KB 静态 | — |
| GET /api/admin/config | system_config | — |
| PUT /api/admin/config | system_config | — |

---

## 7. 多实例部署边界

| 角色 | 跑什么 worker | 跑什么 handler |
|------|---------------|----------------|
| **Instance A (leader)** | W1 + W2 + W3 + W0 | 全部 |
| **Instance B/C**（如部署）| 只跑 W1 + W2（无 admin API 配额）| 全部 |
| **Instance A 挂掉** | Redis SETNX 5min 后 B/C 接管 W3 | 全部 |
| **Instance 全挂** | handler 仍可用（cache 已填）, 0 外部依赖 | 全部 |

---

## 8. 部署+回填顺序

| 步骤 | 动作 | 预计耗时 | 风险 |
|------|------|----------|------|
| S1 | 6 张 cache 表 DDL（AutoMigrate）| 1 min | 0 |
| S2 | W0 backfill 30 天 cache_logs_daily_* | 30-60s | 0 |
| S3 | W1 1min tick 启动（cache_logs_summary_5min）| 立即 | 0 |
| S4 | W2 5min tick 启动 | 立即 | 0 |
| S5 | W3 5min tick + leader election | 立即 | low |
| S6 | 端到端验证（dashboard / monitor / billing）| 5 min | 0 |

**全部 ~10 分钟搞定**。

---

## 9. 风险与回滚

| 风险 | 触发 | 应对 |
|------|------|------|
| RDS 不可达 | 网络 / RDS 维护 | handler 走 RoDB 直查降级（slower）|
| admin API 限流 | 多 client 抢占 | W3 self-throttle + 飞书告警 |
| admin token 失效 | 运营轮换 | 飞书告警，handler 仍用旧 cache（≤5min 滞后）|
| cache 数据错 | 同步 bug | 飞书告警，handler 走 RoDB fallback |
| OPS DB 满 | cache 没滚动 | 7d/30d 滚动删除 job 独立部署 |

---

## 10. 验证清单（部署后跑）

- [ ] 启动 1min 后，cache_logs_summary_5min 至少 49 行
- [ ] 启动 5min 后，cache_logs_daily_user 至少 27×N 天行
- [ ] 启动 5min 后，upstream_channel_cache 至少 44 行
- [ ] 启动 5min 后，upstream_user_cache 至少 107 行
- [ ] dashboard/today 返回非 0
- [ ] monitor/channels 错误率显示非 0
- [ ] billing/customer/47/preview 返回 user_alpha 7 天对账
- [ ] 飞书告警触达测试一次（mock error）
- [ ] 关掉 admin API 模拟 token 失效，飞书告警
- [ ] RDS 网络断开，handler 不挂，dashboard 显示 "数据 X 分钟前更新"

---

## 11. 一次说死：W3 admin API 白名单

| 允许 | 禁止 |
|------|------|
| `GET /api/channel/?p=N` | `GET /api/log/?p=N` |
| `GET /api/user/?p=N` | `GET /api/log/?type=...&start_timestamp=...&end_timestamp=...` |
| `GET /api/token/?p=N` | `GET /api/log/usage` (404) |
| `GET /api/option/` | `GET /api/log/search` |
| `GET /api/user/self` (token 校验) | `GET /api/log/?p=N&page_size=1000` (越权) |

`internal/sync/admin_*` 文件里 import `internal/sync/api_allowlist.go` 的常量，code review grep 守门。

---

## 12. RDS 直连防过载（4 层防御，针对 10 人内部系统）

> **真实业务约束**:
> - 内部系统, **≤10 人使用**（不是你之前担心的 1000+ user）
> - 对账是**月度**（每月 1-5 号看, 平时几乎不查）
> - 单实例部署, **1 台 ECS 1.6GB 内存足够**
>
> 在这个规模下, **之前的 5 层防御过度设计**。**简化版 4 层即可**, **且阈值大幅放宽**。

### 真实压力账（10 人 + 月度对账）

| 项 | 估算 | 备注 |
|----|------|------|
| **RPS 到 api-ops** | **1 req/s** (10 人 × 0.1 req/s) | 运营偶尔刷 dashboard, 不是高频 |
| **到 RDS 直连** | **0.2 req/s** (20% 走直连, 80% 走 cache) | 主要是 ad-hoc 明细查 |
| **RDS 并发 query** | **0.01 并发** (0.2 × 50ms) | **远小于 1**, 几乎永远空载 |
| **admin API** | **0.06 req/s** (3.6 req/5min) | W3 tick + leader election |
| **月度对账** | 1 次 / 月 / 30 天聚合, **~1s 完成** | 业务方月初 5 天看 |
| **单实例 RDS conn** | **峰值 2 conn** (worker tick + 偶发 handler) | 50 上限= 用 4% |

**结论**: **就算 10 人同时刷 + 月初 1 次对账, RDS 实际负载 < 1% CPU**。防过载是"以防万一", 不是"必需"。

### 第 1 层：**连接池硬限制**（单实例即可）

```go
// internal/dal/db.go
sqlDB.SetMaxOpenConns(3)         // 单实例 3 conn (足够, 之前设 50 浪费)
sqlDB.SetMaxIdleConns(2)
sqlDB.SetConnMaxLifetime(10*time.Minute)
sqlDB.SetConnMaxIdleTime(60*time.Second)
```

**算账**:
- 单实例 3 conn × 1 实例 = **3 conn 实际占用 RDS**
- 即使配错忘了这层, RDS 端 50+ conn 也不顶到上限
- **handler 排队等连接概率几乎为 0**（10 人用, 不可能 3 个同时打满）

### 第 2 层：**每查询硬 deadline**（必装）

```go
// 所有 RoDB 查询包 5s ctx
ctx, cancel := context.WithTimeout(r.Request.Context(), 5*time.Second)
defer cancel()
db.WithContext(ctx).Raw(...).Scan(&r)
```

- 即使**对账 30 天聚合**（最重 query 1-2s 完事）也不会触发
- 但**防止极端慢查询**（如全表扫描忘记加 WHERE）占住连接 30s
- 5s 兜底, **任何 query 不会超过 5s**

### 第 3 层：**DB 端 statement timeout**（兜底）

```sql
ALTER ROLE billing SET statement_timeout = '5s';
```

- app 漏 ctx 的兜底
- PG 端强制 5s cancel, 必断
- **RDS 端零配置, 跑一次就生效**

### 第 4 层：**监控 + 飞书告警**（发现用, 不必太严）

```go
// 关键指标: 10s 上报
metrics.Gauge("rds.connections.in_use", rodb.Stats().InUse)  // 告警 >2
metrics.Counter("rds.slow_query_5s",  ...)                 // 告警 >3/min

// 阈值放宽: 10 人用, 触发频率极低
```

### 砍掉的 3 层（vs v1.1）

| 之前 (v1.1) | 现在 (v1.2) | 原因 |
|-------------|------------|------|
| **IP 限速 (5 req/s)** | **不装** | 10 人不可能 1s 内 5 个请求, **10 倍浪费**; 真出问题靠 5s timeout 兜底 |
| **CONNECTION LIMIT 10** | **不装** | 单实例 3 conn 远低于 RDS 默认 100, 装了反而是单点风险（5s 内断不开）|
| **5 实例 leader election** | **不装** | **就 1 实例**, leader election 没必要; 就算扩到 2 实例, 也只 6 conn, 仍 OK |

### 风险评估（10 人 + 月对账）

| 风险 | 概率 | 兜底 |
|------|------|------|
| **用户手抖连点 5 次** | 偶发 | **5s ctx timeout** 自动断, **无感知**（cache 5min 内一致, 重复请求同一数据）|
| **对账 SQL 跑 30s** | 极低 | **5s app ctx + 5s DB statement**, 必断 |
| **10 人同时刷** | 极端 | **3 conn 上限, 排队等待** ~50ms, **几乎无感** |
| **handler bug 死循环** | 极低 | **5s ctx 强制 cancel**, 5s 后回 503 |
| **RDS 整体不可达** | 极低 | handler 走 RoDB fallback（slower but works）, cache 仍可读 |
| **业务扩到 100+ 人** | 1-2 年后再说 | 到时候再装 IP 限速, **当前不浪费工程量** |

### 月度对账特殊性

对账是**月末 / 月初**看的, 不是每天:
- **日常** (1-25 号): W1 1min tick + W2 5min tick + 偶尔 handler = **< 0.5% CPU**
- **月初高峰** (1-5 号): 10 人看 dashboard + 看对账 = **< 2% CPU**
- **月底 28-31 号**: 提前跑 W0 backfill 30 天, 一次性 ~30s, 完了就停

**没有"对账风暴"风险**——10 人不会 1s 内都点 "生成对账"。

### 监控阈值（v1.2 简化版）

| 指标 | 告警阈值 | 行动 |
|------|----------|------|
| `rds.connections.in_use` | **>2 持续 1min** | 飞书告警 (可能有 handler bug) |
| `rds.slow_query_5s_count` | **>3/min** | 飞书告警 (SQL 写错了) |
| `rds.errors.timeout` | **>5/min** | 飞书告警 (RDS 慢 / 网络) |
| `rds.errors.permission_denied` | **>0** | 飞书告警 (GRANT 改了) |

**没有"高 RPS 告警"** —— 10 人用, RPS 1-2 算正常。

---
