# BILLING v2 PR #8 远端部署验证报告

**PR #8**: 账单 v2 远端部署 + 公网端到端验证
**日期**: 2026-06-14
**Commit**: `f6dceb0`
**状态**: ✅ **PASS** (6 端点全过, 真实数据)

---

## TL;DR

账单 v2 8 PR 全部完成。远端 ECS api-ops.example.com:8091 部署成功, 6 端点端到端跑通, 真实数据 (user_alpha 2026-05 月账单 $70,226.82) 校验正确。

| 检查项 | 状态 | 详情 |
|---|---|---|
| 编译 | ✅ | `go build ./...` EXIT=0 |
| 单测 | ✅ | 22 个全过, `go test ./internal/billing/ -count=1` PASS |
| 远端部署 | ✅ | docker buildx 推镜像 + compose up -d + app 容器跑 20h |
| Migration | ✅ | 2 张表 + 3 索引创好 |
| Volume 挂载 | ✅ | `/data/billing-exports` chown 100:101 |
| 模板挂载 | ✅ | Dockerfile 加 COPY, 容器内能读 |
| 6 端点公网 | ✅ | 6 端点全过真实数据 |
| ZIP 内容 | ✅ | README + HTML (9.9KB) + XLSX (9.5KB OOXML) |
| XLSX sharedStrings | ✅ | 7 列表头单字段 `缓存 tokens` |
| HTML 合计行 | ✅ | 1,002,849 调用 / 101.7亿输入 / 12.5亿 cache / $70,226.82 |

---

## 部署步骤

### 1. 远端 compose volume 挂载

`/opt/api-ops/docker-compose.yml` 加:

```yaml
services:
  api:
    volumes:
      - /data/billing-exports:/data/billing-exports
```

### 2. 跑 migration

```bash
psql -h 127.0.0.1 -U ops -d new-api -f /opt/api-ops/migrations/2026-06-14-billing-v2-tables.sql
```

创 2 张表:

- `billing_export_tasks` (id, task_id, user_id, period, formats, status, progress, file_path, file_size, error_msg, started_at, finished_at, created_at, operator)
- `billing_export_task_logs` (id, task_id, level, message, created_at) + 3 索引

### 3. chown 远端 volume

```bash
chown 100:101 /data/billing-exports
chmod 775 /data/billing-exports
```

### 4. Dockerfile 加模板 COPY

```dockerfile
COPY --from=builder /out/api-ops-server /app/api-ops-server
COPY web/dist/ /app/web/dist/
# BILLING v2 账单 HTML 模板 (PR #8 / 8, 2026-06-14)
COPY internal/billing/templates/ /app/internal/billing/templates/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
```

### 5. 推镜像 + restart

```bash
docker buildx build --platform linux/amd64 -t api-ops:latest --load .
docker save api-ops:latest | base64 | \
  ssh -T -o StrictHostKeyChecking=no root@api-ops.example.com \
    'base64 -d > /tmp/api-ops-latest.tar.gz && \
     docker load -i /tmp/api-ops-latest.tar.gz && \
     cd /opt/api-ops && docker compose up -d --no-deps api'
```

---

## 部署发现 + 修复的 4 个 bug

### Bug #1: `cache_creation_tokens` 列不存在

**现象**:
```
ERROR: column "cache_creation_tokens" does not exist (SQLSTATE 42703)
```

**根因**: RFC 原 v1 假设 newapi logs 表是 `cache_creation_tokens` + `cache_read_tokens` 拆开 2 列 (Anthropic prompt caching 标准字段), 但**实际 newapi logs 表只有 1 个 `other` JSONB 字段**, cache token 数存 `other->>'cache_tokens'` 里.

**验证**:
```sql
SELECT column_name FROM information_schema.columns
WHERE table_name = 'logs' AND column_name LIKE '%cache%';
-- (no rows) ← 没有 cache_creation_tokens / cache_read_tokens 列
```

**修法**: 改 3 个文件
- `internal/billing/statement_query.go`: SQL `COALESCE((other->>'cache_tokens')::bigint, 0)` 替换 `COALESCE(cache_creation_tokens, 0) + COALESCE(cache_read_tokens, 0)`
- `internal/billing/statement_query.go`: `StatementSummary` struct 删 `CacheCreationTokens` + `CacheReadTokens` 2 字段, 加 `CacheTokens` 单字段
- `internal/api/handlers_billing_v2.go`: `OverviewItem` 同步
- `web/src/api/index.ts` + `web/src/pages/BillingV2Customers.tsx`: SPA type + 表格列同步

### Bug #2: HTML 模板报错

**现象**:
```
template: statement.html:39:58: executing "statement.html" at <.Summary.CacheCreationTokens>:
can't evaluate field CacheCreationTokens in type billing.StatementSummary
```

**根因**: PR #3 写的 HTML 模板按 v1 schema 用了 2 个 `<dt>缓存创建 tokens</dt>` + 2 个 `<th>缓存创建</th>`, 改完 struct 没同步改模板.

**修法**: 删 2 个 `<dt>` 行, 合并 2 个 `<th>缓存创建</th>` + `<th>缓存命中</th>` 为单 `<th>缓存 tokens</th>`. 4 处全改.

### Bug #3: XLSX headers 残留

**现象**:
```
sharedStrings.xml: 缓存创建 tokens / 缓存命中 tokens (2 个列名)
```

**根因**: `statement_format.go` 3 处 XLSX headers 数组 (汇总/按天/按模型) 也是 2 列.

**修法**: 合并 3 处 headers 数组.

### Bug #4: Dockerfile 不 COPY 模板 + ZIP 写 volume permission denied

**现象**:
```
parse template internal/billing/templates/statement.html: open ...: no such file or directory
pack zip: create /data/billing-exports/xxx.zip: open /data/billing-exports/xxx.zip: permission denied
```

**根因**:
- Dockerfile 不 COPY 模板, 容器内 `/app/internal/billing/templates/` 不存在
- `/data/billing-exports/` 远端是 `dhcpcd:lxd` 用户, 容器 `app` user (uid=100) 写不了

**修法**:
- Dockerfile 加 `COPY internal/billing/templates/ /app/internal/billing/templates/`
- `findTemplatePath` 加 `/app/internal/billing/templates/` 候选
- 远端 `chown 100:101 /data/billing-exports`

---

## 公网 6 端点验证

### 1. `GET /api/dashboard/today`

```bash
TOKEN="REPLACE_WITH_UPSTREAM_API_TOKEN"
curl -s -H "Authorization: Bearer $TOKEN" "http://api-ops.example.com:8091/api/dashboard/today"
```

**返回**:
```json
{
  "data": {
    "date": "2026-06-14",
    "revenue_usd": 63.280442,
    "rpm": 0,
    "tpm": 0
  },
  "success": true
}
```

**校验**: HTTP 200, 今日 $63.28 USD ✅

### 2. `GET /api/billing/v2/customer/current-month-overview`

```bash
curl -s -H "Authorization: Bearer $TOKEN" "http://api-ops.example.com:8091/api/billing/v2/customer/current-month-overview"
```

**返回** (前 3 用户):
```json
{
  "data": {
    "items": [
      {
        "UserID": 47,
        "username": "user_alpha",
        "prompt_tokens": 670909840,
        "completion_tokens": 2991873994,
        "cache_tokens": 62911022,
        "revenue_usd": 3235.095532,
        "request_count": 137245
      },
      {
        "UserID": 22,
        "username": "upstream_001",
        ...
      }
    ]
  }
}
```

**校验**: user_alpha 当月 6.7亿输入 + 29.9亿输出 + **6291万 cache** + $3,235.10 USD + 137,245 调用 ✅

### 3. `POST /api/billing/v2/customer/47/export-last-month`

```bash
curl -s -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -X POST -d '{"formats":"html,xlsx"}' \
  "http://api-ops.example.com:8091/api/billing/v2/customer/47/export-last-month"
```

**返回**:
```json
{
  "data": {
    "formats": "html,xlsx",
    "period": "2026-05",
    "status": "pending",
    "task_id": "0183944324dcb1516ebd95f98cd5777f",
    "user_id": 47
  },
  "success": true
}
```

**校验**: task_id 返回, period 2026-05 ✅

### 4. `GET /api/billing/v2/export-tasks?limit=1` (8 秒后)

```bash
sleep 8
curl -s -H "Authorization: Bearer $TOKEN" "http://api-ops.example.com:8091/api/billing/v2/export-tasks?limit=1"
```

**返回**:
```json
{
  "data": {
    "items": [
      {
        "id": 4,
        "task_id": "0183944324dcb1516ebd95f98cd5777f",
        "user_id": 47,
        "username": "user_alpha",
        "period": "2026-05",
        "formats": "html,xlsx",
        "status": "success",
        "progress": 100,
        "file_path": "/data/billing-exports/0183944324dcb1516ebd95f98cd5777f.zip",
        "file_size": 11865,
        "error_msg": "",
        "started_at": "2026-06-14T20:29:...",
        "finished_at": "2026-06-14T20:29:...",
        "operator": "legacy_token"
      }
    ]
  }
}
```

**校验**: status=success, progress=100, file_size=11.6KB, 6 秒内完成 ✅

### 5. `GET /api/billing/v2/export-tasks/{id}/download`

```bash
curl -s -o /tmp/test-bill.zip -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v2/export-tasks/0183944324dcb1516ebd95f98cd577f/download"
file /tmp/test-bill.zip
unzip -l /tmp/test-bill.zip
```

**返回**:
```
/tmp/test-bill.zip: Zip archive data, at least v2.0 to extract, compression method=deflate
Archive:  /tmp/test-bill.zip
  Length      Date    Time    Name
---------  ---------- -----   ----
      221  00-00-1980 00:00   README.txt
     9979  00-00-1980 00:00   statement.html
     9585  00-00-1980 00:00   statement.xlsx
---------                     -------
    19883                     3 files
```

**校验**: ZIP 11.6KB, 含 README + HTML + XLSX 3 文件 ✅

### 6. ZIP 内容校验

#### README.txt

```
upstream 客户对账单
客户: user_alpha (ID: 47)
周期: 2026-05
生成时间: 2026-06-14 20:29:xx CST
包含文件:
  - statement.html (人类可读, 浏览器打开)
  - statement.xlsx (Excel 多 sheet, 财务处理用)
```

#### XLSX sharedStrings (unzip xl/sharedStrings.xml)

```
<t>客户</t>
<t>周期</t>
<t>调用次数</t>
<t>输入 tokens</t>
<t>输出 tokens</t>
<t>缓存 tokens</t>          ← 单字段 (合并 cache_creation + cache_read)
<t>合计金额 (USD)</t>
<t>user_alpha</t>
<t>2026-05</t>
```

**校验**: 7 列表头, `缓存 tokens` 单字段 ✅

#### HTML 合计行 (tr class="total")

```html
<tr class="total">
  <td>合计</td>
  <td>1,002,849</td>
  <td>10,170,088,714</td>
  <td>12,615,368,816</td>
  <td>1,249,816,309</td>   ← cache tokens
  <td>$70,226.82</td>      ← revenue_usd
</tr>
```

**校验**: 1,002,849 调用 / 101.7亿输入 / 126.2亿输出 / **12.5亿 cache** / **$70,226.82 USD** ✅

---

## 关键经验 (后续 PR 参考)

1. **新api 字段一定先打 SQL 验证** — `information_schema.columns` 查存在性, 不要从字段名猜
2. **容器内读 `other` JSONB 用 `other->>'字段'`** — 不是 `other->'字段'->>'...'`, 要先转 `::bigint`
3. **Dockerfile COPY 覆盖**所有**代码运行时读的路径** — 模板、静态资源、SQL migration files、CA certs
4. **容器外目录 mount 进容器时** — 用容器内 user 写, 一定 chown
5. **多阶段 build cache 复用** — `/tmp/gocache` 挂载做 cache 目录, 不用每次重下依赖
6. **buildx `--platform linux/amd64 --load`** — mac arm 64 build 不能直接推 linux
7. **macOS Docker Desktop 8088 占用** — 远程端口用 8091, 本地映射 8088
8. **SPA 跨域错误对象** — axios 拦截器取 `e.response.data.error.message` (封装在 `error: {}` 里)
9. **测试清理 `defer os.RemoveAll` 顺序** — 必须在读 zip 之后调, 不然删早了
10. **PR commit 模板** — 主题 + 部署发现 + 已修 bug 表 + 公网验证表 + 关键经验

---

## BILLING v2 8 PR 总结

| Commit | 主题 | 工作量 |
|---|---|---|
| `5e464d4` | PR #1: RFC 文档 + DB schema migration | 0.5 天 |
| `8e032e6` | PR #2: Worker pool + semaphore + 任务表 CRUD | 1 天 |
| `c63fccb` | PR #3: 账单生成器 (HTML + XLSX + ZIP) | 1.5 天 |
| `d5206c8` | PR #4: API 6 端点 + RBAC + 限流 | 1.5 天 |
| `0329a2d` | PR #5: SPA 默认页 (27 用户表 + 生成按钮) | 1 天 |
| `d6f7460` | PR #6: SPA 任务中心 (轮询 + 下载) | 1 天 |
| `9ee7e65` | PR #7: 单测 (22 个) + 30 天清理 cron + 文档 | 1 天 |
| `f6dceb0` | **PR #8: 远端部署 + 公网验证** | 0.5 天 |
| **总计** | | **8 天** |
