-- 2026-07-09 客户健康度模块 (api-ops 同步 rezeai-ops, 脱敏单站版)
--
-- 复用 billing_export_tasks 表, kind 字段加 'customer_health' enum 值
-- 跟 ops_models.go BillingExportTask.Kind check 约束同步
--
-- api-ops 单站: 没有 Site 字段, 字段定义跟 rezeai-ops 一致 (v2 时代 schema)
--
-- 注意: 跟 AIReport 表 的 report_type='customer_health' 不冲突,
--       AIReport 存的是 LLM 生成的 markdown 健康度分析报告,
--       BillingExportTask 存的是数据统计 + HTML 详情导出的异步任务.
--       两表同名字段语义独立, 互不影响.
--
-- 回滚:
--   ALTER TABLE billing_export_tasks DROP CONSTRAINT billing_export_tasks_kind_check;
--   ALTER TABLE billing_export_tasks ADD CONSTRAINT billing_export_tasks_kind_check
--     CHECK (kind IN ('customer','upstream'));

ALTER TABLE billing_export_tasks
  DROP CONSTRAINT IF EXISTS billing_export_tasks_kind_check;

ALTER TABLE billing_export_tasks
  ADD CONSTRAINT billing_export_tasks_kind_check
  CHECK (kind IN ('customer', 'upstream', 'customer_health'));

