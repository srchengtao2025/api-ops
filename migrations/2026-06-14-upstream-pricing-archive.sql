-- 2026-06-14 BILLING 价目表 (upstream_pricing) 下线
-- v3 PR #2 之后, cost 反推公式改用 channel_vendor_map.discount (覆盖率 100%)
-- upstream_pricing 表 9 行覆盖率 18% (按 channel 维度), 不再被代码使用
-- 9 行数据归档到 archive schema (跟 v1 billing_statements 一致)
--
-- 关联:
--   - handlers_billing.go: listPricing / deletePricing / importPricing / getImport 4 个 handler
--   - server.go: 4 路由 /upstream-pricing* 全删
--   - SPA: UpstreamPricing.tsx 页面全删 + 路由删 + 菜单删
--   - web/src/api/index.ts: UpstreamPricing + UpstreamPricingImport interface + 4 API 方法全删
--   - ops_repo.go: UpsertPricing / GetPricingAt / ListPricing / DeletePricing / CreateImport / UpdateImport / GetImport 全删
--   - ops_models.go: UpstreamPricing + UpstreamPricingImport struct 全删
--
-- 验证:
--   - 删前: SELECT count(*) FROM public.upstream_pricing;  -> 9
--   - 跑后: SELECT count(*) FROM archive.upstream_pricing; -> 9
--   - SELECT count(*) FROM public.upstream_pricing; -> 0

BEGIN;

CREATE SCHEMA IF NOT EXISTS archive;

-- 1) upstream_pricing 移表
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='upstream_pricing') THEN
        ALTER TABLE public.upstream_pricing SET SCHEMA archive;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='upstream_pricing_imports') THEN
        ALTER TABLE public.upstream_pricing_imports SET SCHEMA archive;
    END IF;
END$$;

-- 2) 移 sequence (bigserial id 列)
-- 注意: upstream_pricing_imports_id_seq 是 OWNED BY public.upstream_pricing_imports (表还没移),
--       不能直接 move. 这里只尝试 move 独立的 sequence, owned 的会跳过 (DO 块不报错)
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
          AND c.relname LIKE 'upstream_pricing%'
          -- 排除 OWNED BY 表的序列 (没改表前不能 move)
          AND c.relname NOT IN (
              SELECT s.relname FROM pg_class s
              JOIN pg_class t ON s.relnamespace = t.relnamespace
              JOIN pg_depend d ON d.objid = s.oid OR d.objid = t.oid
              WHERE s.relkind = 'S' AND t.relname IN ('upstream_pricing_imports')
          )
    LOOP
        EXECUTE format('ALTER SEQUENCE public.%I SET SCHEMA archive', r.seq_name);
    END LOOP;
END$$;

-- 3) (不放 schema, 不放 archive, 留 audit)
COMMENT ON SCHEMA archive IS '归档的旧版本模块表 (只读, 不进业务逻辑). 详细见 archive/v1-docs/README.md';

COMMIT;
