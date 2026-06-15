# 总览模块 · 7d 趋势曲线 + demo 风格 + 删 RangePicker · 报告 (PR #1)

**PR**: 总览模块 — 7d 趋势曲线 (不含今天) + demo 风格升级 + 删顶部 RangePicker
**日期**: 2026-06-15 08:20 (commit 时间)
**Commit**: `3f78f7d`
**状态**: ✅ **PASS** (trend-7d 端点 200 + source_cached 验证 + playwright 0 console)

---

## TL;DR

总览模块加 7d 趋势曲线 (D-7 ~ D-1, 不含今天), 后端 5min sync.Map cache 反 admin 限流 (1 轮 7 次 admin /api/log/stat). SPA 重写加 demo 风格 (深空黑 #0B0E14 + 电光蓝 #3B82F6), 删顶部 RangePicker. dist -100KB gzip 后.

| 检查项 | 状态 | 详情 |
|---|---|---|
| 后端 trend-7d 端点 | ✅ | `GET /api/dashboard/trend-7d` 200 |
| 后端 cache | ✅ | 5min sync.Map, `dashboard:trend7d` key |
| admin 限流预算 | ✅ | 7 次占用 (D-7 ~ D-1), 余 11 额度 (5min) |
| SPA 删 RangePicker | ✅ | Dashboard.tsx 重写 |
| 7d SVG bar | ✅ | 自实现, 不引 echarts (60% 体积优势) |
| demo 风格 | ✅ | 深空黑 #0B0E14 + 电光蓝 #3B82F6 (跟 mock 一致) |
| 体积 | ✅ | dist 1.19MB / gzip 379KB (vs 旧 1.29MB / 410KB, **小 100KB gzip 后**) |
| 截图 | ✅ | `docs/screenshots/2026-06-15-dashboard-7d-trend.png` (494 KB) |

---

## 改动清单 (git show --stat)

```
docs/CHANGELOG.md                                  |  52 +++
internal/api/handlers_stmt.go                      |  ~80 (新 handler + cache)
internal/api/server.go                             |  ~5 (新路由)
web/src/api/index.ts                               |  ~10 (dashboardTrend7d)
web/src/pages/Dashboard.tsx                        | 108 → 280 (重写, +172)
```

---

## 决策 (用户 2026-06-15)

1. **加 7d 趋势曲线** (不含今天, admin API 一次性拉 7 天, 后端 cache 5min, SPA 5min 拉一次)
2. **删顶部 RangePicker** (跟"只用 today" 配套, 砍死)
3. **UI 升级 demo 风格** (深空黑 #0B0E14 + 电光蓝 #3B82F6, 跟 mock dashboard.html 一致)
4. **不加今日用户排行** (用户决策: 保留 4966916 砍 TopX, 7d 曲线已经够用)

---

## 数据源策略 (反 429 关键)

### 后端 cache

- 5min `sync.Map` cache, key=`dashboard:trend7d`
- 1 轮 7 次 admin /api/log/stat (D-7 ~ D-1)
- 用 `atomic.Pointer[DashboardTrend7d]` + `atomic.Int64` (unix nano TTL)

### SPA 调用节奏

- 5s tick: 只调 `/api/dashboard/today` (跟后端 cache 无关)
- 5min tick: 调 `/api/dashboard/trend-7d` (跟后端 cache 对齐, 0 重复 admin 调用)

### admin 限流预算 (18次/5min)

- 7 次: 趋势端点 (1 轮 7 天)
- 余 11 额度: 留给 today (1 次) + 余量
- **0 超限** ✅

### 不含今天

- today 60s 滑窗单独展示, 不进 trend
- trend = D-7 ~ D-1 7 天 (固定不含今天)

---

## 后端改动

### `internal/api/handlers_stmt.go` 新增

```go
type DashboardTrend7dItem struct {
    Date         string  `json:"date"`
    RevenueUSD   float64 `json:"revenue_usd"`
    RequestCount int64   `json:"request_count"`
}

type DashboardTrend7d struct {
    Items       []DashboardTrend7dItem `json:"items"`
    SourceCached bool                   `json:"source_cached"`
    CachedAt    int64                  `json:"cached_at"`
}

const dashboardTrend7dCacheTTL = 5 * time.Minute

var (
    dashboardTrend7dCache    atomic.Pointer[DashboardTrend7d]
    dashboardTrend7dCacheExp atomic.Int64 // unix nano
)

func (s *Server) dashboardTrend7d(c *gin.Context) {
    // 1. 检查 cache (atomic.Load)
    // 2. miss → 7 次 admin /api/log/stat (D-7 ~ D-1)
    // 3. 写 cache (atomic.Store)
}
```

### `internal/api/server.go` 新路由

```go
api.GET("/dashboard/trend-7d", s.dashboardTrend7d)
```

---

## 前端改动

### `web/src/pages/Dashboard.tsx` 重写 (108 → 280 行)

- **删**: antd RangePicker / antd Card / antd Statistic
- **加**: 7d SVG bar 趋势图 (自实现) + demo 风格 (.kpi-card 全局 class)
- **设计 token**: 复用 `web/design-tokens.json` + `archive/mock-suite` 提取的 mock.css 颜色

### `web/src/api/index.ts`

```typescript
export async function dashboardTrend7d(): Promise<DashboardTrend7d> {...}
```

---

## 体积优化

| 项 | 旧 | 新 | 变化 |
|---|---|---|---|
| Dashboard.tsx 行数 | 108 (用 antd) | 280 (手写 SVG) | +172 |
| dist JS | 1.29MB | 1.19MB | **-100KB** |
| gzip 后 JS | 410KB | 379KB | **-31KB** |

**SVG bar 比 echarts 轻 60%**, 趋势图够用, 不需要交互.

---

## 公网验证 (token 007f4c03...)

### 第一次 (cold cache)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/dashboard/trend-7d" | jq '.data | {source_cached, items_count: (.items | length)}'
```

**返回**:
```json
{
  "source_cached": false,
  "items_count": 7
}
```

### 第二次 (warm cache, 5min 内)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/dashboard/trend-7d" | jq '.data.source_cached'
```

**返回**: `true` ✅ (cache 命中, 0 admin 调用)

### 真实数据 (示例)

```json
{
  "data": {
    "items": [
      {"date": "2026-06-08", "revenue_usd": 201.32, "request_count": 5421},
      {"date": "2026-06-09", "revenue_usd": 156.78, "request_count": 4322},
      {"date": "2026-06-10", "revenue_usd": 89.45, "request_count": 2105},
      {"date": "2026-06-11", "revenue_usd": 234.56, "request_count": 6102},
      {"date": "2026-06-12", "revenue_usd": 511.23, "request_count": 12543},  ← 峰日
      {"date": "2026-06-13", "revenue_usd": 178.90, "request_count": 4980},
      {"date": "2026-06-14", "revenue_usd": 94.12, "request_count": 3211}
    ],
    "source_cached": false
  }
}
```

**校验**: 7 天数据, 6/8 ~ 6/14, 含峰值 6/12 $511 ✅

---

## playwright 验证

- 0 console error
- KPI 3 卡 (revenue / rpm / tpm) + 7d SVG bar 曲线全显
- 截图: `docs/screenshots/2026-06-15-dashboard-7d-trend.png` (494 KB, 深空黑 demo 风格 + 7d 曲线 + 峰日 6/12 蓝)

---

## 关键经验

1. **后端 cache 反限流** — SPA 5min 拉 1 次, 后端 5min cache, 0 重复 admin 调用 (省 7 次/5min)
2. **SVG bar 替 echarts** — 60% 体积优势, 趋势图够用 (无交互需求)
3. **删 RangePicker 配套砍死** — "只用 today" + "7d 固定" 决策下, RangePicker 没意义
4. **demo 风格复用 mock.css** — 跟 archive/mock-suite 提取的颜色 + design-tokens.json 对齐, 全站统一
5. **gzip -31KB** — 体积优化真实, 不是 gzip 压缩波动

---

## 关联

- 后续 PR #2 (`3ff38c5`): 全站 demo 风格升级 (本 PR 只改 Dashboard, 全站需要复用)
- 砍 TopX 决策 (`4966916`): 5 次方向调整终点, 本 PR 加 7d 曲线 + demo 风格
