# Changelog

## [Sync 2026-07-09] from rezeai-ops v0.5.0

手动同步本周 rezeai-ops → api-ops 的非敏感 commit (按 7 步 SOP):

### 客户健康度模块 (新模块, 7 PR 合并为 1 批)

- **PR-A**: migration `2026-07-08-customer-health-export-kind.sql` (kind enum 加 customer_health)
- **PR-B**: 5 endpoints + HTML 详情生成 + 异步 worker
- **PR-C**: 前端 1 页面 (CustomerHealth.tsx) + 5 API 封装
- **PR-D**: AGENTS.md §客户健康度模块 文档
- **PR-E**: 9 test / 42 case (单测全过)
- **PR-F**: 部署 bugfix (Dockerfile mkdir /data/customer-health-exports)
- **PR-G**: 多站 detail 跨站 bug (cn 站 detail 拿到 intl 数据, 加 QueryLogsOnDB 显式传 db)

### 脱敏降级

api-ops 单站架构 (无 Site 字段, 无 site_middleware, 无 SiteSwitcher), 跟 rezeai-ops 多站版的差异:

- 删 `internal/api/site_middleware.go` 引用
- `BillingExportTask.Site` 字段保留 (可选, 默认 "intl"), 后续多站接入时启用
- `cache_logs_summary_by_user_5min` 脱敏版 (无 Site 字段, UNIQUE 改 (bucket_ts, user_id))
- cn 跳板 (101.201.239.157 / 15432) 不需要 → 不进 api-ops
- handlers 永远 "intl" 硬编码, 走默认 RoDB

### 验证

- `go build ./cmd/server/` ✅
- `go test ./internal/...` ✅ (容器内跑)
- `npm run build` ✅
- 7 项敏感判定清单全过

### 跳过 (无)

本次 0 个 commit 命中敏感判定, 全部推送.

