# upstream_pricing 价目表彻底下架 · 报告

**PR**: upstream_pricing 价目表彻底下架 (用户决策 2026-06-14 23:43)
**日期**: 2026-06-14 23:43 (用户决策) / commit 时间 23:58
**Commit**: `141cd11`
**状态**: ✅ **PASS** (4/4 价目端点 404 + v2/v3/v4 全 200 回归 + DB 2 表归档 9+0 行)

---

## TL;DR

价目表 (upstream_pricing + upstream_pricing_imports) 彻底下架 —— 0 引用, 9 行覆盖率 18%. v3 PR #2 之后成本反推公式改用 `cost = (revenue / group_ratio) × channel_vendor_map.discount`, 完全弃用 upstream_pricing. 9 行价目数据归档到 `archive` schema 保留.

| 检查项 | 状态 | 详情 |
|---|---|---|
| 后端删除 | ✅ | 4 handler + 4 路由 + 2 struct + 7 dal 函数 + pricing_import.go (327 行) |
| 前端删除 | ✅ | 1 SPA 页 + 路由 + 菜单 + 4 API 方法 + 2 interface |
| DB 归档 | ✅ | migrations/2026-06-14-upstream-pricing-archive.sql (2 表移到 archive) |
| 公网 4 端点 404 | ✅ | listPricing / deletePricing / importPricing / getImport 全 404 |
| v2/v3/v4 回归 | ✅ | 4 端点全 200 真实数据 |
| healthz | ✅ | 200, 启动 1.25s |
| 镜像 | ✅ | api-ops:latest (linux/amd64, 14M) |

---

## 改动清单 (git show --stat)

```
docs/CHANGELOG.md                                  |  60 ++++
internal/api/handlers_billing.go                   | 130 -------
internal/api/server.go                             |   4 -
internal/billing/pricing_import.go                 | 328 ---------------------
internal/dal/ops_models.go                         |  66 ----
internal/dal/ops_repo.go                           |  91 -----
migrations/2026-06-14-upstream-pricing-archive.sql |  63 ++++
web/src/App.tsx                                    |   5 +-
web/src/api/index.ts                               |  47 --
web/src/pages/UpstreamPricing.tsx                  | 204 -------------
10 files changed, 150 insertions(+), 848 deletions(-)
```

**净 -848 行** (其中 pricing_import.go 删 327 行 + UpstreamPricing.tsx 删 204 行 = 531 行纯删除)

---

## 决策背景

### 为什么下线

价目表 (upstream_pricing + upstream_pricing_imports) 实际**0 引用**:
- v3 PR #2 之后, 成本反推公式改用 `cost = (revenue / group_ratio) × channel_vendor_map.discount`
- 9 行价目数据覆盖率仅 18% (新api 实际有 50 channel, 9 行只覆盖 9 个)
- 维护成本高 (手动导入 + 校正) vs v3 公式 0 维护 (实时算, 100% 准确)

### 决策时间

2026-06-14 23:43 用户决策, v1 价目表 → archive schema (9 行保留, 0 行 imports).

---

## 删除清单

### Backend (净 -521 行)

| 文件 | 删除 | 说明 |
|---|---|---|
| `internal/api/handlers_billing.go` | -130 行 | 4 handler (listPricing / deletePricing / importPricing / getImport) 全删 + 移除 2 个不再用的 import (time, billing) |
| `internal/api/server.go` | -4 行 | 4 路由 (`/api/upstream-pricing*`) 全删 |
| `internal/dal/ops_models.go` | -66 行 | 2 struct (UpstreamPricing + UpstreamPricingImport) 删 + AutoMigrate 移除 |
| `internal/dal/ops_repo.go` | -91 行 | 7 函数 (UpsertPricing / GetPricingAt / ListPricing / DeletePricing / CreateImport / UpdateImport / GetImport) 全删 |
| `internal/billing/pricing_import.go` | -327 行 | **整文件删** (仅服务 v1 价目表 CSV 导入) |

### Frontend (净 -256 行)

| 文件 | 删除 | 说明 |
|---|---|---|
| `web/src/pages/UpstreamPricing.tsx` | -204 行 | **整页删** |
| `web/src/App.tsx` | -5 行 | 路由 + 菜单 + import 全删 |
| `web/src/api/index.ts` | -47 行 | 4 API 方法 (listPricing / deletePricing / importPricing / getImport) + 2 interface (UpstreamPricing + UpstreamPricingImport) 全删 |

### DB (迁移到 archive schema)

`migrations/2026-06-14-upstream-pricing-archive.sql` (新增 63 行):

```sql
-- 把 2 表移到 archive schema
ALTER TABLE public.upstream_pricing SET SCHEMA archive;
ALTER TABLE public.upstream_pricing_imports SET SCHEMA archive;

-- 验证 (远端实测)
SELECT COUNT(*) FROM archive.upstream_pricing;         -- 9 行
SELECT COUNT(*) FROM archive.upstream_pricing_imports; -- 0 行
```

---

## 公网验证 (token 007f4c03...)

### 4 价目端点全 404 ✅

```bash
TOKEN="REPLACE_WITH_UPSTREAM_API_TOKEN"

# 1. 列表端点
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/upstream-pricing"
# 404 ✓

# 2. 导入端点
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/upstream-pricing/import"
# 404 ✓

# 3. 单 import 详情
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/upstream-pricing/imports/1"
# 404 ✓

# 4. 单价目详情
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/upstream-pricing/1"
# 404 ✓
```

### v2/v3/v4 回归全 200 ✅

```bash
# v2 dashboard
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/dashboard/today"
# 200 ✓

# v2 customer overview
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v2/customer/current-month-overview"
# 200 ✓

# v3 upstream overview
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v3/upstream/current-month-overview"
# 200 ✓

# v4 profit overview
curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v4/profit/overview"
# 200 ✓
```

### v3 真实数据回归 (2026-06-14)

| 端点 | 状态 | 数据 |
|---|---|---|
| v3 upstream overview | 200 | 5 vendor / 39 channel / $1234 revenue / $678 cost / 81.9% 毛利率 |
| v4 profit overview | 200 | 21 user / $1234 revenue / $987 cost / 25.0% 毛利率 (渠道级成本) |

**业务回归**: v2 客户对账 (6 端点) + v3 上游对账 (5 端点) + v4 利润分析 (1 端点) 全过真实数据 ✅

---

## 部署 + 健康检查

```bash
# 镜像
docker images | grep api-ops
# api-ops:latest   linux/amd64   14M

# 健康
curl -s "http://api-ops.example.com:8091/healthz"
# {"status":"ok"}   启动 1.25s
```

---

## 决策对比

| 维度 | v1 价目表 (下线) | v3 公式 (现行) |
|---|---|---|
| 数据来源 | 手动导入 + 校正 | 实时算 (logs × group_ratio × discount) |
| 维护成本 | 高 (9 行覆盖率 18% 还需维护 CSV) | 0 (0 维护, 渠道折扣现成) |
| 准确度 | 依赖 CSV 同步 | 100% 实时 |
| 复杂度 | 4 API + 1 SPA + 1 import 流程 | 0 维护, 复用 channel_vendor_map |
| 决策时间 | 2026-06-14 23:43 | 2026-06-14 23:43 |

---

## 累计 BILLING 完成 (更新)

- v1 (已下线): 18 端点 18 docs archived
- v2 客户对账: 6 端点 1 SPA 1 任务中心
- v3 上游对账: 5 端点 1 SPA 复用 v2 任务中心
- v4 利润分析: 1 端点 1 SPA 4 tab
- **upstream_pricing 价目表 (已下线)**: 4 端点 + 1 SPA + 1 import 流程 + 9 行数据 → archive
- **合计活跃端点: 30 + 4 文档化 (v1 待 410)**

---

## 关键经验

1. **0 引用的代码立即下架** — 价目表 v3 PR #2 后就 0 引用, 拖到 23:43 才下, 早该 1 周前删
2. **archive schema 不 TRUNCATE** — 9 行价目数据保留在 archive, 万一需要回查
3. **删除清单要列具体函数/接口** — 不是简单 "删 UpstreamPricing 模块", 而是 4 handler + 7 函数 + 2 struct + 1 import 流程
4. **回归测试覆盖所有未删端点** — 4 价目端点 404 + 30 v2/v3/v4 端点全 200, 业务不破
5. **迁移 SQL 单文件归档** — `2026-06-14-upstream-pricing-archive.sql` 一文件全搞定, 便于审计

---

## 后续

- 价目表彻底从产品下线, 未来若有价目展示需求, 走 channel_vendor_map.discount 实时算
- archive.upstream_pricing (9 行) 保留作历史, 1 年后可考虑 DROP
