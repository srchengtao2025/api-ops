# Phase 2 Gate 报告（owner 自评 + 实测）

> **项目**：api-ops Phase 2（灌 mock 数据 + docker compose 一键起 demo）
> **owner**：Mavis
> **日期**：2026-06-11
> **依据**：`docs/PRD-v2.md` + `docs/DESIGN.md v1.0` + Phase 1.5 收口结果

---

## Phase 2 Gate Verdict: **PASS** ✅

---

## 1. 交付清单

| # | 交付 | 路径 | 状态 |
|---|---|---|---|
| 1 | `cmd/seed` mock 数据生成器 | `cmd/seed/main.go` + `internal/seed/{data,logs,ops,seed}.go` | ✅ 1197 行 Go 编译通过 |
| 2 | Docker 集成（server + seed 一体化镜像） | `Dockerfile` + `docker-entrypoint.sh` + `docker-compose.yml` | ✅ 一键 up |
| 3 | Demo 模式降级 | `internal/dal/db.go` 新增 `RoDB()`（RO=nil 时 fallback OPS）+ `config.go` 允许 API_OPS_RO_DSN 空 | ✅ |
| 4 | Mock 数据集 | 23 users / 10 channels / 6 vendors / 9 pricing / 10000 logs / 5 rules / 23 tiers / 3 thresholds / 6 KB / 4 sysconfig | ✅ 真实感命名（vip_acme / beta_corp 等） |
| 5 | README 一键启动 | `README.md`（中文 280+ 行） | ✅ |
| 6 | 演示走查脚本 | `scripts/demo-walkthrough.sh` | ✅ 5/5 场景通过 |

---

## 2. 实测数据（5 个核心 API 真实响应）

### 2.1 health
```bash
$ curl -s http://localhost:8088/api/health
{"ok":true}
```

### 2.2 dashboard/today（751 次调用 / 收入 $0.38）
```json
{
  "data": {
    "date": "2026-06-11",
    "request_count": 751,
    "success_count": 670,
    "error_count": 60,
    "revenue_usd": 0.3808759999999998,
    "prompt_tokens": 152768,
    "completion_tokens": 83348,
    "cache_tokens": 0,
    "avg_latency_ms": 856
  },
  "success": true
}
```

### 2.3 dashboard/trend（7 天趋势）
```json
[
  {"date": "2026-06-04", "request_count": 1177, "revenue_usd": 0.62},
  {"date": "2026-06-05", "request_count": 1507, "revenue_usd": 0.76},
  ...
]
```

### 2.4 billing/customer/1/preview（vip_acme 7 天对账）
```json
{
  "data": {
    "user_id": 1,
    "username": "vip_acme",
    "group": "svip",
    "request_count": 732,
    "success_count": 656,
    "error_count": 62,
    "refund_count": 14,
    "revenue_usd": 0.42,
    "cost_usd": 0.27,
    "profit_usd": 0.15,
    "profit_rate": 0.36,
    "by_model": [
      {"model_name": "llm-model-b", "revenue_usd": 0.19, "profit_rate": 0.38},
      {"model_name": "llm-model-a", "revenue_usd": 0.18, "profit_rate": 0.35},
      ...
    ]
  }
}
```

### 2.5 upstream/channels（10 个真实感渠道）
```json
[
  {"id": 1, "name": "OpenAI-Azure-us-east", "type": 14, "balance": 42.5, "response_time": 850},
  {"id": 2, "name": "Anthropic-Claude-us-west", "type": 2, "balance": 38.2, "response_time": 920},
  ...
]
```

---

## 3. demo-walkthrough.sh 5/5 通过

```
✓ 场景 1 通过（Dashboard KPI）
✓ 场景 2 通过（vip_acme 利润率 36%）
✓ 场景 3 通过（openai-azure 价目）
✓ 场景 4 通过（10 个在监控渠道）
✓ 场景 5 通过（6 个供应商）
```

---

## 4. 决策对齐（DESIGN v1.0 Q1-Q14 + Q-C4 + Q-C6）

| 决策 | mock 体现 | 后端体现 |
|---|---|---|
| Q1 鉴权 bypass | ✅ 11 个 mock HTML 无登录页 | ✅ middleware 留位，demo 模式 bypass |
| Q2 单租户 | ✅ | ✅ single-tenant 假设 |
| Q3 飞书独占 | ✅ alert-center.html 仅飞书 | ⏳ 留到 P1 实现 |
| Q4 AI 报告 | ✅ ai-error-diagnosis.html | ⏳ P3 |
| Q5 审计 | ✅ audit-logs.html | ⏳ P1（审计表 schema 已 ready） |
| Q6 30 天回溯 | ✅ seed 灌 7 天数据 + dashboard 24h 趋势 | ✅ seed 7 天，扩到 30 天改 scale=medium |
| Q7 复用 newapi | ✅ mock HTML 提到 | ⏳ demo 模式跳过，prod 模式接 |
| Q8 Antd 深色 | ✅ 11 HTML 全用 | n/a（前端） |
| Q9 WebSocket | ✅ customer-realtime.html 5s 模拟 | ⏳ P2 |
| Q10 LLM 可配置 | ✅ ai-error-diagnosis.html | ⏳ P3 |
| Q11 i18next | ✅ mock 全 lang="zh-CN" | n/a（前端） |
| Q12 SLA 99.5% | ✅ ops-dashboard.html 实时卡 | ⏳ P1 monitoring 引擎 |
| Q13 GitHub Actions | n/a | ⏳ P3 后 |
| Q14 上游对账反推 | ✅ upstream-statement.html Tab | ✅ 公式实现于 `internal/billing/upstream_statement.go` |
| Q-C4 LLM 不设月预算 | ✅ system-config.html 开关 | ⏳ P3 |
| Q-C6 飞书运行时配置 | ✅ system-config.html 表单 | ⏳ P1（system_config 表已 ready） |

---

## 5. 决策点记录（Phase 2 内 owner 拍板）

| 决策点 | 旧方案 | 新方案 | 原因 |
|---|---|---|---|
| Mac 缺 docker | 用 brew install | 装 Docker Desktop via brew | 用户授权（2026-06-11 11:37） |
| Mac 缺 Go LC_UUID | 期望本地 go run 跑 seed | **完全在 Docker 容器内跑 seed + server** | dyld 严格检查 LC_UUID，复杂 binary 跑不起来；容器内 Linux 工具链无此问题 |
| demo 模式 newapi 连接 | 必须连 newapi | RO=nil 时 `RoDB()` fallback 到 OPS，server 不 fatal | 新增 `internal/dal/db.go` RoDB() + config 允许空值 |
| api 容器 seed 时机 | 单独 seed service | **entrypoint.sh 先 seed 再 exec server** | 减少服务编排，docker compose up -d 一气呵成 |
| error_kb_entries.ErrorCode size:16 | 原始 schema | **改为 size:64** | 'ThrottlingException' 20 字符超 16 |

---

## 6. 走过的弯路（透明）

1. **coder 写 1197 行 Go 编译干净**（T1）→ 但卡在用户 Mac 装 docker 环境（5 次交互才装上）
2. **Mac 装好 docker 但 Go linker 缺 LC_UUID** → 改成"全在容器内跑"，seed + server 都在 api 容器
3. **server 启动 nil pointer panic**（RO 用了 newapi） → 加 `RoDB()` fallback 到 OPS（自有 DB）
4. **error_kb_entries.ErrorCode varchar(16) 太短**（ThrottlingException 20 字符超限）→ 改 size:64

---

## 7. 后续工作（Phase 3+）

- [ ] **P1 监控引擎**：5min 滑窗聚合 + 告警规则 YAML + 飞书 webhook
- [ ] **P2 实时面板**：WebSocket 推送 + 前端 React 接入 API
- [ ] **P3 AI 解读**：聚类 + 知识库 + LLM Provider 抽象 + 3 类报告
- [ ] **L1 优化**：dashboard top-customers 的 GROUP BY SQL bug
- [ ] **L2 优化**：seed 加上生成 progress bar（用 stdout escape 序列）

---

**报告人**：Mavis（owner 自评 + 自修 + 自验）
**生成时间**：2026-06-11 12:22 (Asia/Shanghai)
**产物**：`docs/test-reports/phase2-gate.md`
