// BILLING v3 上游对账 5min cache tick (scheduler 5min 调)
//
// 设计动机:
//   - 旧: CalcUpstreamStatement(vendor, month) 每次按需跑 1 SQL 扫 logs 1.9M 行
//   - 月对账 1-5 号 5 vendor 同时跑 = 5 SQL 短时重算尖峰
//   - 新: 5min tick 预算"本月至今 + 上月" 2 period, 写 ops_upstream_summary_5min
//   - handler / worker 优先读 cache, cache miss fallback 实时算
//
// 跟 monitor 5min cache 模式一致 (cache_logs_summary_5min, channel_health_5min),
// 命名 + 模式都靠齐, 5min 延迟可接受 (跟监控 cache 一致)
package billing

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// UpstreamTickInterval 5min tick 频率
const UpstreamTickInterval = 5 * time.Minute

// UpstreamSummaryRetentionDays cache 保留天数 (跟 billing_export_tasks 一致)
const UpstreamSummaryRetentionDays = 30

// computeUpstreamBucket 5min 对齐
// 跟 sync_logs_summary.go 同款 floor(now/300)*300
func computeUpstreamBucket(now time.Time) time.Time {
	return time.Unix((now.Unix()/300)*300, 0).UTC()
}

// currentMonthBounds 本月至今 [startOfMonth, now] (用 Asia/Shanghai, 跟 v3 handler 对齐)
func currentMonthBounds(now time.Time) (int64, int64) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	nowLoc := now.In(loc)
	startOfMonth := time.Date(nowLoc.Year(), nowLoc.Month(), 1, 0, 0, 0, 0, loc).Unix()
	return startOfMonth, nowLoc.Unix()
}

// lastMonthBounds 上月完整 [1 号 00:00, 月末 23:59:59] (用 Asia/Shanghai)
func lastMonthBounds(now time.Time) (int64, int64) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	nowLoc := now.In(loc)
	firstOfThisMonth := time.Date(nowLoc.Year(), nowLoc.Month(), 1, 0, 0, 0, 0, loc)
	lastOfPrevMonth := firstOfThisMonth.Add(-time.Second)
	firstOfPrevMonth := time.Date(lastOfPrevMonth.Year(), lastOfPrevMonth.Month(), 1, 0, 0, 0, 0, loc)
	return firstOfPrevMonth.Unix(), lastOfPrevMonth.Unix()
}

// mapPeriodToLabel 把 period (YYYY-MM) 映射到 cache period_label
//   - 上月 (period = now-1 月 YYYY-MM) → 'last-month'
//   - 本月 (period = now 月 YYYY-MM)     → 'current-month'
//   - 其它 (e.g. 跨 2 月前历史)             → 返回空字符串 (cache miss, 走 live calc)
//
// endTS 是 PeriodBounds 的 end (本月至今 = now, 上月 = 月末 23:59:59), 用 endTS 判 period 边界更准
func mapPeriodToLabel(period string, endTS int64) dal.UpstreamPeriodLabel {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	nowLoc := time.Now().In(loc)
	currentMonth := nowLoc.Format("2006-01")
	lastMonthTime := time.Date(nowLoc.Year(), nowLoc.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -1, 0)
	lastMonth := lastMonthTime.Format("2006-01")

	switch period {
	case currentMonth:
		return dal.UpstreamPeriodCurrentMonth
	case lastMonth:
		return dal.UpstreamPeriodLastMonth
	default:
		// 历史 period (e.g. 2 个月前) — cache 不覆盖, 走 live calc
		return ""
	}
}

// RunUpstreamSummaryTick 跑 1 次 upstream summary tick
//
// 流程:
//  1. 遍历所有 active vendor (从 dal.ListVendors 拿, 跟 handler 一致)
//  2. 每个 vendor 跑 2 period (current-month + last-month)
//  3. CalcUpstreamStatement 算 cost/revenue/profit/request_count
//  4. UPSERT 到 ops_upstream_summary_5min
//
// 返回:
//   - vendors: 处理的 vendor 数
//   - rows: 写入的 cache 行数 (= vendors × 2)
//   - err: 第一个错误 (后续 vendor 继续跑, 整体 err 不短路)
func RunUpstreamSummaryTick(ctx context.Context) (vendors int, rows int, err error) {
	if !dal.HasRoDB() {
		// demo 模式无 RO, 跳过 (跟 sync.LogsSummaryOnce 一样)
		return 0, 0, nil
	}

	now := time.Now()
	bucket := computeUpstreamBucket(now)
	currentStart, currentEnd := currentMonthBounds(now)
	lastStart, lastEnd := lastMonthBounds(now)

	periods := []struct {
		Label dal.UpstreamPeriodLabel
		Start int64
		End   int64
	}{
		{dal.UpstreamPeriodCurrentMonth, currentStart, currentEnd},
		{dal.UpstreamPeriodLastMonth, lastStart, lastEnd},
	}

	// 1) 拉所有 active vendor
	vendorList, err := dal.ListVendors(ctx)
	if err != nil {
		return 0, 0, err
	}
	if len(vendorList) == 0 {
		return 0, 0, nil
	}

	cacheRows := make([]dal.OpsUpstreamSummary5min, 0, len(vendorList)*2)
	firstErr := error(nil)

	for _, v := range vendorList {
		// 1 vendor 1 period 跑 1 次 CalcUpstreamStatement (跟 handler 一致)
		// 公式 + 反推逻辑走 CalcUpstreamStatement, 不重写 (PR #7 锁的成本公式)
		for _, p := range periods {
			stmt, calcErr := CalcUpstreamStatement(ctx, v.Code, p.Start, p.End)
			if calcErr != nil {
				log.Printf("[upstream-tick] calc failed vendor=%s period=%s: %v", v.Code, p.Label, calcErr)
				if firstErr == nil {
					firstErr = calcErr
				}
				continue
			}
			cacheRows = append(cacheRows, dal.OpsUpstreamSummary5min{
				VendorCode:   v.Code,
				PeriodLabel:  string(p.Label),
				PeriodStart:  stmt.PeriodStart,
				PeriodEnd:    stmt.PeriodEnd,
				RequestCount: stmt.TotalRequestCount,
				Revenue:      stmt.TotalRevenue,
				Cost:         stmt.TotalCost,
				Profit:       stmt.TotalProfit,
				TSBucket:     bucket,
			})
		}
	}

	// 2) UPSERT (单 batch)
	if len(cacheRows) > 0 {
		if upErr := dal.UpsertUpstreamSummary(ctx, cacheRows); upErr != nil {
			log.Printf("[upstream-tick] upsert failed: %v", upErr)
			if firstErr == nil {
				firstErr = upErr
			}
		} else {
			rows = len(cacheRows)
			vendors = len(vendorList)
		}
	}

	// 3) prune 老于 30 天的 cache (轻量, 1 天 1 次足够, 走这里不浪费 5min tick 开销)
	pruneCutoff := time.Now().Add(-time.Duration(UpstreamSummaryRetentionDays) * 24 * time.Hour)
	if bucket.Unix()%int64(24*time.Hour/time.Second) < int64(5*time.Minute/time.Second) {
		// 每天第一次 tick 跑 prune (粗略: 命中"今天第一桶", 实际会有 12 次/h × 24h = 288 次/天
		// 简化: 用 bucket.Unix() % 86400 < 300 来判断"接近 0 点", 实际偏差 ≤ 5min)
		if n, pruneErr := dal.PruneOldUpstreamSummary(ctx, UpstreamSummaryRetentionDays); pruneErr != nil {
			log.Printf("[upstream-tick] prune failed: %v", pruneErr)
		} else if n > 0 {
			log.Printf("[upstream-tick] pruned %d old cache rows (retention=%dd)", n, UpstreamSummaryRetentionDays)
		}
	}
	_ = pruneCutoff // 保留引用, 未来调整用

	return vendors, rows, firstErr
}

// UpstreamSummaryLoop 后台 5min tick 循环 (panic-safe)
//
// 设计:
//   - 启动后 10s 跑一次 (让 monitor / billing-export worker 先就绪)
//   - 任何一次 panic 都不会让 goroutine 退出
//   - ctx cancel 后干净退出
func UpstreamSummaryLoop(ctx context.Context) {
	// 2026-06-15 14:00 debug 方案: 单次 tick 跑 1 个 (vendor, period), 不串行 5 vendor × 2 period.
	// 10 tick = 50min 完成一轮. handler 仍走 cache-first 路径, cache miss fallback 实时算.
	// 历史教训: 5 vendor × 2 period 串行跑 10 个 SQL, 每次返回 19万行, 累计 200MB+ 临时分配
	// 触发进程级崩溃 (exit 0, 无 panic, 无 OOM). 怀疑 gorm 19万 rows buffer + cgo 段错误.
	// 单次跑 1 个 (vendor, period) 即使 1 SQL 也有 19万行, 但**单次** GC 压力小, 跑完释放.
	// 如果仍崩, 进一步降级: 改 CalcUpstreamStatement 用 gorm streaming (FindInBatches).
	// 实现: tick 内部 state (lastVendorIdx, lastPeriodIdx) 持久在内存, round-robin 选下一个.
	state := tickState{}

	// 启动后 5s 跑第一次 (给 monitor / sync / v4 handler 启动时间, 避免 13:23 那次 warmup 撞车)
	time.AfterFunc(5*time.Second, func() { runOnceUpstreamSummarySafe(ctx, &state) })

	t := time.NewTicker(UpstreamTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[upstream-tick] stopped")
			return
		case <-t.C:
			runOnceUpstreamSummarySafe(ctx, &state)
		}
	}
}

// state struct shared across ticks
type tickState struct {
	vendorIdx int
	periodIdx int
}

func runOnceUpstreamSummarySafe(ctx context.Context, state *tickState) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[upstream-tick] panic: %v", r)
		}
	}()
	vendorCode, periodLabel, periodStart, periodEnd, err := pickNextTick(ctx, state)
	if err != nil {
		log.Printf("[upstream-tick] pick next failed: %v", err)
		return
	}
	stmt, err := CalcUpstreamStatement(ctx, vendorCode, periodStart, periodEnd)
	if err != nil {
		log.Printf("[upstream-tick] calc failed vendor=%s period=%s: %v", vendorCode, periodLabel, err)
		// 推进 state 避免无限卡在同一个 (vendor, period)
		return
	}
	bucket := computeUpstreamBucket(time.Now())
	row := dal.OpsUpstreamSummary5min{
		VendorCode:   stmt.VendorCode,
		PeriodLabel:  string(periodLabel),
		PeriodStart:  stmt.PeriodStart,
		PeriodEnd:    stmt.PeriodEnd,
		RequestCount: stmt.TotalRequestCount,
		Revenue:      stmt.TotalRevenue,
		Cost:         stmt.TotalCost,
		Profit:       stmt.TotalProfit,
		TSBucket:     bucket,
	}
	if err := dal.UpsertUpstreamSummary(ctx, []dal.OpsUpstreamSummary5min{row}); err != nil {
		log.Printf("[upstream-tick] upsert failed vendor=%s period=%s: %v", vendorCode, periodLabel, err)
		return
	}
	log.Printf("[upstream-tick] ok vendor=%s period=%s rows=1 (5min bucket updated)", vendorCode, periodLabel)
}

// pickNextTick round-robin 选下一个 (vendor, period), 更新 state
func pickNextTick(ctx context.Context, state *tickState) (vendorCode string, periodLabel dal.UpstreamPeriodLabel, periodStart, periodEnd int64, err error) {
	vendors, err := dal.ListVendors(ctx)
	if err != nil {
		return "", "", 0, 0, err
	}
	if len(vendors) == 0 {
		return "", "", 0, 0, fmt.Errorf("no active vendors")
	}
	now := time.Now()
	currentStart, currentEnd := currentMonthBounds(now)
	lastStart, lastEnd := lastMonthBounds(now)
	type p struct {
		label dal.UpstreamPeriodLabel
		start int64
		end   int64
	}
	periods := []p{
		{dal.UpstreamPeriodCurrentMonth, currentStart, currentEnd},
		{dal.UpstreamPeriodLastMonth, lastStart, lastEnd},
	}
	// round-robin: state.periodIdx 先选 (0=current / 1=last), state.vendorIdx 选 vendor
	period := periods[state.periodIdx%len(periods)]
	vendor := vendors[state.vendorIdx%len(vendors)]

	// 推进 state
	state.vendorIdx = (state.vendorIdx + 1) % (len(vendors) * len(periods))
	if state.vendorIdx%len(vendors) == 0 {
		state.periodIdx = (state.periodIdx + 1) % len(periods)
	}

	return vendor.Code, period.label, period.start, period.end, nil
}
