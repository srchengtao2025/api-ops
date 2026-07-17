-- 2026-07-15 v2 BILLING 客户对账：加 period_start / period_end 字段
--
-- 背景：客户要求"按指定日期导出对账单"，旧 period 字段 varchar(7) 只装 YYYY-MM，
--       不够装任意日期区间 (e.g. 2026-06-15~2026-07-01)。
--
-- 加 2 个 bigint 字段 (Unix 秒)：
--   period_start  含 (e.g. 2026-06-15 00:00:00 BJ 的 epoch)
--   period_end    不含 (e.g. 2026-07-01 00:00:00 BJ 的 epoch, 区间 [start, end))
--
-- period 字段保留 (varchar(7))：
--   - 月份对账 (current-month / last-month) 写 "YYYY-MM" (e.g. "2026-06")
--   - 任意日期区间对账 写 "custom" (start/end 在新字段里)
--
-- worker 读时优先级：
--   1. period_start/end 都不为 0 → 走 [start, end) 任意区间
--   2. period_start/end 都为 0 → 走 period (YYYY-MM) 月份区间
--
-- 现有历史数据：所有 period != "custom" 的 task, period_start/end 默认 NULL/0
-- (worker 走 period 解析, 行为跟之前一样, 向后兼容)

BEGIN;

ALTER TABLE billing_export_tasks
  ADD COLUMN IF NOT EXISTS period_start bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS period_end   bigint NOT NULL DEFAULT 0;

-- 索引：worker 拉任务时按 (kind, status, created_at) 顺序扫,
--       但前端要按 (user_id, period_start, period_end) 查某个区间的任务历史,
--       补 1 个复合索引 (覆盖前端 SPA "客户对账" 页面 filter)
CREATE INDEX IF NOT EXISTS idx_bet_user_period_range
  ON billing_export_tasks (user_id, period_start, period_end);

COMMENT ON COLUMN billing_export_tasks.period_start IS 'Unix 秒 (含), 0 = 走 period 字段 (YYYY-MM 月份对账)';
COMMENT ON COLUMN billing_export_tasks.period_end   IS 'Unix 秒 (不含, 区间 [start, end)), 0 = 走 period 字段';

-- rollback:
--   DROP INDEX IF EXISTS idx_bet_user_period_range;
--   ALTER TABLE billing_export_tasks DROP COLUMN IF EXISTS period_end, DROP COLUMN IF EXISTS period_start;
COMMIT;
