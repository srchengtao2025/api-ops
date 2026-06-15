// scheduler: cron 调度器
//
// 任务 (2026-06-14 v1 下线后):
//   - ~~每日凌晨 2 点生成昨日全部客户对账单~~ (v1 下线, BILLING v2 走异步任务)
//   - 每 1 分钟: 渠道健康度 5min 聚合 + 告警规则评估
//   - 每 5 分钟: 渠道健康度 1h 聚合
//   - 每 1 小时: AI 错误聚类
//   - 每日 02:30: AI 错误日报
//   - 每周一 09:00: AI 周报
//   - 每 5 分钟: BILLING v3 上游对账 cache 聚合 (2026-06-15 PR #9, 跟 monitor 模式一致)
//
// BILLING v2 30 天清理走 `internal/billing/prune.go` 自己的 cron,
// 不在本 scheduler 里跑.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/ai"
	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/config"
	"github.com/api-ops/api-ops/internal/monitor"
)

// Run 启动调度器（goroutine）
func Run(ctx context.Context, cfg *config.Config) {
	// ~~启动每日对账任务~~ (v1 下线)
	// 启动 P1 监控 tick（每 1min）
	go runMonitorTick(ctx)
	// 启动 P3 AI 任务
	go runAITick(ctx)
	// 启动 BILLING v3 上游对账 5min cache tick (PR #9, 2026-06-15)
	go runUpstreamTick(ctx)
}

// runUpstreamTick BILLING v3 上游对账 5min cache tick
//
// 跟 monitor 5min tick 模式一致 (cache_logs_summary_5min / channel_health_5min),
// 5min 延迟可接受 (月对账 1-5 号老板跑上月账单场景).
// 流程详见 internal/billing/upstream_summary_tick.go UpstreamSummaryLoop
func runUpstreamTick(ctx context.Context) {
	log.Println("[scheduler] BILLING v3 upstream summary tick started (interval=5min)")
	billing.UpstreamSummaryLoop(ctx)
}

// ~~runDailyBilling 每日对账任务~~ 已下线 (2026-06-14 v1 下线).
// BILLING v2 走异步任务 + UI 创建, 不再走 cron 自动跑.
// 30 天清理走 internal/billing/prune.go.

// runMonitorTick P1 监控 1min tick
// 流程：
//  1. 5min 聚合 → channel_health_5min（每 1min 必跑）
//  2. 1h 聚合  → channel_health_1h  （每 5min 跑一次，即 minute % 5 == 0）
//  3. 评估告警规则 → alert_histories（每 1min 必跑）
//  4. 收尾：把已不再满足的 firing 告警 → resolved（在 evaluator 内部）
func runMonitorTick(ctx context.Context) {
	// 启动后 5 秒跑一次（让 DB / 其它初始化先就绪）
	time.AfterFunc(5*time.Second, func() {
		runOnce(ctx)
	})
	t := time.NewTicker(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce(ctx)
		}
	}
}

func runOnce(ctx context.Context) {
	now := time.Now()
	skipHourly := now.Minute()%5 != 0
	res := monitor.RunTick(ctx, 0, skipHourly)
	if res.Err != nil {
		log.Printf("[monitor] tick failed: %v", res.Err)
	}
	if res.Skipped {
		log.Printf("[monitor] tick skipped (no db)")
		return
	}
	fired, err := monitor.EvaluateRules(ctx, now)
	if err != nil {
		log.Printf("[monitor] evaluate rules failed: %v", err)
		return
	}
	if res.Buckets5min > 0 || res.Buckets1h > 0 || fired > 0 {
		log.Printf("[monitor] tick: 5min_buckets=%d 1h_buckets=%d alerts_fired=%d skip_hourly=%v",
			res.Buckets5min, res.Buckets1h, fired, skipHourly)
	}
}

// runAITick P3 AI 任务调度
//   - 每 1 小时整点跑：1h 错误聚类 + 对每个 cluster 触发 Diagnose
//   - 每日 02:30 跑：错误日报
//   - 每周一 09:00 跑：周报
func runAITick(ctx context.Context) {
	// 启动后 30 秒跑一次（让 DB 等初始化先就绪）
	time.AfterFunc(30*time.Second, func() {
		n, err := ai.ClusterOneHour(ctx)
		if err != nil {
			log.Printf("[ai] initial cluster failed: %v", err)
		} else {
			log.Printf("[ai] initial cluster: upserted %d", n)
		}
	})
	t := time.NewTicker(1 * time.Minute) // 每分钟 tick，检查时间窗口
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			loc, _ := time.LoadLocation("Asia/Shanghai")
			nowLoc := now.In(loc)
			hh, mm := nowLoc.Hour(), nowLoc.Minute()
			weekday := nowLoc.Weekday()
			// 1h 聚类：整点（mm==0）跑
			if mm == 0 {
				if n, err := ai.ClusterOneHour(ctx); err != nil {
					log.Printf("[ai] cluster failed: %v", err)
				} else if n > 0 {
					log.Printf("[ai] cluster: upserted %d", n)
				}
			}
			// 每日 02:30 跑日报
			if hh == 2 && mm == 30 {
				if rep, err := ai.GenerateErrorDailyReport(ctx, nowLoc.Unix()); err != nil {
					log.Printf("[ai] daily report failed: %v", err)
				} else {
					log.Printf("[ai] daily report done id=%d title=%s", rep.ID, rep.Title)
				}
			}
			// 每周一 09:00 跑周报
			if weekday == time.Monday && hh == 9 && mm == 0 {
				if rep, err := ai.GenerateWeeklySummaryReport(ctx, nowLoc.Unix()); err != nil {
					log.Printf("[ai] weekly report failed: %v", err)
				} else {
					log.Printf("[ai] weekly report done id=%d title=%s", rep.ID, rep.Title)
				}
			}
		}
	}
}
