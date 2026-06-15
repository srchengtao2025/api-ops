# BILLING v4 RFC (利润分析)

> **作者**: Mavis
> **生成时间**: 2026-06-14 23:15
> **状态**: 🟡 草案 v0.1
> **范围**: 仅利润分析 (v3 = 上游对账 已完成, v4 = 利润分析)
> **关联**: `docs/BILLING-v3-RFC.md` (v3 上游对账), `docs/BILLING-v2-RFC.md` (v2 客户对账), `archive/v1-docs/BILLING-RULES.md` (v1 业务规则)

---

## 状态 (2026-06-15)

| 维度 | 状态 |
|---|---|
| **上线端点** | ✅ **1 端点全部上线** (PR #1-#6 合并 4 commit, 2026-06-14) |
| **公网验证** | ✅ 1 端点 200, 真实数据 21 user / $5,090 消耗 / $4,168 成本 / $921 利润 (22.1% 毛利率) |
| **镜像** | `api-ops:latest` (commit `a6b2b8d`) |
| **业务规则** | R1-R5 + R6 全复用, **0 新公式**, 0 新业务规则 |
| **复用 v2/v3** | ✅ 复用 v2 revenue (RoDB SQL) + v3 cost (CalcLogCost), 1 端点合并 |
| **后续演进** | ✅ BILLING 全套完成, 监控/AI 报告模块是 BILLING 范围外 |

### v4 已上线 1 端点

1. `GET /api/billing/v4/profit/overview` — 利润分析汇总 (1 端点返完整数据: 汇总 + 趋势 + 3 维度拆分)

**单端点设计**: 1 次 HTTP 返完整数据 (汇总 + 趋势 + by_user 27 客户 + by_vendor 5 vendor + by_model top 10), SPA 单页面 1 次 fetch.

### v4 关键决策 (4 Q&A)

- Q1 范围: **仅利润分析** (汇总 + 趋势 + 3 维度拆分)
- Q2 数据源: **复用 v2 当月 overview (revenue) + v3 当月 overview (cost)** → server 端合并算 profit
- Q3 SPA: **1 新页面 + 4 tab** (趋势 / 客户 v2 视角 / 上游 v3 视角 / 模型)
- Q4 时间范围: **当月 1 号 ~ 至今** (跟 v2/v3 overview 一致)

### v4 部署清单关键事实

- **无 migration** (1 端点不加字段)
- **6 PR 合并 4 commit** (v4 简单): `7150dfb` (RFC) + `a6b2b8d` (CalcProfitOverview + API + SPA + 文档)
- **趋势图**: 自实现 SVG bar (不引 recharts, 避免新增 dep, 体积 +60KB → 0)
- **体积**: 新 dist 1.18MB JS / 8.2KB CSS (gzip 377KB / 2.15KB)

---

## v4 利润分析口径 (复用 v2 revenue + v3 cost, 0 新公式)

> **核心**: v4 = v2 revenue + v3 cost, server 端合并算 profit. 0 新公式.

### 利润反推公式 (跟 v3 完全一致, 复用)

```
1. revenue (消耗)     = log.quota / 500000                          (RoDB SQL, 跟 v2 overview 一致)
2. cost (累计成本)    = (revenue / group_ratio) × channel_discount  (复用 v3 CalcLogCost, 0 重写)
3. profit (毛利)      = revenue - cost                                (新, 但公式简单)
4. profit_rate (毛利率) = profit / cost                              (新, "赚几倍")
```

### v4 跟 v2/v3 的数据依赖

| 字段 | 数据源 | 复用 |
|---|---|---|
| `total_revenue` | RoDB SQL `SELECT SUM(quota)/500000 FROM logs WHERE type=2 AND created_at >= ?` | **复用 v2 SQL 模式** |
| `total_cost` | v3 CalcLogCost × channel_discount | **复用 v3 反推公式** |
| `total_profit` | revenue - cost | 新 (简单减法) |
| `profit_rate` | profit / cost | 新 (跟 v3 profit_margin 公式同) |
| `by_day` | RoDB GROUP BY day (30 天) | 复用 v2/v3 SQL 模式 |
| `by_user` | server 端聚合 (27 客户) | 复用 v2 客户维度 |
| `by_vendor` | 复用 v3 CalcUpstreamStatement | **复用 v3 vendor 维度** |
| `by_model` | RoDB GROUP BY model (top 10) | 复用 v2/v3 SQL 模式 |

### 4 tab SPA 设计

| Tab | 字段 | 行数 |
|---|---|---|
| **趋势 (默认)** | 30 天每日 revenue/cost/profit (SVG bar) | 30 |
| **客户 (v2 视角)** | 27 客户 profit 排名 (按利润降序) | 27 |
| **上游 (v3 视角)** | 5 vendor profit 排名 | 5 |
| **模型** | top 10 model profit | 10 |

### v4 vs v3 overview 简化 vs ZIP 精确

| 来源 | 公式 | 准确度 | 场景 |
|---|---|---|---|
| **v4 overview** | `cost = revenue × channel_discount` (简化版) | 趋势监控 (GROUP BY 后 group_ratio 取 avg) | 财务月度 1-5 号看趋势 |
| **v3 overview** | 同 v4, SQL GROUP BY 简化 | 趋势监控 | 财务看上游 vendor 趋势 |
| **v3 ZIP 精确** | `cost = (revenue / log.other.group_ratio) × channel.discount` | **财务对账** (逐 log 计算, 100% 准) | 财务算成本 + 付款上游 |
| **v2 ZIP** | revenue only (不算 cost) | 客户账单精确 | 客户对账 |

**财务对账永远用 v3 ZIP 精确数**, v4 overview 是"看趋势秒看"的工具, 不是最终对账依据.

### v4 拒绝的方向 (决策依据)

| 拒绝项 | 理由 |
|---|---|
| 不新建 profit_export_tasks 表 | 1 端点不需要异步 |
| 不用 ZIP 导出 | 1 SPA 页面 + 1 端点足够, 财务要详细数去 v2/v3 |
| 不用新增 worker | 无异步任务 |
| 不用新增 RBAC | admin/finance/viewer 复用 |

---

## 0. 背景

2026-06-14 v1 下线 (commit `72d82d4`) → v2 客户对账 (8 PR) → v3 上游对账 (7 PR, commit `4092f8b`).

v3 跑通后, **利润分析** 是剩下没做的 1 个 v1 模块. 数据源全在 v2 + v3:
- **revenue (客户消耗)**: v2 当月 overview 已经有
- **cost (累计成本)**: v3 已经有 CalcLogCost + CalcUpstreamStatement
- **profit_margin**: 直接 `profit / cost` 公式

**v4 = 1 端点 + 1 SPA**, 工作量比 v3 少 (没有新 worker, 没有新表, 没有 ZIP).

---

## 1. 决策表 (4 个 Q&A)

### Q1 范围
- **选**: 仅利润分析 (汇总 + 趋势 + 3 维度拆分)
- **理由**: 数据源 v2/v3 都有了, 1 端点足够覆盖
- **拒绝**: 一起做更多模块 (利润率 + 销售预测, 范围超出)

### Q2 数据源
- **选**: 复用 v2 当月 overview (revenue) + v3 当月 overview (cost) → server 端合并算 profit
- **数据源 1 (v2)**: RoDB SQL `SELECT user_id, SUM(quota)/500000 AS revenue FROM logs WHERE type=2 AND created_at >= ?`
- **数据源 2 (v3)**: 复用 v3 CalcUpstreamStatement (cost 反推公式已经在 v3)
- **拒绝**: 拉 admin /api/data/ (DataExportEnabled=false 返空), 走新 SQL (重复 v2/v3)

### Q3 SPA 设计
- **选**: 1 新页面, 跟 v3 上游对账对称 (vendor+channel 类似的拆分)
- **3 个 tab**:
  - **汇总**: 总收入 / 总成本 / 总毛利 / 毛利率
  - **趋势**: 当月 30 天每日 revenue / cost / profit (折线图, 用 recharts 或 antv)
  - **拆分**: 27 客户 (v2) × 5 vendor (v3) 交叉表
- **拒绝**: 1 页面纯汇总 (信息量太少), 6 页面 (太碎)

### Q4 时间范围
- **选**: 当月 1 号 ~ 至今 (跟 v2/v3 overview 一致)
- **拒绝**: 30 天滚动 (跟 v2/v3 不一致, 财务月初 1-5 号用不顺)

---

## 2. API 设计 (1 端点)

### 2.1 端点列表

| # | 端点 | 方法 | 权限 | 用途 |
|---|---|---|---|---|
| 1 | `/api/billing/v4/profit/overview` | GET | admin/finance/viewer | 1 端点返汇总 + 趋势 + 3 维度拆分 |

**单端点** (不像 v2/v3 拆 4-5 个):
- 1 次 HTTP 返完整数据 (汇总 + 趋势 + 拆分)
- 1 端点足够, SPA 单页面 1 次 fetch

### 2.2 数据流

```
1. SPA 加载 /billing/v4/profit 页面
2. GET /api/billing/v4/profit/overview
3. server 端:
   a. SQL #1 (RoDB): SELECT user_id, SUM(quota)/500000 AS revenue, SUM(prompt_tokens), SUM(completion_tokens), SUM((other::jsonb->>'cache_tokens')::bigint) AS cache_tokens, COUNT(*) AS request_count FROM logs WHERE type=2 AND created_at >= ? AND created_at < ? GROUP BY user_id
   b. server 端: 用 v3 CalcLogCost 反推 cost (拿 channel_id → vendor_code → discount)
   c. 算汇总: total_revenue + total_cost + total_profit + total_margin
   d. 算趋势: 按天分组 (30 天每天的 revenue + cost)
   e. 算 3 维度拆分: by_user (27 客户), by_vendor (5 vendor), by_model (top 10)
4. 返 JSON
```

### 2.3 字段定义

| 字段 | 含义 | 来源 |
|---|---|---|
| `total_revenue` | 客户消耗 USD | RoDB `SUM(quota) / 500000` |
| `total_cost` | 累计成本 USD (反推) | v3 CalcLogCost × channel_discount |
| `total_profit` | 毛利 USD | revenue - cost |
| `profit_rate` | 毛利率 | profit / cost ("赚几倍") |
| `by_day` | 30 天每日 trend | RoDB GROUP BY day |
| `by_user` | 27 客户 profit 排名 | server 端聚合 |
| `by_vendor` | 5 vendor profit 排名 | 复用 v3 CalcUpstreamStatement |
| `by_model` | top 10 model profit 排名 | RoDB GROUP BY model |

---

## 3. SPA 设计 (1 新页面, 3 tab)

### 3.1 路由 + 菜单

- **路由**: `/billing/v4/profit`
- **菜单**: 对账中心 > 利润分析 (v4)

### 3.2 3 tab

#### Tab 1: 汇总 (默认)
```
┌─────────────────────────────────────────────────────────────┐
│ 利润分析 (2026-06 至今)              [刷新]                   │
├─────────────────────────────────────────────────────────────┤
│ [总览卡 4 项]                                                │
│   客户消耗 $X,XXX    累计成本 $Y,YYY                        │
│   毛利 $Z,ZZZ         毛利率 NN%                            │
├─────────────────────────────────────────────────────────────┤
│ [趋势图 30 天]                                                │
│   ↑ Y 轴: USD ($1-$1000)                                   │
│   → X 轴: 日期 (06-01 ~ 06-14)                              │
│   3 条线: revenue (蓝) / cost (橙) / profit (绿)           │
└─────────────────────────────────────────────────────────────┘
```

#### Tab 2: 客户 (v2 视角)
```
表格列: 客户 / 等级 / 调用次数 / 4 token / 消耗 (USD) / 成本 (USD) / 毛利 (USD) / 毛利率
27 行, 按利润降序
```

#### Tab 3: 上游 (v3 视角)
```
表格列: 上游 / 调用次数 / 4 token / 消耗 (USD) / 成本 (USD) / 毛利 (USD) / 毛利率
5 行, 按利润降序
```

### 3.3 复用 v2 + v3 现有 UI

- v2 BillingV2Customers 表格样式
- v3 BillingV3Upstream 双层表格样式
- 复用 Ant Design Tabs + Table + Card

---

## 4. 业务规则 (复用 v1 R1-R5 + v3 R6)

v4 不引入新业务规则, 全复用:
- R1 零输出免单 / R2 图片生成 / R4 错误不计 / R5 未匹配上游
- **R6 缺渠道折扣** (v3 新增, v4 复用)
- 详细: `archive/v1-docs/BILLING-RULES.md` + `docs/BILLING-v3-RULES.md`

---

## 5. 数据源 (3 铁律不破坏)

- **API**: 无 (v4 不调 admin API)
- **RoDB**: newapi.logs (revenue + 4 token)
- **cache**: OPS.channel_vendor_map.discount (cost 反推)

---

## 6. 6 PR 切分 (短于 v3 7 PR)

| PR # | 主题 | 工作量 |
|---|---|---|
| **#1** | RFC + 文档 (本 PR) | 0.3 天 |
| **#2** | 利润分析聚合 (CalcProfitOverview + unit test) | 1 天 |
| **#3** | SPA 利润分析页 (3 tab) | 1 天 |
| **#4** | API 1 端点 (overview) | 0.5 天 |
| **#5** | 单测 + 文档 (v4 RULES) | 0.3 天 |
| **#6** | 远端部署 + 公网验证 | 0.5 天 |
| **总计** | | **3.6 天** |

---

## 7. 跟 v2/v3 关系

```
v2 (客户对账, 6 端点)  ←─ v4 复用 (revenue, by_user 27 客户)
v3 (上游对账, 5 端点)  ←─ v4 复用 (cost, by_vendor 5 vendor)
v4 (利润分析, 1 端点)  ←─ v2 + v3 合并
```

v4 没有新表, 没有新 worker, 没有新 ZIP, 没有新 RBAC. 1 端点 + 1 SPA.

---

## 8. 风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| v2/v3 cost/revenue 各自有误差, 合并时叠加 | 低 | 总利润不准 | overview 是趋势监控, 详细看 v2 ZIP + v3 ZIP |
| RoDB 1 月 logs 慢 | 中 | overview 5s+ | 走 cache_logs_summary_by_model_5min (后续优化) |
| 27 user × 5 vendor 交叉表 (v2 客户和 v3 vendor 没直接关系) | 中 | 显示 27 客户 + 5 vendor 两表, 不交叉 | Tab 2 客户视角, Tab 3 上游视角 |
| 趋势图库依赖 (recharts) | 低 | bundle 大 | 已有 antd 生态, 用 @ant-design/plots 或简单 SVG |

---

## 9. 业务连续性

- v2 客户对账 6 端点 / v3 上游对账 5 端点 / **v4 利润分析 1 端点** 一起跑
- 财务月初 1-5 号:
  1. 打开 v4 利润分析, 看汇总卡 (本月总利润)
  2. 切到 v2 生成 27 客户对账 ZIP
  3. 切到 v3 生成 5 vendor 对账 ZIP
  4. 算成本 + 付款上游 + 发账单给客户

---

## 10. 部署 (PR #6)

- 无 migration (1 端点不加字段)
- 推镜像 + restart api
- 公网 1 端点验证
- SPA 1 页面验证

---

## 11. 拒绝的方向

- 不用新建 profit_export_tasks 表 (1 端点不需要异步)
- 不用 ZIP 导出 (1 SPA 页面 + 1 端点足够, 财务要详细数去 v2/v3)
- 不用新增 worker (无异步任务)
- 不用新增 RBAC (admin/finance/viewer 复用)

---

## BILLING 累计成果 (跨 v2 / v3 / v4, 2026-06-15)

> **共享章节**: v2/v3/v4 RFC 同步包含, 财务/运营看 1 处即可了解全貌.

| 模块 | 状态 | 端点 | SPA | 任务中心 | 公网验证 |
|---|---|---|---|---|---|
| v1 (已下线) | ❌ 2026-06-14 20:39 | 0 (原 18) | 0 (原 3) | — | 全 404 |
| **v2 客户对账** | ✅ 2026-06-14 (PR #1-#8) | **6** | **1** (`/billing/customers`) | **1** (`/billing/exports`, 复用) | 真实 27 用户 + 11.6KB ZIP |
| **v3 上游对账** | ✅ 2026-06-14 (PR #1-#7) | **5** | **1** (`/billing/v3/upstream`) | 复用 v2 | 真实 5 vendor 39 channel |
| **v4 利润分析** | ✅ 2026-06-14 (6 PR 合并 4 commit) | **1** | **1** (`/billing/v4/profit`, 4 tab) | — | 21 user $5090 rev $4168 cost $921 profit |
| upstream_pricing (已下架) | ❌ 2026-06-14 23:43 | 0 (原 4) | 0 (原 1) | — | 全 404 |
| **合计活跃** | | **30** | **6** | **1** | — |

### v4 在累计中的位置

- v4 = v2 + v3 合并: revenue 来自 v2 overview, cost 来自 v3 CalcLogCost, profit = revenue - cost
- v4 6 PR 合并 4 commit 提交, **0 新表 / 0 新 worker / 0 新 ZIP / 0 新 RBAC**, 是 BILLING 4 模块中**最轻**的 (3.9 天)
- v4 完成 = BILLING 全套完成 (v1 已下线, v2/v3/v4 上线), 财务月度对账 4 模块齐全

### 累计 PR + commit 时间线

| 模块 | PR 数 | Commit 范围 | 工作量 |
|---|---|---|---|
| v1 (已下线) | — | 2026-06-14 `72d82d4` 下线 | — |
| v2 客户对账 | 8 | `5e464d4` ~ `f6dceb0` | 8 天 |
| v3 上游对账 | 7 | `df40966` ~ 待 `PR #7` | 7 天 |
| v4 利润分析 | 6 (合并 4 commit) | `7150dfb` + `a6b2b8d` | 3.9 天 |
| upstream_pricing (已下架) | 1 | 2026-06-14 23:43 | 0.5 天 |
| **BILLING 总投入** | | | **≈ 19.4 天** |

### 业务规则累计 (v1 → v4 一脉相承)

- **R1 零输出免单** / **R2 图片生成标记** / **R3 退款不计** / **R4 错误不计** / **R5 未匹配上游不计** — v1 制定, v2/v3/v4 复用
- **R6 缺渠道折扣** — v3 新增 (channel_vendor_map.discount 缺失), v4 复用
- **5 业务规则在 4 模块零断点** (BILLING 业务连续性最强)

### 财务月初工作流 (1-5 号标准流程)

```
1. 打开 v4 利润分析, 看汇总卡 (本月总利润 / 毛利率)
2. 切到 v4 客户 tab (哪些客户亏钱 / 按毛利排名)
3. 切到 v4 上游 tab (哪些 vendor 亏钱)
4. 切到 v2 生成 27 客户对账 ZIP (HTML + XLSX)
5. 切到 v3 生成 5 vendor 对账 ZIP (HTML + XLSX)
6. 用 ZIP 精确数算成本 + 付款上游 + 发账单给客户
```

4 模块一起跑, 财务月度对账工作量从"1 周"压到"1 天" (v3 反推公式覆盖率 100% + v2 异步 ZIP 不阻塞 + v4 汇总卡秒看趋势).

### BILLING 完成后, 接下来的方向 (范围外, 不在 v2/v3/v4 RFC)

- **监控中心**: 已上线"渠道健康度", 错误率新口径 (业务请求 / 独立错误)
- **AI 报告**: 日报 + 周报 + 客户健康度 (Q4 决策, 手动模式, 待规划)
- **告警模块**: rule / alert / ack / resolve / 飞书推送 (Q3 决策, 监控 SPA 暂不开放)
- **健康分公式**: 错误率 40% + 延迟 30% + 余额 20% + 调用量 10% (DESIGN.md v1.0 Q-DASH)
