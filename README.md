# api-ops

> 针对 [newapi](https://github.com/songquanpeng/one-api) 写的运营管理工具。  
> Dashboard 看板 / 客户对账 / 上游对账 / 利润分析 / 监控告警，**3 数据源铁律 + 5 大能力**一站式搞定。

> English: see [README.en.md](./README.en.md) — Operations management tool written for [newapi](https://github.com/songquanpeng/one-api)
> (dashboard, customer billing, vendor billing, profit analysis, monitoring & alerts).

---

## 适用场景 / Use case

你在跑一个 [newapi](https://github.com/songquanpeng/one-api) 实例，卖 token 给你自己的客户，想：

- ✅ 每天看每个客户的消耗、付款、成本、毛利
- ✅ 跟上游供应商月度对账（算你欠每个供应商多少钱）
- ✅ 监控渠道错误率、延迟、稳定性
- ✅ 异常告警 + AI 自动归因

**api-ops 就是干这个的。** 通过只读 DB + Admin API Token 与 newapi 通信（**不修改 newapi 任何代码**），单租户、内网部署、10 人内运营团队、月对账级别精度。

---

## 架构 / Architecture

```
              ┌─────────────────┐
              │  upstream LLM   │  (e.g. OpenAI / Anthropic / 自建)
              │  API 供应商     │
              └────────┬────────┘
                       │ 调用记录
                       ▼
              ┌─────────────────┐
              │  upstream DB    │  (你跑在另一台服务器, RoDB 只读账号)
              └────────┬────────┘
                       │
                       ▼
   ┌──────────────────────────────────────┐
   │           api-ops (本项目)            │
   │                                      │
   │  data sources (3 个铁律):            │
   │   1. upstream admin API              │ 实时列表 (≤ 1000 行)
   │   2. 直连 upstream DB (RoDB)         │ 大量聚合 SQL
   │   3. 本地缓存 DB (5min tick)         │ 准实时 dashboard
   │                                      │
   │  5 大能力:                            │
   │   - Dashboard (今日/7d 趋势)         │
   │   - BILLING v2 客户对账              │
   │   - BILLING v3 上游对账              │
   │   - BILLING v4 利润分析              │
   │   - 监控中心 (渠道健康 + 告警)       │
   │                                      │
   │  技术栈:                              │
   │   - Go 1.22 + Gin + GORM             │
   │   - PostgreSQL 15 (自有)             │
   │   - Redis 7 (可选)                   │
   │   - React 18 + Antd 5 + ECharts 5    │
   │   - 飞书 webhook 告警                │
   └──────────────────────────────────────┘
                       │
                       ▼
              ┌─────────────────┐
              │  Web 前端       │  http://your-server:8088/
              │  (dist 烤进      │  → admin 登录 → 6 SPA 页面
              │   api 镜像)     │
              └─────────────────┘
```

---

## 三大数据源铁律 / 3 data-source rule

**api-ops 严格只能有 3 个数据源**，"第 4 源"必须砍掉或归档到 `archive/`。

| 数据源 | 路径 | 实时性 | 何时用 |
|---|---|---|---|
| upstream API | upstream admin `/api/*` | 实时 | 列表/详情（≤ 1000 行） |
| 直连 DB (RoDB) | upstream RDS `*.logs` | 实时 | 大量聚合 SQL |
| 本地缓存 DB | 自有库 `cache_*` 表 | 准实时 (5min 延迟) | dashboard 实时面板 |

5 大能力的每个端点都从这 3 个源之一取数。**不允许 mock / 影子表 / 临时 import 路径**。

详见 [docs/DATA-SOURCES.md](./docs/DATA-SOURCES.md)（30 活跃端点 × 数据源对照表）。

---

## 快速开始 / Quick start

### 前置条件 / Prerequisites

- Go 1.22+ （[macOS 1.22.5 有 LC_UUID bug](docs/test-reports/) → 用容器跑 `go test`）
- Node 20+ / npm
- PostgreSQL 15
- Redis 7（可选，挂了降级为 no-cache 模式）
- Docker + docker compose
- 一个 upstream LLM API 实例 + 它的 admin token + 只读 DB 账号

### 一键起 demo / Demo with docker compose

```bash
# 1. 复制 .env 模板
cp .env.example .env
cp .env.production.example .env.production

# 2. 编辑 .env，填 3 个必填（占位符以 PLEASE_FILL_ / change_me / sk-xxx 形式给出）:
#    - API_OPS_ADMIN_TOKEN=PLEASE_FILL_ADMIN_TOKEN
#    - API_OPS_RO_DSN=...password=PLEASE_FILL_PASSWORD
#    - OPS_DB_DSN=...password=change_me
#    详见 .env.example / .env.production.example 注释

# 3. 起服务（自带 PG / Redis / api 容器）
docker compose up -d

# 4. 访问
# http://localhost:8088/
# 首次启动会要求你设置 bootstrap admin 密码（`OPS_ADMIN_BOOTSTRAP_PASSWORD`）
```

### 生产部署 / Production deploy

```bash
# 1. 编译跨平台镜像 (macOS arm64 → 远程 linux/amd64)
docker buildx build --platform linux/amd64 -t api-ops:latest . --load

# 2. 推镜像到 ECS
docker save api-ops:latest | gzip > /tmp/api-ops.tar.gz
scp /tmp/api-ops.tar.gz root@<your.server>:/tmp/

# 3. ECS 端 load + docker compose
ssh root@<your.server> 'cd /opt/api-ops && \
  docker load -i /tmp/api-ops.tar.gz && \
  docker compose -f docker-compose.prod.yml up -d api'

# 4. 公网暴露：用 nginx 反代 80 → 8088, 阿里云安全组开 80/80 入站
```

详细见 [docs/DESIGN.md §部署铁律](./docs/DESIGN.md) + `scripts/deploy-prod.sh`。

---

## 5 大能力 / 5 capabilities

### 1. Dashboard 今日 / 7d 趋势

- `GET /api/dashboard/today` — 今日累计 revenue / rpm / tpm
- `GET /api/dashboard/trend7d` — 7 天趋势曲线（**后端 5min cache, SPA 5min tick 拉 1 次**）

### 2. BILLING v2 客户对账

- 6 端点：当前月汇总 / 上月导出任务 / 任务中心 / ZIP 下载 / 取消
- ZIP 含 README + HTML + XLSX，**XLSX sharedStrings 校验 7 列表头**
- 5 业务规则 (R1-R5)：零输出免单 / 图片标记 / 退款不计 / 错误不计 / 未匹配上游不计
- 30 天任务保留 + 自动 prune

### 3. BILLING v3 上游对账

- 5 端点：上游当前月汇总 / 上月导出 / 单供应商任务 / 任务列表 / ZIP 下载
- **成本反推公式**：`cost = revenue / group_ratio × channel_vendor_map.discount`
- 5min cache tick 预计算（round-robin 模式：单次 1 个 (vendor, period)）
- handler 优先读 cache，cache miss fallback live calc

### 4. BILLING v4 利润分析

- 1 端点返汇总 + 30 天趋势 + 27 客户 + 5 供应商 + top10 模型
- 复用 v2 revenue + v3 cost 反推
- 4 tab SPA：趋势（折线）/ 客户（柱）/ 上游（饼）/ 模型（柱）

### 5. 监控中心

- `GET /api/monitor/channels` — 24h 业务请求 + 独立错误 + P95/P99 延迟
- **新口径错误率**（业务请求分母 + 独立错误分子）：
  - 分母：`type IN (2, 5, 6)` 跨 24h（排除登录/充值/管理操作）
  - 分子：`type=5 AND jsonb_array_length(use_channel)=1`（排除 retry 中间失败）
  - P95：走 `channel_health_5min` 桶 MAX（避免 RoDB percentile_cont 慢查询）
- 错误率 ≥ 20% → 渠道卡片红色发光 + 1.5s 脉动
- 飞书 webhook 告警（10+ 内置规则 + 自定义）

---

## 文档 / Documentation

| 文档 | 用途 |
|---|---|
| `README.md` / `README.en.md` | 本文件 / English version |
| [AGENTS.md](./AGENTS.md) | 项目铁律（部署 / 命名 / 字段陷阱） |
| [docs/DESIGN.md](./docs/DESIGN.md) | 21 项决策基线 + 3 数据源铁律 + 缓存表 |
| [docs/PRD-v2.md](./docs/PRD-v2.md) | 产品需求（P0-P3 + 验收标准） |
| [docs/DATA-SOURCES.md](./docs/DATA-SOURCES.md) | handler × 数据源对照表（30 活跃端点） |
| `docs/BILLING-v{2,3,4}-RFC.md` / `-RULES.md` | 客户/上游/利润 RFC + 业务规则 |
| [docs/SYNC-ARCHITECTURE.md](./docs/SYNC-ARCHITECTURE.md) | 数据流图 |
| [docs/CHANGELOG.md](./docs/CHANGELOG.md) | 变更历史 |
| [docs/test-reports/](./docs/test-reports/) | 17 份部署测试报告（按 commit hash 归档） |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | 贡献指南（中英） |
| [LICENSE](./LICENSE) | MIT |

---

## 开发约定 / Dev conventions

- **Go 代码风格**：跟 `internal/` 看，GORM + Gin + zap-style structured log
- **3 数据源铁律**：新端点必须从 API / RoDB / cache_* 三选一，mock 影子表 → `archive/`
- **业务规则优先**：任何 PR 改 SQL 前查 `information_schema.columns`，不改锁定的成本公式
- **前端**：React 18 + Antd 5 + ECharts 5，**JSX `{xxx}` 永远求值**（描述文字用 `<task_id>` 或 `$(task_id)`）
- **commit**：feat(web/v4) / fix(billing/v3) / docs(P1) / refactor(api) 格式
- **PR 流程**：先 RFC → 再 PR → maintainer review → squash merge

详见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

---

## 隐私铁律 / Privacy

> **永远不要**在 issue / PR / 截图 / 文档里贴：
> - 真实的 token / API key / SSH 密码 / 数据库密码
> - 真实的 ECS IP / RDS host / 内网域名
> - 真实的客户名 / 渠道名 / 模型名 / 业务数字
> 
> 脱敏后用占位符：`REPLACE_WITH_*` / `xxx.example.com` / `provider_alpha` / `user_alpha` 等。

详见 [AGENTS.md §隐私铁律](./AGENTS.md)。

---

## License

MIT — see [LICENSE](./LICENSE).

---

## 致谢 / Acknowledgments

- 灵感来自 [upstream 项目](https://github.com/songquanpeng/one-api) 和 LLM API 代理生态
- 核心贡献者见 [CONTRIBUTING.md](./CONTRIBUTING.md#致谢--acknowledgments)
