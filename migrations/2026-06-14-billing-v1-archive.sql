-- 2026-06-14 BILLING v1 下线
-- 把 v1 用的 2 张表 (billing_statements + billing_statement_lines) 移到 archive schema
-- 表里 0 行数据 (2026-06-14 查), 但保留结构, 万一以后要查 5 月对账有迹可循
--
-- 上线步骤:
--   1. docker cp 到 api-ops-postgres:/tmp/
--   2. docker exec api-ops-postgres sh -c 'PGPASSWORD=change_me psql -U api_ops -d api_ops -f /tmp/2026-06-14-billing-v1-archive.sql'
--
-- 验证:
--   SELECT schemaname, tablename FROM pg_tables WHERE tablename LIKE 'billing%' ORDER BY schemaname, tablename;
--   public.billing_export_tasks (v2 在用)
--   public.billing_export_task_logs (v2 在用)
--   archive.billing_statements (v1 归档)
--   archive.billing_statement_lines (v1 归档)

BEGIN;

CREATE SCHEMA IF NOT EXISTS archive;

-- 移表 (ALTER ... SET SCHEMA 会自动改外键/视图, 但 v1 表无外键约束, 安全)
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='billing_statements') THEN
        ALTER TABLE public.billing_statements SET SCHEMA archive;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='billing_statement_lines') THEN
        ALTER TABLE public.billing_statement_lines SET SCHEMA archive;
    END IF;
END$$;

-- 移表注释
COMMENT ON SCHEMA archive IS '归档的旧版本模块表 (只读, 不进业务逻辑). 例: BILLING v1 billing_statements/lines (2026-06-14 下线)';

-- 把可能的序列也搬过去 (id 列用 bigserial 的话有 sequence)
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT c.relname AS seq_name
        FROM pg_class c
        JOIN pg_namespace n ON c.relnamespace = n.oid
        WHERE c.relkind = 'S'
          AND n.nspname = 'public'
          AND (c.relname LIKE 'billing_statements%' OR c.relname LIKE 'billing_statement_lines%')
    LOOP
        EXECUTE format('ALTER SEQUENCE public.%I SET SCHEMA archive', r.seq_name);
    END LOOP;
END$$;

COMMIT;
