-- BILLING v3 上游对账 5min cache (2026-06-15)
--   docs/BILLING-v3-RULES.md
--   配合: PR (v3-cache-aggregate) - scheduler 5min tick 写 cache
--
-- 1 张新表:
--   - ops_upstream_summary_5min: 上游对账 5min 聚合 cache
--     字段: vendor_code, period_label, period_start, period_end,
--           request_count, revenue, cost, profit, ts_bucket
--
-- 设计动机 (v3 月对账痛点):
--   - 旧: CalcUpstreamStatement(vendor, month) 每次按需跑 1 SQL 扫 logs 1.9M 行
--   - 月对账 1-5 号老板跑上月账单 → 5 vendor 同时跑 = 5 SQL 短时重算
--   - 新: scheduler 每 5min tick 预算"本月至今 + 上月" 2 个 period, 写 cache
--   - 读取 (handler / worker / 报表) 优先读 cache, 5min 延迟可接受
--
-- PRIMARY KEY: (vendor_code, period_label, ts_bucket) 允许 UPSERT 幂等
-- 数据量: 5 vendor × 2 period × 12 tick/小时 × 24h = 2880 行/天, 30 天保留 ≈ 86k 行
--
-- 跟 monitor 5min cache 命名一致 (channel_health_5min / cache_logs_summary_5min),
-- 走同一套 scheduler tick 模式 (参考 runMonitorTick + time.NewTicker(5*time.Minute))

BEGIN;

-- =========================================================================
-- ops_upstream_summary_5min 上游对账 5min cache
-- =========================================================================

CREATE TABLE IF NOT EXISTS ops_upstream_summary_5min (
  id              bigserial PRIMARY KEY,
  vendor_code     varchar(64)  NOT NULL,
  period_label    varchar(16)  NOT NULL,        -- 'current-month' | 'last-month'
  period_start    bigint       NOT NULL,        -- unix 秒, 含
  period_end      bigint       NOT NULL,        -- unix 秒, 含 (本月至今 endOfNow / 上月 lastOfPrevMonth)
  request_count   bigint       NOT NULL DEFAULT 0,
  revenue         numeric(20,8) NOT NULL DEFAULT 0,  -- 客户消耗 USD
  cost            numeric(20,8) NOT NULL DEFAULT 0,  -- 上游成本 USD (反推)
  profit          numeric(20,8) NOT NULL DEFAULT 0,  -- revenue - cost
  ts_bucket       timestamptz  NOT NULL,             -- 5min 对齐 (tick 时刻)
  created_at      timestamptz  NOT NULL DEFAULT now(),
  updated_at      timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uq_ops_upstream_summary_vendor_period_bucket
    UNIQUE (vendor_code, period_label, ts_bucket)
);

-- 查询索引: handler 读"最新 1 桶" → 命中 PK (vendor_code, period_label, ts_bucket)
-- 不加额外索引, PK 足够

COMMENT ON TABLE  ops_upstream_summary_5min IS 'BILLING v3 上游对账 5min 聚合 cache; scheduler 5min tick 写, handler/worker 优先读';
COMMENT ON COLUMN ops_upstream_summary_5min.vendor_code  IS '上游 vendor code, 跟 upstream_vendors.code 关联';
COMMENT ON COLUMN ops_upstream_summary_5min.period_label IS 'period 标签: current-month (本月至今) | last-month (上月完整)';
COMMENT ON COLUMN ops_upstream_summary_5min.ts_bucket    IS '5min 对齐的 tick 时刻; UNIQUE (vendor_code, period_label, ts_bucket) 幂等 UPSERT';

COMMIT;
