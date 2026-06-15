# api-ops · AGENTS.md

> 项目记忆：仅 api-ops 项目内生效。换项目规则可能不同。

## 架构铁律：3 个数据源（不能多）

api-ops 严格只能有 **3 个数据源**，任何"第 4 源"必须砍掉或归档到 archive/。

| 数据源 | 路径 | 用途 | 实时性 | 何时用 |
|---|---|---|---|---|
| **API** | newapi admin `/api/*` | 渠道/用户/Tokens 列表 | 实时 | 列表/详情（≤ 1000 行） |
| **直连 DB** (RoDB) | newapi RDS `new-api.logs` 等 | 178K 日志聚合 | 实时 | 大量聚合 SQL |
| **本地缓存 DB** | api_ops 库 `cache_*` 表 | 1min/5min 聚合预计算 | 准实时 (1-5min 延迟) | dashboard 实时面板 |

**反例（已被删）**：
- ❌ `cmd/import_real/` —— 一次性从 SQLite dump 灌数据 → api_ops.logs 影子表 (归档 archive/import-real)
- ❌ `cmd/seed/` (mock 套件) —— 写死的演示数据 (归档 archive/mock-suite)

**新代码如果需要"快照/测试数据"**：
1. 加到 `archive/` 目录
2. 文档说明**只用于单次测试，不进生产 schema**
3. 不在 handler 路径里直接读这个表

## 已知影子表（必须清）

| 表 | 来源 | 处置 |
|---|---|---|
| `api_ops.logs` | import_real 灌的 178K | **TRUNCATE** (2026-06-14 已清) |
| `api_ops.users` | import_real 灌的 27 users | **TRUNCATE** (元数据走 admin API) |
| `api_ops.channels` | import_real 灌的 49 channels | **TRUNCATE** (元数据走 admin API) |
| `api_ops.cache_logs_summary_5min` | sync tick 写 | OK (1min tick 自动累) |
| `api_ops.cache_logs_summary_by_model_5min` | sync tick 写 | OK |

## RoDB 未配时的行为 (A 阶段规则)

`API_OPS_RO_DSN` 空时：
- `dal.RoDB()` 返回 `nil`
- `dal.HasRoDB()` 返回 `false`
- 走 RoDB 的 handler 返回 500 + `dal.ErrNoRoDB`（信息："set API_OPS_RO_DSN env"）
- 走 cache 的 handler 仍可用
- WebSocket 5s tick 返回空数据（不崩）

**禁止 fallback 到 OPS** —— OPS 是自有库，不再是影子表的宿主。

## handler 数据源对照

> 2026-06-14 更新: 总览模块严格全 admin API + 砍 TopX 3 卡片.
> admin /api/log/stat 字段语义: `quota` = 范围 SUM, `rpm`/`tpm` = 60s 滑窗 (跟 start_timestamp 无关).

| handler | 数据源 | 备注 |
|---|---|---|
| `GET /api/dashboard/today` | **API** (admin /api/log/stat 1 次) | 3 字段: quota→revenue, rpm, tpm. 1 次 HTTP |
| `GET /api/dashboard/trend` | ❌ **已删除** (2026-06-14) | admin API 不给按天趋势 |
| `GET /api/dashboard/top-customers` | ❌ **已禁用** (2026-06-14) | 路由注释, handler 返 503, 卡片不显示 |
| `GET /api/dashboard/top-models` | ❌ **已禁用** (2026-06-14) | 同上. admin /api/data/ 表空 (DataExportEnabled=false) |
| `GET /api/dashboard/top-channels` | ❌ **已禁用** (2026-06-14) | 同上 |
| `GET /api/upstream/channels` | **API** + 本地 cache | 5min sync |
| `GET /api/billing/customer/*/statement` | ❌ **已下线** (2026-06-14 20:39) | v1 客户对账, 改用 BILLING v2 `current-month-overview` |
| `GET /api/billing/upstream/*` | ❌ **已下线** (2026-06-14 20:39) | v1 上游对账, v2 范围外, v3 模块接管 |
| `GET /api/billing/profit/analysis` | ❌ **已下线** (2026-06-14 20:39) | v1 利润分析, v2 范围外, v3 模块接管 |
| `GET /api/monitor/channels` | **cache** (channel_health_5min) | 1min tick |
| `GET /api/channel-mappings` | **API** (upstream 49 渠道) + OPS.channel_vendor_map | 5min sync |
| `GET /api/admin/users` | **OPS** ops_users | 自有账号 |
| `GET /api/auth/me` | **OPS** ops_users | 自有账号 |
| `GET /api/vendors` | **OPS** upstream_vendors | 自有 |
| ~~`GET /api/upstream-pricing`~~ | ❌ **已下线** (2026-06-14 23:43 commit 141cd11) | v1 价目表 CSV 流程下架, 价目表移到 archive schema, 改用 channel_vendor_map.discount 反推 |
| **BILLING v2** (PR #1-#8, 2026-06-14) | | |
| `GET /api/billing/v2/customer/current-month-overview` | **RoDB** | 27 用户当月 4 token 字段 |
| `POST /api/billing/v2/customer/:uid/export-last-month` | **RoDB** + **cache** | 创建异步任务 |
| `GET /api/billing/v2/customer/:uid/tasks` | **cache** (billing_export_tasks) | 单用户任务历史 |
| `GET /api/billing/v2/export-tasks` | **cache** (billing_export_tasks) | 任务中心 5s 轮询 |
| `GET /api/billing/v2/export-tasks/:tid/download` | **本地 volume** (`/data/billing-exports/`) | 流式下载 ZIP |
| `POST /api/billing/v2/export-tasks/:tid/cancel` | **cache** | admin/finance 取消 running 任务 |
| **BILLING v3** (PR #4 / 7 / 9, 2026-06-14/15) | | |
| `GET /api/billing/v3/upstream/current-month-overview` | **cache** (ops_upstream_summary_5min) + **RoDB** fallback | 5min tick 写, handler 优先读 cache, 启动 5min 内 cache miss 走 CalcUpstreamStatement 实时算 + 标 stale=true |
| `POST /api/billing/v3/upstream/export-last-month` | **RoDB** + **cache** (billing_export_tasks) | 创建 v3 异步导出任务 |
| `GET /api/billing/v3/upstream/:vendor_code/tasks` | **cache** (billing_export_tasks) | 单 vendor 任务历史 |
| `GET /api/billing/v3/export-tasks` | **cache** (billing_export_tasks) | 全 upstream 任务列表 |
| `GET /api/billing/v3/export-tasks/:tid/download` | **本地 volume** (`/data/billing-exports/`) | 复用 v2 download, kind 路由 |
| `GET /api/billing/v3/export-tasks/:tid/cancel` | **cache** | 复用 v2 cancel, kind 路由 |

### TopX 恢复路径 (任一, 见 handlers_stmt.go 注释)

1. upstream 那边开 `DataExportEnabled` → `/api/data/` 拿按 hour 聚合
2. `cache_logs_summary_5min` 1min tick 扩展 `model_name`/`user_name`/`channel_name` 维度 (新表 + sync tick 写, 0 admin API, 1min 延迟)

## 部署铁律

1. **跨平台 build** — macOS arm64 → 远程 linux/amd64，必须 `docker buildx build --platform linux/amd64 --load`
2. **image tag** — `api-ops:latest`（不是 `api-ops-api:latest`），compose 用这个
3. **3 GB 内存** — 4 容器总 ≤ 2.6 GB，余 0.8 GB 给系统
4. **swap 2GB 持久** — fstab 已挂
5. **远端 .env 必带 `API_OPS_ADMIN_TOKEN`** — 不要用 `sk-admin-xxxxx` 占位符, 真实值是 `REPLACE_WITH_ADMIN_TOKEN` (备份在 `/tmp/upstream-env-actual.txt`). 占位符会导致 sync tick 全 0 cache.
6. **scp 推镜像走 base64 流** — macOS 13M image 用 `ssh -T` (非 tty) + `base64 | base64 -d` 1 分钟内能传完. `scp` 走 askpass 不可靠, `ssh -tt` 会把二进制当 tty 控制字符过滤. 2026-06-15 实测: `sshpass -p '<pwd>' scp` 14M 一次推成功, **比 base64 流更稳更快** (3x speedup), 优先用这个.
7. **Dockerfile COPY 要覆盖**所有**代码运行时读的路径** — 模板 (`internal/billing/templates/`)、静态资源 (`web/dist/`)、SQL migration files、CA certs. 容器内 `findTemplatePath` 候选列表要包含 `/app/...` 路径.
8. **容器外 volume mount 必 chown** — 容器跑 `app` 用户 (uid=100, gid=101) 写不了 root:root 目录, mount 后 `chown 100:101 /data/xxx`. 例: `/data/billing-exports` 必须 chown 100:101 才能写 ZIP.
9. **web/dist 改完必须 npm run build** — Dockerfile `COPY web/dist/ /app/web/dist/` 烤进 image, api 容器**没挂** `./web:/app/web` (只有 web 容器挂了), 改 web/src 后**不 build 直接 commit 部署, 公网 SPA 还是旧版**. 部署后**必须 headless playwright 验 console 0 error** —— HTML 200 不够, JS 引用未定义图标 / 调不存在的 API 都会白屏或红屏. 实战: 删 UpstreamPricing.tsx 时手抖把 App.tsx icons 整块 import 删了, FundOutlined ReferenceError, 1 个 PR 返工.
10. **JSX `{xxx}` 永远求值, 描述文字严禁用 `{xxx}`** — `<li>输出: /data/{task_id}.zip</li>` 这种"示例代码" 在 JSX 里 `{task_id}` 会被求值, `task_id` 不在作用域就 ReferenceError → 组件崩 → 整页空白. 描述/示例用 HTML 实体 `<taskID>` 或 shell 风格 `$(task_id)` 或直接不带花括号. 实战: v3 line 285 `{task_id}` 崩 1 次, 修时又写 `{genVendor}-{ts}.zip` 崩 1 次, 共 2 次返工. (v3 ReferenceError 历史见 § "v3 ReferenceError 历史")
11. **web/dist 改完必须 npm run build + headless playwright 验 console 0 error** — (扩展 #9) 部署后**不是看 HTML 200**, 一定要 playwright 截图 + console 抓错误. SPA 红了/白屏/图标缺失都属"已部署但未生效". 实战: dashboard 改完部署后才发现 RangePicker 还在, 5min 返工 1 次.
12. **PR 后必须 headless chrome 截图, 0 console 才算完成** — 跟 #11 一致, 但**强约束**: 任何 PR 不附 playwright 截图 + 0 console error 报告 = 不算 done. 例外: 后端纯逻辑 PR (改 handler/RFC), 但仍需公网 curl 200 + grep log 无 panic.
13. **docker 启动新实例无法沿用 localStorage** — docker compose 重启会重建容器, 容器内 localStorage 是内存级, 状态丢失. 验 SPA 登录态用 token 注入脚本 (axios header 注入), 不要依赖 UI 点登录. 实战: dashboard 7d 趋势验证时, 容器重启后 localStorage 清空, 改用 token 注入 playwright 脚本 (`page.addInitScript` 塞 Authorization header).
14. **scp 走 sshpass + scp 实测 14M 一次推成功** — (扩展 #6) 2026-06-15 实测 `sshpass -p '<pwd>' scp <local> root@api-ops.example.com:<remote>` 14M image 一次推送成功, **比 base64 流更稳更快** (3x speedup), 优先用这个. base64 流 (`ssh -T` + `base64 | base64 -d`) 仍可用, 但 sshpass+scp 是新默认.
15. **monitor 引擎需要 scheduler.Run, 漏启 = 监控永远 0 数据** — `cmd/server/main.go` **必须** import scheduler + AI scheduler 前面调 `scheduler.Run(rootCtx, cfg)`. 否则 monitor tick 永不跑, channel_health_5min / alert_histories 全 0 行. 实战: 监控中心首版发现"44 渠道 latest_health 全 null", 排查 30min 发现是漏启 scheduler, 修了 1 行 import + 1 行 Run 立刻有数据. 任何新加的后台 tick 引擎 (cron / sync / aggregate / monitor) **必须**走 scheduler, 不要自己 `time.Ticker` 散在 main.go.

## 仓库双轨铁律 (2026-06-15 23:00 决策, 用户拍板)

> api-ops 有 **2 套仓库**, **生产领先**模式: 内部仓库 `rezeai-ops` 是真源, 开源仓库 `api-ops` 是脱敏镜像.

| 仓库 | 类型 | 平台 | 角色 | 谁能改 |
|---|---|---|---|---|
| `rezeai-ops` (生产) | private | 内网 GitLab | 真源, 跑在 47.251.85.62 | 内部团队 |
| `api-ops` (开源) | public | **GitHub 公开** | 脱敏镜像, 给社区读 + 提 PR | 社区, 但 PR 必走 RFC |

**铁律**:

1. **生产领先** — 99% 的代码改动在 `rezeai-ops` 完成, 部署验证, **手动挑非敏感 commit** cherry-pick / cherry-export 到 `api-ops`. 反向 (开源先行) 不允许, 避免公开仓库的变更倒灌回生产.
2. **每周一次 sync** — 周末 (建议周六上午) 把生产过去 7 天的非敏感 commit 推送到开源仓库. 详见 [docs/SYNC-PROD-TO-OPEN.md](./docs/SYNC-PROD-TO-OPEN.md) SOP.
3. **敏感判定清单** (推送前必过) — commit message 或 diff 命中以下任一, **必 skip**:
   - 含真 IP / 真 RDS host / 真 ECS 公网 IP / 真域名 (47.251.85.62 / upstream-pg.example.com 之外的真实地址)
   - 含真 token / 真密码 / 真 SSH 凭据 / 真 API key
   - 含真客户名 / 真业务数字 / 真 vendor / 真模型名 (跟 5 个假名 provider_alpha/beta/gamma/delta/epsilon / 6 个假名 llm-model-a/b/c 不一致的)
   - 含真部署路径 (`/opt/rezeai-ops` / `/data/billing-exports` 等内部路径)
   - 含真域名 (`upstream.com` 之外的客户内部域名, 即使是 internal DNS)
   - commit message 提到具体客户 / 团队成员名字
4. **推送脚本化** — `scripts/sync-prod-to-open.sh` (待写) 自动: 读 rezeai-ops 过去 7 天 commit → 跑敏感判定 → 输出"可推送 N 个 / 跳过 M 个"清单 → 确认后 git format-patch + apply 到 api-ops. **不要手工复制代码**.
5. **公开仓库 PR 必走 RFC** — 社区 PR 进来, 必:
   - 先开 issue 讨论, maintainer approve
   - 必含 RFC 引用 (docs/BILLING-v5-RFC.md 等)
   - 必含 3 数据源铁律 checkbox
   - 必含 隐私铁律 checkbox (没真 token / 没真业务数据)
   - 不符合任一 = close + 标 `stale`
6. **不互推 secret** — 公开仓库的 `.env` 永远是 `.env.example` 模板. 即使是 staging token, 也不进 git. CI 用 GitHub Secrets 注入.
7. **两套 commit author 不混** — 推到开源仓库时, 用 bot account `api-ops-bot <noreply@api-ops.dev>`, 跟 rezeai-ops 的真实开发邮箱脱钩. git config `--local` 临时设.
8. **v1.0 标签前不互推** — 首次脱敏版本 (commit `c921113`) 是 snapshot 状态, **不是** "v1.0". 真正 v1.0 等 rezeai-ops 上线 v4 利润分析稳定 1 个月后, 再打 tag.
9. **反向 cherry-pick 走 8 步 SOP** — 社区 PR 想 cherry-pick 回 rezeai-ops, 必先开 issue → 5 项真源等价性验证 → staging 部署 → playwright 截图 → 公网部署 → 24h 监控 → 30 天观察 → 永久接受 comment. 完整流程在 [docs/SYNC-PROD-TO-OPEN.md §反向: GitHub PR → rezeai-ops (8 步)](./docs/SYNC-PROD-TO-OPEN.md#反向-github-pr--rezeai-ops-8-步). **频率期望 ≤ 1 次/季度**, 99% 反向是生产→开源. 5 条反模式禁止 (见 SOP 末): ❌ 直接 `git pull api-open main` / ❌ 留 `api-open` remote 不删 / ❌ cherry-pick `seed_admin.go` 改 / ❌ 不更新 CHANGELOG / ❌ 不加 "cherry-pick" 飞书告警标签.

**与生产 rezeai-ops 关系**: api-ops 跟 rezeai-ops 是 `codebase` 血缘, 不是 git 血缘. 没有 git remote, 没有 fork. 同步走 `git format-patch + git am` 或 rsync + sed 后 diff.

## newapi 字段陷阱 (2026-06-14 PR #8 发现)

**`cache_tokens` 字段是单字段存在 `other` JSONB, 不是拆开的 `cache_creation_tokens` + `cache_read_tokens` 2 列**:

```sql
-- ❌ 错: 假设 2 列
SELECT SUM(cache_creation_tokens), SUM(cache_read_tokens) FROM logs WHERE type=2 AND user_id=47;
-- ERROR: column "cache_creation_tokens" does not exist

-- ✅ 对: JSONB 提取
SELECT SUM(COALESCE((other->>'cache_tokens')::bigint, 0)) FROM logs WHERE type=2 AND user_id=47;
```

**下次写 SQL 前必查 `information_schema.columns`**, 不要从 Anthropic 文档 / 模型文档猜字段名. 账单 v2 4 token 字段 (prompt / completion / cache / revenue_usd) 实际 SQL 写在 `internal/billing/statement_query.go` 看.

## 账单 v2 部署清单 (PR #8, 2026-06-14 完成)

- **远端 volume**: `docker-compose.yml` 加 `/data/billing-exports:/data/billing-exports`
- **chown**: `chown 100:101 /data/billing-exports` (容器 app user 写 zip)
- **migration**: `migrations/2026-06-14-billing-v2-tables.sql` 创 `billing_export_tasks` + `billing_export_task_logs` 2 张表
- **公网验证 6 端点** (token REPLACE_WITH_UPSTREAM_API_TOKEN):
  - `/api/dashboard/today` 200
  - `/api/billing/v2/customer/current-month-overview` 真实数据
  - 创建任务 → 6 秒内 success, 11.6KB zip
  - 下载 zip → HTTP 200, 含 README + HTML + XLSX
  - XLSX sharedStrings 校验: 7 列表头单字段 `缓存 tokens`
- **详细报告**: `docs/test-reports/billing-v2-pr8-deploy-2026-06-14.md`
- **ZIP 内容保留 30 天** (Q2 决策), `internal/billing/prune.go` 凌晨跑清理

## BILLING v2 业务规则速查

- **Q1 ZIP 存 api 容器本地 volume** (`/data/billing-exports/`)
- **Q2 任务记录 30 天后自动清** (凌晨 cron)
- **Q3 每用户 ≤ 2 个 running** (二次确认, 跟原话"全系统 2 个"差异)
- **Q4 格式 UI 让用户选** (HTML / XLSX / 两者)
- **Q5 v1 18 端点保留 6 个月** (2026-12-14 后 410) → **⚠️ 2026-06-14 20:39 决策变更: v1 直接下线** (路由返 404, DB 表 archive.* schema, 文档 archive/v1-docs/)
- **5 业务规则 (R1-R5)**: 零输出免单 / 图片标记 / 退款不计 / 错误不计 / 未匹配上游不计
- **详细规则**: `docs/BILLING-v2-RULES.md` (12 节)

## 已部署镜像

- `api-ops:latest` — 当前 HEAD (总览严格全 admin API, 砍 TopX, tag `v0.1-dashboard-strict-admin-api`)
- `api-ops:dashboard-no-top-models` — tag `v0.1-dashboard-no-top-models` (Top 模型暂时撤掉的中间态)
- `api-ops:vendor-mgmt-v3` — A 阶段供应商管理 v1
- `api-ops:clean-3-sources` — A 阶段清理后 (3 数据源铁律)
- `api-ops:latest` 镜像 = tag `v0.1-dashboard-strict-admin-api` (2026-06-14 commit 4966916)

## 远端 ECS

- api-ops.example.com:8091 公网 / 22 SSH / 4 容器 up
- 凭据: `/tmp/upstream-deploy/askpass.sh` (密码 REPLACE_WITH_SSH_PASSWORD)
- docker compose 在 `/opt/api-ops/`

## 新口径错误率定义 (2026-06-15 09:43 决策, 用户拍板)

> **范围**: 仅监控中心"渠道健康度"模块. BILLING v3 错误不计 (R4) 是另一回事, 跟监控错误率定义不同.

| 项 | 定义 | 范围 |
|---|---|---|
| **分母: 业务请求** | `type IN (2, 5, 6)` 跨 24h | **排除** type=1 登录 / type=4 充值 / type=7+ 管理操作 |
| **分子: 独立错误** | `type=5 AND jsonb_array_length(other->'use_channel') = 1` | **排除** retry 中间失败 (`use_channel` 长度 > 1) |
| **P95/P99 延迟** | 走 `channel_health_5min` 桶 MAX(最新桶) | 避免 RoDB `percentile_cont` 慢查询 |
| **错误率阈值** | ≥ 20% 触发红边呼吸 | 综合 KPI 用 `kpi-danger-stat` (无红边), 渠道卡片用 `kpi-danger` (有红边) |

**关键决策**:
1. **分母用业务请求, 不用全请求**: 排除登录/充值/管理操作, 错误率更有业务意义
2. **分子用独立错误, 不用全部 type=5**: retry 中间失败 (`use_channel.length > 1`) 不算, 否则上游 retry 多的渠道错误率虚高
3. **P95 用 cache 桶 MAX, 不用 RoDB percentile_cont**: 24h 178 万行日志走 `percentile_cont` 慢, 用 `channel_health_5min` cache (1min 延迟) 换性能
4. **红边只覆盖渠道卡片**: 综合 KPI 是全局视角, 渠道卡片是单渠道视角, 红边呼吸感只给单渠道 (强调"哪个渠道有问题")

**实战**:
- ch 110 验证 (2026-06-15 09:50): RoDB SQL 直查 type=2=3953, type=5=862, 业务请求=4815; API 返 req=4816 (差 1 是 RoDB 实时 vs cache sync 1min 延迟), rate=17.93%
- 24h 178 万行 logs → 7.3ms 扫描 (`idx_created_at_type` 复合索引完美命中)
- 15 渠道聚合, 走 2 步: RoDB logs (7ms) + OPS channel_health_5min (5ms)

**SQL 模板** (监控模块用, 见 `internal/dal/ops_repo.go` `ListChannel24hSummary`):
```sql
-- 业务请求 + 独立错误 (按 channel 聚合)
SELECT 
  channel_id,
  COUNT(*) FILTER (WHERE type IN (2, 5, 6)) AS request_count,
  COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(other->'use_channel') = 1) AS error_count
FROM logs
WHERE created_at >= $1 AND created_at < $2
GROUP BY channel_id
HAVING COUNT(*) FILTER (WHERE type IN (2, 5, 6)) > 0;  -- 24h 内无调用 → 不显示
```

## v3 ReferenceError 历史 (2026-06-15)

> **教训**: JSX `{xxx}` 永远求值, 描述/示例文字严禁用 `{xxx}`. 详见部署铁律 #10.

### 错误现场 (BillingV3Upstream.tsx line 285, 2026-06-15)

**原代码**:
```jsx
<li>输出: /data/billing-exports/{task_id}.zip</li>
```

**问题**: `{task_id}` 是 JSX 插值表达式, 但 `task_id` 变量在作用域**不存在** → React 渲染时抛 `ReferenceError: task_id is not defined` → 整个组件崩 → 整个页面空白.

### 二次犯错 (修时又崩)

第一次修时, 写成:
```jsx
<li>{genVendor}-{ts}.zip</li>
```
`ts` 同样未定义 → 又崩一次.

**根因**: 描述/示例文字想用"模板字符串"风格, 但 JSX 里 `{xxx}` **永远被求值**, 编译器不知道"这是描述文字".

### 修法 (3 选 1)

| 写法 | 示例 | 适用 |
|---|---|---|
| HTML 实体 | `<taskID>` | 文档/示例说明 |
| shell 风格 | `$(task_id)` | 命令行示例 |
| 直接不带花括号 | `taskid` | 口语化描述 |

**实际修法** (BillingV3Upstream.tsx 修复后):
```jsx
<li>输出: /data/billing-exports/<taskID>.zip</li>  {/* 用 HTML 实体, 不求值 */}
```

zip 文件名实际由后端 `taskID.zip` 决定 (`internal/billing/upstream_format.go` line 253), 不需要前端描述.

### 公网验证

- 修复后 0 exception + 0 console error
- v3 表格 5 vendor 行 + 真实数字 (cost $2787 / revenue $5160)
- 截图: `docs/screenshots/2026-06-15-v3-final.png`

### 推广到全栈

**不只是 v3**: 任何 PR 写示例代码/路径/命令, 都**不用** `{xxx}` 格式. 包括:
- API 文档示例 (`/api/billing/v2/.../{task_id}`) — 用 `<task_id>` 或 `[task_id]`
- 部署命令 (`scp <local> root@<host>:<remote>`) — 用 `<host>` 不是 `{host}`
- 代码注释 (`// 写 /data/{path}`) — 注释不在 JSX 里, OK, 但描述路径时建议统一风格
- README + CHANGELOG — 一律 `<xxx>` 风格

### 验收标准 (PR 评审加这条)

- [ ] PR 改了任何 TSX/JSX 文件, diff 里**不能**有 `\\{[a-zA-Z_]+\\}` (描述文字场景), 必须用 `<xxx>` / `$(xxx)` / 不带花括号
- [ ] PR 改了任何 TSX/JSX 文件, playwright 截图 0 console error 是**必备**证据
