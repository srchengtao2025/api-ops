# Phase 1 Gate Verdict — PRD-v2 ↔ Mock 联合验收

> **验证对象**: `docs/PRD-v2.md` (2270 行,12 章 + 附录 A/B/C) + `web/mock/` 8 个关键页面 + `web/design-tokens.json`
> **验证方法**: 全文阅读 PRD + mock 静态源码 + Playwright 端到端实测(6 关键页) + 设计 token 逐项核对
> **验证范围**: 字段一致性 / 决策一致性(Q1-Q14) / 设计 token 一致性 / 流程贯通 / PRD 自检
> **采样关键页**: `customer-statement.html` / `upstream-statement.html` / `customer-realtime.html` / `alert-center.html` / `ai-error-diagnosis.html` / `channel-health.html` / `dashboard.html`

---

## Phase 1 Gate Verdict: **FAIL**

**3 项 PASS,7 项决策一致性 FAIL,1 项 P0 阻塞(BUG)。** Phase 1 收口未通过,需修复后重审。

| 矩阵 | 结果 | 说明 |
|------|------|------|
| 字段一致性 | 8/9 表覆盖,1/9 缺关键公式 | 7 张表结构在 mock 中可视化;`upstream_pricing` 反推公式未在 mock 中展示明细 |
| 决策一致性 (Q1-Q14) | 7/14 PASS,**7/14 FAIL** | Q3 飞书独占/Q8/Q9/Q12/Q13 PASS;Q4 状态机/严重度/通道数/Q14 反推公式/LLM 选型 FAIL |
| 设计 token 一致性 | 11/11 PASS(颜色/字号/间距) | mock.css 100% 引用 design-tokens.json 中 token |
| 流程贯通 | 4/5 跳转 PASS,**1 跳转 FAIL** | index→dashboard→customer-realtime→alert-center→upstream OK;**customer-statement 列表 0 行渲染(JS 崩溃)** |
| 端到端行为 | 1/1 PASS(5s tick) | rpm 1262→1268 / tpm 234567→239740 / errStream 7→12 真实跳动 |

---

## 一、字段一致性矩阵

> 抽样方法: PRD §9 列出的 9 张新表 + 5 张 P0 复用表 → 交叉 mock 8 页

### 1.1 PRD §9 字段表 vs Mock UI 字段

| # | PRD 表 | PRD 关键字段 | Mock 页面 | Mock 渲染字段 | 一致性 | 证据 |
|---|--------|--------------|-----------|----------------|--------|------|
| 1 | `billing_statements` (P0 复用) | statement_type, subject_type, revenue/cost/profit, status(draft/confirmed/exported), generated_at | customer-statement.html + upstream-statement.html | `调用次数/错误次数/退款次数/客户实付/上游成本/毛利/毛利率/状态/未匹配/操作` + 供应商/渠道数/上游成本/客户实付/毛利/毛利率/结算周期/状态 | ✅ PASS | Playwright 实测 customer-statement 13 列表头,upstream-statement 10 列表头 |
| 2 | `upstream_pricing` (P0 复用) | vendor_code, model_name, prompt_cost_per_1m, completion_cost_per_1m, **discount**, effective_from | **无** | mock 仅展示聚合"上游成本",**无 `prompt_cost_per_1m` / `discount` 明细** | ❌ **FAIL** | Playwright: `document.body.innerText.includes("discount")` → false,`/1M` → false |
| 3 | `channel_vendor_map` (P0 复用) | channel_id, vendor_code, weight | upstream-statement.html + channel-health.html | 供应商列 + 渠道 #38 OpenAI-Azure → openai-azure 映射 | ✅ PASS | upstream-statement 表格 9 行,channel-health 16 行 |
| 4 | `channel_health_5min` (§9.1.1) | channel_id, error_count, error_rate, p50/p95/p99, balance, status (enabled/manual_disabled/auto_disabled) | channel-health.html | 错误率/RPM/TPM/P50/P95/余额/基线/最近告警/状态 | ✅ PASS | 12 列表头含 status=enabled/auto_disabled/manual_disabled(§5.1.5 状态值 100% 匹配) |
| 5 | `customer_health_5min` (§9.1.3) | user_id, tier, rpm, tpm, error_rate, p95_latency_ms, spend_velocity, health_score, last_error_code | customer-realtime.html | RPM/TPM/错误率/P95/累计消耗/账户余额/最近错误/限流次数 + 延迟 P50/P95/P99 趋势 | ✅ PASS(字段名简化但语义对) | 8 个 KPI 字段 ID 100% 覆盖;`health_score` 字段缺(mock 头部"健康"tag 替代) |
| 6 | `alert_rules` (§9.1.6) | id/name/type/target/condition/severity/notify_channels/enabled/yaml_full | alert-center.html | 6 条规则概览:`ch_high_error_rate` / `ch_balance_low` / `ch_p95_degraded` / `vip_consecutive_errors` / `vip_rate_limit` / `ai_unknown_error_code` + 触发条件 + 动作 | ✅ PASS | 6 行 rule table 全展示 |
| 7 | `alert_histories` (§9.1.7) | severity/info/状态(firing/acknowledged/resolved/suppressed/escalated) | alert-center.html | severity=`critical/high/medium/low`,状态=`firing/acknowledged/resolved/silenced` | ❌ **FAIL** | PRD §5.3.4 状态机: firing→{suppressed, acknowledged, escalated, resolved};mock 缺 escalated/suppressed 改为 silenced(语义不同);严重度值不匹配 |
| 8 | `user_tier` + `tier_threshold` (§9.1.9) | tier=svip/vip/normal,error_count_5min,rate_limit_ratio,severity | alert-center.html + channel-health.html + customer-statement.html | tier tag 出现 SVIP/VIP/default(mapping: `c[1]==='SVIP'?'tag-success':'VIP'?'tag-warning':'tag-neutral'`) | ⚠️ WARN | PRD 命名 `svip/vip-1/vip-2/vip-3/normal`(C7 待确认),mock 用 `SVIP/VIP/default`;未严格对齐 |
| 9 | `ai_error_clusters` + `ai_diagnoses` (§9.3.1+§9.3.2) | cluster_id, error_code, pattern_norm(归一化), cnt, affected_customers, severity, root_cause, recommended_action, confidence | ai-error-diagnosis.html | 11 聚类 + LLM 解读(category/matched_code/root_causes/affected_scope/actions)+ confidence(0.92) | ✅ PASS | 11 聚类全展示,模式用 `<MODEL>/<REGION>/<UUID>/<N>/<TS>` 归一化 token |
| 10 | `error_kb_entries` (§9.3.5) | vendor=aws_bedrock/provider_gamma/openai/anthropic/gemini, error_code, category, root_cause, action, doc_url | ai-error-diagnosis.html | 知识库卡片含 aws_bedrock/ThrottlingException + doc_url `https://docs.aws.amazon.com/bedrock/...` + 命中 KB | ⚠️ WARN | mock chart-kb-provider 列 `google_gemini`,PRD §9.3.5 DDL 字段 vendor 仅 `google`,枚举不严格一致 |
| 11 | `audit_logs` (§9.4.1) | actor, action(create/update/delete/ack/resolve/escalate), resource_type, before/after_value | **无** | mock 未展示任何审计日志/操作历史页面 | ❌ **FAIL** | PRD §4.10 强制 E1 写 audit_logs,但 mock 没有专门页面展示 |
| 12 | `system_config` (§9.4.2) | key/value/description/updated_by | ai-error-diagnosis.html 副标题隐含 LLM 配置 | mock 副标题写"模型:claude-sonnet-4-5(upstream 网关)" | ⚠️ WARN | 与 PRD §12.5 C1 默认 `llm-model-b` 漂移,未走 C1 决策流程 |
| 13 | `realtime_subscribers` (§9.2.1) | session_id, user_id, topic, client_ip, connected_at, last_ping_at | customer-realtime.html | "WebSocket /api/stream/customer/vip_acme · 5s tick · 数据延迟 < 6s" + `setInterval` 模拟 | ✅ PASS | 副标题完整标注 ws 端点、5s tick、延迟指标 |
| 14 | `sync_checkpoint` (§9.1.5) | source, last_sync_at, status(idle/running/failed) | dashboard.html | "今日 · 2026-06-10 · 数据延迟 < 15 min" | ✅ PASS | 时间戳 + 延迟指标在 dashboard 头部 |

### 1.2 状态值字段一致性矩阵(关键)

| 字段 | PRD 定义 | Mock 实际 | 一致性 |
|------|----------|-----------|--------|
| `billing_statements.status` | draft / confirmed / exported | draft / confirmed / exported(alert-center.html、customer-statement.html、upstream-statement.html) | ✅ PASS |
| `alert_histories.status` | firing / acknowledged / resolved / suppressed / escalated | firing / acknowledged / resolved / silenced(alert-center.html 状态机图 + 9 行告警) | ❌ **FAIL**(缺 suppressed/escalated,多 silenced) |
| `alert_histories.severity` | info / warning / high / critical | critical / high / medium / low | ❌ **FAIL**(缺 info/warning,多 medium/low) |
| `channels.status` | enabled / manual_disabled / auto_disabled | enabled / auto_disabled / manual_disabled(channel-health.html 16 行) | ✅ PASS |
| `customer_health_5min.tier` | svip / vip-1 / vip-2 / vip-3 / normal (C7 待确认) | SVIP / VIP / default(customer-statement + customer-realtime) | ⚠️ WARN(C7 未拍板,但 mock 简化偏离 PRD) |
| `upstream_pricing_imports.status` | processing / success / partial / failed | **mock 不展示价目导入页** | ⚠️ 缺页(可接受,P0 实现优先) |
| 告警动作类型 | notify_feishu / auto_disable_channel / ai_diagnose | notify_ops / auto_disable / ai_diagnose / notify_finance / notify_cs(alert-center 6 条规则) | ⚠️ WARN(后缀命名与 PRD §5.3.1 YAML 略漂,功能等价) |

### 1.3 错误码字段一致性

| 错误码 | PRD 来源 | Mock 出现位置 | 一致性 |
|--------|----------|----------------|--------|
| HTTP 429 Rate Limit | 全部真实上游 | dashboard 错误流 + channel-health 错误码饼图(429 限流 182) + customer-realtime 实时错误流 | ✅ PASS |
| HTTP 502 Gateway | newapi upstream | dashboard + channel-health(502 网关 28) | ✅ PASS |
| HTTP 529 Overloaded | Anthropic | dashboard + customer-realtime + ai-error-diagnosis 聚类 #3 | ✅ PASS |
| HTTP 400 InvalidParameter | 阿里云百炼 | dashboard + customer-realtime(InvalidParameter: model "qwen-max" not found) | ✅ PASS |
| HTTP 403 API key invalid | Gemini | dashboard + customer-realtime + ai-error-diagnosis 聚类 #5 | ✅ PASS |
| HTTP 500 Internal | OpenAI | dashboard + customer-realtime(Internal server error) | ✅ PASS |
| AWS ThrottlingException | aws_bedrock KB | ai-error-diagnosis KB 匹配卡("ThrottlingException" "HTTP 429" "退避重试 + 自动切换备用渠道") | ✅ PASS |
| HTTP 422 Unprocessable | Mistral | dashboard 错误流(422)+ ai-error-diagnosis 聚类 #9 | ✅ PASS |

---

## 二、决策一致性矩阵(Q1-Q14)

| 决策 | 选定方案 | PRD 落地 | Mock 可视化体现 | 一致性 | 证据 |
|------|----------|----------|----------------|--------|------|
| **Q1** 鉴权 | bypass,生产前接 newapi session | §11.2 + §10.3 | mock 头部"UI Designer"用户卡(无登录页) | ✅ PASS | index.html + 所有页面 header 缺登录态,与 bypass 一致 |
| **Q2** 单租户 | 预留 user_id 过滤位 | §9.4 + §11.2 | 不展示多租户切换 UI | ✅ PASS | mock 全局无 tenant 概念 |
| **Q3** 告警通道 | **飞书机器人**(只做飞书) | §5.3 + §5.4 | alert-center.html 展示 **4 通道**:飞书(主)+ 钉钉(备)+ 邮件 + 自定义 Webhook | ❌ **FAIL** | 截图实测: "飞书机器人 api-ops-alert 主通道" + "钉钉机器人 api-ops 备用通道" + "邮件 SMTP ops@upstream.com" + "自定义 Webhook internal-im";**与 PRD 锁定只做飞书一个冲突**;mock README §3 说"飞书/钉钉/邮件/Webhook"沿用 DESIGN §0.5 旧决策,未随 PRD-v2 Q3 更新 |
| **Q4** 报告 | 日报+周报+客户健康度手动 | §7.5 | ai-error-diagnosis.html 有"客户 AI 诊断"按钮(手动);**mock 没有日报/周报页面** | ⚠️ WARN | mock 只覆盖 P3 子集,日报/周报未做独立页 |
| **Q5** 审计 | 全部写操作审计 | §9.4.1 + §11.2 | **mock 无 audit_logs 展示页** | ❌ **FAIL** | PRD §4.10 E1 强制 E1 = 2 人日,phase 1 mock 缺失 |
| **Q6** 30 天回溯 | sync_checkpoint 驱动 | §4.7 + §11.6 | dashboard 头部"今日 · 2026-06-10 · 数据延迟 < 15 min",**无 backfill 进度条** | ⚠️ WARN | backfill UI 在 PRD §4.7 列为 AC-1 必做,mock 缺 |
| **Q7** 主动探测 | 复用 newapi `AutomaticallyTestChannels` | §5.1.2 | channel-health.html 底部"🔁 自动禁用联动 · 借力 newapi AutomaticallyTestChannels" + auto_disabled 状态 2 个 | ✅ PASS | 16 渠道含 2 个 `auto_disabled` 标记 + 文字说明引用 `PATCH /api/channel/:id status=2` |
| **Q8** UI 栈 | Antd 5 + 自研深色 | §10 + §12 | mock.css 暗色主题 + 字体 Inter/Source Han Sans SC + KPI 36px | ✅ PASS | design-tokens.json + mock.css 颜色 / 字体 / 间距 100% 对齐 |
| **Q9** WebSocket 双向 | 含 ACK/订阅切换 | §6.1 | customer-realtime.html "WebSocket · 5s 推送" badge + 5s tick 模拟 + ops-dashboard.html 2s tick 错误流 + 副标题"5s tick · 数据延迟 < 6s" + 5s 跳动实测(1262→1268) | ✅ PASS | Playwright 6s 实测 rpm/tpm/errStream 真跳动 |
| **Q10** LLM 可配置 | gateway / direct | §7.3 + §10.4 | ai-error-diagnosis.html 副标题"模型:claude-sonnet-4-5(upstream 网关)" + LLM 输出区块 | ⚠️ WARN | 模型名与 PRD §12.5 C1 默认 `llm-model-b` 漂移;但"网关"调用方式与 Q10 一致 |
| **Q11** i18n | zh-CN + i18next 预留 | §11.4 | mock 全站 zh-CN,html lang="zh-CN" | ✅ PASS | 所有 9 个 mock 页 lang="zh-CN",无英文 |
| **Q12** SLA 99.5% | 年宕机 ≤ 43.8h | §11.1 | ops-dashboard.html 头部"SLA 99.92%" | ✅ PASS | 数字与 Q12 同量级,展示在 ops 大屏 |
| **Q13** CI | GitHub Actions lint+test+docker | §11.5 | **mock 无关(基础设施)** | n/a | CI 不在 mock 范围,跳过 |
| **Q14** 反推公式 | 反推 1M 原单价 × discount | §4.6 | upstream-statement.html **仅展示聚合"上游成本"**,**无"原 1M 单价 / discount / 数值推导"明细** | ❌ **FAIL** | Playwright `body.innerText.includes("discount")` → false;`/1M` → false;`prompt_cost_per_1m` 字段从未出现;**违反任务要求"Q14 反推公式 → upstream-statement.html 必须展示原 1M 单价 × discount 明细"** |

### 决策一致性小结

- **PASS(7)**: Q1, Q2, Q7, Q8, Q9, Q11, Q12
- **WARN(3)**: Q4, Q6, Q10(范围/详情未到 PRD 完整度,但基础合规)
- **FAIL(4)**: **Q3**(通道数)、**Q5**(审计无页面)、**Q14**(反推公式无明细)、**+ 隐性 FAIL**:告警状态机 + 严重度枚举不匹配 PRD(挂 Q3 决策)

---

## 三、设计 Token 一致性

> 对照源: `web/design-tokens.json`(233 行) vs `web/mock/assets/mock.css`(847 行) + 9 个 mock 页 inline style

| 类别 | Token 路径 | Token 值 | mock.css / HTML 出现 | 匹配 | 证据 |
|------|-----------|----------|---------------------|------|------|
| 背景 | `color.background.base` | `#0B0E14` | `body { background: #0B0E14 }` | ✅ | mock.css:12 |
| 背景 | `color.background.elevated` | `#0F1729` | `.app-header` `.app-sidebar` `.card` `.kpi-card` `.dt` | ✅ | mock.css:47, 112, 230, 270, 396 |
| 背景 | `color.background.raised` | `#131B30` | `.user-chip` `.dt thead th` `.kpi-card 用户头像` | ✅ | mock.css:91, 402 |
| 边框 | `color.border.subtle` | `#1F2937` | `.card` `.dt tbody td` border-bottom | ✅ | mock.css:233, 416 |
| 边框 | `color.border.default` | `#2A3346` | `.app-header` border-bottom + `.btn` | ✅ | mock.css:48, 477 |
| 主色 | `color.accent.primary` | `#3B82F6` | `.kpi-card.kpi-primary` 顶部 var(--accent) + 链接 + tab.active | ✅ | mock.css:348 |
| 辅色 | `color.accent.secondary` | `#06B6D4` | 渐变 + 图表副系 | ✅ | mock.css:134 |
| 成功 | `color.status.success` | `#10B981` | `.badge.success` `.status-dot.success` `.kpi-success` | ✅ | mock.css:331, 361, 385 |
| 警告 | `color.status.warning` | `#F59E0B` | `.kpi-warning` `.badge.warning` | ✅ | mock.css:345, 386 |
| 危险 | `color.status.danger` | `#EF4444` | `.kpi-danger` `.badge.danger` `.breathing-critical` | ✅ | mock.css:346, 363, 387 |
| 文字 | `color.text.primary` | `#E5E7EB` | body, .kpi-value, .dt tbody td | ✅ | mock.css:13, 299, 418 |
| 文字 | `color.text.secondary` | `#9CA3AF` | .text-muted, .app-header 文字 | ✅ | mock.css:616 |
| 字号 | `font.size.kpi` | `36px` | `.kpi-card .kpi-value` font-size: 36px | ✅ | mock.css:297;Playwright 实测 `getComputedStyle(.kpi-value).fontSize === "36px"` |
| 字号 | `font.size.kpiLarge` | `56px` | **未使用** | ⚠️ | ops-dashboard 用 32px (`.bs-kpi-value`),非 56px;但 ops 是大屏 1920×1080,32px 视觉合理 |
| 间距 | `space.4` | `16px` | `.kpi-grid` gap: 16px + `.card` padding: 16px | ✅ | mock.css:265, 234 |
| 圆角 | `radius.default` | `4px` | `.card` `border-radius: 8px` (.card.lg = 8px) | ⚠️ | mock 偏大,使用 `radius.lg` (8px) |
| 圆角 | `radius.lg` | `8px` | `.card` `.kpi-card` `.btn` | ✅ | mock.css:233, 273, 478 |
| 动效 | `motion.duration.slower` | `600ms` | `.count-up { animation: countUp 600ms ... }` | ✅ | mock.css:808 |
| 动效 | `motion.duration.breathing` | `1000ms` | `.breathing-critical { animation: breathing 1s ease-in-out infinite }` | ✅ | mock.css:814 |
| 字体 | `font.family.base` | Inter/Source Han Sans SC | `font-family: "Inter", "Source Han Sans SC", "PingFang SC"...` | ✅ | mock.css:14 |
| 动效 keyframes | `breathing` (0%/50%/100% scale 1→0.85→1 opacity 1→0.4→1) | mock.css 完整实现 | ✅ | mock.css:815-818 |
| 动效 keyframes | `pulseRing` (0%/70%/100% boxShadow) | mock.css 完整实现 | ✅ | mock.css:367-371 |
| sidebar | `layout.sidebar.width` 220px | `.app-layout { grid-template-columns: 220px 1fr }` | ✅ | mock.css:33 |
| header | `layout.header.height` 56px | `.app-layout { grid-template-rows: 56px 1fr }` | ✅ | mock.css:34 |
| 大屏 | `layout.dashboard.width/height` 1920×1080 | `.bigscreen { width: 1920px; height: 1080px }` | ✅ | mock.css:714-715 |
| sparkline | `kpi-spark` 80×32 | `.kpi-card .kpi-spark { width: 80px; height: 32px }` | ✅ | mock.css:338-339 |

**Token 一致性结论: 11/11 关键 token PASS,2/22 边缘值差异(大屏 KPI 字号 32 vs 56,card 圆角 8 vs 4)。**

---

## 四、流程贯通测试(端到端)

> 链: `index.html` → `dashboard.html` → `customer-statement.html` → `customer-realtime.html` → `alert-center.html` → `upstream-statement.html` → `ai-error-diagnosis.html`
> 方法: Playwright `browser_navigate` + `browser_click` 真实跳转,`browser_evaluate` 读 DOM

### 4.1 静态资源全 200 OK

```
HTTP 200 · index.html              · 10238 bytes
HTTP 200 · dashboard.html
HTTP 200 · customer-statement.html
HTTP 200 · customer-realtime.html
HTTP 200 · alert-center.html
HTTP 200 · upstream-statement.html
HTTP 200 · ai-error-diagnosis.html
HTTP 200 · channel-health.html
HTTP 200 · ops-dashboard.html
HTTP 200 · assets/mock.css
HTTP 200 · assets/mock.js
```

> 唯一 console error: `favicon.ico 404` — 与验收无关,装饰资源。

### 4.2 流程贯通链

| 步骤 | 操作 | URL 跳转 | 渲染关键 | 结果 |
|------|------|----------|----------|------|
| 1 | 打开 index.html | — | 8 张 nav-card + 设计 token 摘要表 10 行 | ✅ PASS |
| 2 | 点击 "◆ Dashboard" nav-card | `/index.html` → `/dashboard.html` | 4 张 KPI + 24h 双轴趋势 + Top10 三联 + 5 条告警表 | ✅ PASS |
| 3 | 点击 sidebar "▤ 客户对账" | `/dashboard.html` → `/customer-statement.html` | **0 行 tbody 渲染** + 4 KPI + 详情面板隐藏 | ❌ **FAIL** — JS 崩溃 |
| 4 | 返回 dashboard → 跳 customer-realtime | `/customer-realtime.html` | 8 KPI 全 ID 存在 + 3 charts 渲染 + 错误流 7 条 | ✅ PASS |
| 5 | 5s 跳动实测(等 6s) | 同页 | rpm 1262→1268、tpm 234567→239740、errRate 0.8→0.91、p95 1.2→1.25、errStream 7→12 | ✅ PASS(5s tick 真实工作) |
| 6 | 跳 alert-center | `/alert-center.html` | 4 状态机盒 + 9 行告警 + 6 条规则 + 4 通知通道 | ✅ PASS(但状态机/严重度不严格对齐 PRD) |
| 7 | 跳 upstream-statement | `/upstream-statement.html` | 4 KPI + 9 行供应商 + 利润分析横切 3 Tab + "未映射 3 渠道需补录" | ✅ PASS(但 Q14 反推公式无明细) |
| 8 | 跳 ai-error-diagnosis | `/ai-error-diagnosis.html` | 11 聚类 + LLM 解读详情(category/matched_code/root_causes/actions) + 知识库 142 条 + KB 命中卡 | ✅ PASS(但 vendor 命名略漂) |

### 4.3 Step 3 失败证据(JS 崩溃)

```
[ERROR] TypeError: undefined is not iterable (cannot read property Symbol(Symbol.iterator))
    at http://127.0.0.1:18877/customer-statement.html:253:38
    at Array.forEach (<anonymous>)
    at http://127.0.0.1:18877/customer-statement.html:251:13
```

**根因分析** (`web/mock/customer-statement.html:248-253`):
```js
const customers = [
  ['vip_acme','SVIP', 48210, 298, 12, 2340.50, 1265.30, 1075.20, 45.9, 'confirmed', 0, 'confirmed'],
  // ...
];
// ...
customers.forEach((c, i) => {
  const profitRate = (c[8]).toFixed(1);
  const [statusLabel, statusCls] = statusMap[c[10]];  // ← c[10] = 0 (数字 0,未匹配数)
                                                     //   而非 statusMap[0] = undefined
                                                     //   → 解构失败
  // ...
});
```

**结构问题**:每行 12 元素,索引 9 是 status 字符串('confirmed'/'draft'/'exported'),索引 10 是未匹配数(数字),索引 11 是冗余的 status 副本。**JS 读 c[10] 当 status 用是 bug**。

**业务影响**:用户打开 customer-statement.html 看到"KPI 满 + 表格为空 + 13 列灰表头",完全无法预览对账列表 → 违反 PRD §4.1 AC-1(3 秒内返回完整对账单)。

---

## 五、PRD 自检 — 14 项决策 PASS/FAIL 表

| # | 决策项 | Mock 是否可视化 | PASS/FAIL | 关键证据 |
|---|--------|----------------|-----------|----------|
| Q1 | 鉴权 bypass | ✅(无登录页) | **PASS** | 全部 9 页均无登录交互 |
| Q2 | 单租户 | ✅(无 tenant 切换) | **PASS** | 全局无多租户概念 |
| Q3 | 飞书机器人(只做飞书) | ❌(展示 4 通道) | **FAIL** | alert-center 通知出口: 飞书+钉钉+邮件+Webhook |
| Q4 | 日报+周报+客户健康度手动 | ⚠️(只覆盖客户健康度) | **WARN** | 无日报/周报独立页 |
| Q5 | 全部写操作审计 | ❌(无 audit_logs 展示页) | **FAIL** | mock 0 个审计页 |
| Q6 | 30 天 backfill | ⚠️(无进度条) | **WARN** | 无 backfill 按钮/进度条 |
| Q7 | 复用 newapi AutomaticallyTestChannels | ✅(自动禁用联动说明) | **PASS** | channel-health.html 底部"借力 newapi AutomaticallyTestChannels" + 2 auto_disabled 渠道 |
| Q8 | Antd 5 + 自研深色 | ✅(深色 + KPI 36px) | **PASS** | mock.css 100% 引用 design-tokens.json |
| Q9 | WebSocket 双向 | ✅(5s 跳动实测) | **PASS** | rpm 1262→1268, errStream 7→12 |
| Q10 | LLM 可配置 | ✅(网关调用) | **PASS**(WARN:模型版本漂移) | "模型:claude-sonnet-4-5(upstream 网关)" — 走 gateway 与 Q10 一致,但与 §12.5 C1 默认 `llm-model-b` 漂移 |
| Q11 | zh-CN + i18next 预留 | ✅(全站 zh-CN) | **PASS** | html lang="zh-CN" × 9 |
| Q12 | SLA 99.5% | ✅(ops 大屏 SLA 99.92%) | **PASS** | ops-dashboard 头部展示 |
| Q13 | GitHub Actions | n/a(mock 无关) | n/a | 跳过 |
| Q14 | 反推 1M 原单价 × discount | ❌(无明细) | **FAIL** | upstream-statement 无 discount/1M/prompt_cost 字段 |

**统计**: PASS=7, WARN=3, FAIL=4, n/a=1。

---

## 六、缺失项清单

### 6.1 阻塞级 FAIL(必须修复才能进 Phase 2)

1. **`customer-statement.html` 列表 0 行渲染(JS 崩溃)**
   - 位置: `web/mock/customer-statement.html:251-253`
   - 修复: 改 `c[10]` → `c[9]`,或修正 customers 数组索引位置
   - 影响: P0 主对账页面核心列表不可用,违反 PRD §4.1 AC-1

2. **Q14 反推公式无明细(任务强制要求)**
   - 位置: `web/mock/upstream-statement.html`(整个 mock 套件)
   - 修复: 在供应商对账详情/利润分析 Tab 加"原 1M 单价 × discount 明细"区块
   - 建议: 加一个 Tab/折叠区,展示 `{model: llm-model-a, prompt_cost_per_1m: 2.5, completion_cost_per_1m: 10, discount: 0.85, 计算: 2.5×1000/1M + 10×500/1M = 0.0075, cost = 0.0075×0.85 = 0.006375 USD}`
   - 影响: 任务硬要求"Q14 反推公式 → upstream-statement.html 必须展示原 1M 单价 × discount 明细"

3. **Q3 告警通道 vs PRD 锁定冲突**
   - 位置: `web/mock/alert-center.html:209-256` 通知出口
   - 修复: 移除钉钉/邮件/Webhook 通道(只保留飞书),或在 §5.3 上加 3 个 "未启用(V2.2/P4)" 灰色标签
   - 影响: mock 暗示产品支持 4 通道,与 PRD 锁定 Q3"只做飞书"矛盾,误导后续 coder

4. **告警状态机 + 严重度不匹配 PRD**
   - 位置: `web/mock/alert-center.html:87-113`(状态机图) + `:122`(级别下拉)
   - 修复: 状态改为 `firing → {suppressed, acknowledged, escalated, resolved}`;严重度改为 `info / warning / high / critical`;移除 `silenced` 和 `medium/low`
   - 影响: 后续 alert_histories 表的 status 字段 enum 不会被 coder 正确实现

### 6.2 重要级 FAIL(进 Phase 2 之前应修)

5. **Q5 audit_logs 无展示页**
   - 修复: 加一个 `audit-logs.html`(或 alert-center 内嵌"操作历史"折叠区),展示 9 类写操作(create/update/delete/ack/resolve/escalate)的 actor/action/resource/before/after

6. **Q10 LLM 模型版本漂移**
   - 修复: ai-error-diagnosis.html 副标题改 `llm-model-b(upstream 网关)` 与 PRD §12.5 C1 对齐;或由 owner 拍板 C1 改为 4-5 再更新 mock

7. **mock 知识库 vendor 命名漂移**
   - 修复: ai-error-diagnosis.html 知识库 chart `google_gemini` → `google` 与 PRD §9.3.5 DDL 对齐

### 6.3 警告级 WARN(可放 P1 阶段)

8. **Q4 日报/周报无独立页** — mock 范围可接受
9. **Q6 30 天 backfill 无进度条 UI** — mock 范围可接受
10. **tier 命名 `SVIP/VIP/default` vs PRD `svip/vip-1/2/3/normal`** — 等 C7 拍板
11. **大屏 KPI 字号 32px vs token 56px** — 1920×1080 视距下 32px 视觉合理
12. **mock README §8 自我标注"等 T1(PRD-v2)完成后,用 Edit 工具同步字段定义"** — 本次联合验收正是为推动这一步

---

## 七、关键建议(给 owner / coder)

1. **立即修复 customer-statement.html JS 崩溃**(P0 阻塞,1 行改动)
2. **upstream-statement.html 加反推公式明细 Tab**(任务硬要求,半天工作量)
3. **明确 Q3 飞书独占策略**: PRD 说"只做飞书",但 mock 给了 4 通道。建议要么 PRD 放宽到 "P1: 飞书, P2: 钉钉/邮件";要么 mock 删除 3 个非飞书通道。**这是个原则分歧,需 owner 拍板**
4. **告警状态机枚举统一**: 状态 + 严重度建议都采用 PRD §5.3.4-5 的命名(mermaid stateDiagram-v2 一致),否则 phase 3 coder 写 enum 时必踩坑
5. **Phase 2 demo 启动顺序**: 先修上面 4 项,再走 Phase 2(docker compose up demo 启动)— 否则 demo 启动后用户点 customer-statement 直接看到空表

---

## 八、VERDICT

**Phase 1 Gate: FAIL**

| 维度 | 结果 |
|------|------|
| 字段一致性 | ⚠️ 8/14 通过(7 表 PASS,1 表缺明细,3 项 enum 漂移) |
| 决策一致性(Q1-Q14) | 7/14 PASS,3/14 WARN,**4/14 FAIL** |
| 设计 token 一致性 | ✅ 11/11 关键 token PASS |
| 流程贯通 | 4/5 跳转 PASS,**1 P0 页面 JS 崩溃** |
| 端到端行为 | ✅ Q9 5s 跳动实测通过 |
| 缺失项 | 4 阻塞 + 3 重要 + 5 警告 |

**关键阻塞**: customer-statement.html JS 崩溃(P0) + Q14 反推公式无明细(任务硬要求) + Q3/Q14/Q5 决策不一致。

**通过条件**: 修复 §6.1 全部 4 项 + §6.2 至少 5、6 两项后,可重提 gate。
