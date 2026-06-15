# BILLING v3 RFC (上游对账)

> **作者**: Mavis
> **生成时间**: 2026-06-14 21:15
> **状态**: 🟡 草案 v0.2, 待用户 review
> **范围**: 仅上游对账 (利润分析 v4 做)
> **关联**: `docs/BILLING-v2-RFC.md` (v2 客户对账), `archive/v1-docs/MONTHLY-RECONCILIATION.md` (v1 上游)

---

## 状态 (2026-06-15)

| 维度 | 状态 |
|---|---|
| **上线端点** | ✅ **5 端点全部上线** (PR #1-#7, 2026-06-14) |
| **公网验证** | ✅ 5 端点 200, 真实数据 5 vendor / 39 channel / $1234 revenue / $678 cost / 81.9% 毛利率 |
| **镜像** | `api-ops:latest` (commit `539a82e` SPA + 后端累计 commit) |
| **业务规则** | R1-R5 复用 v1 + 新增 R6 缺渠道折扣, 0 业务断点 |
| **复用 v2** | ✅ worker / download / cancel / 任务中心 / 30 天清理, 0 改动 v2 代码 |
| **后续演进** | ✅ v3 cost 反推数据 = v4 利润分析的 cost 来源, v4 复用 CalcLogCost |

### v3 已上线 5 端点

1. `GET /api/billing/v3/upstream/current-month-overview` — 上游当月累计 (vendor + channel 双层, **cache-first, 5min 延迟, cache miss fallback live calc**)
2. `POST /api/billing/v3/upstream/export-last-month` — 创建上游月度对账任务 (走 v2 worker pool)
3. `GET /api/billing/v3/upstream/:vendor_code/tasks` — 单上游任务历史
4. `GET /api/billing/v3/export-tasks` — 全任务 (kind='upstream'), 复用 v2 任务中心
5. `GET /api/billing/v3/export-tasks/:task_id/download` + `cancel` — ZIP 下载 / 取消 (复用 v2 handler)

### v3 上游对账 cache 化 (2026-06-15 PR #9 + 14:00 hotfix)

**架构** (跟 monitor 5min cache 模式一致):
- 新增表 `ops_upstream_summary_5min` (PRIMARY KEY = `vendor_code + period_label + ts_bucket`)
- `internal/scheduler/scheduler.go` 新增 `runUpstreamTick` goroutine, 跟 monitor / AI scheduler 同根
- `internal/billing/upstream_summary_tick.go` 调 `CalcUpstreamStatement` 1 vendor × 1 period, 写 cache
- handler / worker 优先读 cache, cache miss (启动 50min 内 1 轮没跑完) 走 `CalcUpstreamStatement` 实时算 + 标 `stale=true`

**5min tick 模式 (round-robin, 2026-06-15 14:00 hotfix commit 1c5a936)**:
- 单次 tick 跑 **1 个 (vendor, period)**, 5min 1 个, 10 tick = 50min 跑完 1 轮
- **不串行跑 5 vendor × 2 period = 10 SQL** (历史教训: 累加 19万行 logs × 10 = 200MB+ 临时分配, 触发进程级崩溃 exit 0, 无 panic / OOM / dmesg, 怀疑 gorm buffer + cgo 段错误)
- handler cache miss 仍走 live calc, 月对账延迟 50min 内可接受
- 公网实测 6+ 小时稳跑, restart 0, cache 表累积 78 行 (5 vendor × 2 period × 多轮)

**handler 路径**:
```
GET /api/billing/v3/upstream/current-month-overview
  → ListLatestUpstreamSummary(vendor_code, period_label)
    → cache hit: 返 cache (5min 延迟)
    → cache miss: 走 CalcUpstreamStatement live calc + 标 stale=true
```

### v3 关键决策 (5 Q&A)

- Q1 范围: **仅上游对账**, 利润分析 v4 做
- Q2 数据源: **RoDB (newapi.logs) + OPS.channel_vendor_map + log.other (group_ratio)**, 拒绝 admin /api/data/ (DataExportEnabled=false) + cache_logs_summary (缺 cache_tokens 拆分)
- Q3 任务模式: **异步 + 30 天清理**, 复用 v2
- Q4 SPA: **1 新页面 (vendor/channel 双层)** + 复用 v2 任务中心
- Q5 缓存字段: **单字段 cache_tokens** (跟 v2 一致, newapi 实际只存 1 JSON 字段)

### v3 部署清单关键事实

- **migration**: `migrations/2026-06-14-billing-v3-upstream-tasks.sql` 给 `billing_export_tasks` 加 `kind` + `vendor_code` 2 列 + 2 索引 + 1 约束 (CHECK kind IN ('customer', 'upstream'))
- **5 vendor 39 channel 全配折扣**: OPS.channel_vendor_map 覆盖率 100%, 0 命中 R6 兜底
- **27 user.group 分布**: mu-aws 0.64 (71%) / cl-aws-svip 0.65 (13%) / provider_gamma-glm 0.4 (12%) / ast-aws 0.77 (2.7%) / ...

### v3 ReferenceError 历史 (2026-06-15, 部署铁律 #10 实战)

BillingV3Upstream.tsx line 285 写错:
```jsx
<li>输出: /data/billing-exports/{task_id}.zip</li>
```
→ `{task_id}` JSX 插值但作用域没定义 → ReferenceError → 整页白屏. 修时又写 `{genVendor}-{ts}.zip` 崩 1 次. 修法: HTML 实体 `<taskID>`. 详细: `AGENTS.md` "v3 ReferenceError 历史" 节.

---

## v3 成本公式 (5 步反推, RFC §2 + 实战校准)

> **核心公式**: `cost = revenue / group_ratio × channel_vendor_map.discount`

### 5 步详解 (v3 RFC §2 + 实测校正)

```
1. revenue (消耗)    = log.quota / 500000                              (RoDB SQL, v2 同源)
2. group_ratio       = (log.other::jsonb->>'group_ratio')::numeric     (实测 log.other 已有, 不用拉 admin)
3. 原价               = revenue / group_ratio                            (还原 vendor 原始定价)
4. cost (累计成本)   = 原价 × channel_vendor_map.discount               (50 行 channel 折扣, v1 价目表 9 行已下架)
5. profit_margin     = (revenue - cost) / cost                          (财务看"赚几倍", 不用百分比)
```

### 实测案例 (user_alpha 5 月, 2026-06-14 远端)

```
user_alpha (uid=47, group=mu-aws, group_ratio=0.64) 调 ch-2 (provider_alpha, discount=0.24) 1 次
  quota=50000 → revenue = $0.1 USD
  原价 = $0.1 / 0.64 = $0.15625
  cost = $0.15625 × 0.24 = $0.0375
  profit_margin = ($0.1 - $0.0375) / $0.0375 = 1.667 = 166.7%

5 月总账单: 1,002,849 调用, $70,226.82 消耗, ~$17,557 成本 (按 0.24 平均), ~$52,669 毛利, 300% 利润率
```

### overview 简化 vs ZIP 精确

| 用途 | 公式 | 场景 |
|---|---|---|
| **overview 简化** | `cost = revenue × channel_discount` | SPA 默认页, GROUP BY 后 group_ratio 取 avg (不准) |
| **ZIP 精确** | `cost = (revenue / log.other.group_ratio) × channel.discount` | 财务对账, 逐 log 计算 (准) |

**财务对账用 ZIP 精确数**, overview 是趋势监控.

### 公式源头 (跟 v1 对比)

| 维度 | v1 (已下线, 价目表) | v3 (现行, 公式反推) |
|---|---|---|
| 数据来源 | upstream_pricing 9 行 CSV (覆盖率 18%) | **实时算** (50 行 channel_vendor_map, 覆盖率 100%) |
| 维护成本 | 高 (CSV 同步 + 校正) | 0 (渠道折扣现成) |
| 准确度 | 依赖 CSV 同步 | 100% 实时 |
| 复杂度 | 4 API + 1 SPA + 1 import 流程 | 0 维护, 复用 channel_vendor_map |
| 决策时间 | 2026-06-14 23:43 (价目表下架) | 2026-06-14 21:00 (v3 公式) |

---

## 0. 背景

2026-06-14 v1 billing 模块下线 (commit `72d82d4`):
- 18 端点全 404
- DB 表归档
- 文档归档
- Go 代码全删 (git history 保留)

v2 BILLING (commit `f6dceb0`) 替代了 v1 **客户对账** 8 端点, 但**没做**:
- **上游对账** 4 端点
- **利润分析** 1 端点

**v3 = 上游对账**, **v4 = 利润分析** (v4 必须等 v3 cost 出来).

---

## 1. 用户 3 个核心设计 (2026-06-14 21:00 决定)

### 设计 1: 上游对账默认页
> "根据供应商管理里面的录入, 把供应商在这里进行表格列出, 并将该供应商底下归属的几个渠道在这个月产生的消耗和消耗反推的累计成本以及计算出来的利润率进行展示."

**表格列**:
- 上游 (vendor_name)
- 渠道 (channel_name) —— 嵌套行, vendor 下面是 channel
- 当月消耗 (revenue USD)
- 累计成本 (cost USD)
- 利润率 ((消耗 - 成本) / 成本)
- 状态 / 操作

**双层结构**:
```
| 上游 openai-azure (合) | $4,000 | $1,000 | 300%  |
|   ├─ ch-78-azure       | $2,500 | $625   | 300%  |
|   └─ ch-79-azure       | $1,500 | $375   | 300%  |
| 上游 provider_alpha (合)     | $5,000 | $1,200 | 316%  |
|   ├─ ch-2-provider_alpha     | $3,000 | $720   | 317%  |  (discount=0.24)
|   └─ ch-7-provider_alpha     | $2,000 | $480   | 317%  |  (discount=0.24)
```

### 设计 2: 生成上月对账单 (按按钮)
> "生成了上个月的对账单, 对账单包括了按日期、渠道、还有模型聚合的输入tokens数量、输出tokens数量、缓存创建tokens、缓存个读写tokens、合计成本价格."

**对账单内容 (HTML + XLSX)**:
- 汇总: vendor / period / total_cost / total_revenue / total_profit / profit_rate
- 按日期: date / request_count / prompt_tokens / completion_tokens / cache_creation_tokens / cache_read_tokens / total_cost
- 按渠道: channel / request_count / 4 token 字段 / total_cost
- 按模型: model / request_count / 4 token 字段 / total_cost

**字段说明 (缓存拆分)**:
- 缓存创建 tokens = 客户用 prompt cache 写入的 tokens
- 缓存读写 tokens = 客户命中 cache 读取的 tokens
- (v3 范围内只跟 v2 一样, 单字段 `cache_tokens` 暂用 v1 同名显示, 标记为 "缓存 (合计)")

### 设计 3: 异步 + ZIP
> "对账单的生成和下载参考客户账单部分, 采用异步的方式进行生成和下载, 同样提供html和excel两种格式."

- 复用 v2 `billing_export_tasks` 表 + worker pool + 30 天清理
- 复用 v2 任务中心 UI (kind='upstream' 区分)
- 复用 v2 ZIP 打包 + 下载
- HTML + XLSX 两格式

---

## 2. 成本反推公式 (5 步反推)

```
1. revenue (消耗)       = log.quota / 500000  USD
2. group_ratio          = GroupRatio[user.group]    ← admin /api/option/ GroupRatio JSON
                            (默认 1.0, 27 用户 × group 名 缓存)
3. 原价                  = revenue / group_ratio
                            (vip 客户 0.85 倍率, 原价 = 实际收 / 0.85)
4. cost (累计成本)      = 原价 × channel_vendor_map.discount
                            (渠道折扣: 0.42=4.2 折, 0.10=1 折)
5. profit_margin        = (revenue - cost) / cost
                            (利润率, 财务看 "赚几倍")
```

**示例**:
- user_alpha (group=mu-aws) 调 ch-2 (provider_alpha, discount=0.24)
- 1 个 log: quota=50000 (=$0.1 USD)
- group_ratio = GroupRatio["mu-aws"] = 1.0
- 原价 = $0.1 / 1.0 = $0.1
- cost = $0.1 × 0.24 = $0.024
- profit_margin = ($0.1 - $0.024) / $0.024 = **316%**

---

## 3. 数据源

### 3.1 RoDB (newapi.logs) — 客户消耗 + group

| 字段 | 来源 | 用途 |
|---|---|---|
| `quota` | log.quota | 客户消耗 (内部单位) |
| `group` | log.group | 客户分组成员 (用于查 group_ratio) |
| `channel_id` | log.channel_id | 渠道 ID (映射 vendor + discount) |
| `model_name` | log.model_name | 模型名 (按模型聚合) |
| `prompt_tokens` | log.prompt_tokens | 输入 tokens |
| `completion_tokens` | log.completion_tokens | 输出 tokens |
| `other->>'cache_tokens'` | log.other JSONB | 缓存 tokens (单字段, v3 标 "缓存 (合计)") |
| `created_at` | log.created_at | 按日期聚合 |

**注意**: log 表**没有 `group_ratio` 字段**（v1 老 newapi 才有, upstream 1.0+ 走 `GroupRatio` 系统 setting + admin /api/option/）

### 3.2 admin /api/option/ — group_ratio 缓存

| 字段 | 路径 | 更新频率 |
|---|---|---|
| `GroupRatio` JSON | `/api/option/?key=GroupRatio` | 1 hour (新 admin setting 变更不频繁) |
| `GroupGroupRatio` JSON | `/api/option/?key=GroupGroupRatio` | 1 hour (v3 暂不用, v4 用) |

**实际拿到的 GroupRatio 示例 (2026-06-14 远端 admin /api/option/)**:
```json
{
  "default": 1,
  "svip": 1,
  "special": 1,
  "cl-of-svip": 0.85,
  "claude-of-svip": 0.85,
  ...
}
```

### 3.3 OPS.channel_vendor_map — 渠道折扣

| 字段 | 含义 | 实测示例 |
|---|---|---|
| `channel_id` | 渠道 ID | 1, 2, 3, ... |
| `vendor_code` | 供应商代码 | openai-azure, provider_alpha |
| `discount` | **折扣系数** (0.42 = 4.2 折) | 0.42, 0.75, 0.10, 0.24, 0.06 |
| `auto_discount` | 自动识别折扣 | 同 discount (备份) |
| `auto_matched` | 识别来源 | "42折", "0.1折" |

### 3.4 3 数据源铁律 (不破坏)
- API: admin /api/option/ (group_ratio) — 数据源 1 ✅
- RoDB: newapi.logs (客户消耗 + 渠道) — 数据源 2 ✅
- cache: OPS.channel_vendor_map (折扣) — 数据源 3 ✅

---

## 4. 决策表 (5 Q&A)

### Q1 范围
- **选**: 仅上游对账, 利润分析 v4 做
- **理由**: 利润分析依赖上游 cost, 先做上游

### Q2 数据源
- **选**: RoDB (newapi.logs) + admin /api/option/ (group_ratio) + OPS.channel_vendor_map (discount)
- **拒绝**: admin /api/data/ (DataExportEnabled=false 返空, 拿不到 quota 拆分), cache_logs_summary (缺 cache_tokens 拆分)

### Q3 任务模式
- **选**: 异步任务 + 30 天清理 (跟 v2 完全一样)
- **复用**: v2 `billing_export_tasks` 表 + worker pool + ZIP 打包 + 任务中心

### Q4 SPA 页面
- **选**: 1 新页面 = 上游对账默认页 (含 vendor/channel 双层) + 复用 v2 任务中心
- **拒绝**: 利润分析是 v4 范围

### Q5 缓存字段拆分
- **选**: 跟 v2 一样单字段 `cache_tokens` (实际 newapi 只存单 JSON 字段)
- **拒绝**: 反查 newapi 加列 (跨部门, 慢), 拆 50/50 (粗略)

---

## 5. API 设计 (5 端点)

### 5.1 端点列表

| # | 端点 | 方法 | 权限 | 用途 |
|---|---|---|---|---|
| 1 | `/api/billing/v3/upstream/current-month-overview` | GET | admin/finance/viewer | 上游当月累计 (vendor + channel 双层) |
| 2 | `/api/billing/v3/upstream/export-last-month` | POST | admin/finance | 创建上游月度对账任务 (异步 ZIP) |
| 3 | `/api/billing/v3/upstream/:vendor_code/tasks` | GET | admin/finance/viewer | 单上游任务历史 |
| 4 | `/api/billing/v3/export-tasks` | GET | admin/finance/viewer | 全任务 (kind='upstream') |
| 5 | `/api/billing/v3/export-tasks/:task_id/download` + `cancel` | GET/POST | admin/finance (cancel) | ZIP 下载 / 取消 |

**复用 v2**:
- `billing_export_tasks` 表 (加 `kind` + `vendor_code` 字段)
- `export_worker` worker pool + 每用户 semaphore(2)
- `prune.go` 30 天清理
- 任务中心 UI (v2 已有, 加 kind 列)

### 5.2 数据流 (生成上游对账)

```
1. SPA "生成本月对账 ZIP" 按钮
2. POST /api/billing/v3/upstream/export-last-month {formats:"html,xlsx"}
3. handler:
   a. 写 billing_export_tasks (kind='upstream', period=YYYY-MM, status=pending)
   b. enqueue 到 export_worker (跟 v2 一样)
   c. 返 task_id
4. worker (跟 v2 复用, 派发按 kind):
   a. RoDB 拉上月 logs WHERE channel_id IN (本 vendor 的 channel_ids)
   b. 反推 cost (按 §2 公式)
   c. 算 by_date / by_channel / by_model
   d. 生成 HTML (upstream.html 模板) + XLSX (上游多 sheet) + README
   e. ZIP 打包到 /data/billing-exports/{task_id}.zip
   f. 写 billing_export_tasks (status=success, file_size, file_path)
5. SPA 任务中心下载 ZIP
```

---

## 6. DB Schema (复用 v2 + 2 字段)

### 6.1 复用 v2 表 + 2 字段

```sql
-- migration: 2026-06-14-billing-v3-upstream-tasks.sql
ALTER TABLE billing_export_tasks ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'customer';
ALTER TABLE billing_export_tasks ADD COLUMN IF NOT EXISTS vendor_code TEXT;

-- 约束
ALTER TABLE billing_export_tasks ADD CONSTRAINT billing_export_tasks_kind_check
    CHECK (kind IN ('customer', 'upstream'));

-- 索引
CREATE INDEX IF NOT EXISTS idx_billing_export_tasks_kind ON billing_export_tasks(kind);
CREATE INDEX IF NOT EXISTS idx_billing_export_tasks_vendor_code ON billing_export_tasks(vendor_code) WHERE vendor_code IS NOT NULL;
```

### 6.2 不增表

- 价目表: 复用 `upstream_vendors` + `upstream_pricing` (50 渠道 5 vendor 9 pricing)
- 渠道映射: 复用 `channel_vendor_map` (50 行, 已有 discount 字段)
- 任务记录: 复用 `billing_export_tasks` + `billing_export_task_logs` (只加 2 列 + 2 索引)

---

## 7. SPA 设计

### 7.1 上游对账默认页 `/billing/v3/upstream`

**布局**:
```
┌─────────────────────────────────────────────────────────────┐
│ 上游对账 (2026-06 至今)             [生成本月对账 ZIP]      │
├─────────────────────────────────────────────────────────────┤
│ [总览卡]                                                    │
│   总消耗 $X,XXX    总成本 $Y,YYY    利润率 Z%              │
├─────────────────────────────────────────────────────────────┤
│ [表格 (vendor + channel 双层)]                              │
│   上游 / 渠道 / 消耗 / 成本 / 利润率 / 操作                 │
│   ▼ openai-azure (合)                                       │
│     ch-78-azure    $X,XXX  $XXX   300%                      │
│     ch-79-azure    $X,XXX  $XXX   300%                      │
│   ▼ provider_alpha (合)                                           │
│     ch-2-provider_alpha  $X,XXX  $XXX   317%   (折扣 0.24)        │
│     ch-7-provider_alpha  $X,XXX  $XXX   317%   (折扣 0.24)        │
└─────────────────────────────────────────────────────────────┘
```

### 7.2 复用 v2 任务中心

- `/billing/exports` 列表加 1 列 `类型` (客户/上游)
- 复用 v2 下载/取消逻辑

---

## 8. 业务规则 (复用 v1 R1-R5)

- R1 零输出免单: cost 照算, revenue=0, profit=-cost
- R2 图片生成: is_image_generation=true 标记
- R3 退款: revenue=-quota×$0.002/1K
- R4 错误不计: type=5 cost=0, revenue=0
- R5 未匹配上游: unmatched_reason (missing_pricing / unmapped_channel / unknown_group)
- 详细: `archive/v1-docs/BILLING-RULES.md`

**v3 新增 R6**: `missing_group_ratio` — `log.group` 不在 `GroupRatio` 字典里时, 默认 1.0, 标记 `unmatched_reason="unknown_group"`. 财务对账时手工补.

---

## 9. 部署 (PR #7)

### 9.1 步骤

1. 跑 migration `migrations/2026-06-14-billing-v3-upstream-tasks.sql` (2 字段 + 2 索引)
2. 推镜像 + restart api
3. 公网 5 端点验证
4. 创建 1 测试任务 (provider_alpha 上月) → success → 下载 ZIP → 解压校验 4 维度

### 9.2 关键经验 (PR #8 学到的)

- 字段先 `information_schema.columns` 验证
- 容器内 JSONB 用 `other->>'字段'` + `::bigint`
- Dockerfile COPY 覆盖所有运行时读路径
- volume mount 必 chown
- 测清理 `defer os.RemoveAll` 必须在读 zip 之后

---

## 10. 工作量 + 7 PR 切分

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| **#1** | v3 RFC + kind + vendor_code 字段 migration | (待) | 0.5 天 |
| **#2** | 成本反推核心 (GroupRatio provider + CalcLogCost) + unit test | (待) | 1.5 天 |
| **#3** | 上游对账生成器 (HTML + XLSX + ZIP + 模板) | (待) | 1.5 天 |
| **#4** | API 5 端点 (overview/export/customer-tasks/复用 v2 download/cancel) | (待) | 1.5 天 |
| **#5** | SPA 上游对账默认页 (vendor/channel 双层) | (待) | 1 天 |
| **#6** | 单测 (cost 反推 + 生成器) + 文档 (v3 RULES) | (待) | 0.5 天 |
| **#7** | 远端部署 + 公网验证 5 端点 | (待) | 0.5 天 |
| **总计** | | | **7 天** |

---

## 11. 跟 v2 / v4 的关系

```
v2 (客户对账, PR #1-#8, 2026-06-14 完成) → 6 端点 + 1 SPA + 1 任务中心
v3 (上游对账, PR #1-#7, 2026-06-14 起)   → 5 端点 + 1 SPA + 复用 v2 任务中心
v4 (利润分析, v3 跑通 1 周后)           → 1 端点 + 1 SPA 汇总卡
```

v4 利润分析 = v2 收入 + v3 成本, 数据依赖 v3.

---

## 12. 风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| GroupRatio 缓存跟实际 setting 不一致 (新配置未生效) | 中 | cost 不准 | 1 hour TTL, 强制刷新端点 |
| 27 用户 group 变化 (新建用户) | 低 | 缓存缺 | 5min 增量, 新用户从 admin /api/user/?p=0 拉 |
| RoDB 1 月 logs (50 万行) 慢 | 中 | 任务超时 | 跟 v2 一样用 cache_logs_summary_by_model_5min |
| channel_vendor_map.discount=0 (上游白送) | 低 | cost=0, profit 巨大 | 标记 unmatched_reason, 财务查 |
| channel_vendor_map.discount_override=true | 中 | discount 是手工覆盖, 不是自动 | 仍用 discount 字段 (但备注 manual create) |

---

## 13. v1 业务规则 R1-R5 复用

- v1 上游对账 (commit `72d82d4` 前) 走 RoDB + CalcLogCost 逐 log 反推
- v1 CalcLogCost 用 upstream_pricing 价目表 (9 行)
- v3 改用 channel_vendor_map.discount (50 行, 更准, 跟业务运营配的对得上)
- v3 不依赖 upstream_pricing (因为价目表只有 9 行覆盖率低, channel_vendor_map.discount 覆盖率 100%)

**业务连续性**: R1-R5 全复用, 公式从价目表换到 channel discount, 成本可能略变 (v1 算错 vendor 价格, v3 算实际渠道折扣).

---

## 14. 监控模块 (2026-06-15, 错误率新口径对接)

> **范围**: v3 上游对账跟监控中心"渠道健康度"模块的数据关联. v3 cost 反推**不依赖**监控, 但监控错误率跟 v3 R4 (错误不计) 是不同口径.

### 跟 v3 业务规则的区别

| 维度 | v3 R4 错误不计 | 监控错误率 (新口径) |
|---|---|---|
| 范围 | BILLING 账单计算 (cost + revenue) | 监控中心 channel health 卡片 |
| 触发 | `type=5` (任何错误) → cost=0, revenue=0 | `type=5 AND jsonb_array_length(use_channel) = 1` → 独立错误 |
| 分母 | 全部 type=2 业务请求 | type IN (2, 5, 6) 业务请求 |
| 用途 | 财务对账 (错误请求不收钱也不算成本) | 运营监控 (渠道健康度) |

**关键区别**: v3 R4 = **账单视角**, "这条请求是错的, 不收钱也不花钱"; 监控错误率 = **运营视角**, "这个渠道 24h 内错误率高, 要查".

### 渠道健康度 3 规则 (2026-06-15 09:18 用户决策)

1. **24h 内无调用 → 不显示**: `HAVING SUM(request) > 0`
2. **禁用状态 → 不显示**: `channel.status != 1` 排除
3. **卡片展示**: 表格 → 卡片 grid (auto-fill 360px), 关键信息 = 错误率 + 供应商

### 错误率新口径 (2026-06-15 09:43)

| 项 | 定义 | SQL |
|---|---|---|
| **分母**: 业务请求 | type IN (2, 5, 6) 跨 24h | `COUNT(*) FILTER (WHERE type IN (2, 5, 6))` |
| **分子**: 独立错误 | type=5 AND use_channel.length = 1 | `COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(other->'use_channel') = 1)` |
| **P95/P99** | channel_health_5min 桶 MAX | 避免 RoDB percentile_cont 慢 |
| **阈值** | ≥ 20% 触发红边呼吸 | 红边只覆盖渠道卡片, 不覆盖综合 KPI |

**详细**: `AGENTS.md` "新口径错误率定义" 节. **实测**: ch 110 RoDB SQL 直查 type=2=3953, type=5=862, 业务请求=4815; API 返 rate=17.93% (24h 178 万行 logs → 7.3ms 扫描).

### 监控引擎关键发现 (部署铁律 #15)

`cmd/server/main.go` **必须** import scheduler + 调 `scheduler.Run(rootCtx, cfg)`. 否则 monitor tick 永不跑, channel_health_5min 0 行. 实战: 监控中心首版发现"44 渠道 latest_health 全 null", 排查 30min 发现是漏启 scheduler, 修了 1 行 import + 1 行 Run 立刻有数据.

**v3 不依赖 monitor tick**: v3 cost 反推走 RoDB 实时 (`newapi.logs` + `OPS.channel_vendor_map`), 不走 `channel_health_5min` cache. 监控模块用 cache 是因为 dashboard 5s tick 频繁, cache 减压; v3 overview 是财务手动点, RoDB 实时够用.

### 监控 + BILLING 数据流 (跨模块)

```
newapi.logs (RoDB)
    ├─> channel_health_5min (cache, 1min tick via scheduler.Run)
    │     └─> 监控中心: 错误率 / P95 / P99 (渠道卡片)
    └─> v3 cost 反推 (实时)
          └─> 上游对账: vendor/channel 双层 cost + margin

OPS.channel_vendor_map (cache, 5min sync)
    ├─> v3 cost 反推公式 step 4 (原价 × discount)
    └─> 监控卡片 "供应商" 字段展示 (join upstream_vendors 拿 vendor_name)
```

**3 数据源铁律不破坏**: API (admin /api/option/ GroupRatio, 备用) + RoDB (logs) + cache (channel_vendor_map / channel_health_5min).

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

### v3 在累计中的位置

- v3 是 v2 跟 v4 之间的桥梁: v2 提供 revenue, v3 提供 cost, v4 提供 profit = revenue - cost
- v3 复用 v2 worker / 任务中心 / ZIP 路径 / 30 天清理 → **省 3 PR 工作量** (≈ 3 天, v3 7 PR 比完全独立 10 PR 短)
- v3 5 业务规则 R1-R5 + R6, v4 利润分析 0 新规则全复用 → **业务连续性 0 断点**

### 累计 PR + commit 时间线

| 模块 | PR 数 | Commit 范围 | 工作量 |
|---|---|---|---|
| v1 (已下线) | — | 2026-06-14 `72d82d4` 下线 | — |
| v2 客户对账 | 8 | `5e464d4` ~ `f6dceb0` | 8 天 |
| v3 上游对账 | 7 | `df40966` ~ 待 `PR #7` | 7 天 |
| v4 利润分析 | 6 (合并 4 commit) | `7150dfb` + `a6b2b8d` | 3.9 天 |
| upstream_pricing (已下架) | 1 | 2026-06-14 23:43 | 0.5 天 |
| **BILLING 总投入** | | | **≈ 19.4 天** |

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
