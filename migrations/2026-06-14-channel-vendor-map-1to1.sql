-- A 阶段 - 供应商管理模块: channel_vendor_map schema 改造
-- 2026-06-14
--
-- 旧 schema:
--   id / channel_id (idx_channel_vendor unique) / vendor_code (idx_channel_vendor unique) / weight / remark
--   N:M 关系, weight 字段名混淆 (以为是分摊, 其实是折扣)
--
-- 新 schema:
--   id / channel_id (UNIQUE 1:1) / vendor_code
--   / discount (final, 0-1) / auto_discount (只读, 自动解析) / auto_matched (匹配到) / auto_recognized (bool)
--   / discount_override (bool, 人工矫正过)
--   / remark / created_at / updated_at
--
-- 业务规则: 1 渠道 → 1 供应商 (1:1)
-- 数据迁移: 旧 weight 全部 = 1.0, 直接重命名为 discount
--           旧 idx_channel_vendor (channel_id, vendor_code) 删, 改 channel_id 单独 UNIQUE
--           1 渠道挂多供应商的会触发 conflict, 业务上保证不会发生

BEGIN;

-- 1) 加新字段 (带默认值, 不影响旧数据)
ALTER TABLE channel_vendor_map ADD COLUMN IF NOT EXISTS discount numeric(5,4) NOT NULL DEFAULT 1.0;
ALTER TABLE channel_vendor_map ADD COLUMN IF NOT EXISTS auto_discount numeric(5,4) NOT NULL DEFAULT 1.0;
ALTER TABLE channel_vendor_map ADD COLUMN IF NOT EXISTS auto_matched varchar(64) NOT NULL DEFAULT '';
ALTER TABLE channel_vendor_map ADD COLUMN IF NOT EXISTS auto_recognized boolean NOT NULL DEFAULT false;
ALTER TABLE channel_vendor_map ADD COLUMN IF NOT EXISTS discount_override boolean NOT NULL DEFAULT false;

-- 2) 把旧 weight 数据迁到 discount (旧 weight 几乎全是 1.0)
UPDATE channel_vendor_map SET discount = weight WHERE discount = 1.0 AND weight != 1.0;

-- 3) 删旧 idx_channel_vendor (channel_id, vendor_code) 复合 unique
--    (业务已确认 1:1, 不需要复合约束)
DROP INDEX IF EXISTS idx_channel_vendor;

-- 4) 加 channel_id 单字段 UNIQUE
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_id ON channel_vendor_map (channel_id);

-- 5) 删 weight 字段
ALTER TABLE channel_vendor_map DROP COLUMN IF EXISTS weight;

-- 6) 验证
-- SELECT id, channel_id, vendor_code, discount, auto_discount, auto_recognized, discount_override FROM channel_vendor_map ORDER BY channel_id;

COMMIT;
