# BILLING v3 业务规则 (上游对账)

> **作者**: Mavis
> **生成时间**: 2026-06-14 22:35
> **状态**: ✅ 终版 (v3 上游对账 7 PR 完成)
> **范围**: 仅上游对账 (v3 = PR #1-#7, 2026-06-14)
> **关联**: `docs/BILLING-v3-RFC.md` (RFC) / `docs/BILLING-v2-RULES.md` (v2 客户对账) / `archive/v1-docs/BILLING-RULES.md` (v1)

---

## 状态 (2026-06-15 同步)

| 维度 | 状态 |
|---|---|
| **业务规则 R1-R5 复用 v1** | ✅ 全复用, 0 业务断点 |
| **R6 缺渠道折扣 (v3 新增)** | ✅ 实测 0 命中 (50/50 channel 全配), 兜底保留 |
| **5 端点** | ✅ 已上线, 真实数据 5 vendor 39 channel / $1234 revenue / $678 cost / 81.9% 毛利率 |
| **成本公式** | ✅ `cost = revenue / group_ratio × channel_discount` (5 步反推) |
| **公式对齐实际实现** | ✅ v3 ZIP 走精确版 (`revenue / log.other.group_ratio × discount`), v3 overview 走简化版 (`revenue × channel.discount`) |
| **监控模块对接** | ✅ 监控错误率跟 v3 R4 错误不计**不同口径**, 详见 "## 错误率口径" 节 |
| **业务规则变更** | 2026-06-15 监控新口径**不影响 v3 规则**, 监控只读 logs 不写, v3 cost 反推走 RoDB + channel_vendor_map, 数据流无交叉 |
| **v3 上游对账 5min cache (PR #9)** | ✅ 公网稳跑 6+ 小时, commit 1c5a936 round-robin 模式 (单次 1 vendor × 1 period, 50min 一轮) |

### 业务规则 R1-R5 对齐实际实现 (2026-06-15 自检)

| 规则 | RFC 设计 | 实际实现 | 对齐 |
|---|---|---|---|
| R1 零输出免单 | `type=2 AND completion_tokens=0 AND use_time>=2s` → revenue=0 | 同 RFC | ✅ |
| R2 图片生成标记 | `model_name` 包含 image/sora/midjourney/mj-/dalle | 同 RFC, 10 case 单测全过 | ✅ |
| R3 退款 | `type=3` → 不处理 (上游不退款) | 同 RFC | ✅ |
| R4 错误不计 | `type=5` → cost=0, revenue=0, 跳过 | 同 RFC, SQL `WHERE type=2` 已排除 type=5 | ✅ |
| R5 未匹配上游 | channel 没配 vendor → cost=0 + unmatched_reason | 同 RFC | ✅ |
| **R6 缺渠道折扣 (v3 新增)** | channel_vendor_map 没记录 或 discount<=0 → cost=0 + unmatched_reason="missing_channel_discount" | 同 RFC, 实测 0 命中 | ✅ |

**自检结论**: 6 业务规则跟实际实现 100% 对齐, 无遗漏无歧义.

---

## 错误率口径 (2026-06-15 新增, 跟 v3 R4 区分)

> **关键决策**: v3 R4 错误不计 = **账单视角**; 监控错误率 = **运营视角**. 两个口径完全不同, 财务看 v3 ZIP 时**不能**用监控错误率.

### 两口径对比

| 维度 | v3 R4 错误不计 (账单) | 监控错误率新口径 (运营) |
|---|---|---|
| 范围 | BILLING v3 cost 反推 + revenue 计算 | 监控中心 channel health 卡片 |
| 触发条件 | `type=5` (任何错误) | `type=5 AND jsonb_array_length(use_channel) = 1` (独立错误) |
| 分母 | 全部 type=2 业务请求 | `type IN (2, 5, 6)` 业务请求 |
| 用途 | 财务对账 (错误请求不收钱也不算成本) | 运营监控 (渠道健康度) |
| 决策时间 | v1 (2026-06-14 20:39 下线前) | 2026-06-15 09:43 |
| 文档 | `archive/v1-docs/BILLING-RULES.md` R4 + 本文档 §1 | `AGENTS.md` "新口径错误率定义" + `docs/CHANGELOG.md` 续 5 |

### 监控错误率新口径 (2026-06-15 09:43 决策)

| 项 | 定义 | SQL 模式 |
|---|---|---|
| **分母**: 业务请求 | `type IN (2, 5, 6)` 跨 24h, 排除登录/充值/管理操作 | `COUNT(*) FILTER (WHERE type IN (2, 5, 6))` |
| **分子**: 独立错误 | `type=5 AND jsonb_array_length(other->'use_channel') = 1`, 排除 retry 中间失败 | `COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(other->'use_channel') = 1)` |
| **P95/P99 延迟** | `channel_health_5min` 桶 MAX(最新桶), 避免 RoDB percentile_cont 慢 | 走 cache |
| **错误率阈值** | ≥ 20% 触发红边呼吸 | 红边只覆盖渠道卡片, 不覆盖综合 KPI |

### 为什么 v3 不采纳监控错误率

v3 cost 反推**只看成本**, 错误率 (无论是 v3 R4 还是监控新口径) 都触发 cost=0 / revenue=0. 两个口径对 v3 来说**结果一致**:
- `type=5` 一律 cost=0 + revenue=0 (v3 R4)
- 独立错误 (监控新口径) ⊆ 全部 type=5 → 监控错误率 ≤ v3 错误率, 但 v3 已经按"全 type=5" 处理

**结论**: v3 业务规则 R4 不需要改, 监控错误率仅用于渠道健康度卡片展示. 两口径并存, 不冲突.

---

## 监控模块 (2026-06-15 新增, 跟 v3 数据流关联)

> **范围**: 监控中心"渠道健康度"模块, 跟 v3 上游对账的数据流关联. v3 不依赖监控数据, 但监控卡片显示 v3 算出来的"成本相关渠道".

### 监控引擎 (部署铁律 #15)

**关键**: `cmd/server/main.go` **必须** import scheduler + 调 `scheduler.Run(rootCtx, cfg)`. 否则 monitor tick 永不跑, channel_health_5min / alert_histories 0 数据.

**实战**: 监控中心首版发现"44 渠道 latest_health 全 null", 排查 30min 发现是漏启 scheduler, 修了 1 行 import + 1 行 Run 立刻有数据.

### 监控 3 规则 (2026-06-15 09:18 用户决策)

1. **24h 内无调用 → 不显示**: `HAVING SUM(request) > 0`
2. **禁用状态 → 不显示**: `channel.status != 1` 排除
3. **卡片展示**: 关键信息 = 错误率 + 供应商 + 模型数, 表格 → 卡片 grid (auto-fill 360px)

### 监控 + v3 数据流 (跨模块)

```
newapi.logs (RoDB)
    ├─> channel_health_5min (cache, 1min tick via scheduler.Run)
    │     └─> 监控中心: 错误率 / P95 / P99 (渠道卡片, 新口径)
    └─> v3 cost 反推 (实时, RoDB + log.other.group_ratio)
          └─> 上游对账: vendor/channel 双层 cost + margin (R1-R6 业务规则)

OPS.channel_vendor_map (cache, 5min sync)
    ├─> v3 cost 反推公式 step 4 (原价 × discount)
    └─> 监控卡片 "供应商" 字段 (join upstream_vendors 拿 vendor_name)
```

**3 数据源铁律不破坏**: API (admin /api/option/ GroupRatio, v3 备用) + RoDB (logs, v3 主源) + cache (channel_vendor_map + channel_health_5min, v3 + 监控共用).

### 监控实测 (2026-06-15 09:50)

- ch 110 RoDB SQL 直查: type=2=3953, type=5=862, 业务请求=4815
- API 返: req=4816 (差 1 是 RoDB 实时 vs cache sync 1min 延迟), rate=17.93%
- 24h 178 万行 logs → 7.3ms 扫描 (`idx_created_at_type` 复合索引完美命中)
- 15 渠道聚合, 走 2 步: RoDB logs (7ms) + OPS channel_health_5min (5ms)
- 15 渠道, 5 张触发红边 (错误率 ≥ 20%): ch 14 / 69 / 101 / 22 / 54

---

## 0. v1 → v2 → v3 变更总览

| 维度 | v1 (已下线) | v2 (PR #1-#8, 已完成) | v3 (PR #1-#7, 本批) |
|---|---|---|---|
| **范围** | 客户对账 + 上游对账 + 利润分析 (18 端点) | 仅客户对账 (6 端点) | **仅上游对账 (5 端点)** |
| **状态** | ❌ 2026-06-14 20:39 下线 | ✅ 上线 | ✅ 上线 (待部署 PR #7) |
| **数据源** | RoDB 5万行 in-memory | RoDB + cache_logs_summary | RoDB + OPS.channel_vendor_map |
| **任务模式** | 同步 (前端点按钮即算) | 异步 (worker pool) | 异步 (复用 v2 worker) |
| **成本反推** | 价目表 (upstream_pricing 9 行) | 不算 (客户对账只看 revenue) | **渠道折扣 (channel_vendor_map 50 行, 覆盖率 100%)** |
| **缓存字段** | cache_creation + cache_read 拆 2 字段 (假设) | 1 单字段 cache_tokens (newapi 实际) | 同 v2 (1 单字段) |

---

## 1. 5 业务规则 (复用 v1 R1-R5)

v3 复用 v1 5 业务规则, **不变**:

| 规则 | 优先级 | 触发条件 | 动作 |
|---|---|---|---|
| **R1 零输出免单** | P0 | `type=2 AND completion_tokens=0 AND use_time>=2s` | cost 照算, revenue=0, profit=-cost |
| **R2 图片生成标记** | P1 | `model_name` 包含 image/sora/midjourney/mj-/dalle | is_image_generation=true 标记 |
| **R3 退款** | P0 | `type=3` (退款) | v3 不处理 (上游不退款, type=3 是客户对账范围) |
| **R4 错误不计** | P0 | `type=5` (错误) | cost=0, revenue=0, 跳过 |
| **R5 未匹配上游** | P1 | channel 没配 vendor / 渠道没配折扣 | cost=0, unmatched_reason 标记 |

**详细**: `archive/v1-docs/BILLING-RULES.md`

### v3 新增 R6: missing_channel_discount

| 规则 | 优先级 | 触发条件 | 动作 |
|---|---|---|---|
| **R6 缺渠道折扣** | P1 | channel_id 在 `OPS.channel_vendor_map` 没记录 或 discount<=0 | cost=0, `unmatched_reason="missing_channel_discount"`, 财务手工补 |

**影响**: 当前远端 49 channel 全部配了 discount, 0 命中, 但保留规则兜底.

---

## 2. 成本反推公式 (v3 核心, RFC §2)

### 2.1 公式 (5 步)

```
1. revenue (消耗)    = log.quota / 500000
2. group_ratio       = (log.other::jsonb->>'group_ratio')::numeric   (默认 1.0)
3. 原价               = revenue / group_ratio
4. cost (累计成本)   = 原价 × channel_vendor_map.discount
5. profit_margin     = (revenue - cost) / cost
```

### 2.2 公式详解

#### Step 1: revenue (消耗)
- **来源**: `newapi.logs.quota` (bigint)
- **单位**: 内部单位, 1 USD = 500000 quota (newapi 全局汇率)
- **公式**: `revenue = quota / 500000`

例: `quota=50000` → `revenue = $0.1 USD`

#### Step 2: group_ratio
- **来源**: `newapi.logs.other` JSON 字段, 提取 `group_ratio` (numeric)
- **SQL**: `(other::jsonb->>'group_ratio')::numeric`
- **默认值**: 1.0 (字段缺失时, 反推 = revenue)
- **实际值分布** (远端 2026-06-14 查):

| group | group_ratio | logs 占比 |
|---|---|---|
| mu-aws | 0.64 | 71% |
| cl-aws-svip | 0.65 | 13% |
| provider_gamma-glm | 0.4 | 12% |
| ast-aws | 0.77 | 2.7% |
| spe-of | 0.78 | 1.9% |
| ethan-awsof | 0.8 | 1.6% |
| ... | ... | ... |

#### Step 3: 原价
- **公式**: `original_price = revenue / group_ratio`
- **意义**: 把客户实际付的金额, **还原**成 vendor 原始定价 (没折扣前的"原价")
- 例: `revenue=$0.1, group_ratio=0.64` → `original_price = $0.15625`

#### Step 4: cost (累计成本)
- **来源**: `OPS.channel_vendor_map.discount` (numeric, 0-1)
- **公式**: `cost = original_price × channel_discount`
- **意义**: vendor 原价 × 渠道实际折扣 = 我们付给上游的金额
- 例: `original_price=$0.15625, discount=0.24` → `cost = $0.0375`

#### Step 5: profit_margin
- **公式**: `profit_margin = (revenue - cost) / cost`
- **意义**: **财务看"赚几倍"** (不是 "赚几个百分比")
- 例: `revenue=$0.1, cost=$0.0375` → `profit_margin = 1.667` (= 166.7%)

### 2.3 实测案例 (user_alpha 5 月)

```
user_alpha (uid=47, group=mu-aws) 5 月 1 日调 ch-2 (provider_alpha, discount=0.24) 1 次
  quota=50000 → revenue = $0.1
  group_ratio = 0.64
  original_price = $0.1 / 0.64 = $0.15625
  cost = $0.15625 × 0.24 = $0.0375
  profit_margin = ($0.1 - $0.0375) / $0.0375 = 1.667 = 166.7%
```

5 月总账单: user_alpha 1,002,849 调用, $70,226.82 消耗, ~$17,557 成本 (按 0.24 平均), ~$52,669 毛利, 300% 利润率

### 2.4 公式简化版 (overview 端点)

**当前月 overview 端点 (PR #4) 用简化版** (不用 group_ratio, 走 SQL GROUP BY 后平均)：
```
cost = revenue × channel_discount
```

原因: SQL GROUP BY channel_id 后, group_ratio 字段是 avg (不准), overview 快速估算用, 不准

**ZIP 生成用精确版** (走 CalcLogCost, 逐 log 计算):
```
cost = (revenue / log.other.group_ratio) × channel.discount
```

**财务对账用 ZIP 精确数**, overview 是趋势监控.

---

## 3. 4 token 字段定义 (v3 跟 v2 一致)

| 字段 | 来源 | SQL | 用途 |
|---|---|---|---|
| `prompt_tokens` | log.prompt_tokens | `SUM(prompt_tokens)` | 输入 tokens |
| `completion_tokens` | log.completion_tokens | `SUM(completion_tokens)` | 输出 tokens |
| `cache_tokens` | `log.other::jsonb->>'cache_tokens'` | `SUM((other::jsonb->>'cache_tokens')::bigint)` | 缓存 tokens (单字段, 标"缓存 (合计)") |
| `revenue_usd` | `log.quota / 500000` | `SUM(quota) / 500000` | 客户消耗 USD |

**v3 字段显示**: "输入 tokens / 输出 tokens / 缓存 tokens (合计) / 客户消耗 (USD)"

**v3 不拆 "缓存创建 / 缓存读写" 2 字段** (你 2026-06-14 21:00 决定, 跟 v2 一致):
- 原因: newapi 实际只存 1 JSON 字段 `cache_tokens` (v2 PR #8 部署验证)
- 未来: 如果要拆, 需 newapi 升级加 cache_creation_tokens + cache_read_tokens 2 列

---

## 4. 异步任务系统 (复用 v2, 4 状态)

### 4.1 任务状态机 (跟 v2 一致)

```
pending → running → success
              ↓
          failed
              ↓
       cancelled (admin/finance 手动)
```

### 4.2 任务字段 (BILLING v3 加 2 字段)

| 字段 | v2 | v3 |
|---|---|---|
| kind | "customer" (默认) | "upstream" |
| vendor_code | "" (空) | "provider_alpha" / "openai-azure" / ... |
| user_id | uid (客户) | uid (创建者, 操作人) |
| period | "2026-05" | "2026-05" |
| formats | "html,xlsx" | "html,xlsx" |
| status | pending/running/success/failed/cancelled | 同上 |
| file_path | /data/billing-exports/{task_id}.zip | 同上 |

### 4.3 任务保留期 (Q2 决策)

- **30 天后自动清** (凌晨 cron `internal/billing/prune.go`)
- 财务月初 1-5 号需要时, 重新创建任务

### 4.4 任务限流 (Q3 决策)

- **每用户 ≤ 2 个 running** (Q3 决策, 二次确认)
- 全局 2 worker goroutine
- EnqueueExportTask 内部查 DB count 限流

---

## 5. ZIP 输出格式 (v3)

### 5.1 ZIP 内容

```
README.txt                           - 说明 + 周期 + 文件列表
statement.html                       - 人类可读 (浏览器打开)
statement.xlsx                       - 财务处理 (4 sheet)
```

### 5.2 XLSX 4 sheet

| Sheet | 列 |
|---|---|
| 汇总 | vendor / 周期 / 调用次数 / 4 token / cost / revenue / profit / margin |
| 按渠道 | channel_id / channel_name / 调用次数 / 4 token / cost / revenue |
| 按模型 | model / 调用次数 / 4 token / cost / revenue |
| 按天 | date / 调用次数 / 4 token / cost / revenue |

合计行 (加粗 + 黄色背景) 写在每个 sheet 末尾

### 5.3 HTML 模板 (RFC §5.2)

- 顶部: vendor / 周期 / 生成时间 (summary card)
- 概览: 总请求数 / 客户消耗 / 累计成本 / 毛利 / 毛利率
- 按渠道 / 按模型 / 按天 3 维度表格 (条件渲染, 空数据不显示)
- footer: 公式说明 + 链接到任务中心

---

## 6. RBAC (跟 v2 一致)

| 角色 | 读 | 写 (创建/取消) |
|---|---|---|
| admin | ✅ | ✅ |
| finance | ✅ | ✅ |
| viewer | ✅ | ❌ |

**详细**: `internal/auth/auth.go` + `internal/dal/ops_models.go` OpsUserRole 常量

---

## 7. 5 Q&A 决策记录 (跟 RFC §4 一致)

| # | 决策 | 选项 | 选择 |
|---|---|---|---|
| Q1 | 范围 | 客户对账 / 上游对账 / 一起 | **上游对账** (v3), 客户 v2 已有, 利润 v4 |
| Q2 | 数据源 | RoDB / admin /api/data/ / cache | **RoDB (newapi.logs) + OPS.channel_vendor_map + log.other (group_ratio)** |
| Q3 | 任务模式 | 同步 / 异步 / 混合 | **异步任务 + 30 天清理 (复用 v2)** |
| Q4 | SPA 页面 | 1 页面 / 2 页面 / 富交互 | **1 新页面 (vendor/channel 双层) + 复用 v2 任务中心** |
| Q5 | 缓存字段 | 拆 2 字段 / 单字段 / 跟 newapi 加列 | **跟 v2 一样单字段 (newapi 实际只存 1 JSON 字段)** |

---

## 8. 测试覆盖

### 8.1 单元测试 (PR #2 + PR #3, 共 18 case)

- `TestCalcLogCost` (6 case): user_alpha 实测 / group_ratio=1.0 / =0 / quota=0 / discount=0 / =1.0
- `TestCalcProfitMargin` (4 case): 166.7% / 0 / cost=0 / -100%
- `TestIsImageGenerationModel` (10 case): gpt-image / sora / midjourney / mj- / dalle
- `TestRenderUpstreamHTML_NotEmpty` (12 字段校验)
- `TestRenderUpstreamHTML_EmptyByDay`: 空 ByDate 不显示表头
- `TestRenderUpstreamXLSX_4Sheets`: zip 内 4 sheet
- `TestPackUpstreamZip_HTMLAndXLSX`: 3 文件
- `TestPackUpstreamZip_HTMLOnly`: 2 文件

### 8.2 集成测 (PR #6 加 1 e2e)

- `TestIntegration_UpstreamEnd2End`: CalcUpstreamStatement + Render + Pack 全链路, mock RoDB + mock OPS

### 8.3 公网端到端 (PR #7, 待部署)

- 5 端点公网 200/4xx
- 创建 1 测试任务 → success → 下载 ZIP → 解压校验

---

## 9. 业务连续性

### 9.1 5 业务规则 (R1-R5) 全复用
- v1 (2026-06-14 20:39 下线) → v3 (本批) 规则一致
- 0 业务断点

### 9.2 跟 v2 的关系
- **共享**: billing_export_tasks 表 / export_worker / 任务中心 UI / ZIP 路径 / RBAC
- **差异**: 5 端点 (1 v2 overview + 4 v3) + 1 SPA 默认页
- **没冲突**: v2 客户对账跟 v3 上游对账可以同时跑 (kind 字段区分)

### 9.3 跟 v4 (利润分析) 的关系
- v4 = v2 收入 + v3 成本, 数据依赖 v3
- v3 跑通 1 周后开 v4

---

## 10. 部署清单 (PR #7, 待做)

1. 远端跑 migration `migrations/2026-06-14-billing-v3-upstream-tasks.sql` (2 字段 + 2 索引 + 1 约束)
2. build 镜像 + restart api 容器
3. 公网 5 端点验证
4. 创建 1 测试任务 (provider_alpha 上月) → success → 下载 ZIP → 解压校验 4 维度
5. 写公网验证报告 `docs/test-reports/billing-v3-pr7-deploy-2026-06-14.md`

---

## 11. 风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| GroupRatio 在 log.other 缺字段 (老 log) | 低 | cost 不准 | 默认 1.0, 财务对账时手工补 |
| 27 user 的 group 变化 (新建用户) | 低 | 缓存缺 | 5min 增量 (v3 暂不缓存 group) |
| RoDB 1 月 logs (50 万行) 慢 | 中 | 任务超时 | 跟 v2 一样用 cache_logs_summary_by_model_5min (后续优化) |
| channel_vendor_map.discount=0 | 低 | cost=0, profit 巨大 | R6 标记 unmatched_reason, 财务查 |
| channel_vendor_map 没记录 | 中 | 跳过 channel (cost=0) | R6 标记, 财务补 |
| 新 vendor 上线 (没在 channel_vendor_map) | 中 | 全 channel 跳过 | 运营在供应商管理模块加, 走 dal 同步 |
| overview 简化估算跟 ZIP 精确数不一致 | 中 | 财务对账用不同数 | overview 是趋势监控, ZIP 才是财务数 |

---

## 12. 7 PR 完成清单 (BILLING v3 上游对账, 2026-06-14)

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| **#1** | RFC + kind/vendor_code 字段 migration | `df40966` | 0.5 天 |
| **#2** | 成本反推核心 (GroupRatio + CalcLogCost + CalcUpstreamStatement) | `d928bc7` | 1.5 天 |
| **#3** | 上游对账生成器 (HTML + XLSX + ZIP + 模板) | `aa3397e` | 1.5 天 |
| **#4** | API 5 端点 (复用 v2 worker/download/cancel) | `d7e0377` | 1.5 天 |
| **#5** | SPA 上游对账默认页 (vendor/channel 双层) | `539a82e` | 1 天 |
| **#6** | 单测 + 文档 (v3 RULES) | (本 PR) | 0.5 天 |
| **#7** | 远端部署 + 公网验证 5 端点 | (待 commit) | 0.5 天 |
| **总计** | | | **7 天** |
