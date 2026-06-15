-- 2026-06-14 BILLING v3 上游对账
-- 复用 v2 billing_export_tasks 表, 加 1 字段 kind (customer / upstream)
-- 加 1 索引
--
-- 上线步骤:
--   1. scp migrations/2026-06-14-billing-v3-upstream-tasks.sql 到远端
--   2. docker cp 到 api-ops-postgres:/tmp/m.sql
--   3. docker exec api-ops-postgres sh -c 'PGPASSWORD=change_me psql -U api_ops -d api_ops -f /tmp/m.sql'
--
-- 验证:
--   \d billing_export_tasks
--   SELECT kind, count(*) FROM billing_export_tasks GROUP BY kind;
--   v2 已有 6 任务: 6 customer
--   v3 跑后:     N upstream

BEGIN;

-- 加 kind 列 (v2 已有数据 default 'customer' 兼容)
ALTER TABLE billing_export_tasks
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'customer';

-- 加注释
COMMENT ON COLUMN billing_export_tasks.kind IS '任务类型: customer (v2 客户对账) / upstream (v3 上游对账)';

-- 加约束 (CHECK 防止非法值, 不影响历史数据)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'billing_export_tasks_kind_check'
    ) THEN
        ALTER TABLE billing_export_tasks
            ADD CONSTRAINT billing_export_tasks_kind_check
            CHECK (kind IN ('customer', 'upstream'));
    END IF;
END$$;

-- 加索引 (v3 默认页 + 任务中心会按 kind 过滤)
CREATE INDEX IF NOT EXISTS idx_billing_export_tasks_kind
    ON billing_export_tasks(kind);

-- 加 vendor_code 列 (v3 任务关联到具体上游, 便于按上游查任务历史)
ALTER TABLE billing_export_tasks
    ADD COLUMN IF NOT EXISTS vendor_code TEXT;

-- 加注释
COMMENT ON COLUMN billing_export_tasks.vendor_code IS 'v3 上游对账任务用: vendor_code (openai-azure / provider_alpha / ...). v2 客户对账为空';

-- 加索引
CREATE INDEX IF NOT EXISTS idx_billing_export_tasks_vendor_code
    ON billing_export_tasks(vendor_code) WHERE vendor_code IS NOT NULL;

COMMIT;
