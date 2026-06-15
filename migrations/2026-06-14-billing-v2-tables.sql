-- BILLING v2 (2026-06-14)
--   docs/BILLING-v2-RFC.md
--   配合: PR #2 (worker + 任务表 CRUD) / PR #3 (账单生成器) / PR #4 (API 6 端点)
--
-- 3 张新表:
--   - billing_export_tasks: 异步导出任务主表
--   - billing_export_task_logs: 进度日志 (可选, 调试用)
--   - (无) 30 天清理不需新表, 复用 DAILY_STMT_CRON 调度器
--
-- 注意: 跟 v1 18 端点 + billing_statements 表完全独立, 不动 v1 数据
-- v1 保留 6 个月, 2026-12-14 后 v1 端点返 410 Gone

BEGIN;

-- =========================================================================
-- 1) billing_export_tasks 主表
-- =========================================================================

CREATE TABLE IF NOT EXISTS billing_export_tasks (
  id              bigserial PRIMARY KEY,
  task_id         uuid UNIQUE NOT NULL DEFAULT gen_random_uuid(),  -- 暴露给前端
  user_id         integer NOT NULL,
  username        varchar(64) NOT NULL,  -- 冗余, 列表展示
  period          varchar(7)  NOT NULL,    -- '2026-05' (上月)
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
  operator        varchar(64) NOT NULL
);

-- 任务中心列表按 status + created_at DESC
CREATE INDEX IF NOT EXISTS idx_bet_status_created
  ON billing_export_tasks(status, created_at DESC);

-- 单用户历史任务 (PR #4 端点: GET /v2/customer/:uid/tasks)
CREATE INDEX IF NOT EXISTS idx_bet_user_period
  ON billing_export_tasks(user_id, period);

-- 查"同 user_id 已 running 数" (限流: 每用户 ≤ 2)
CREATE INDEX IF NOT EXISTS idx_bet_user_status
  ON billing_export_tasks(user_id, status);

-- =========================================================================
-- 2) billing_export_task_logs 进度日志 (可选, 调试用)
-- =========================================================================

CREATE TABLE IF NOT EXISTS billing_export_task_logs (
  id     bigserial PRIMARY KEY,
  task_id uuid REFERENCES billing_export_tasks(task_id) ON DELETE CASCADE,
  ts     timestamptz DEFAULT now(),
  level  varchar(8),  -- info/warn/error
  msg    text
);

CREATE INDEX IF NOT EXISTS idx_betl_task
  ON billing_export_task_logs(task_id, ts);

-- =========================================================================
-- 3) 30 天清理任务 (不需要新表)
-- =========================================================================
-- 用现有 DAILY_STMT_CRON 调度器 (每天 03:00 跑) 加一个 job:
--   filepath.Walk /data/billing-exports/, 删 30 天前 .zip 文件
--   db.Where("created_at < ?", cutoff).Delete(&BillingExportTask{})
--   db.Where("ts < ?", cutoff).Delete(&BillingExportTaskLog{})
-- 实现位置: internal/billing/prune.go (PR #7 加)

-- =========================================================================
-- 4) 验证
-- =========================================================================
-- \d billing_export_tasks
-- \d billing_export_task_logs

COMMIT;
