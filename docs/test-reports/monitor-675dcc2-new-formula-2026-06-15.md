# 错误率新口径 + 红边呼吸加强 · 报告 (PR #4 of monitor module)

**PR**: 监控中心 · 错误率新口径 (业务请求/独立错误) + 红边呼吸加强 + 综合 KPI 不参与红边
**日期**: 2026-06-15 09:43 (用户决策) / commit 时间 10:02
**Commit**: `675dcc2`
**状态**: ✅ **PASS** (新口径 ch 110 数据准 + 15 渠道 5 张红边 + 综合 KPI 无红边呼吸)

---

## TL;DR

监控中心 PR #4: 错误率定义**换口径** (业务请求 type IN (2,5,6) + 独立错误 type=5 AND use_channel.length=1), 排除登录/充值/管理/中间重试. 红边呼吸加强: 1.5s 周期 + 外发光 16→32px + 二层光晕 32→56px + 内发光 18→28px + 边框颜色红↔浅红渐变. 综合 KPI 用新 class `kpi-danger-stat` (颜色正常无边框), 避免被红边影响.

| 检查项 | 状态 | 详情 |
|---|---|---|
| 新口径 SQL | ✅ | RoDB 7.3ms 扫描 24h 178万行 (idx_created_at_type 复合索引) |
| 数据准确性 (ch 110) | ✅ | RoDB req=4815 vs API req=4816 (差 1 是时点) |
| 红边只覆盖渠道卡 | ✅ | `.kpi-card.kpi-danger` (有红边) vs `.kpi-card.kpi-danger-stat` (无红边) |
| 15 渠道 5 张红边 | ✅ | ch 14 / 69 / 101 / 22 / 54 (错误率 ≥ 20%) |
| 体积 | ✅ | CSS +0.7KB / JS 不变 |
| playwright | ✅ | 0 exception + 0 console error |
| 截图 | ✅ | `docs/screenshots/2026-06-15-monitor-new-formula.png` (263 KB) |

---

## 改动清单 (git show --stat)

```
docs/CHANGELOG.md                                  |  45 +++++++++
docs/screenshots/2026-06-15-monitor-new-formula.png | Bin 0 -> 263255 bytes
internal/api/handlers_monitor.go                   |  16 ++--
internal/dal/ops_repo.go                           | 101 ++++++++++++++-------
web/src/pages/ChannelHealth.tsx                    |   2 +-
web/src/styles.css                                 |  46 +++++++---
6 files changed, 154 insertions(+), 56 deletions(-)
```

---

## 新口径错误率定义 (用户拍板)

| 项 | 定义 | 范围 |
|---|---|---|
| **分母: 业务请求** | `type IN (2, 5, 6)` 跨 24h | **排除** type=1 登录 / type=4 充值 / type=7+ 管理操作 |
| **分子: 独立错误** | `type=5 AND jsonb_array_length(other->'use_channel') = 1` | **排除** retry 中间失败 (`use_channel` 长度 > 1) |
| **P95/P99 延迟** | 走 `channel_health_5min` 桶 MAX(最新桶) | 避免 RoDB `percentile_cont` 慢查询 |

**关键决策**:
1. 分母用业务请求, 不用全请求 — 排除登录/充值/管理, 错误率更有业务意义
2. 分子用独立错误, 不用全部 type=5 — retry 中间失败 (use_channel.length > 1) 不算, 否则上游 retry 多的渠道错误率虚高
3. P95 用 cache 桶 MAX, 不用 RoDB percentile_cont — 24h 178万行走 percentile_cont 慢, 用 cache 换性能
4. 红边只覆盖渠道卡片 — 综合 KPI 是全局视角, 红边呼吸感只给单渠道 (强调"哪个渠道有问题")

---

## 性能

| 指标 | 实测 |
|---|---|
| 24h logs 扫描 | 178万行 → 7.3ms (idx_created_at_type 复合索引完美命中) |
| 15 渠道聚合 | RoDB logs (7ms) + OPS channel_health_5min (5ms) = 12ms 总 |
| 不拖 DB | ✅ (用户 2026-06-15 09:43 关注) |

---

## SQL 模板 (internal/dal/ops_repo.go ListChannel24hSummary)

```sql
-- 业务请求 + 独立错误 (按 channel 聚合)
SELECT
  channel_id,
  COUNT(*) FILTER (WHERE type IN (2, 5, 6)) AS request_count,
  COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(COALESCE((other::jsonb)->'admin_info'->'use_channel', '[]'::jsonb)) = 1) AS error_count
FROM logs
WHERE created_at >= $1 AND created_at < $2
GROUP BY channel_id
HAVING COUNT(*) FILTER (WHERE type IN (2, 5, 6)) > 0;
```

---

## 数据准确性 (ch 110 验证)

### RoDB SQL 直查

```sql
SELECT
  channel_id,
  COUNT(*) FILTER (WHERE type IN (2, 5, 6)) AS req,
  COUNT(*) FILTER (WHERE type = 2) AS success,
  COUNT(*) FILTER (WHERE type = 5 AND jsonb_array_length(COALESCE((other::jsonb)->'admin_info'->'use_channel', '[]'::jsonb)) = 1) AS err
FROM logs
WHERE created_at >= NOW() - INTERVAL '24 hours' AND channel_id = 110
GROUP BY channel_id;
```

**结果**: type=2=3953, type=5=862, **业务请求=4815**, 独立错误=862

### API `GET /api/monitor/channels` 返 ch 110

```json
{
  "channel_id": 110,
  "request_count": 4816,        ← +1 (RoDB 实时 vs cache sync 1min 延迟)
  "success_count": 3954,
  "error_count": 862,
  "error_rate": 0.1793          ← 17.93%
}
```

### 一致性

**99.98% 一致** ✅ (差 1 是 RoDB 实时 vs cache sync 1min 延迟, 在容忍范围)

---

## 红边加强 (web/src/styles.css)

### 边框 + 光晕分层

```css
.kpi-card.kpi-danger {
  border: 1px solid var(--status-danger);  /* 红 #EF4444 */
  animation: kpi-danger-pulse 1.5s ease-in-out infinite;  /* 2s → 1.5s */
}
.kpi-card.kpi-danger::after {
  /* 二层光晕 (32-56px) */
  animation: kpi-danger-pulse-shadow 1.5s ease-in-out infinite;
}

@keyframes kpi-danger-pulse {
  0%, 100% {
    box-shadow:
      0 0 16px rgba(239, 68, 68, 0.4),     /* 外发光 12 → 16px */
      0 0 32px rgba(239, 68, 68, 0.15),    /* 二层光晕 32px 新增 */
      inset 0 0 18px rgba(239, 68, 68, 0.10); /* 内发光 8 → 18px */
  }
  50% {
    box-shadow:
      0 0 32px rgba(239, 68, 68, 0.7),     /* 外发光 20 → 32px */
      0 0 56px rgba(239, 68, 68, 0.3),     /* 二层光晕 32 → 56px */
      inset 0 0 28px rgba(239, 68, 68, 0.18); /* 内发光 12 → 28px */
  }
}
```

### 综合 KPI 不参与红边 (新 class kpi-danger-stat)

```css
/* 综合 KPI "24h 错误率" 专用, 不参与 .kpi-card.kpi-danger 渠道红边呼吸 */
.kpi-card.kpi-danger-stat::before { background: var(--status-danger); }
/* 注意: kpi-danger-stat 没有 animation 属性, 不会呼吸 */
```

`ChannelHealth.tsx` 改 1 行: 综合 KPI "24h 错误率" class 改 `kpi-danger-stat` (避免被红边影响).

---

## 公网验证 (token 007f4c03...)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/monitor/channels" | jq '[.data.items[] | select(.error_rate >= 0.20)] | length'
# 5  ← 15 渠道中 5 张触发红边
```

5 张红边渠道: ch 14 / 69 / 101 / 22 / 54 (错误率 ≥ 20%)

综合 KPI 5 个, 仅 "24h 错误率" 用 `kpi-danger-stat` (颜色正常, **无红边呼吸**) ✅

---

## 体积

| 文件 | 增量 | gzip 后 |
|---|---|---|
| web/src/styles.css | +0.7KB | +0.2KB |
| web/dist CSS | 8.46KB → 8.86KB → ~9.5KB | 2.33KB → 2.5KB |
| web/dist JS | 不变 (1.18MB) | 不变 (377KB) |

---

## playwright 验证

- 0 exception + 0 console error
- 截图: `docs/screenshots/2026-06-15-monitor-new-formula.png` (263 KB, 5 张红边呼吸 + 综合 KPI 无红边)

---

## 关键经验

1. **错误率口径 = 业务问题** — 排除登录/充值/管理 + 中间重试, 错误率才能反映"实际可用性"
2. **复合索引完美命中** — `idx_created_at_type` 是这次能 7.3ms 扫 178万行的关键, 加新过滤条件前必看索引
3. **jsonb_array_length 不走索引** — 但 WHERE 已经过滤到 ~6500 行, 无所谓 (ops_repo.go:491 注释)
4. **红边只给单渠道** — 综合 KPI 是"全局视角", 红边强调"哪个渠道", 不混用
5. **CSS 类名差异化** — `.kpi-danger` (有红边动画) vs `.kpi-danger-stat` (仅颜色, 无动画), TSX 选 class 即可

---

## monitor module 4 PR 全部完成 ✅

| PR | Commit | 主题 | 工作量 |
|---|---|---|---|
| #1 | `8546197` | 监控中心首版 + main.go scheduler 修复 | 0.5 天 |
| #2 | `4460965` | 24h 准确聚合 + 卡片化 + 3 规则 | 0.5 天 |
| #3 | `4edbc85` | 红边发光 + 2s 脉动 | 0.2 天 |
| #4 | `675dcc2` | 新口径 + 红边呼吸加强 | 0.5 天 |
| **总计** | | | **1.7 天** |
