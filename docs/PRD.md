# api-ops 产品需求文档（PRD）

| 项目 | api-ops（upstream.com 外挂运营管理系统） |
|------|------------------------------------------|
| 版本 | v0.1.0（P0 已完成，待评审） |
| 作者 | Mavis · upstream 产品运营 |
| 日期 | 2026-06-10 |
| 状态 | P0（对账中心）已实现，文档已完整覆盖 P0-P4 路线图 |
| 关联系统 | [QuantumNous/new-api](https://github.com/QuantumNous/new-api)（v1.0.0-rc.10） |

---

## 1. 背景与目标###1.1 业务背景

upstream.com 是一个基于 [new-api](https://github.com/QuantumNous/new-api)（v1.0.0-rc.10，38k stars）自建的 **大模型聚合服务平台**。它聚合了 40+ 上游 AI 提供商（OpenAI / Claude / Gemini / Azure / DeepSeek / 智谱 / 月之暗面 / 字节 / 阿里 / 腾讯……），对外暴露 OpenAI / Claude / Gemini 兼容的统一 API，向下游开发者客户按 token 消耗计费。

随着业务规模扩大，**运营层面的痛点**日益突出：

| 痛点 | 具体表现 |
|------|----------|
| **对账混乱** | 客户消耗靠 new-api 的 `logs` 表手工算；上游结账靠 Excel 邮件；算不出真实毛利 |
| **渠道健康度不透明** | 哪个渠道错误率高、哪个上游余额快没了，全靠经验 |
| **重点客户无感知** | VIP 客户连续报错没人发现，等客户投诉才知道 |
| **错误处理靠人** | 错误堆栈全靠工程师肉眼分类，每月出事故复盘报告 |
| **运营看板缺失** | 想看"今天赚了多少、调用了多少、哪个模型最赚钱"，得现场跑 SQL |
| **客户实时状态无视图** | 客服接到客户电话"我的请求失败了"，只能让客户截图，无法主动发现 |

###1.2 产品目标

构建一套**外挂式运营管理系统 api-ops**，围绕上述痛点提供五个核心能力：

| # | 能力 | 优先级 | 状态 |
|---|------|--------|------|
| 1 | **对账**（下游客户 + 上游供应商 + 利润分析） | P0 | ✅ 已实现 |
| 2 | **日常监控**（渠道健康度 + 重点客户 SLA） | P1 | 📋 待开发 |
| 3 | **错误智能分析与报告**（LLM 聚类 + 报告生成） | P3 | 📋 待开发 |
| 4 | **客户运行状态实时面板 + AI 分析** | P2 | 📋 待开发 |
| 5 | **运营总看板** | P2 | ✅ 已实现基础版 |

###1.3 关键约束

- **不修改 new-api 任何代码**——所有能力外挂实现
- 通过 **只读 DB 账号** + **Admin API Token** 与 new-api 通信
- api-ops 自身数据（上游价目、对账单、告警、报告）放 **独立 PostgreSQL database**
- 单节点部署起步，预留多节点扩展能力
- 上游价目数据由上游供应商按月提供 CSV / Excel，**信息结构与 new-api `logs.other` JSON 对齐**

---

## 2. 目标用户与场景###2.1 用户角色

| 角色 | 占比 | 关注点 |
|------|------|--------|
| **运营经理** | 1-2 人 | 总览看板、对账审批、客户投诉响应、AI 报告 |
| **客服** | 2-5 人 | 客户实时状态、错误查询、限流排查 |
| **财务** | 1 人 | 上游对账单审核、客户发票、利润率分析 |
| **研发 / 运维** | 2-3 人 | 渠道健康度监控、告警处理、AI 错误分析 |

###2.2 关键使用场景

**场景 1：月末对账**
> 运营经理在每月 1 号打开 api-ops，看到"5 月已自动生成 XXX 个客户对账单"，点开某个 VIP 客户的账单，确认毛利符合预期后点击"确认"。财务同时审核上游对账单，下载 Excel 发给上游供应商核对。

**场景 2：渠道异常告警**
> 凌晨 3 点，钉钉群里弹出告警："【渠道 #38 OpenAI-Azure】最近 10 分钟错误率 35%，已自动禁用"。研发看到后切换到备用渠道，并在 api-ops 上看"错误聚类 Top 1：上游返回 503"。

**场景 3：客户投诉排查**
> 客服接到 VIP 客户电话"我的请求全失败了"。打开 api-ops，输入客户用户名，看到"该用户过去 5 分钟连续 30 次 429 限流错误"，判断是上游速率限制，建议客户切换到备用 key。

**场景 4：AI 周会报告**
> 每周一上午 10 点，api-ops 自动生成上周"运营周报"，包含：错误 Top10、客户健康度变化、毛利环比、新发现异常。推送到飞书群。

---

## 3. 详细功能需求

### 3.1 P0：对账中心（已实现）

#### 3.1.1 下游客户对账单

**业务规则**：
- 按客户 × 时间区间 × 模型维度聚合
- 客户实付金额 = `Σ logs.quota / QuotaPerUnit`（USD）
- 上游成本 = `Σ (各 token 类型 × 对应上游价 / 1M)`
- 毛利 = 客户实付 − 上游成本
- 退款（LogTypeRefund）作为负值参与计算
- 错误请求不计费（quota = 0），但参与调用次数统计

**支持维度**：
- 时间：日 / 周 / 月 / 自定义区间
- 客户：用户名、用户 ID
- 模型、渠道、分组

**对账单字段**：
- 头部：客户、周期、调用次数、错误次数、退款次数、客户实付、上游成本、毛利、毛利率
- 明细（按模型）：每个模型的 prompt / completion / cache tokens、收入、成本、利润
- 明细（按渠道）：每个渠道的请求数、收入、成本
- 明细（按日）：每日调用数和金额

**未匹配警告**：若某条日志的 channel 未映射到任何上游供应商，或模型无对应价目，**在账单中标注警告**，运营人员需补录。

**操作**：
- 生成（批量或单用户）
- 预览（实时算，不落库）
- 确认（draft → confirmed）
- 导出 CSV / Excel

#### 3.1.2 上游供应商对账单

**业务规则**：
- 按供应商 × 时间区间聚合
- 总成本 = 该供应商下所有渠道、所有模型的成本之和
- 总收入 = 该供应商渠道服务产生的客户实付之和
- 毛利 = 收入 − 成本

**操作**：
- 生成（按指定供应商或全部）
- 列表 / 详情 / 导出 CSV

#### 3.1.3 上游价目管理

**数据来源**：上游供应商定期发送的 CSV / Excel

**字段映射**（与 new-api `logs.other` JSON 字段一一对应）：
| 字段 | 含义 | new-api 对应字段 |
|------|------|------------------|
| `prompt_cost_per_1m` | 输入 token 单价 (USD / 1M) | `Other.model_price` |
| `completion_cost_per_1m` | 输出 token 单价 | `Other.completion_ratio × model_price` |
| `cache_read_cost_per_1m` | 缓存读取单价 | `Other.cache_ratio × model_price` |
| `cache_write_cost_per_1m` | 缓存写入单价 | `Other.cache_creation_tokens` 计费 |
| `image_cost_per_1m` | 图片输入单价 | `Other.image_ratio` |
| `audio_input_cost_per_1m` | 音频输入 | `Other.audio_ratio` |
| `audio_output_cost_per_1m` | 音频输出 | `Other.audio_completion_ratio` |
| `web_search_cost_per_call` | Web 搜索按次 | `Other.web_search` |
| `file_search_cost_per_call` | File 搜索按次 | `Other.file_search` |
| `per_call_cost` | 按次计费（视频/图片生成） | 按任务类型 |
| `discount` | 折扣（0.8 = 8折） | 单一字段 |
| `effective_from` / `effective_to` | 生效区间 | 时间分桶 |

**操作**：
- 手工新增单条
- CSV 批量导入（带导入批次记录、错误行明细、批次状态机）
- 列表 / 搜索 / 删除

**CSV 模板**支持中英文表头别名，便于上游按自己的习惯出表。

#### 3.1.4 渠道 ↔ 上游供应商映射

**必要性**：new-api 的 `channels` 表只标记"是 OpenAI 类型"，**不知道这个渠道最终由哪家供应商结算**。需要 api-ops 自己维护映射。

**特性**：
- 一对多：一条渠道可对应多个上游（例如按 region 拆分）
- 权重：用于多供应商场景的成本分摊（默认 1.0）

#### 3.1.5 利润分析（横切视图）

**三个维度切换**：
- 按模型：哪个模型赚钱最多
- 按渠道：哪个渠道毛利最高
- 按客户：哪些客户贡献最多利润

**展示**：图表 + 明细表，每日趋势可叠加。

#### 3.1.6 Dashboard 实时 KPI（P0 已实现基础）

| 卡片 | 数据源 |
|------|--------|
| 今日调用量 | logs today |
| 今日收入 (USD) | logs today quota |
| 今日平均耗时 | AVG(use_time) |
| 今日错误率 | error / total |

**趋势图**：调用量 + 收入双 Y 轴。

**Top 排行**：Top 20 客户 / 模型 / 渠道。

---

### 3.2 P1：监控与告警（待开发）

#### 3.2.1 渠道监控

**指标**：
- 错误率（5 分钟滑窗 / 持续时长）
- RPM / TPM
- P50 / P95 / P99 延迟
- 当前状态（enabled / manual_disabled / auto_disabled）
- 余额预警
- 响应时间劣化（> 历史 P95 × 1.5）

**自动复用 newapi 已有能力**：
- `controller/channel-test.go:986 AutomaticallyTestChannels()` — 定期健康检查 + 自动禁用
- `controller/channel_upstream_update.go` — 上游模型巡检（30 分钟一次）

**api-ops 在此基础上补充**：实时错误率聚合（5 分钟滑窗）。

#### 3.2.2 重点客户监控

**配置**：
- 客户分级（VIP / SVIP / 战略客户）
- 每级配置不同阈值（错误数、限流率、并发数）

**告警触发**：
- 连续 N 次错误（如 5 分钟内 > 5 次）
- 限流率超过 X%
- 突然的请求量下降（可能客户端挂掉）

#### 3.2.3 告警规则引擎

**YAML 配置示例**：
```yaml
rules:
 - name: channel_high_error_rate
 type: channel
 metric: error_rate
 window: 5m
 condition: ">0.20"
 duration: 10m
 severity: critical
 actions: [notify_ops_team, auto_disable_channel]
 - name: vip_user_consecutive_errors
 type: user
 target_tier: vip
 metric: consecutive_errors
 window: 5m
 condition: ">5"
 severity: high
 actions: [notify_cs, notify_ops]
```

**告警抑制**：相同规则同一主体 1 小时内不重复发送。

**告警升级**：critical 告警 5 分钟内无人 ack → 升级到电话通知。

#### 3.2.4 通知出口

| 通道 | 用途 | 实现 |
|------|------|------|
| 钉钉机器人 | 群内告警 | 自定义 webhook + 加签 |
| 飞书机器人 | 群内告警 | 自定义 webhook |
| 企业微信 | 群内告警 | 自定义 webhook |
| 邮件 | 详细报告 | SMTP |
| 自定义 Webhook | 集成内部 IM | HMAC-SHA256 签名 |
| SMS | critical 升级 | 阿里云 / Twilio（可选） |

---

### 3.3 P2：实时面板与总看板（待开发）

#### 3.3.1 客户运行状态实时面板

**单客户视图**：
- 实时 RPM / TPM（5 秒滑窗）
- 错误率
- P95 延迟
- 当前调用最多的模型 / 渠道
- 最近 10 条错误详情

**数据流**：SSE（Server-Sent Events），5 秒推送一次。

#### 3.3.2 运营总看板（大屏）

**布局**：
```
┌──────────────────────────────────────────────────────┐│ 顶部 KPI 区（4 张数字卡）                              │
├──────────────────────────────┬───────────────────────┤│ 调用趋势（左）         │ 错误 Top 10（右）         │
├──────────────────────────────┼───────────────────────┤│ 模型收入占比（左）     │ Top 10 客户消耗（右）      │
├──────────────────────────────┼───────────────────────┤│ 实时错误流（左）       │ 告警面板（右）            │
└──────────────────────────────┴───────────────────────┘```

**刷新策略**：
- 数字卡：30 秒
- 趋势图：5 分钟
- 错误流：5 秒 SSE
- 告警：即时推送

#### 3.3.3 错误流实时面板

订阅 new-api `RecordErrorLog`（model/log.go:161-205）新写入的错误，**实时显示**最近的错误模式 + 频率。

---

### 3.4 P3：错误智能分析与报告（待开发）

#### 3.4.1 错误聚类

**算法**：
- 把 `RecordErrorLog.content` 里的 UUID / request_id / 时间戳归一化
- 按 (pattern, channel_id, model_name) 聚合
- Top 50 模式送给 LLM 解读

**实现**：
```sql
SELECT REGEXP_REPLACE(content, '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', '<UUID>', 'g') AS pattern,
 channel_id, model_name,
 COUNT(*) AS count,
 ARRAY_AGG(DISTINCT username) AS affected_usersFROM logs
WHERE type = 5 AND created_at >= NOW() - INTERVAL '1 hour'
GROUP BY pattern, channel_id, model_name
ORDER BY count DESC LIMIT 50;
```

#### 3.4.2 LLM 分析 prompt

```python
SYSTEM: 你是 upstream 平台的 AI 运维助手。基于给出的错误样本，输出：
1. 错误类别（网络/认证/上游限流/模型超载/参数错误/余额不足…）
2. 根因可能性（按概率排序）
3. 影响范围（哪些用户/渠道/模型受影响）
4. 建议处置动作

USER:
# 错误模式样本（最近 1 小时 Top20）
{error_pattern_samples}

# 渠道健康度
{channel_health}

# 上游公告（可选）
{upstream_status}
```

**LLM 调用复用 upstream 网关自身**：用 upstream.com 的 Claude / GPT 模型，验证自家链路。

#### 3.4.3 客户健康度 AI 诊断

**触发**：客户运行面板的"AI 诊断"按钮 / 客服主动调用 / 定期 cron

**输入**：客户最近 1 小时的日志聚合 + 历史基线 + 当前告警

**输出**：Markdown 报告，包含：
- 健康状态（绿/黄/红）
- 主要问题点
- 给客户的解释建议
- 给运维的处置建议

#### 3.4.4 周期报告

| 报告 | 频率 | 内容 |
|------|------|------|
| 错误分析日报 | 每日 | 昨日错误 Top10、影响客户数、修复建议 |
| 运营周报 | 每周一 | 调用量 / 收入 / 利润变化、错误趋势、容量规划 |
| 异常即时报告 | 触发式 | critical 告警触发后 5 分钟内出报告 |

**存储**：所有报告存在 `ai_reports` 表，前端用 Markdown 渲染。

---

### 3.5 P4：优化与扩展（持续）

- new-api stdout 日志 shipper（捕获 `insufficient_user_quota` 拒绝事件）
- 历史日志归档到对象存储
- 数据量超阈值时引入 ClickHouse OLAP 旁路
- 自动化测试覆盖
- 多节点部署支持

---

## 4. 数据架构### 4.1 api-ops 自有 DB（PostgreSQL `api_ops`）

#### 4.1.1 上游管理域

```sql
-- 上游供应商
upstream_vendors
├── id PK
├── code UNIQUE -- "openai-azure"
├── name
├── contact_name/email/phone
├── billing_cycle -- monthly / weekly / custom
└── created_at / updated_at

-- 上游模型价目
upstream_pricing
├── id PK
├── vendor_code + model_name + effective_from -- 联合唯一
├── pricing_mode -- per_1m_tokens | per_call | tiered
├── prompt_cost_per_1m / completion_cost_per_1m
├── cache_read_cost_per_1m / cache_write_cost_per_1m
├── image_cost_per_1m / audio_input_cost_per_1m / audio_output_cost_per_1m
├── web_search_cost_per_call / file_search_cost_per_call
├── per_call_cost / discount
├── effective_from / effective_to -- 0 = 至今
└── source / source_file / remark

-- 渠道-供应商映射
channel_vendor_map
├── id PK
├── channel_id + vendor_code -- 联合唯一
└── weight -- 成本分摊权重

-- 价目导入批次
upstream_pricing_imports
├── id PK
├── filename / vendor_code
├── total_rows / success_rows / failed_rows
├── failed_detail -- JSON 错误明细
└── status -- processing | success | partial | failed
```

#### 4.1.2 对账域

```sql
-- 对账单主表
billing_statements
├── id PK
├── statement_type -- customer | upstream
├── subject_type -- user | vendor
├── subject_id / subject_name
├── period_start / period_end
├── revenue / cost / profit / profit_rate
├── request_count / error_count / refund_count
├── prompt_tokens / completion_tokens / cache_tokens
├── status -- draft | confirmed | exported
└── generated_at / confirmed_at / exported_at

-- 对账单明细行
billing_statement_lines
├── id PK
├── statement_id FK
├── model_name / group / channel_id / channel_name / vendor_code
├── request_count / prompt_tokens / completion_tokens / cache_tokens
├── revenue_usd / cost_usd / profit_usd / profit_rate
```

#### 4.1.3 告警域（P1）

```sql
alert_rules / alert_histories
```

#### 4.1.4 AI 报告域（P3）

```sql
ai_reports
├── id PK
├── report_type -- error_analysis | weekly_summary | customer_health
├── period_start / period_end
├── subject_type / subject_id
├── title / content (Markdown) / metadata (JSON)
└── generated_at
```

### 4.2 new-api DB（只读）

通过 `API_OPS_RO_DSN` 只读账号访问，关键表：
- `logs` —— 计费主数据源（model/log.go:34-56）
- `channels` —— 渠道主数据
- `users` / `tokens` —— 用户与 API key
- `quota_data` —— 模型×小时聚合（看板趋势）
- `tasks` —— 异步任务（视频/图片生成）
- `perf_metrics` —— 性能指标（TTFT、GenerationMs）

**索引利用**：
- `logs.created_at`（按时间查）
- `logs.user_id` + `created_at`（按用户聚合）
- `logs.channel`（按渠道聚合）
- `logs.model_name`（按模型聚合）
- `logs.request_id` / `upstream_request_id`（精确追踪）

### 4.3 Redis（独立实例）

**用途**：
- 聚合结果缓存（5 分钟 TTL）
- 分布式锁（每日对账调度）
- 限流计数器
- 通知发送抑制（key = `notify_limit:%d:%s:%s`）

**降级策略**：Redis 故障时所有功能降级到直连 DB，不阻塞业务。

---

## 5. 技术架构### 5.1 部署拓扑

```
┌────────────────────────────────────────────────────────┐│ 浏览器 (运营 / 客服 / 财务 / 运维) │
│ React 18 + Vite + Antd + ECharts │
└────────────────────────────────────────────────────────┘ │ HTTPS┌────────────────────────────────────────────────────────┐│ api-ops 应用层 │
│ │
│ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ │
│ │ 对账引擎 │ │监控引擎 │ │AI 引擎│ │看板服务│ │
│ │ (P0 ✅)│ │ (P1) │ │ (P3) │ │ (P2) │ │
│ └────────┘ └────────┘ └────────┘ └────────┘ │
│ ┌──────────────────────────────────────────────────┐ │
│ │ HTTP API 层 (Gin) + 调度器 (scheduler) │ │
│ └──────────────────────────────────────────────────┘ │
│ ┌──────────────────────────────────────────────────┐ │
│ │ 数据访问层 (DAL) / newapi SDK │ │
│ └──────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────┘ │ 只读 │ │ 
 ▼ ▼ ┌─────────────────┐ ┌─────────────────┐│ new-api 主库 │ │ new-api 日志库 ││ (PostgreSQL) │ │ (PostgreSQL) │└─────────────────┘ └─────────────────┘
```

### 5.2 技术选型

| 层 | 选型 | 理由 |
|----|------|------|
| 后端语言 | **Go 1.22** | 与 new-api 同语言，类型安全，单二进制部署，运维简单 |
| Web 框架 | Gin | 主流，与 new-api 一致 |
| ORM | GORM v2 | 与 new-api 一致 |
| 前端 | React 18 + Vite + TS + Antd + ECharts | 后台运营主流栈 |
| 数据库 | PostgreSQL 15 | 与 new-api 同集群，便于运维 |
| 缓存 | Redis 7 | 独立实例，避免影响 new-api |
| 容器化 | Docker Compose | 一键起开发环境 |
| 可执行产物 | Linux 单二进制（≤ 25 MB） | 部署简单 |

**未来扩展**：AI 分析用 Python worker 微服务（独立 HTTP / gRPC），与 Go 主服务并行。

### 5.3 关键模块说明

#### 5.3.1 DAL 层设计原则

- `dal.RO` 只连接 new-api DB；**只能 SELECT**，写会被 PG 权限直接拒绝
- `dal.OPS` 连接自有 api_ops DB，承担全部自有元数据
- 所有 new-api 表的镜像结构体都有注释指明"对应 new-api 源码行号"，便于追踪

#### 5.3.2 对账计算

**核心算法**（`internal/billing/customer_statement.go`）：

```
对每条 type=consume 日志:
 1. customer_revenue += quota / QuotaPerUnit  // 客户实付
 2. parse Other JSON → cache_tokens / image_tokens / audio_tokens
 3. 根据 channel_id 查 channel_vendor_map → vendor_code
 4. 根据 vendor_code + model_name + created_at 查 upstream_pricing
 5. cost = (prompt_tokens×prompt_per_1m + completion_tokens×completion_per_1m + ...) / 1M
 6. 按 (model_name / channel_id / day) 维度累加

profit = revenue - cost
profit_rate = profit / revenue
```

**对账的可信度**：
- 信任 new-api `logs.quota`（已经是 new-api 算好的客户实付）
- 上游成本由 api-ops 独立按价目表算
- **差异风险点**：tiered_expr 计费模式（new-api 的高级计费）的对账，本期先信任 `logs.quota`，P3 加入 10% 抽样重算

#### 5.3.3 quota ↔ USD 换算锚点

new-api 默认 `1 USD = 500,000 quota`（`newapi/common/constants.go:62`）。
api-ops 通过 `cfg.QuotaToUSD(quota)` / `cfg.USDToQuota(usd)` 统一换算，**任何模块都不允许自己写除法**。

---

## 6. API 设计

完整 API 见 `api-ops/README.md`，核心分组：

| 分组 | 端点 |
|------|------|
| 供应商管理 | `GET/POST/PUT/DELETE /api/vendors` |
| 价目管理 | `GET/POST/DELETE /api/upstream-pricing` + `/import` |
| 渠道映射 | `GET/POST/DELETE /api/channel-vendors` |
| 客户对账 | `/api/billing/customer/...`（preview / generate / statements / confirm / export） |
| 上游对账 | `/api/billing/upstream/...`（generate / statements / export） |
| 利润分析 | `GET /api/billing/profit/analysis?dimension=...` |
| Dashboard | `/api/dashboard/today /trend /top-*` |
| 健康检查 | `GET /api/health` |

**响应格式统一**：
```json
{ "success": true, "data": ... }
{ "success": false, "error": { "message": "...", "detail": ... } }
```

**鉴权**：本期预留 `authMiddleware` 接口位（当前 bypass），生产应接 newapi session 或 OIDC。

---

## 7. 实施路线图### 7.1 P0 ✅（2 周，2026-06-10 完成）

- [x] 项目骨架（Go + React + Docker）
- [x] Config / DAL / Redis / newapi SDK
- [x] 上游供应商 / 价目 / 渠道映射管理
- [x] CSV 批量导入上游价目
- [x] 客户对账单（实时预览 + 落库）
- [x] 上游对账单
- [x] 利润率分析
- [x] CSV / Excel 导出
- [x] Dashboard 实时 KPI

### 7.2 P1（2 周）

- [ ] 渠道监控：5 分钟滑窗错误率 / RPM / TPM / 延迟 / 余额
- [ ] 重点客户 SLA（连续失败 / 限流率）
- [ ] 告警规则引擎（YAML 配置 + 抑制 + 升级）
- [ ] 通知出口：钉钉 / 飞书 / 企微 / 邮件 / 自定义 Webhook

### 7.3 P2（2 周）

- [ ] SSE 5 秒级实时数据流
- [ ] 客户运行状态面板
- [ ] 运营总看板（大屏布局）
- [ ] 实时错误流

### 7.4 P3（2 周）

- [ ] 错误聚类 + LLM 解读
- [ ] 客户健康度 AI 诊断（一键调用）
- [ ] 周报 / 月报自动生成
- [ ] tiered_expr 抽样重算任务

### 7.5 P4（持续）

- [ ] new-api stdout 日志 shipper
- [ ] 历史日志归档
- [ ] ClickHouse OLAP 旁路
- [ ] 多节点部署

---

## 8. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| **new-api 升级导致 logs 表 schema 变化** | api-ops 查询失败 | DAL 层镜像结构体集中管理，升级时统一改；与 new-api 版本绑定 |
| **tiered_expr 模式对账误差** | 上游成本算错 | P0 信任 `logs.quota`；P3 加抽样重算任务 |
| **配额不足事件无 logs 记录** | 监控漏掉拒绝事件 | new-api 用 `NoRecordErrorLog()` 显式不写日志；中期通过 stdout 日志 shipper 兜底 |
| **多节点 logs 无法归属节点** | 多节点对账难 | 单节点起步；P4 做节点心跳表 + `request_id` 前缀关联 |
| **数据量大 SQL 慢** | dashboard 卡 | 走 `quota_data` 聚合表（已有小时桶）；数据量超阈值时引入 ClickHouse |
| **上游价目导入失败** | 对账漏算 | 导入批次记录错误明细；前端 UI 标红；运营手工补录 |
| **AI 成本** | 调用 LLM 花钱 | 复用 upstream 网关自家模型，验证链路且不额外花钱 |

---

## 9. 度量指标（成功标准）

**P0 验收**（当前）：
- 上游价目导入支持 CSV，含错误明细、批次状态
- 客户对账单生成 1 万条/分钟（数据库允许范围内）
- 客户对账单导出 Excel 含模型/渠道/日三维明细
- Dashboard 首次加载 ≤ 2 秒
- 单节点 1000 QPS 聚合查询 P95 ≤ 500ms

**业务结果**（3 个月内）：
- 客户对账时间从 3 天 → 30 分钟
- 渠道异常发现时间从数小时 → 5 分钟
- VIP 客户问题响应时间从被动投诉 → 主动通知
- 月度运营报告产出从人工整理 → 自动生成

---

## 10. 附录### 10.1 关键文件索引

| 文件 | 用途 |
|------|------|
| `cmd/server/main.go` | 启动入口 |
| `internal/config/config.go` | 配置中心 |
| `internal/dal/db.go` | 双 DB 连接 |
| `internal/dal/logs_repo.go` | new-api logs 只读 |
| `internal/dal/ops_models.go` | 自有 DB schema |
| `internal/billing/customer_statement.go` | 客户对账核心算法 |
| `internal/billing/upstream_statement.go` | 上游对账 + 利润分析 |
| `internal/billing/import_csv.go` | CSV 价目导入 |
| `internal/billing/exporter.go` | CSV / Excel 导出 |
| `internal/api/server.go` | 路由注册 |
| `internal/api/handlers_billing.go` | 供应商 / 价目 handlers |
| `internal/api/handlers_stmt.go` | 对账 + dashboard handlers |
| `internal/scheduler/scheduler.go` | 每日对账调度 |
| `web/src/App.tsx` | 前端布局 |
| `web/src/pages/Dashboard.tsx` | 总览看板 |
| `web/src/pages/UpstreamPricing.tsx` | 价目管理（含导入） |
| `web/src/pages/CustomerStatements.tsx` | 客户账单 |
| `web/src/pages/UpstreamStatements.tsx` | 上游账单 |
| `web/src/pages/ProfitAnalysis.tsx` | 利润分析 |

### 10.2 new-api 关键数据源

| 表 | 用途 | 字段 |
|----|------|------|
| `logs` | 请求日志（消费/错误/退款） | `quota / prompt_tokens / completion_tokens / channel / model_name / group / type / created_at / other` |
| `channels` | 上游渠道 | `id / name / type / status / balance / response_time / used_quota` |
| `users` | 客户 | `id / username / group / quota / used_quota` |
| `tokens` | API key | `id / user_id / name / group / used_quota / model_limits` |
| `quota_data` | 模型×小时聚合 | `model_name / username / created_at / count / quota / token_used` |
| `perf_metrics` | 性能指标 | `model_name / group / bucket_ts / request_count / total_latency_ms` |

### 10.3 配额换算公式

```
1 USD = 500,000 quota （newapi/common/constants.go:62 QuotaPerUnit 默认值）

USD = quota / 500,000
quota = USD × 500,000

CNY = USD × USDCNYRate （默认 7.20，通过配置可调）
```

### 10.4 CSV 价目表模板

```csv
vendor_code,model_name,prompt_cost_per_1m,completion_cost_per_1m,cache_read_cost_per_1m,cache_write_cost_per_1m,image_cost_per_1m,audio_input_cost_per_1m,audio_output_cost_per_1m,web_search_cost_per_call,file_search_cost_per_call,per_call_cost,discount,effective_from,effective_to,remark
openai-azure,llm-model-a,2.5,10,1.25,0,0,0,0,0,0,0,1.0,2026-01-01,,官方价
openai-azure,llm-model-a-mini,0.15,0.6,0.075,0,0,0,0,0,0,0,1.0,2026-01-01,,官方价
```

支持中文表头别名（输入价、缓存读取价、模型 等）。详细见 `web/src/pages/UpstreamPricing.tsx` 的 downloadTemplate()。

### 10.5 启动命令

```bash
# 1. 准备 newapi 只读账号（PG 层 GRANT）
# 2. 复制 .env.example 为 .env 并填入 DSN / Token
# 3. 本地开发
docker compose up -d postgres redis   # 起依赖
go run ./cmd/server                    # 起后端
cd web && bun install && bun run dev  # 起前端
# 浏览器访问 http://localhost:5173
```

---

## 11. 评审要点（待确认）

- [ ] **鉴权**：本期 API 完全开放（仅本地）是否符合预期？生产环境如何接 newapi session？
- [ ] **多租户**：api-ops 是否需要按"团队 / 部门"隔离数据？当前是单租户。
- [ ] **告警通道优先级**：P1 阶段先实现哪几个？钉钉 / 飞书 / 企微 全要？
- [ ] **AI 报告**：周报 / 日报 / 即时报告 三种都要，还是先做一种？
- [ ] **审计日志**：api-ops 自身的操作（确认账单、删除价目）是否需要审计？
- [ ] **历史数据**：上线时是否要回溯一批历史对账？（建议 30 天起步）

---

**文档维护**：本文档随 api-ops 版本演进。任何模块的设计变更需同步更新本文档。