// Package sync: logs 1min 摘要同步（RoDB → OPS cache）
//
// 设计目标：
//   - 替代 dashboard / monitor / alert / billing 等场景的"大聚合 SQL 直读 RoDB"
//   - 1min 滑窗：每分钟 UPSERT 一桶（channel_id=0 是 global + N 个 channel）
//   - 实时性：最差 1min 延迟
//
// 数据流（每 1min）：
//
//	newapi logs (RoDB) ── 2 SQL ──► cache_logs_summary_5min (OPS)
//	                               ├─ channel_id=0（global）
//	                               └─ channel_id IN (N 个 active channel)
//
// 性能对比：
//   - 旧 monitor 1 query/min（5min 窗 percentile_cont GROUP BY）→ CPU 重但可控
//   - 新 sync_logs_summary 2 query/min（1min 窗 GROUP BY + AVG）→ 极轻
//   - monitor 读 cache（5 个 1min 桶汇总）→ 0 query/min 到 RoDB
package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// LogsSummaryInterval logs 摘要同步频率（默认 1min）
const LogsSummaryInterval = 1 * time.Minute

// LogsSummaryRetentionDays 缓存保留天数（每天 prune 一次）
const LogsSummaryRetentionDays = 7

// syncLogsSummary 同步一次：
//   - 2 SQL 写 cache_logs_summary_5min（global + 各 channel GROUP BY）→ monitor/dashboard 用
//   - 1 SQL 写 cache_logs_summary_by_model_5min（按 channel × model GROUP BY）→ billing 用
//   - bucket_ts = floor(now/60)*60（对齐到分钟）
//   - 1min 窗：created_at ∈ [bucket_ts, bucket_ts+60)
//   - 返回：成功条数 + error
func (s *upstreamSync) syncLogsSummary(ctx context.Context) (int, error) {
	if dal.RO == nil {
		// demo 模式无 RO，跳过
		return 0, nil
	}

	nowSec := time.Now().Unix()
	bucketTS := (nowSec / 60) * 60 // 对齐到分钟
	startTS := bucketTS
	endTS := bucketTS + 60

	// 1) Global（channel_id=0，无 GROUP BY）
	global, err := aggregateLogsSummaryOne(ctx, 0, startTS, endTS)
	if err != nil {
		return 0, fmt.Errorf("global: %w", err)
	}
	rows := []dal.LogsSummary5min{global}

	// 2) 各 channel GROUP BY（单 SQL 一次拿 N 个 channel 的 1min 聚合）
	perCh, err := aggregateLogsSummaryByChannel(ctx, startTS, endTS)
	if err != nil {
		return 0, fmt.Errorf("by channel: %w", err)
	}
	rows = append(rows, perCh...)

	// 3) UPSERT 到 OPS
	if err := dal.UpsertLogsSummary(ctx, rows); err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}
	channelCount := len(rows)

	// 4) by-model：用于 billing 对账（成本计算需要 model × token 拆分）
	// by-model 失败不能阻塞主流程（monitor/dashboard 已用 channel 维度够了）
	modelCount := 0
	bucketRows, err := aggregateLogsSummaryByModel(ctx, startTS, endTS)
	if err != nil {
		log.Printf("[sync] by-model aggregate failed (skip): %v", err)
	} else if len(bucketRows) > 0 {
		if err := dal.UpsertLogsSummaryByModel(ctx, bucketRows); err != nil {
			log.Printf("[sync] by-model upsert failed (skip): %v", err)
		} else {
			modelCount = len(bucketRows)
		}
	}
	return channelCount + modelCount, nil
}

// aggregateLogsSummaryOne 单 channel_id（或 0=global）的 1min 聚合
func aggregateLogsSummaryOne(ctx context.Context, channelID int, startTS, endTS int64) (dal.LogsSummary5min, error) {
	// SQL：1min 窗，固定 channel_id（channelID=0 表示无条件）
	// 注意 use_time 是秒，×1000 转 ms
	var sqlStr string
	args := []interface{}{dal.LogTypeError, dal.LogTypeConsume, startTS, endTS}
	if channelID == 0 {
		sqlStr = `
SELECT
  COUNT(*) AS request_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS error_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS success_count,
  COALESCE(SUM(CASE WHEN type IN (2, 6) THEN quota ELSE 0 END), 0) AS quota,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p50_ms,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p95_ms,
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p99_ms,
  COALESCE(AVG(use_time), 0)::int AS avg_latency_ms
FROM logs
WHERE created_at >= ? AND created_at < ?`
	} else {
		sqlStr = `
SELECT
  COUNT(*) AS request_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS error_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS success_count,
  COALESCE(SUM(CASE WHEN type IN (2, 6) THEN quota ELSE 0 END), 0) AS quota,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p50_ms,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p95_ms,
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p99_ms,
  COALESCE(AVG(use_time), 0)::int AS avg_latency_ms
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND channel_id = ?`
		args = append(args, channelID)
	}

	type raw struct {
		RequestCount     int64 `gorm:"column:request_count"`
		ErrorCount       int64 `gorm:"column:error_count"`
		SuccessCount     int64 `gorm:"column:success_count"`
		Quota            int64 `gorm:"column:quota"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
		P50Ms            int   `gorm:"column:p50_ms"`
		P95Ms            int   `gorm:"column:p95_ms"`
		P99Ms            int   `gorm:"column:p99_ms"`
		AvgLatencyMs     int   `gorm:"column:avg_latency_ms"`
	}
	var r raw
	if err := dal.RO.WithContext(ctx).Raw(sqlStr, args...).Scan(&r).Error; err != nil {
		return dal.LogsSummary5min{}, err
	}

	errRate := float64(0)
	if r.RequestCount > 0 {
		errRate = float64(r.ErrorCount) / float64(r.RequestCount)
	}
	return dal.LogsSummary5min{
		ChannelID:        channelID,
		BucketTS:         (startTS / 60) * 60,
		RequestCount:     r.RequestCount,
		ErrorCount:       r.ErrorCount,
		SuccessCount:     r.SuccessCount,
		Quota:            r.Quota,
		PromptTokens:     r.PromptTokens,
		CompletionTokens: r.CompletionTokens,
		P50LatencyMs:     r.P50Ms,
		P95LatencyMs:     r.P95Ms,
		P99LatencyMs:     r.P99Ms,
		AvgLatencyMs:     r.AvgLatencyMs,
		ErrorRate:        errRate,
	}, nil
}

// aggregateLogsSummaryByChannel 1 SQL 拿所有 channel 的 1min 聚合（含 p50/p95/p99 + quota）
func aggregateLogsSummaryByChannel(ctx context.Context, startTS, endTS int64) ([]dal.LogsSummary5min, error) {
	sqlStr := `
SELECT
  channel_id,
  COUNT(*) AS request_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS error_count,
  COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS success_count,
  COALESCE(SUM(CASE WHEN type IN (2, 6) THEN quota ELSE 0 END), 0) AS quota,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p50_ms,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p95_ms,
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY use_time) * 1000, 0)::int AS p99_ms,
  COALESCE(AVG(use_time), 0)::int AS avg_latency_ms
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND channel_id > 0
GROUP BY channel_id`

	type raw struct {
		ChannelID        int   `gorm:"column:channel_id"`
		RequestCount     int64 `gorm:"column:request_count"`
		ErrorCount       int64 `gorm:"column:error_count"`
		SuccessCount     int64 `gorm:"column:success_count"`
		Quota            int64 `gorm:"column:quota"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
		P50Ms            int   `gorm:"column:p50_ms"`
		P95Ms            int   `gorm:"column:p95_ms"`
		P99Ms            int   `gorm:"column:p99_ms"`
		AvgLatencyMs     int   `gorm:"column:avg_latency_ms"`
	}
	var raws []raw
	if err := dal.RO.WithContext(ctx).Raw(sqlStr,
		dal.LogTypeError, dal.LogTypeConsume, startTS, endTS).
		Scan(&raws).Error; err != nil {
		return nil, err
	}

	bucket := (startTS / 60) * 60
	out := make([]dal.LogsSummary5min, 0, len(raws))
	for _, r := range raws {
		errRate := float64(0)
		if r.RequestCount > 0 {
			errRate = float64(r.ErrorCount) / float64(r.RequestCount)
		}
		out = append(out, dal.LogsSummary5min{
			ChannelID:        r.ChannelID,
			BucketTS:         bucket,
			RequestCount:     r.RequestCount,
			ErrorCount:       r.ErrorCount,
			SuccessCount:     r.SuccessCount,
			Quota:            r.Quota,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			P50LatencyMs:     r.P50Ms,
			P95LatencyMs:     r.P95Ms,
			P99LatencyMs:     r.P99Ms,
			AvgLatencyMs:     r.AvgLatencyMs,
			ErrorRate:        errRate,
		})
	}
	return out, nil
}

// aggregateLogsSummaryByModel 1 SQL 拉 logs 按 (channel_id, model_name) 聚合，附带 cache 细分 token
// 用于 billing 对账算上游成本（必须 model × token 拆分 才能乘上价目）
func aggregateLogsSummaryByModel(ctx context.Context, startTS, endTS int64) ([]dal.LogsSummaryByModel5min, error) {
	// 1) 取 channel→vendor 映射（一次拉全部，内存 group by）
	// 注：channel 可能映射到多个 vendor，本期按 weight 顺序取第一个（实际场景 1:1 居多）
	mappings, err := dal.ListChannelVendors(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("load channel-vendor map: %w", err)
	}
	ch2vendor := make(map[int]string, len(mappings))
	for _, m := range mappings {
		if _, exists := ch2vendor[m.ChannelID]; !exists {
			ch2vendor[m.ChannelID] = m.VendorCode
		}
	}

	// 2) 1 SQL 拉 logs 聚合（other JSON 提取 cache 细分 token）
	// PG 表达式：other::jsonb->>'key' 提取 string，::int 转 int
	sqlStr := `
SELECT
  channel_id,
  model_name,
  COUNT(*) AS request_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS refund_count,
  COALESCE(SUM(CASE WHEN type IN (?, ?) THEN quota ELSE 0 END), 0) AS quota,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  COALESCE(SUM(CASE WHEN other IS NOT NULL AND other <> '' THEN (other::jsonb->>'cache_tokens')::int ELSE 0 END), 0) AS cache_tokens,
  COALESCE(SUM(CASE WHEN other IS NOT NULL AND other <> '' THEN (other::jsonb->>'cache_creation_tokens_5m')::int ELSE 0 END), 0) AS cache_creation_tokens_5m,
  COALESCE(SUM(CASE WHEN other IS NOT NULL AND other <> '' THEN (other::jsonb->>'cache_creation_tokens_1h')::int ELSE 0 END), 0) AS cache_creation_tokens_1h
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND channel_id > 0
  AND model_name <> ''
GROUP BY channel_id, model_name`
	args := []interface{}{
		dal.LogTypeError, dal.LogTypeConsume, dal.LogTypeRefund,
		dal.LogTypeConsume, dal.LogTypeRefund,
		startTS, endTS,
	}

	type raw struct {
		ChannelID             int    `gorm:"column:channel_id"`
		ModelName             string `gorm:"column:model_name"`
		RequestCount          int64  `gorm:"column:request_count"`
		ErrorCount            int64  `gorm:"column:error_count"`
		SuccessCount          int64  `gorm:"column:success_count"`
		RefundCount           int64  `gorm:"column:refund_count"`
		Quota                 int64  `gorm:"column:quota"`
		PromptTokens          int64  `gorm:"column:prompt_tokens"`
		CompletionTokens      int64  `gorm:"column:completion_tokens"`
		CacheTokens           int64  `gorm:"column:cache_tokens"`
		CacheCreationTokens5m int64  `gorm:"column:cache_creation_tokens_5m"`
		CacheCreationTokens1h int64  `gorm:"column:cache_creation_tokens_1h"`
	}
	var raws []raw
	if err := dal.RO.WithContext(ctx).Raw(sqlStr, args...).Scan(&raws).Error; err != nil {
		return nil, err
	}

	bucket := (startTS / 60) * 60
	out := make([]dal.LogsSummaryByModel5min, 0, len(raws))
	for _, r := range raws {
		out = append(out, dal.LogsSummaryByModel5min{
			BucketTS:              bucket,
			ChannelID:             r.ChannelID,
			ModelName:             r.ModelName,
			VendorCode:            ch2vendor[r.ChannelID],
			RequestCount:          r.RequestCount,
			ErrorCount:            r.ErrorCount,
			SuccessCount:          r.SuccessCount,
			RefundCount:           r.RefundCount,
			Quota:                 r.Quota,
			PromptTokens:          r.PromptTokens,
			CompletionTokens:      r.CompletionTokens,
			CacheTokens:           r.CacheTokens,
			CacheCreationTokens5m: r.CacheCreationTokens5m,
			CacheCreationTokens1h: r.CacheCreationTokens1h,
		})
	}
	return out, nil
}

// pruneLogsSummary 删除老于 N 天的摘要（每天调用一次）
func (s *upstreamSync) pruneLogsSummary(ctx context.Context) (int64, error) {
	if dal.OPS == nil {
		return 0, nil
	}
	n, err := dal.PruneOldSummary(ctx, LogsSummaryRetentionDays)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("[sync] pruned %d old logs_summary rows (>%d days)", n, LogsSummaryRetentionDays)
	}
	return n, nil
}

// LogsSummaryOnce 公开入口：跑一次同步（main.go 启 1min tick 调）
// 设计原则：失败只记 log，不 panic（下游监控可容忍单次失败）
func LogsSummaryOnce(ctx context.Context) (int, error) {
	if dal.RO == nil {
		return 0, nil // demo 模式：跳过
	}
	s := &upstreamSync{} // 不依赖 client（syncLogsSummary 只用 dal.RO / dal.OPS）
	return s.syncLogsSummary(ctx)
}

// LogsSummaryLoop 后台 1min tick 循环（panic-safe）
// 设计：
//   - 启动后 5s 跑一次（保证 cache 有数据再接 HTTP 请求）
//   - 任何一次 panic 都不会让 goroutine 退出
//   - ctx cancel 后干净退出
func LogsSummaryLoop(ctx context.Context) {
	time.AfterFunc(5*time.Second, func() {
		runOnceLogsSummarySafe(ctx)
	})
	t := time.NewTicker(LogsSummaryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[sync.logs_summary] stopped")
			return
		case <-t.C:
			runOnceLogsSummarySafe(ctx)
		}
	}
}

func runOnceLogsSummarySafe(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[sync.logs_summary] panic: %v", r)
		}
	}()
	n, err := LogsSummaryOnce(ctx)
	if err != nil {
		log.Printf("[sync.logs_summary] failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[sync.logs_summary] upserted=%d rows", n)
	}
}
