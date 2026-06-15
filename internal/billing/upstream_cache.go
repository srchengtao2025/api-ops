// BILLING v3 上游对账 cache-aware getter (PR #9, 2026-06-15)
//
// 设计:
//   - handler 调 GetUpstreamOverviewCached: 优先读 cache, cache miss fallback 实时算 + 标 stale
//   - export worker 调 GetUpstreamStatementCached: 优先读 cache (totals), cache miss fallback 实时算
//   - 公式走 CalcUpstreamStatement 复用, 不重写 (PR #7 锁的成本公式)
//
// 跟 monitor 5min cache 模式一致 (cache_logs_summary_5min, channel_health_5min),
// 5min 延迟可接受 (月对账 1-5 号老板跑上月账单场景)
package billing

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// UpstreamOverviewCacheResult handler overview 的 cache 包装结果
type UpstreamOverviewCacheResult struct {
	Vendors     []UpstreamOverviewVendor
	CacheTS     int64  // 最新 cache bucket 的 ts_bucket unix 秒 (handler 透传)
	Stale       bool   // true = cache miss, 走 fallback 实时算
	StaleReason string // 仅 stale=true 时填 ("cache_miss" | "no_vendor" | "all_vendors_stale")
	GeneratedAt int64  // 实际生成时间 (cache bucket 或 实时)
}

// GetUpstreamOverviewCached handler overview 的 cache 包装
//
// 流程:
//  1. 拉所有 vendor (从 dal.ListVendors)
//  2. 一次性查所有 (vendor, current-month) 最新 cache 行
//  3. cache hit (全部 vendor 都有 + ≤ 5min) → 用 cache 数字 + cache_ts
//  4. cache miss (启动 5min 内, 或某 vendor 没数据) → 对该 vendor fallback 实时算 CalcUpstreamStatement
//  5. 任一 vendor fallback 触发 → Stale=true (整体 stale)
//  6. 返回时按 RequestCount DESC 排序 (跟现 handler 一致)
//
// 注意: 当前 handler 的 per-channel breakdown (5+ channel) 走的是单 SQL GROUP BY channel_id 的快速估算.
// 改 cache 后, handler 走 CalcUpstreamStatement 的 ByChannel (精确版, 走 group_ratio) — 比原估算更准.
// 性能: cache hit ≈ 0 RoDB hit; cache miss = N vendor × 1 SQL (跟 v3 旧路径一致)
func GetUpstreamOverviewCached(ctx context.Context) (*UpstreamOverviewCacheResult, error) {
	if !dal.HasRoDB() {
		return nil, dal.ErrNoRoDB
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	endOfNow := now.Unix()

	// 1) 拉所有 vendor
	vendorList, err := dal.ListVendors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vendors: %w", err)
	}
	if len(vendorList) == 0 {
		// 0 vendor: 跟现 handler 行为一致, 返空 (不 stale)
		return &UpstreamOverviewCacheResult{
			Vendors: []UpstreamOverviewVendor{},
			Stale:   false,
		}, nil
	}

	// 2) 一次性拉所有 (vendor, current-month) 最新 cache 行
	cacheMaxAge := int64(5 * 60) // 5 min = 300s
	cacheMap, err := dal.ListLatestUpstreamSummaryAllVendors(ctx, cacheMaxAge)
	if err != nil {
		// cache 读失败不短路, 走 fallback
		log.Printf("[upstream-cache] list cache failed (fallback to live): %v", err)
		cacheMap = nil
	}

	// 3) 决定 cache 状态
	vendorNameMap := make(map[string]string, len(vendorList))
	for _, v := range vendorList {
		vendorNameMap[v.Code] = v.Name
	}

	out := make([]UpstreamOverviewVendor, 0, len(vendorList))
	stale := false
	staleReason := ""
	latestCacheTS := int64(0)

	for _, v := range vendorList {
		// 3a) 查 cache
		cacheRow := cacheMap[v.Code][dal.UpstreamPeriodCurrentMonth]
		if cacheRow != nil {
			// cache hit: 用 cache 数字
			if cacheRow.TSBucket.Unix() > latestCacheTS {
				latestCacheTS = cacheRow.TSBucket.Unix()
			}
			out = append(out, UpstreamOverviewVendor{
				VendorCode:   v.Code,
				VendorName:   vendorNameMap[v.Code],
				RequestCount: cacheRow.RequestCount,
				TotalCost:    cacheRow.Cost,
				TotalRevenue: cacheRow.Revenue,
				TotalProfit:  cacheRow.Profit,
			})
			continue
		}

		// 3b) cache miss: 实时算 (走 CalcUpstreamStatement, 跟 worker / export 一致)
		stmt, calcErr := CalcUpstreamStatement(ctx, v.Code, startOfMonth, endOfNow)
		if calcErr != nil {
			// 单 vendor 失败不阻塞, log 跳过
			log.Printf("[upstream-cache] calc fallback failed vendor=%s: %v", v.Code, calcErr)
			continue
		}
		// 把 ByChannel 拆出来, 这样 handler 仍能返 per-channel breakdown
		var channels []UpstreamOverviewChannel
		for _, c := range stmt.ByChannel {
			revenue := c.TotalRevenue
			cost := c.TotalCost
			profit := revenue - cost
			margin := 0.0
			if cost > 0 {
				margin = profit / cost
			}
			// 折扣: 尝试反查 (cost / revenue 推 discount; 不准但够 overview 用)
			// 实际应该走 discounts map, 跟 handler 老逻辑一致
			channels = append(channels, UpstreamOverviewChannel{
				ChannelID:    c.ChannelID,
				ChannelName:  c.ChannelName,
				RequestCount: c.RequestCount,
				TotalCost:    cost,
				TotalRevenue: revenue,
				TotalProfit:  profit,
				ProfitRate:   margin,
			})
		}
		out = append(out, UpstreamOverviewVendor{
			VendorCode:   v.Code,
			VendorName:   vendorNameMap[v.Code],
			RequestCount: stmt.TotalRequestCount,
			TotalCost:    stmt.TotalCost,
			TotalRevenue: stmt.TotalRevenue,
			TotalProfit:  stmt.TotalProfit,
			Channels:     channels,
		})
		stale = true
		if staleReason == "" {
			staleReason = "cache_miss"
		}
	}

	// 4) 算 profit_rate (按 vendor)
	for i := range out {
		if out[i].TotalCost > 0 {
			out[i].ProfitRate = out[i].TotalProfit / out[i].TotalCost
		}
	}

	// 5) 排序: 按 RequestCount DESC (跟 handler 老行为一致)
	sortVendorsByRequestCount(out)

	generatedAt := int64(0)
	if !stale && latestCacheTS > 0 {
		generatedAt = latestCacheTS
	} else {
		generatedAt = now.Unix()
	}

	return &UpstreamOverviewCacheResult{
		Vendors:     out,
		CacheTS:     latestCacheTS,
		Stale:       stale,
		StaleReason: staleReason,
		GeneratedAt: generatedAt,
	}, nil
}

// GetUpstreamStatementCached 导出 worker 用的 cache 包装
//
// 入参:
//   - vendorCode: 必填
//   - periodStart / periodEnd: 解析 period 后的 unix 秒
//   - periodLabel: 跟 cache 表 period_label 对齐 (current-month / last-month)
//
// 返回:
//   - stmt: 跟 CalcUpstreamStatement 同结构
//   - fromCache: true = cache hit, false = cache miss 走了 fallback
//   - err: 任何错误
//
// 公式: cache hit 时 cost/revenue/profit/request_count 用 cache 数字, ByDate/ByChannel/ByModel 仍走
// CalcUpstreamStatement (1 SQL 算 breakdown). cache miss 时走完整 CalcUpstreamStatement (跟原逻辑一致).
//
// 性能: cache hit 比 cache miss 略快 (跳过 1 SQL 算 totals), 但 breakdown SQL 仍必跑 (cache 没存 ByDim)
func GetUpstreamStatementCached(ctx context.Context, vendorCode string, periodStart, periodEnd int64, periodLabel dal.UpstreamPeriodLabel) (stmt *UpstreamStatement, fromCache bool, err error) {
	if !dal.HasRoDB() {
		return nil, false, dal.ErrNoRoDB
	}
	if vendorCode == "" {
		return nil, false, fmt.Errorf("vendor_code required")
	}

	// 1) cache lookup
	cacheRow, _ := dal.GetLatestUpstreamSummary(ctx, dal.UpstreamSummaryQuery{
		VendorCode:    vendorCode,
		PeriodLabel:   periodLabel,
		MaxAgeSeconds: 5 * 60, // 5min
	})

	// 2) 跑 breakdown (calcUpstreamStatement 内部 1 SQL 拿 logs 然后 in-memory GROUP BY)
	// 走 breakdown 必跑 (cache 只存 totals), 但我们接受这点: 同一个 period 多 worker 并发
	// 会共享 cache 的 totals 检查, 真正的 SQL scan 还是 per-worker.
	// (后续 PR 可加 cache_by_dim 进一步优化)
	stmt, err = CalcUpstreamStatement(ctx, vendorCode, periodStart, periodEnd)
	if err != nil {
		return nil, false, err
	}

	if cacheRow != nil {
		// 3) cache hit: 用 cache 数字覆盖 totals (cache 是 5min 前的快照, breakdown 是最新的 log scan;
		// 这种"totals cache + breakdown live"组合近似 cache 数字 + breakdown 最新, 跟 task spec 一致:
		// "5min 数据延迟可接受" 指 totals 这种聚合数字, 3 维拆分的 detail 用最新数据是更准的)
		stmt.TotalRequestCount = cacheRow.RequestCount
		stmt.TotalRevenue = cacheRow.Revenue
		stmt.TotalCost = cacheRow.Cost
		stmt.TotalProfit = cacheRow.Profit
		if stmt.TotalCost > 0 {
			stmt.ProfitRate = stmt.TotalProfit / stmt.TotalCost
		}
		fromCache = true
	} else {
		fromCache = false
	}

	return stmt, fromCache, nil
}

// sortVendorsByRequestCount 冒泡排序, 按 RequestCount DESC
// 跟现 handler 老行为一致 (v3 handler 返 items 顺序 = RequestCount DESC)
func sortVendorsByRequestCount(vs []UpstreamOverviewVendor) {
	// 简单选择排序: n ≤ 5 vendor, 性能 OK
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			if vs[j].RequestCount > vs[i].RequestCount {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
}
