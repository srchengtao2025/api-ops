# api-ops 整体设计方案 v0.1（讨论稿）

> **状态**：✅ v1.0 已确认，进入实施阶段
> **作者**：Mavis
> **日期**：2026-06-10（v1.0 决策已锁定）
> **作用域**：基于 QuantumNous/new-api 之上外挂构建的运营管理系统
> **前置**：在现有 P0（对账中心）已基本落地的前提下，扩展 P1 监控、P2 实时面板、P3 AI 分析

---

## 0.5 决策基线（v1.0 用户已确认）

| # | 决策项 | 选定方案 |
|---|---|---|
| Q1 | 鉴权 | 本期 bypass，文档标"生产前接 newapi session" |
| Q2 | 多租户 | 单租户 |
| Q3 | 告警通道 | **飞书机器人**（先做飞书一个，后续可扩展） |
| Q4 | AI 报告 | 错误分析日报 + 运营周报 + 客户健康度 AI 诊断（手动） |
| Q5 | 审计 | ✅ 全部写操作审计 |
| Q6 | 历史回溯 | 30 天 |
| Q7 | 主动探测 | 复用 newapi AutomaticallyTestChannels |
| Q8 | UI 栈 | Antd 5 + 自研深色主题 |
| Q9 | 实时推送 | **WebSocket**（双向） |
| Q10 | LLM 调用 | **可配置**：默认 upstream 网关，可切直连上游 |
| Q11 | i18n | zh-CN 为主，预留 i18next 框架 |
| Q12 | SLA | **99.5%**（年宕机 ≤ 43.8h） |
| Q13 | CI | ✅ GitHub Actions：lint + test + docker build |
| Q14 | 上游对账 | **反推 1M 原单价 × discount**（Q14 已确认） |
| Q-C4 | LLM 月预算 | **不设硬性月预算**（owner 2026-06-10 拍板）。系统保留 token 用量埋点 + 单日/单次成本告警开关（运行时可配，缺省关闭） |
| Q-C6 | 飞书 webhook | **运行时配置**：URL 从 `system_config` 表读，缺值/为空时优雅降级（不报错、记录日志 + Dashboard 红条），所有配置项可在 UI 运行时修改无需重启 |
| Q-D1 | **Dashboard 数据源** (2026-06-14) | **严格全 admin API** (用户决策). 仅 1 端点 `GET /api/dashboard/today` 走 admin `/api/log/stat` 1 次返 3 字段 (quota/rpm/tpm). 砍掉: `/api/dashboard/trend` + TopX 3 卡片 + 错误率/延迟/tokens 字段. 接受 18次/5min 限流 (SPA 5s 刷新会触发, 降频或接受 429). admin stat 字段语义: `quota`=范围 SUM, `rpm/tpm`=60s 滑窗 |
| Q-D2 | **TopX 恢复路径** (2026-06-14) | 暂不实现. 3 端点路由注释, handler 函数保留返 503. 恢复路径见 `handlers_stmt.go` 注释: (a) upstream 开 `DataExportEnabled` (b) cache_logs_summary_5min 1min tick 扩展 model/user/channel 维度 |
| Q-C7 | **upstream_pricing 价目表下线** (2026-06-14 23:43) | **彻底下架**. 0 引用 + 9 行覆盖率 18% + v3 反推公式 (cost = revenue / group_ratio × channel_vendor_map.discount) 完全弃用价目表. DB 表移到 `archive` schema 保留, 4 端点 + 1 SPA + 1 import 流程全删. 详见 PRD-v2 §4.15 |
| Q-C8 | **Dashboard TopX 永不再反转** (2026-06-14 + 2026-06-15) | 用户决策保留 4966916 砍 TopX 决定. 不加今日用户排行 (用户 2026-06-15 明确: 7d 曲线够用). 恢复路径仅 Q-D2 列的两条 (upstream 开 DataExportEnabled / cache_logs_summary_5min 1min tick 扩维度) |
| Q-C9 | **Dashboard 7d 趋势曲线** (2026-06-15) | 不含今天, 后端 cache 5min (`sync.Map` key=`dashboard:trend7d`), 1 轮 7 次 admin /api/log/stat (D-7 ~ D-1). SPA 5min tick 拉 1 次 (跟后端 cache 对齐, 0 重复 admin). today 60s 滑窗单独展示, 不进 trend |
| Q-C10 | **监控中心 - 渠道健康** (2026-06-15 09:01) | **先做渠道健康, 告警模块暂缓** (rule / alert / ack / resolve / 飞书推送暂不开 SPA). 复用 1min tick `channel_health_5min` + 新建 `monitorChannels` / `monitorChannelHealth` 端点. main.go 顶部启 `scheduler.Run` (基础设施 bug 修复: monitor tick 之前 0 数据) |
| Q-C11 | **错误率新口径** (2026-06-15 09:43) | **分母**: 业务请求 = `type IN (2, 5, 6)` 跨 24h (排除登录/充值/管理操作 type=1/3/4). **分子**: 独立错误 = `type=5 AND jsonb_array_length(use_channel) = 1` (排除被 retry 中间失败). **P95**: 走 `channel_health_5min` 桶 MAX (最新桶值, 避免 RoDB percentile_cont 慢). 性能: 24h 178 万行 → 7.3ms 扫描 (`idx_created_at_type` 完美命中) |

---

## 0. 一句话定位

> **api-ops = upstream.com 的"运营驾驶舱 + 财务台账 + 智能运维"三合一外挂系统**

不改一行 new-api 代码，通过只读 DB 账号 + Admin API Token + 增量日志通道，把对账、监控、AI 解读、实时面板四件事做到"准、稳、快、漂亮"。

---

## 1. 业务问题 → 能力映射

| 你提的业务问题 | 落到 api-ops 的能力 | 优先级 | 输出形态 |
|---|---|---|---|
| 一.1 用户消耗与质量实时监控 + 预警 | **客户级实时监控**：RPM/TPM、错误率、p95 延迟、错误模式聚类；VIP 分级告警 | P1 | 客户实时面板 + 告警事件流 |
| 一.2 渠道调用量与质量（错误率/命中率/首 token 延迟/p95） | **渠道健康度看板**：5min 滑窗、自动禁用联动（借 new-api `AutomaticallyTestChannels`）、余额预警 | P1 | 渠道大屏 + 告警 |
| 二 上游对账 | 价目表管理 + 成本折扣 + 渠道供应商映射 + 对账引擎（P0 已实现） | P0 | 上游账单 + 利润分析 |
| 二 下游对账 | 客户对账（P0 已实现）+ 分模型/渠道/日三维度 | P0 | 客户账单 + 利润分析 |
| 三 报错错误 AI 解析（aws bedrock / 阿里云百炼 官方文档） | **AI 错误解读器**：上游官方错误码 → 标准分类 + 根因 + 处置建议 | P3 | 错误详情弹窗 + 周报 |
| 四 admin token 接入能力 | 已有 API_OPS_ADMIN_TOKEN；新增"用户余额/渠道列表/封禁"等只读能力 | P0/P1 | 详情查询 + 跨表关联 |
| 五 24h 稳定运行 + ≤15min 延迟 + 准确性 | 同步链路（每 1 min 增量拉取 + 每 5 min 聚合刷新）+ 自有监控 + 降级 + 重试 | 横切 | SLO 仪表盘 |
| 现代科技风 UI | 主题 + 动效 + 信息密度 | 体验 | 全站 |

---

## 2. 系统能力总览（一张大图）

```
                       ┌────────────────────────────┐
                       │  浏览器（运营/客服/财务/运维） │
                       │  React 18 + Vite + Antd + ECharts│
                       └──────────────┬─────────────┘
                                     │ HTTPS / SSE / WebSocket
                       ┌──────────────▼─────────────┐
                       │       api-ops API 层     │
                       │  (Gin · 中间件：Auth/CORS/限流/审计)
                       │  路由：REST + SSE           │
                       └──┬───────┬───────┬───────┬──┘
                          │       │       │       │
                ┌─────────▼┐ ┌────▼───┐ ┌─▼────┐ ┌─▼─────┐
                │ 对账引擎 │ │监控引擎│ │AI 引擎│ │看板引擎│
                │  (P0)    │ │ (P1)  │ │ (P3) │ │ (P2)  │
                │ 账单+导出│ │告警+抑制│ │聚类+LLM│ │SSE+大屏│
                └────┬────┘ └───┬────┘ └──┬───┘ └───┬───┘
                     │          │         │         │
                ┌────▼──────────▼─────────▼─────────▼─────┐
                │          数据访问层 (DAL)              │
                │  · 只读 DAL → new-api (PG)            │
                │  · 读写 DAL → api_ops (PG)         │
                │  · 缓存层 → Redis (聚合/锁/限流)     │
                └────┬──────────────┬──────────┬────────┘
                     │ 只读         │ 读写     │ 缓存
              ┌──────▼──────┐ ┌────▼───────┐ ┌─▼────┐
              │ upstream/new-api│ │ api_ops │ │ Redis│
              │ PG · 业务库  │ │ PG · 元数据│ │  7   │
              │ PG · 日志库  │ │ + 告警/报告│ │      │
              └──────────────┘ └────────────┘ └──────┘
                     ▲
                     │ 主动探测 (可选 P4)
                     │ HTTP / 测试请求
                ┌────┴───────┐
                │ 主动探测器  │ → 健康检查、余额探测
                └────────────┘
```

---

## 3. 五大核心能力 — 详细设计

### 3.1 业务实时监控（P1）

#### 3.1.1 渠道监控 — `internal/monitor/channel_health.go`

**指标（5 分钟滑窗，每 1 分钟 roll）**：

| 指标 | SQL 聚合（基于 `logs` + `channels`） | 用途 |
|---|---|---|
| 错误率 | `count(type=error) / count(*)` | 告警触发 |
| RPM / TPM | `count(*)`、`sum(prompt_tokens+completion_tokens)` / 5min | 容量规划 |
| P50 / P95 / P99 延迟 | `percentile_cont(0.95) within group (order by use_time) over ()` | 体感 |
| 首 token 延迟 | `use_time` 拆出（new-api 已记录在 `perf_metrics`） | 流式体感 |
| 命中率 | `count(type=consume) / count(*)` | 渠道可用性 |
| 余额 | `channels.balance` 周期拉取 | 余额预警 |
| 状态变化 | `AutomaticallyTestChannels`（new-api 已实现）触发的 disabled 事件 | 自动联动 |

**关键点**：
- 聚合写入 `channel_health_5min` 表（自有 DB），每 5 分钟一条，存 30 天；小时级 `channel_health_1h` 存 1 年。
- 不在 new-api 库内做 GROUP BY（只读账号只 SELECT，DDL 会被拒），而是用 api-ops 定时任务做 `INSERT … SELECT`。
- 命中率定义需要运营确认（按业务语义"成功调用/总调用"）。

**告警规则示例**（YAML 配置，落在 `alert_rules` 表）：

```yaml
- id: ch_high_error_rate
  name: 渠道错误率过高
  target: channel
  metric: error_rate
  window: 5m
  condition: "ratio > 0.20"      # 错误率 20%
  duration: 10m                  # 持续 10 分钟
  severity: critical
  actions: [notify_ops, auto_disable_channel, ai_diagnose]

- id: ch_balance_low
  name: 渠道余额低
  target: channel
  metric: balance
  condition: "balance < 5.0"     # 5 USD
  severity: high
  actions: [notify_finance]

- id: ch_p95_degraded
  name: 渠道 p95 延迟劣化
  target: channel
  metric: p95_latency
  window: 15m
  condition: "p95 > baseline_p95 * 1.5"   # 历史基线 × 1.5
  severity: high
  actions: [notify_ops, ai_diagnose]
```

#### 3.1.2 客户级 SLA 监控 — `internal/monitor/customer_sla.go`

**用户分级**（写入 `users` 表新加 `tier` 字段由 api-ops 维护映射；或独立表 `customer_tiers`）：

| 等级 | 阈值配置 |
|---|---|
| SVIP | 5min 内 ≥3 次错误 → critical；请求量环比 −50% → high |
| VIP | 5min 内 ≥10 次错误 → high；限流率 > 30% → high |
| 普通 | 5min 内 ≥50 次错误 → warning |

**聚合**（每 1 分钟一次）：

```sql
SELECT user_id, username,
       count(*) FILTER (WHERE type=5) AS errors,
       count(*) AS total,
       avg(use_time) FILTER (WHERE type=2) AS avg_latency
FROM logs
WHERE created_at >= $now - 5min
GROUP BY user_id, username;
```

#### 3.1.3 告警引擎 — `internal/monitor/alerting/`

- **规则评估器**：每 1 分钟 tick，按规则的 `window` 取最近一段时间聚合值，比对 `condition`。
- **抑制器**：Redis key `alert_fire:{rule_id}:{subject_id}` TTL = `duration` 或 1h（取大），存在则跳过。
- **升级器**：critical 告警 fire 后启动一个 5 分钟 timer，没 ack 则升级通知。
- **通知出口**：
  - 钉钉机器人 webhook（HMAC-SHA256 签名）
  - 飞书机器人 webhook
  - 企业微信 webhook
  - 邮件 SMTP
  - 自定义 webhook（HMAC）
  - 飞书/钉钉 @ 特定人
- **告警状态机**：`firing → acknowledged → resolved`，写入 `alert_histories`。

#### 3.1.4 24h 稳定运行保障

- **同步链路**：每 1 min 增量拉取 `logs where created_at > checkpoint`，checkpoint 持久化到 `sync_checkpoint` 表；Redis 分布式锁保证多节点不重不漏。
- **重试**：网络/DB 抖动时指数退避；3 次失败进入死信，运维收到告警。
- **降级**：Redis 挂了所有功能仍可工作（直连 DB）；new-api 只读 DB 短暂不可达时展示「最新数据时间戳」让运维知情。
- **断点续传**：每次聚合写入带 `as_of_ts`，重跑不会污染已生成的告警。
- **资源**：单节点先跑 1000 QPS 量级聚合，CPU/MEM 上限 2C/4G；瓶颈在 `logs` 表读 IO，索引对齐 `idx_created_at_id / idx_user_id_id`。

---

### 3.2 上游/下游对账（P0 — 已在 P0 落地，本设计侧重复核与扩展点）

**复核要点**（基于现有 PRD 和实现）：

| 项 | 现状 | 本次设计复核/扩展 |
|---|---|---|
| 价目 CSV 导入 | ✅ 已实现 | 复核中文别名解析、批次回滚 |
| 客户对账（preview + 落库） | ✅ 已实现 | 复核未匹配告警路径、Excel 多 sheet 导出 |
| 上游对账 | ✅ 已实现 | 复核「未映射渠道」兜底 |
| 利润分析（模型/渠道/客户） | ✅ 已实现 | 复核图表维度联动 |
| Dashboard KPI | ✅ 基础 | 增强：实时错误流入口、告警入口 |
| **tiered_expr 计费的对账** | ⏳ P3 | 本期先信任 `logs.quota`；P3 加抽样重算（5%） |

**上游对账的折扣计算**（你强调的重点，复述确认）：

> 成本 = Σ[ 日志按 token 类型拆分后的原 1M 单价 × tokens / 1M ] × discount

即 `logs.other` 里的 `model_price` 等比率字段**仅用于还原"不含倍率"的原单价**（new-api 默认把 `quota` 计成 `model_price × group_ratio × token × 含倍率系数`），**api-ops 反推出原 1M 单价后再乘以 `discount`**。这个反推公式需在 PRD 中明确，避免上游账单金额对不齐。

**下游对账**：直接用 `logs.quota / QuotaPerUnit`（USD），按用户 × 时间 × 模型聚合。

**对账一致性**：
- 同一批日志在 t=生成时刻 算一次 = T0；
- t=T1 再算（recompute 校验）值应一致；
- 若不一致（new-api 改了 ratio 配置回溯），生成新版本 `statement_version` 记录。

---

### 3.3 报错错误 AI 解析（P3 — 你的明确需求，详细设计）

#### 3.3.1 错误聚类 — `internal/ai/error_cluster.go`

**输入**：`logs` 表 type=error 的最近 N 小时。

**聚类步骤**：

1. **模板化**：把 `content` 里的 UUID/request_id/timestamp/IP/email/digit 序列归一化：
   ```sql
   SELECT REGEXP_REPLACE(
            REGEXP_REPLACE(
              REGEXP_REPLACE(content,
                '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}',
                '<UUID>', 'g'),
              '\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}',
              '<TS>', 'g'),
            '\b\d+\b',
            '<N>', 'g') AS pattern
   FROM logs
   WHERE type=5 AND created_at >= now() - interval '1 hour'
   ```
2. **聚合**：按 (pattern, channel_id, model_name) 计数 + 影响用户去重数。
3. **Top N**：取前 50 模式送 LLM。

#### 3.3.2 上游官方错误知识库 — `internal/ai/knowledge/`

**结构**：

```
internal/ai/knowledge/
├── aws_bedrock.yaml        # ValidationException, ThrottlingException, AccessDeniedException …
├── provider_gamma.yaml     # 阿里云百炼官方错误码
├── openai.yaml
├── anthropic.yaml
├── google_gemini.yaml
└── generic.yaml
```

**YAML 结构**（以 AWS Bedrock 为例）：

```yaml
provider: aws_bedrock
errors:
  - code: ValidationException
    http_status: 400
    category: 参数错误
    root_causes:
      - 模型 ID 不存在或该 region 未启用
      - 请求体字段类型错误（messages/tool_use 结构）
    actions:
      - 校验 model 名称 + region 映射
      - 校验请求体 schema（temperature/max_tokens 范围）
    doc_url: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html#API_runtime_InvokeModel_Errors
  - code: ThrottlingException
    http_status: 429
    category: 上游限流
    root_causes:
      - 账户级 RPM/TPM 超限
      - 特定模型突发配额不足
    actions:
      - 退避重试 + 自动切换备用渠道
      - 申请 AWS 配额提升
    doc_url: https://docs.aws.amazon.com/bedrock/latest/userguide/troubleshooting-api-error-codes.html
```

**采集方式**：
- **手动初始化**：Mavis 起团队后由产品经理 + coder 抓取各云厂商官方错误码文档，写入 YAML。
- **定期更新**：`internal/ai/knowledge/refresh` 任务每季度从官方文档页面抓取（可用 webfetch / Playwright），自动 diff 后人工 review。
- **运行时扩展**：聚类发现新 pattern 且 LLM 返回"未知错误码"时，落库到 `ai_error_discoveries`，运营后续人工补录。

#### 3.3.3 LLM 解读 — `internal/ai/llm_client/`

**复用 upstream 网关自身模型**（PRD 已定）：

```go
type LLMClient struct {
    BaseURL string  // https://www.upstream.com/v1
    APIKey  string
    Model   string  // claude-sonnet-4-5 默认
}

func (c *LLMClient) DiagnoseError(ctx context.Context, sample ErrorSample) (*Diagnosis, error)
```

**Prompt 模板**（核心）：

```
SYSTEM:
你是 upstream 平台的 AI 运维助手。基于给定的错误样本与上游官方错误码知识库，输出 JSON：
{
  "category": "<网络/认证/上游限流/模型超载/参数错误/余额不足/速率/其他>",
  "matched_code": "<匹配到的官方错误码，如 ThrottlingException>",
  "root_causes": ["<按概率排序的根因列表>"],
  "affected_scope": {"channels": [...], "users": [...], "models": [...]},
  "actions": ["<给运维>", "<给客服>", "<给客户>"],
  "confidence": 0.0-1.0,
  "doc_url": "<官方文档链接>"
}

INPUTS:
1. 错误模式样本（Top 20）: {samples}
2. 渠道健康度（最近 1h）: {channel_health}
3. 上游公告（可选）: {upstream_status}
4. 知识库匹配（按 channel type）: {kb_candidates}
```

**缓存与限流**：
- 同 (channel_id, model_name, pattern_hash) 5 分钟内只调用 1 次 LLM。
- LLM 调用走 upstream 网关自己，按用户级 RPM 限流（防止 LLM 反向把上游打爆）。
- 失败 fallback：直接基于知识库 + 简单规则给出一个"无 LLM"的诊断结果。

#### 3.3.4 触发方式

| 触发点 | 行为 |
|---|---|
| 实时面板点击某条错误 → "AI 解读" | 同步调用 1 次 LLM，返回弹窗 |
| 告警 fire 时关联的 channel/model 自动调用 | 异步 fire-and-forget，结果入 `ai_diagnoses` |
| 每天 02:30 出"错误分析日报" | 批量聚类 + 1 次 LLM 总结，存 `ai_reports` |
| 每周一 09:00 出"上周运营周报" | 错误趋势 + 客户健康度变化 + 利润环比，1 次 LLM 总结 |

---

### 3.4 实时运行面板 + 大看板（P2）

#### 3.4.1 实时数据流 — SSE

**架构**：

```
api-ops API 层
    ├── /api/stream/customer/:user_id  (SSE, 5s push)
    ├── /api/stream/global             (SSE, 5s push)
    └── /api/stream/errors             (SSE, 实时告警)
```

**数据通道**：
- 5 秒 tick：api-ops 后台聚合器读最近 30s 的 `logs`（已落 1min 聚合的 `logs_5min_agg`）→ push。
- 增量：定期轮询 `logs` 表的 `MAX(created_at)`，新行同步到 Redis Stream `errors:new`，消费者订阅 SSE。

#### 3.4.2 客户级实时面板

布局（参考自研 dashboard 风格）：

```
┌─────────────────────────────────────────────────────┐
│  客户：xxx@xxx  | 等级：VIP  |  状态：🟢健康         │
├─────────────────────┬───────────────────────────────┤
│ RPM  1,234          │ 错误率  0.8%                  │
│ TPM  234,567        │ P95 延迟  1.2s                │
│ 累计消耗 $1,234.5   │ 余额  $5,678 (剩余 18 天)     │
├─────────────────────┴───────────────────────────────┤
│  当前模型分布（饼图）        │  渠道分布（环形）      │
├─────────────────────────────────────────────────────┤
│  最近 10 条错误（含 AI 解读）                       │
│  · 03:12:45 [OpenAI-Azure #38] 429 Rate limit       │
│    AI: 上游 GPT-4o 配额不足，建议切换备用渠道        │
│  · 03:08:22 [DeepSeek #12]   502 Bad Gateway        │
│    AI: 上游网关短暂不可用，已自动重试                │
└─────────────────────────────────────────────────────┘
```

#### 3.4.3 运营总看板（大屏模式，1920×1080）

```
┌─────────────────────────────────────────────────────────┐
│  今日收入 $12,345  │ 今日调用 234,567  │ 错误率 0.8% │ 在线渠道 28/30 │
├──────────────────────────────┬──────────────────────────┤
│  调用量+收入双轴趋势（左）   │  错误 Top10（右）         │
├──────────────────────────────┼──────────────────────────┤
│  模型收入 Top 10（饼图）     │  客户消耗 Top 10（柱）    │
├──────────────────────────────┼──────────────────────────┤
│  实时错误流 SSE（左）        │  告警面板（右）           │
│  （最近 5min 错误动态滚动） │ （firing/ack/resolved）  │
└──────────────────────────────┴──────────────────────────┘
```

---

### 3.6 五大能力当前实施状态 (2026-06-15 同步)

> 本节是 §3.1-§3.5 在 2026-06-14~15 25+ commits 后的"实施盘点"。原 §3 章节是设计意图，本节是当前真实落地。

| # | 能力 | 当前端点 | 数据源 | SPA 页面 | 状态 | 关键变更 |
|---|------|---------|--------|---------|------|---------|
| 1 | **BILLING v1 客户对账** | 18 端点 | — | 3 页 | ❌ **已下线** (2026-06-14 20:39) | DB 表 archive.* schema, 文档 archive/v1-docs/ |
| 2 | **BILLING v2 客户对账** | 6 端点 | RoDB + cache | 1 默认页 + 1 任务中心 | ✅ **已上线** (8 PR / 8 天) | 异步 ZIP + worker pool + 每用户 ≤ 2 running |
| 3 | **BILLING v3 上游对账** | 5 端点 | RoDB + cache (billing_export_tasks.kind=upstream) | 1 双层表格页 | ✅ **已上线** (7 PR / 7 天) | 复用 v2 worker/download/cancel, **新成本反推公式** |
| 4 | **BILLING v4 利润分析** | 1 端点 | RoDB + v3 CalcLogCost | 1 SPA 4 tab | ✅ **已上线** (2 PR / 4 天) | 复用 v2 SQL 模式, 自实现 SVG bar (不引 echarts) |
| 5 | **总览 Dashboard** | 4 端点 (1 用 + 3 禁) | admin API | 1 页 + 7d 曲线 | ✅ **demo 风格 + 7d 趋势** (2026-06-15) | 全 admin API + 砍 TopX + 5min cache trend-7d |
| 6 | **监控中心 - 渠道健康** | 2 端点 (channels / channels/:id/health) | cache (channel_health_5min) | 1 卡片化页 (44 渠道) | ✅ **已上线** (2026-06-15) | 3 规则 + 健康 24h 聚合 + 错误率新口径 |
| 7 | **供应商管理** | /api/vendors + /api/channel-mappings | OPS 表 + RoDB | 1 页 | ✅ A 阶段 | 1 渠道→1 供应商 + 折扣自动解析 |
| 8 | **upstream_pricing 价目表** | 4 端点 | — | 1 页 + import 流程 | ❌ **已下架** (2026-06-14 23:43) | DB 表 archive.* schema, 完全弃用 |

**当前活跃端点合计**：30 个 (dashboard 1 + v2 6 + v3 5 + v4 1 + 监控 2 + vendors 2 + auth/admin/misc 13)
**当前活跃 SPA**：6 个 (Dashboard / BillingV2 / BillingV2Exports 任务中心 / BillingV3Upstream / BillingV4Profit / ChannelHealth / VendorManagement / Vendors + Login)
**已下线**：v1 18 端点 + 价目表 4 端点 = 22 端点 → archive

### 3.7 本期新增能力地图 (2026-06-14~15)

#### 3.7.1 BILLING v2/v3/v4 全套 (8+7+2 = 17 PR)

```
                    ┌──────────────────────────────────────────────────┐
                    │  BILLING v2/v3/v4 统一架构                         │
                    │  ─ 复用 1 张表: billing_export_tasks (kind 字段)   │
                    │  ─ 复用 1 个 worker pool + semaphore (≤2/user)   │
                    │  ─ 复用 1 个 ZIP 输出目录 (/data/billing-exports) │
                    │  ─ 复用 1 个任务中心 SPA (按 kind 列区分)         │
                    └────────────────────┬─────────────────────────────┘
                                         │
            ┌─────────────────┬──────────┼──────────┬─────────────────┐
            ▼                 ▼          ▼          ▼                 ▼
        ┌───────┐         ┌───────┐  ┌───────┐  ┌───────┐         ┌───────┐
        │ v2 客 │         │ v3 上 │  │ v4 利 │  │ future│         │ future│
        │ 户对账│         │ 游对账│  │ 润分析│  │  v5?  │         │  v6?  │
        └───────┘         └───────┘  └───────┘  └───────┘         └───────┘
        27 user 4 token   5 vendor/  21 user   (预留)            (预留)
        ZIP 11.6KB         39 channel revenue-cost-profit
        HTML+XLSX         revenue × cost = profit
                          (revenue/group_ratio
                           × discount)
```

#### 3.7.2 监控中心一期 (渠道健康)

```
                       ┌─────────────────────┐
                       │  1min scheduler tick│  ← main.go 启 Run() (bug fix)
                       └──────────┬──────────┘
                                  │ 聚合 24h + 5min 桶
                                  ▼
                  ┌──────────────────────────────┐
                  │ channel_health_5min 表        │
                  │ (request / error / p50/95/99) │
                  └──────────┬───────────────────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
        ┌──────────┐  ┌──────────┐  ┌─────────────┐
        │ /api/mon │  │ /api/mon │  │ 错误率新口径 │
        │ itor/    │  │ itor/    │  │ 分母 type IN│
        │ channels │  │ channels │  │ (2,5,6)     │
        │          │  │ /:id/    │  │ 分子 type=5 │
        │ 44 渠道  │  │ health   │  │ & use_ch=1  │
        │ 卡片化   │  │ 趋势线   │  │             │
        └──────────┘  └──────────┘  └─────────────┘
```

#### 3.7.3 价目表完全弃用 → v3 反推公式

| 维度 | v1 价目表 (已下线) | v3 反推公式 (现行) |
|------|-------------------|---------------------|
| 数据来源 | 手动导入 + CSV 校正 | **实时算** (logs × group_ratio × discount) |
| 维护成本 | 高 (9 行覆盖率 18% 还要维护 CSV) | **0** (渠道折扣已现成) |
| 准确度 | 依赖 CSV 同步 | **100% 实时** |
| 复杂度 | 4 API + 1 SPA + 1 import 流程 | **0 维护**, 复用 channel_vendor_map |
| 决策 | 2026-06-14 23:43 用户决策 | 立即生效 |

#### 3.7.4 Dashboard 全站 demo 风格升级

```
旧: Antd 5 + Card/Statistic 默认浅色 → 自研 dark token 但只覆盖少量组件
新: design-tokens.json 全站 + mock-suite 提取 + 全组件 theme + 自实现布局 (不用 antd Layout)
    ↓
结果: 1.17MB JS / 8.2KB CSS (gzip 375KB / 2.15KB) — 比旧 1.29MB / 410KB **小 100KB gzip**
```

设计 token 来源：
- `web/design-tokens.json` (A 阶段已有)
- `archive/mock-suite/mock.css` (2026-06-15 提取的颜色)
- 全局 class: `.app-layout` / `.app-sidebar` / `.app-header` / `.kpi-card` / `.ops-card` / `.status-*` / `.badge-*`

#### 3.7.5 main.go scheduler 启动修复 (基础设施 bug)

**Bug**: `cmd/server/main.go` 漏启 `scheduler.Run(rootCtx, cfg)` —— monitor tick 永远不跑, `channel_health_5min` / `alert_histories` 0 数据.
**修复**: main.go 顶部 import scheduler + AI scheduler 前面加 `scheduler.Run(rootCtx, cfg)`.
**效果**: 启动 5s 后 tick 跑, 5min_buckets=6 (1h 后 49 渠道都该有).
**后端 log 验证**: `[main] P1 monitor scheduler started (1min tick: 5min aggregate + alert eval)` + `[monitor] tick: 5min_buckets=6 1h_buckets=0 alerts_fired=0`.

---

### 3.5 现代化 UI 设计规范

#### 3.5.1 主题

- **基色**：深空黑 `#0B0E14` 主背景 + 深蓝灰 `#0F1729` 卡片背景
- **强调色**：电光蓝 `#3B82F6`、霓虹青 `#06B6D4`、警示橙 `#F59E0B`、危险红 `#EF4444`、成功绿 `#10B981`
- **字体**：Inter / 思源黑体 / 等宽（数字用）等距字体强化可读性
- **信息密度**：表格行高 36px，卡片 16px padding，组件间距 12-16px
- **动效**：数字 count-up（600ms ease-out）、图表渐入（200ms）、状态点呼吸（critical 1s）

#### 3.5.2 组件选型

- **基础**：Ant Design 5.x（保留后台标准交互）
- **图表**：ECharts 5.x（丰富图表库 + 大屏适配）
- **大屏**：DataV-React 或自研 Grid 布局（1920×1080 起步，4K 缩放）
- **图标**：Lucide React / Ant Design Icons 混用
- **暗色/亮色**：默认暗色（运营大屏需要），提供亮色切换

#### 3.5.3 关键页面信息密度原则

- **数字优先**：所有 KPI 卡片数字字号 ≥ 28px，附 ±环比
- **颜色编码**：绿/黄/红 一眼判断状态
- **迷你图**：所有卡片右下角内嵌 24h 迷你折线
- **空状态**：所有表格无数据时给"导入数据 →"引导
- **加载**：骨架屏 + 数字 placeholder，不出现白屏

---

## 4. 数据架构（自有 DB api_ops）

### 4.1 已有表（P0）

```sql
-- 基础元数据
upstream_vendors
upstream_pricing
channel_vendor_map
upstream_pricing_imports
billing_statements
billing_statement_lines
```

### 4.2 新增表（P1/P2/P3）

```sql
-- 监控域
channel_health_5min     -- 渠道 5min 滑窗聚合
channel_health_1h       -- 渠道 1h 聚合
customer_health_5min    -- 客户 5min 滑窗聚合
sync_checkpoint         -- 同步位点
alert_rules
alert_histories
alert_actions           -- 通知发送记录
user_tier               -- 客户分级
tier_threshold          -- 分级阈值

-- 实时流
realtime_subscribers    -- SSE 连接管理

-- AI 域
ai_error_clusters
ai_diagnoses            -- 单次 LLM 输出
ai_reports              -- 周期报告
ai_error_discoveries    -- 未知错误码

-- 知识库
error_kb_entries        -- 来自 YAML 导入的官方错误码

-- 系统
audit_logs              -- api-ops 自身操作审计
system_config
```

### 4.3 new-api 库（只读）

`logs / channels / users / tokens / quota_data / perf_metrics / tasks`

索引利用：现有 `idx_created_at_id / idx_user_id_id / idx_created_at_type / idx_logs_request_id` 已足够。

### 4.4 Redis 用途

| 用途 | Key 模式 | TTL |
|---|---|---|
| 聚合缓存 | `agg:channel:5m:{ch_id}:{bucket}` | 5min |
| 告警抑制 | `alert_fire:{rule_id}:{subject_id}` | 1h |
| LLM 缓存 | `llm_diag:{channel_id}:{model}:{pattern_hash}` | 5min |
| 分布式锁 | `lock:daily_stmt:{date}` | 1h |
| 实时事件流 | Stream `errors:new`、`alerts:new` | 滚动 |
| 限流 | `ratelimit:{user_id}:{op}` | 1min |

### 4.5 缓存聚合表 (本期新增 / 已扩, 2026-06-14~15)

> 三大数据源之一的 "本地缓存 DB" 在本期实施中扩展/新增了以下表。**所有 tick 都通过 `scheduler.Run()` 在 main.go 启动** (修复 bug 之前, 这些表全是 0 数据).

| 表 | Tick | 写入方 | 读取方 | 用途 |
|---|---|---|---|---|
| `cache_logs_summary_5min` | 1min | sync_logs_summary (RoDB → 聚合) | v2/v3/v4 SQL fallback | 27 user 1 SQL 拿 4 token + USD |
| `cache_logs_summary_by_model_5min` | **1min** | sync_logs_summary (RoDB → 聚合) | v3 CalcLogCost (按模型维度) | v3 利润分析按模型聚合 |
| `cache_logs_summary_by_channel_5min` | **1min** | sync_logs_summary (RoDB → 聚合) | v4 CalcProfitOverview (按渠道维度) | v4 利润分析按渠道聚合 |
| `channel_health_5min` | **1min** | monitor scheduler | monitor/channels + monitor/channels/:id/health | 渠道 5min 滑窗 (request/error/p50/p95/p99) |
| `channel_health_1h` | **5min** | monitor scheduler | monitor/channels 历史趋势 | 1h 聚合, 存 1 年 |
| `dashboard:trend7d` (in-memory sync.Map) | **5min** | `dashboardTrend7d` handler (1 轮 7 次 admin) | Dashboard SPA (5min tick) | 7d 趋势曲线, 不含今天 |
| `ops_upstream_summary_5min` | **5min round-robin** (单次 1 vendor × 1 period) | billing scheduler (`runUpstreamTick`) | v3 上游对账 handler / worker | v3 上游对账 5min cache, 6+ 小时稳跑, 0 restart |

**关键点**：
- v2/v3/v4 SQL 全部优先走 `cache_logs_summary_*` (RoDB fallback 仍存在但延迟秒级)
- channel_health_5min 在 main.go `scheduler.Run()` 启动后才开始写 (2026-06-15 bug fix 之前 = 0 数据)
- dashboard:trend7d 是 in-memory `sync.Map` 不是 Redis, SPA 5min tick 拉一次跟后端 cache 对齐
- **ops_upstream_summary_5min 单次 tick 跑 1 个 (vendor, period), round-robin 10 tick = 50min 一轮** (2026-06-15 14:00 fix). 历史教训: 5 vendor × 2 period 串行 10 SQL 累加 200MB+ 临时分配触发进程级崩溃 (exit 0, 无 panic, 无 OOM), 怀疑 gorm 19万 rows buffer + cgo 段错误. 单次跑 GC 压力小, 6+ 小时稳跑, 0 restart.

---

## 5. Admin Token 与能力获取

PRD 已规划 `API_OPS_ADMIN_TOKEN`。我对照 new-api 的实际接口给一个对接清单：

| new-api 接口 | 用途 | api-ops 用途 |
|---|---|---|
| `GET /api/user/search?keyword=` | 用户搜索 | 客户对账页选择客户 |
| `GET /api/user/:id` | 用户详情 | 客户运行面板 |
| `GET /api/user/:id/dashboard` | 用户 dashboard（quota/RPM/TPM） | 客户余额 + 实时配额 |
| `GET /api/user/self` | 当前 token 用户 | 鉴权（可选） |
| `GET /api/channel/?p=0` | 渠道列表 | 渠道-供应商映射、监控 |
| `GET /api/channel/:id` | 渠道详情 | 渠道监控详情 |
| `POST /api/channel/test/:id` | 测试渠道连通性 | 一键测活 |
| `PATCH /api/channel/:id` | 更新渠道（启用/禁用/优先级） | 自动联动 |
| `GET /api/models` | 模型列表 | 价目管理、利润分析 |
| `GET /api/log/search` | 日志搜索 | 错误查询 |
| `GET /api/log/self` | 自查询 | 不使用 |

> 注：上面所有"读"接口都先尝试 DB 直连，Admin API 作为兜底（new-api 不直连 DB 时的 fallback）。

---

## 6. 性能 / SLO / 稳定性

| 指标 | 目标 | 实现手段 |
|---|---|---|
| 数据延迟（用户/渠道最新数据可查） | ≤ 15 min | 1 min 增量同步 + 5 min 聚合 |
| Dashboard 首次加载 | ≤ 2 s | 聚合表 + Redis 缓存 + 懒加载 |
| 聚合查询 P95 | ≤ 500 ms | 索引对齐 + 预聚合表 |
| 单节点 QPS | 1000 QPS | Go + 连接池 + GORM |
| 告警延迟（firing 到通知送达） | ≤ 60 s | 1 min tick + 异步通知队列 |
| 可用性 | 99.9% | 双实例 + 健康检查 + 自动重启 |
| 数据准确性 | 0.05% 以内误差 | 日志直读 + 反推对账（带 reconciliation） |
| 24h 不停机 | ✅ | 滚动重启 + DB 迁移兼容（先发兼容版本，再清字段） |

**降级矩阵**：

| 故障 | 影响 | 降级 |
|---|---|---|
| Redis 不可用 | 缓存/锁失效 | 跳过缓存直连 DB；锁用 PG advisory lock 兜底 |
| new-api 只读 DB 短暂不可达 | 监控/对账失败 | 拉取旧数据 + 顶部红色 banner "数据更新于 X 分钟前" |
| api-ops 自有 DB 不可用 | 元数据写入失败 | 健康检查告警 + 仅读路径继续工作 |
| LLM 不可用 | AI 解读无 | 降级到知识库规则匹配，标注"无 AI 解读" |
| 告警通知出口全挂 | 告警无法外发 | 写入 DB，运维看板红色 banner 提醒 |

### 6.1 错误率定义 (新口径, Q-C11 2026-06-15)

> 这是监控中心 - 渠道健康模块的核心口径定义。**所有"渠道错误率" / "全平台错误率" 字段从此节取口径**.

#### 6.1.1 业务定义

**错误率 = 独立错误数 / 业务请求数** (跨 24h 窗口).

#### 6.1.2 分子分母 SQL (实测可用)

```sql
-- 业务请求 (分母): type=2 正常消耗 + type=5 错误 + type=6 重试 (排除登录/充值/管理操作)
SELECT COUNT(*) AS biz_requests
FROM newapi.logs
WHERE created_at >= NOW() - INTERVAL '24 hours'
  AND type IN (2, 5, 6);

-- 独立错误 (分子): type=5 且只用 1 个渠道 (排除被 retry 中间失败, retry 用 use_channel.length > 1)
SELECT COUNT(*) AS independent_errors
FROM newapi.logs
WHERE created_at >= NOW() - INTERVAL '24 hours'
  AND type = 5
  AND jsonb_array_length(other::jsonb->'admin_info'->'use_channel') = 1;
```

#### 6.1.3 实测案例 (ch_id=110, 2026-06-15)

| 来源 | 业务请求 | 成功 | 独立错误 | 错误率 |
|------|---------|------|---------|--------|
| RoDB 直查 SQL | 4,815 | — | 862 | 17.93% |
| API monitor/channels | 4,816 | 3,954 | 862 | 17.93% |
| **差** | **1** | — | 0 | — |

差 1 行的根因: RoDB 实时 (NOW()) vs cache sync 1min 延迟造成的窗口错位. **在可接受范围内**.

#### 6.1.4 P95 / P50 / P99 口径

- **不**走 RoDB `percentile_cont()` (实测 178 万行扫描慢)
- **走** `channel_health_5min` 桶取 **MAX(对应分位数)**: 等价于"最新一桶的分位值"
- 24h 取最大 = 跨桶反映当前体感 (用户关心最新, 不关心历史均值)

#### 6.1.5 性能与索引

- 24h 178 万行 logs → **7.3ms 扫描** (`idx_created_at_type` 复合索引完美命中)
- 15 渠道聚合 → 走 2 步: RoDB logs (7ms) + OPS channel_health_5min (5ms)
- 不会拖垮 DB (用户 2026-06-15 09:43 关注)

#### 6.1.6 与 v1 旧口径对比

| 维度 | v1 旧口径 (已废弃) | v2 新口径 (Q-C11 现行) |
|------|---------------------|-------------------------|
| 分母 | 全部 type=2 + type=5 | **业务请求** type IN (2, 5, 6) |
| 分子 | type=5 (含 retry 中间失败) | **独立错误** type=5 AND use_channel.length=1 |
| 数字 | 偏高 (retry 重复计) | **偏低且更准确** |
| P95 | RoDB percentile_cont (慢) | channel_health_5min 桶 MAX (快) |
| 24h 行数 | 178 万 | 同 (但 SQL 7.3ms 命中索引) |

#### 6.1.7 红边展示规则

- **错误率 ≥ 20%** 的渠道卡片触发红边呼吸 (用户决策 2026-06-15 09:26)
- 呼吸周期: 1.5s (原 2s, 加强)
- 外发光: 16px → 32px
- 二层光晕: 32px → 56px
- 红边**只覆盖渠道卡片** (`.kpi-card.kpi-danger`), 不覆盖综合 KPI
- 综合 KPI "24h 错误率" 用 `.kpi-card.kpi-danger-stat` (无红边呼吸, 避免视觉污染)

---

## 7. 部署架构

### 7.1 单节点起步（v1）

```
┌─────────────────────────────────┐
│  Host (Linux 2C4G 起步)         │
│  ┌────────────┐  ┌───────────┐ │
│  │ api-ops │  │ api-ops│ │
│  │ api (8088) │  │ web (5173)│ │
│  └────────────┘  └───────────┘ │
│  ┌────────────┐  ┌───────────┐ │
│  │ postgres   │  │ redis     │ │
│  └────────────┘  └───────────┘ │
└─────────────────────────────────┘
```

### 7.2 生产（v2，可选）

- API 容器化 + 多副本
- PG 主从（api_ops 独立集群）
- Redis 哨兵
- 独立告警通道 worker
- nginx 反代 + HTTPS + 域名

### 7.3 docker-compose 一键起

（已有 `docker-compose.yml`，需补充：监控 worker、AI worker、SSE hub）

---

## 8. 安全 / 鉴权 / 审计

- **鉴权**：本期本地开放（按 PRD）；生产接 new-api session token 或 OIDC。
- **API 限流**：按 IP + 路径 100 req/s。
- **审计**：`audit_logs` 表记录所有写操作（账单确认、价目删除、告警确认、LLM 调用）。
- **密钥管理**：Admin Token / LLM Key 走环境变量 + K8s Secret；自有 DB 密码 12 位以上。
- **前端**：登录态 + HttpOnly cookie + CSRF token。

---

## 9. 实施路线（按你的需求，4 个交付物顺序）

### Phase 1：详细 PRD（最先）
- Mavis 起「产品经理」agent + 「UI 设计师」agent 并行：
  - PM：写 `docs/PRD-v2.md`（在现有 PRD 上扩 P1/P2/P3），含：用户故事、用例、字段定义、状态机、异常路径
  - UI：出 Figma/HTML 原型 + 设计 token + 关键页面 mock（大屏、对账、监控、AI 报告）

### Phase 2：可演示 demo（演示数据 + 完整 UI 流程）
- Mavis 起「coder」agent 跑通：
  - 注入 30 天 mock 数据（fake logs / channels / users / pricing）
  - 完整跑通"对账生成 → 利润分析 → 监控告警 → AI 解读"四大流程
  - 输出可本地 `docker compose up` 启动的 demo 仓库

### Phase 3：可部署代码（生产可用的实现）
- Coder 实现 P1/P2/P3 全量
- 起「tester」agent 写单测 + 集成测试 + 端到端 Playwright
- CI：lint + test + docker build

### Phase 4：README
- 项目说明、架构、部署、运维手册、FAQ
- 中英双版

---

## 10. 关键风险 & 决策点（请你确认）

| 编号 | 待确认 | 我的建议 |
|---|---|---|
| Q1 | 鉴权：本期是否完全开放（按现有 PRD 写的） | **建议**：先 bypass，文档标注"生产前必接"，UI 留登录页占位 |
| Q2 | 多租户：是否需要按团队/部门隔离 | **建议**：本期单租户，预留 user_id 过滤位 |
| Q3 | 告警通道优先级：先做哪几个 | **建议**：钉钉 + 飞书 + 邮件 + 自定义 webhook 四个 |
| Q4 | AI 报告：日报 + 周报 + 即时报告 三类 | **建议**：先做"错误分析日报" + "运营周报"，即时报告复用告警 fire |
| Q5 | 审计：是否需要 api-ops 自身操作审计 | **建议**：要，覆盖所有写操作 |
| Q6 | 历史数据：上线是否回溯 30 天 | **建议**：回溯，提供"批量回溯"按钮 + 进度条 |
| Q7 | 主动探测：要不要实现？ | **建议**：本期用 new-api `AutomaticallyTestChannels` + 余额轮询，主动探测器 P4 再做 |
| Q8 | UI 技术栈：Antd vs 自研？ | **建议**：Antd 5 + 自研深色主题，效率高 + 美观 |
| Q9 | 实时面板：SSE vs WebSocket | **建议**：SSE（单向推送够用，自动重连） |
| Q10 | 大模型调用：复用 upstream 网关还是直连上游 | **建议**：复用 upstream 网关（PRD 已定），可顺便验证自家链路 |
| Q11 | 是否需要多语言（i18n） | **建议**：先 zh-CN，预留 i18next 框架 |
| Q12 | 24h 稳定运行的 SLA 目标 | **建议**：99.9%（年度宕机 ≤ 8.76h），可由监控系统保证 |
| Q13 | 是否需要 GitHub Actions / CI | **建议**：要，lint + test + build 镜像 |
| Q14 | 上游价目"反推原 1M 单价"的反推公式 | **建议**：跟 new-api ratio 配置一致；提供手工 override |

---

## 11. 总结：我们要交付的 4 个产出物

| 产出 | 内容 | 度量 |
|---|---|---|
| 1️⃣ 详细 PRD | `docs/PRD-v2.md` + 设计稿（Figma / HTML） | 评审通过 |
| 2️⃣ 可演示 demo | 注入 mock 数据 + 完整 UI 流程，可 `docker compose up` 启动 | 跑通 4 大主流程 |
| 3️⃣ 可部署代码 | 完整 P0-P3 实现 + 测试 + CI | 单测 ≥ 60% 覆盖，docker build 通过 |
| 4️⃣ README | 中英双版，覆盖介绍/架构/部署/运维/FAQ | 30 分钟能跑起来 |

---

**下一步**：等你的反馈，对 Q1-Q14 给意见 + 整体设计哪里要调整，我再起团队开干 🚀
