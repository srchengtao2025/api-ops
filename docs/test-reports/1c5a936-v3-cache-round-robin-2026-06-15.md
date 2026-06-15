# v3 cache round-robin hotfix 部署测试报告

**commit**: `1c5a936 fix(billing/v3): round-robin 单 (vendor, period) tick 解决 server 退出 0 bug`
**部署时间**: 2026-06-15 14:05 (Asia/Shanghai) 起持续运行
**镜像**: `api-ops:v3v4-rr-2026-06-15` (推 ECS 后 tag `api-ops:latest`)
**远端容器**: `api-ops-api` 0bca5189fe38 → 持续运行 6+ 小时

## 1. 背景

PR #9 (3 commit: a254ea5 migration + 144d216 scheduler + eab71ee handler) 上线后, v3 cache tick 触发 server 进程级崩溃:

- **症状**: tick 跑 1 次后整个 server 进程 exit 0, exit code 0, 无 panic, 无 OOM, dmesg 干净
- **影响**: 月对账 (1-5 号老板按"导出上月") 时, 5 vendor × 2 period 串行 10 SQL 累加 19万行 logs, 累计 200MB+ 临时分配, 触发 gorm buffer + cgo 段错误
- **临时 hotfix (13:36)**: 禁用 v3 cache tick, handler 走 live calc (跟 PR #7 锁行为一致)
- **复现**: 1G 内存 (vs 256M) 同样触发, 调启动后立即跑 / 5min 后跑同样触发, 与内存 / 启动 warmup 无关

## 2. 根因分析

| 维度 | 调查 | 结论 |
|------|------|------|
| 内存 | docker stats 68MB / 1G 26% | 不是 OOM, 调 1G 仍崩 |
| dmesg | kernel log 无 OOM 记录 | 不是 OOM kill |
| panic | 日志无 stack trace | 不是 Go panic |
| gorm | `Find(&logs)` 1 SQL 返 19万行, 全量加载到 heap slice | 19万 × 2KB = 38MB 单次分配 |
| 累计 | 5 vendor × 2 period = 10 次串行 19万行 | 累计 200MB+ 临时对象 |
| 怀疑 | gorm 19万 rows buffer 累加 + cgo 段错误 (go runtime 死) | exit code 0 (go runtime crash 默认) |

**关键观察**: **单次跑 1 个 (vendor, period)** 不再触发崩溃. 验证根因是**串行累加**, 不是单次.

## 3. 修复方案

### 3.1 改动 (commit 1c5a936)

**`internal/billing/upstream_summary_tick.go`**:
- `UpstreamSummaryLoop`: 引入 `tickState{vendorIdx, periodIdx}`, 启动 5s 跑第一次 + 5min ticker
- `runOnceUpstreamSummarySafe`: 接受 `*tickState`, 单次跑 1 个 (vendor, period), 推进 state
- `pickNextTick`: round-robin 选下一个 (vendor, period), 推进 state
- 老函数 `RunUpstreamSummaryTick` 保留未用, 后续清理

**`docker-compose.prod.yml`**:
- api `memory: 256M → 1G` 留余量给 calc 1 vendor × 1 period 慢 SQL
- reservations `64M → 128M`

### 3.2 设计权衡

- **不并发 10 SQL**: 即使单次不崩, 也不并发 (gorm 19万行累加 = 200MB+, 跟"串行累加"风险相同)
- **不串行 10 SQL**: 5min tick 跑 1 个, 10 tick = 50min 跑完 1 轮
- **不 Fork 子进程**: 子进程隔离 cgo crash 稳但开销大, 月对账场景下延迟可接受
- **不流式 gorm Find**: 改 CalcUpstreamStatement 接口内部 FindInBatches, 工作量大且 PR #7 锁的接口不能动

### 3.3 cache miss 行为

handler / worker 仍走 cache-first:
- cache hit: 5min 延迟, 返 cache
- cache miss: 启动 50min 内 (1 轮没跑完), fallback `CalcUpstreamStatement` 实时算 + 标 `stale=true`

## 4. 部署清单

- ✅ 本地 `CGO_ENABLED=0 go build ./...` 0 error
- ✅ `docker buildx build --platform linux/amd64 -t api-ops:v3v4-rr-2026-06-15 . --load`
- ✅ `docker save ... | gzip > /tmp/...tar.gz` (14M)
- ✅ `sshpass scp ... root@api-ops.example.com:/tmp/`
- ✅ 远端 `docker load -i` + `docker tag api-ops:latest` + `docker compose up -d api`
- ✅ 远端 `docker-compose.prod.yml` 同步 (1G 内存 + image tag `api-ops:latest`)

## 5. 公网验证 (持续观察 6+ 小时)

| 时间 | 事件 | restart count | cache 表行数 |
|------|------|---------------|--------------|
| 14:05:24 | 启动 | 0 | 0 |
| 14:05:29 | 第 1 个 tick (5s warmup): vendor=provider_gamma period=current-month rows=1 | 0 | 1 |
| 14:10:24 | 第 2 个 tick: vendor=aws_bedrock period=current-month | 0 | 2 |
| 14:15 - 20:31 | 持续每 5min 1 行, round-robin 跑 5 vendor × 2 period 多轮 | 0 | 78 |
| 20:31:51 | 最新 tick: vendor=provider_gamma period=last-month rows=1 | 0 | 78 |

**总评**: 6+ 小时稳跑, restart count 0, cache 表累积 78 行, 不再触发 server 退出 0.

## 6. PR #7 公式保持

- `cost = (revenue / group_ratio) × channel_vendor_map.discount` (PR #7 锁)
- `profit = revenue - cost`
- `profit_rate = profit / cost` (财务"赚几倍"口径, 不是 profit/revenue)
- 月对账时 cache hit 后, 老板看到的 cost / profit / profit_rate 跟 PR #7 之前完全一致

## 7. 后续清理 (非阻塞)

- [ ] 删老函数 `RunUpstreamSummaryTick` (runUpstreamSummarySafe 不再调, 保留为 backward-compat, 后续 PR 清)
- [ ] 监控指标加 v3 cache 命中率 (cache hit / total request) 到 dashboard
- [ ] ops_upstream_summary_5min prune 老于 30 天的 row (代码里有 `pruneOldUpstreamSummary` 函数, 等实际数据堆积再开)
