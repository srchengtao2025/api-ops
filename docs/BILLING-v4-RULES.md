# BILLING v4 业务规则 (利润分析)

> **作者**: Mavis
> **生成时间**: 2026-06-14 23:25
> **状态**: ✅ 终版
> **关联**: `docs/BILLING-v4-RFC.md` / `docs/BILLING-v3-RULES.md` / `archive/v1-docs/BILLING-RULES.md`

---

## 状态 (2026-06-15 同步)

| 维度 | 状态 |
|---|---|
| **业务规则 R1-R5 + R6 复用** | ✅ 全复用, 0 新业务规则 |
| **1 端点** | ✅ 已上线, 真实数据 21 user / $5,090 消耗 / $4,168 成本 / $921 利润 (22.1% 毛利率) |
| **公式** | ✅ 复用 v2 revenue + v3 cost, 0 新公式, profit = revenue - cost, profit_rate = profit / cost |
| **SPA 4 tab** | ✅ 趋势 (默认) / 客户 (v2 视角) / 上游 (v3 视角) / 模型 (top 10) |
| **监控模块对接** | ✅ v4 跟监控无数据耦合, v4 cost 走 v3 CalcLogCost, 监控走 channel_health_5min |

### 业务规则 R1-R6 对齐实际实现 (2026-06-15 自检)

v4 不引入新业务规则, 全复用 v1 R1-R5 + v3 R6:

| 规则 | 复用源头 | 实际实现 | 对齐 |
|---|---|---|---|
| R1 零输出免单 | v1 | v2/v3/v4 都走 SQL `WHERE type=2` 排除 type=5, completion_tokens=0 由 v2/v3 R4 兜底 | ✅ |
| R2 图片生成标记 | v1 | v3 `IsImageGenerationModel` 函数, 10 case 单测全过 | ✅ |
| R3 退款不计 | v1 | `WHERE type=2` 排除 type=3 | ✅ |
| R4 错误不计 | v1 | `WHERE type=2` 排除 type=5 | ✅ |
| R5 未匹配上游 | v1 | v3 CalcLogCost 标记 unmatched_reason | ✅ |
| **R6 缺渠道折扣** | v3 | v3 CalcLogCost 标记 unmatched_reason="missing_channel_discount", v4 复用 cost=0 | ✅ |

**自检结论**: 6 业务规则跟实际实现 100% 对齐, v4 0 业务规则变动.

### v4 累计 PR 完成 (2026-06-14)

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| #1 | RFC + 6 PR 切分 | `7150dfb` | 0.3 天 |
| #2-#5 | CalcProfitOverview + API 1 端点 + SPA 4 tab + 文档 (合并) | `a6b2b8d` | 3.1 天 |
| #6 | 远端部署 + 公网验证 | (本) | 0.5 天 |
| **总计** | | | **3.9 天** |

---

## 错误率口径 (2026-06-15 新增, 跟 v4 业务规则区分)

> **关键**: v4 利润分析**不含错误率**, 错误率仅在监控中心. v4 看的是"利润", 监控看的是"健康", 两口径不冲突.

### v4 不含错误率的原因

v4 1 端点返 `total_revenue` + `total_cost` + `total_profit` + `profit_rate` + 4 个 tab (趋势/客户/上游/模型). 错误率不在 v4 字段中.

**为什么**: v4 = v2 + v3 合并, 错误请求已经在 v3 R4 中 cost=0 + revenue=0 (即"忽略不计"), v4 自然就把错误过滤掉了. **v4 利润是"扣完错误后的真实利润"**.

### v4 跟监控错误率的关系

| 维度 | v4 利润 | 监控错误率 |
|---|---|---|
| 触发 | revenue - cost (v2 + v3 公式) | type=5 独立错误 / 业务请求 |
| 错误处理 | R4 错误不计 → 利润**已经扣除**错误成本 | 错误率 = 独立错误 / 业务请求 |
| 关系 | v4 是"已扣错"利润 | 监控是"原始错误率" |
| 业务用途 | 财务对账 (真实利润) | 运营监控 (渠道健康) |

**反推公式**: 如果想从 v4 利润推算"如果不算错误会怎样", 需要 v2/v3 原始数据 (R4 之前的), 但 v4 不存这个, 财务要去 v2/v3 ZIP 看.

### 监控错误率新口径 (2026-06-15 09:43 决策)

| 项 | 定义 | SQL 模式 |
|---|---|---|
| **分母**: 业务请求 | `type IN (2, 5, 6)` 跨 24h, 排除登录/充值/管理操作 | `COUNT(*) FILTER (WHERE type IN (2, 5, 6))` |
| **分子**: 独立错误 | `type=5 AND jsonb_array_length(other->'use_channel') = 1`, 排除 retry 中间失败 | `COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(other->'use_channel') = 1)` |
| **P95/P99 延迟** | `channel_health_5min` 桶 MAX(最新桶) | 走 cache |
| **错误率阈值** | ≥ 20% 触发红边呼吸 | 红边只覆盖渠道卡片, 不覆盖综合 KPI |

**v4 不采纳监控错误率**: v4 不展示错误率字段, 不需要.

---

## 监控模块 (2026-06-15 新增, v4 跟监控数据流无耦合)

> **范围**: 监控中心"渠道健康度"模块, v4 跟监控无数据耦合 (v4 cost 走 v3 CalcLogCost, 监控走 channel_health_5min).

### v4 不依赖监控数据

v4 1 端点 `GET /api/billing/v4/profit/overview` server 端流程:
1. RoDB SQL: `SELECT user_id, SUM(quota)/500000 AS revenue FROM logs WHERE type=2 AND created_at >= ? GROUP BY user_id`
2. server 端: v3 CalcLogCost 反推 cost (拿 channel_id → vendor_code → discount)
3. 算汇总: total_revenue + total_cost + total_profit + total_margin
4. 算趋势: 按天分组 (30 天每天的 revenue + cost)
5. 算 3 维度拆分: by_user (27 客户), by_vendor (5 vendor), by_model (top 10)

**关键**: 步骤 2 走 v3 CalcLogCost, **不走** channel_health_5min. 监控模块用 cache 是因为 dashboard 5s tick 频繁, cache 减压; v4 是财务手动点 (1 天 1 次), RoDB 实时够用.

### 监控引擎 (部署铁律 #15, v4 同 v3 必须)

**关键**: `cmd/server/main.go` **必须** import scheduler + 调 `scheduler.Run(rootCtx, cfg)`. 否则 monitor tick 永不跑, channel_health_5min 0 数据. v4 不直接读 channel_health_5min, 但 scheduler 启了之后监控才有数据, **v4 SPA 页面引用"健康度"提示**才会显示.

### v4 跟监控的数据流图 (无耦合, 并行存在)

```
newapi.logs (RoDB)
    ├─> channel_health_5min (cache, 1min tick via scheduler.Run)
    │     └─> 监控中心: 错误率 / P95 / P99 (渠道卡片)
    ├─> v2 revenue (RoDB SQL)
    │     └─> v4 total_revenue (复用 v2 SQL)
    └─> v3 cost (CalcLogCost, RoDB + log.other.group_ratio)
          └─> v3 vendor overview
                └─> v4 total_cost (复用 v3 公式)
```

**结论**: v4 跟监控**没有直接数据依赖**, 但都依赖 scheduler.Run 启 monitor tick (供监控) 和 data sync tick (供 v2/v3 cache).

---

## 0. v1 → v4 完整路径

| 阶段 | 状态 | 端点数 | SPA 页数 |
|---|---|---|---|
| v1 (已下线) | ❌ 2026-06-14 20:39 | 18 | 3 |
| v2 (客户对账) | ✅ 上线 | 6 | 1 |
| v3 (上游对账) | ✅ 上线 | 5 | 1 |
| **v4 (利润分析)** | ✅ 上线 (PR #6) | **1** | **1** |
| **合计** | | **30** | **6** |

---

## 1. 业务规则 (全复用 v1 R1-R5 + v3 R6)

v4 不引入新规则, 复用 v1 + v3 的:
- R1 零输出免单 / R2 图片生成 / R4 错误不计 / R5 未匹配上游
- R6 缺渠道折扣 (v3 新增)

---

## 2. 利润反推公式 (复用 v3 cost + v2 revenue)

```
1. revenue (消耗)     = log.quota / 500000          (RoDB SQL, 跟 v2 overview 一致)
2. cost (累计成本)    = (revenue / group_ratio) × channel_discount   (v3 CalcLogCost)
3. profit (毛利)      = revenue - cost
4. profit_rate (毛利率) = profit / cost
```

跟 v3 完全一致, **无新公式**, 只是 1 端点把 v2/v3 数据合并展示.

---

## 3. 1 端点 + 1 SPA

### 3.1 API

```
GET /api/billing/v4/profit/overview
  query: ?start=unix&end=unix  (默认本月 1 号 至今)
  返: 汇总 + by_day(30) + by_user(27) + by_vendor(5) + by_model(top 10)
```

### 3.2 SPA 4 tab

| Tab | 字段 | 行数 |
|---|---|---|
| 趋势 (默认) | 30 天每日 revenue/cost/profit (SVG bar 简化版) | 30 |
| 客户 (v2 视角) | 27 客户 profit 排名 | 27 |
| 上游 (v3 视角) | 5 vendor profit 排名 | 5 |
| 模型 | top 10 model profit | 10 |

---

## 4. 部署清单 (PR #6 已完成, 2026-06-14)

- 无 migration (1 端点不加字段)
- 推镜像 + restart api
- 公网 1 端点验证
- 写公网验证报告

---

## 5. 6 PR 完成

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| #1 | RFC + 6 PR 切分 | (本) | 0.3 天 |
| #2 | CalcProfitOverview + unit test | (本) | 1 天 |
| #3 | SPA 4 tab (汇总/客户/上游/模型) | (本) | 1 天 |
| #4 | API 1 端点 (overview) | (本) | 0.5 天 |
| #5 | 单测 + 文档 (v4 RULES) | (本) | 0.3 天 |
| #6 | 远端部署 + 公网验证 | (本) | 0.5 天 |
| **总计** | | | **3.6 天** |

(全部合并到 1 commit 提交, 因为简单)

---

## 6. 业务连续性

- v2 客户对账 6 端点 + v3 上游对账 5 端点 + v4 利润分析 1 端点一起跑
- 财务月初 1-5 号:
  1. 看 v4 利润分析 汇总卡 (本月总利润)
  2. 看 v4 客户 tab (哪些客户亏钱)
  3. 看 v4 上游 tab (哪些 vendor 亏钱)
  4. 用 v2/v3 生成 ZIP 详细数
