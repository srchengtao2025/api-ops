// Package monitor: P1 监控引擎 —— 5min 滑窗聚合 + 1h 聚合
// 设计要点：
//   - 聚合源：upstream/newapi logs 表（只读，RO 账号）；demo 模式 RO 为 nil 时 fallback 到 OPS 镜像
//   - 写入目标：api_ops 自己的 channel_health_5min / channel_health_1h（GORM + ON CONFLICT DO UPDATE）
//   - 频率：5min 表每 1min roll 一条；1h 表每 5min roll 一条
//   - 指标：错误率 / 成功率 / p50/p95/p99 延迟(ms) / ttft_p95(ms) / prompt+completion tokens / 余额 / 状态
//   - 余额 + 状态：每桶在聚合时从 channels 表取最新（独立 SQL JOIN），保证余额实时
package monitor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"gorm.io/gorm"
)

// Bucket 时间桶粒度常量
const (
	Bucket5min = 5 * 60         // 5min 桶长度（秒）
	Bucket1h   = 60 * 60        // 1h 桶长度（秒）
	Window5min = int64(5 * 60)  // 聚合窗口
	Window1h   = int64(60 * 60) // 1h 聚合窗口
)

// ChannelAggregate 聚合中间结果（一条 = 一个 channel × 一个 bucket）
type ChannelAggregate struct {
	ChannelID        int
	BucketTS         int64
	RequestCount     int64
	ErrorCount       int64
	SuccessCount     int64
	PromptTokens     int64
	CompletionTokens int64
	P50LatencyMs     int
	P95LatencyMs     int
	P99LatencyMs     int
	TTFTP95Ms        int
	ErrorRate        float64
	Balance          float64
	Status           string
}

// ChannelStatus 状态码 → 字符串
func channelStatusStr(status int) string {
	switch status {
	case dal.ChannelStatusEnabled:
		return "enabled"
	case dal.ChannelStatusManuallyDisabled:
		return "manual_disabled"
	case dal.ChannelStatusAutoDisabled:
		return "auto_disabled"
	default:
		return "unknown"
	}
}

// AggregateChannelHealth5min 对最近 5min 数据 roll 一次，落库到 channel_health_5min
//   - startTS/endTS 显式传入便于测试；运行时通常 = now-5min / now
//   - 返回写入行数 + 错误
//   - 自动跳过 RO 连接为 nil 且 seed 无 logs 的情况（安全降级）
func AggregateChannelHealth5min(ctx context.Context, endTS int64) (int, error) {
	if !dal.HasRoDB() {
		return 0, nil // A 阶段: RoDB 未配 → 跳过 (3 数据源原则)
	}
	bucketTS := floorToBucket(endTS, Bucket5min)
	startTS := bucketTS - Window5min

	// 优先读 cache（cache 5min 窗 = 5 个 1min 桶）
	// 优势：
	//   - 1 次 cache 读代替 1 次 RoDB percentile_cont（CPU 重） → RoDB CPU 降为 0/min
	//   - cache 已被 sync 包 1min 刷过，不会占 RoDB 连接
	// 劣势：
	//   - 1min 实时性损失（用户在'近 5min'场景可接受）
	rows, err := aggregateChannelHealthFromCache(ctx, startTS, bucketTS)
	if err != nil {
		// cache miss / 异常 → fallback 到原始 RoDB SQL（保证可用性）
		log.Printf("[monitor] cache path failed (%v), fallback to RoDB direct", err)
		rows, err = aggregateChannelHealthRaw(ctx, startTS, bucketTS)
		if err != nil {
			return 0, fmt.Errorf("aggregate 5min (cache+fallback): %w", err)
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	upsert := make([]dal.ChannelHealth5min, 0, len(rows))
	for _, r := range rows {
		upsert = append(upsert, r.toFiveMin())
	}
	if err := dal.UpsertChannelHealth5min(ctx, upsert); err != nil {
		return 0, fmt.Errorf("upsert 5min: %w", err)
	}
	return len(upsert), nil
}

// AggregateChannelHealth1h 对最近 1h 数据 roll 一次，落库到 channel_health_1h
func AggregateChannelHealth1h(ctx context.Context, endTS int64) (int, error) {
	if !dal.HasRoDB() {
		return 0, nil
	}
	bucketTS := floorToBucket(endTS, Bucket1h)
	startTS := bucketTS - Window1h

	rows, err := aggregateChannelHealthFromCache(ctx, startTS, bucketTS)
	if err != nil {
		log.Printf("[monitor] cache path 1h failed (%v), fallback to RoDB direct", err)
		rows, err = aggregateChannelHealthRaw(ctx, startTS, bucketTS)
		if err != nil {
			return 0, fmt.Errorf("aggregate 1h (cache+fallback): %w", err)
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	upsert := make([]dal.ChannelHealth1h, 0, len(rows))
	for _, r := range rows {
		upsert = append(upsert, r.toOneHour())
	}
	if err := dal.UpsertChannelHealth1h(ctx, upsert); err != nil {
		return 0, fmt.Errorf("upsert 1h: %w", err)
	}
	return len(upsert), nil
}

// floorToBucket 把时间戳向下对齐到 bucket 边界
func floorToBucket(ts int64, bucket int64) int64 {
	return (ts / bucket) * bucket
}

// channelAggRow 通用聚合结果（5min / 1h 共用）
type channelAggRow struct {
	ChannelID        int
	BucketTS         int64
	RequestCount     int64
	ErrorCount       int64
	SuccessCount     int64
	PromptTokens     int64
	CompletionTokens int64
	P50LatencyMs     int
	P95LatencyMs     int
	P99LatencyMs     int
	TTFTP95Ms        int
	ErrorRate        float64
	Balance          float64
	Status           string
}

func (r channelAggRow) toFiveMin() dal.ChannelHealth5min {
	return dal.ChannelHealth5min{
		ChannelID:        r.ChannelID,
		BucketTS:         r.BucketTS,
		RequestCount:     r.RequestCount,
		ErrorCount:       r.ErrorCount,
		SuccessCount:     r.SuccessCount,
		PromptTokens:     r.PromptTokens,
		CompletionTokens: r.CompletionTokens,
		P50LatencyMs:     r.P50LatencyMs,
		P95LatencyMs:     r.P95LatencyMs,
		P99LatencyMs:     r.P99LatencyMs,
		TTFTP95Ms:        r.TTFTP95Ms,
		ErrorRate:        r.ErrorRate,
		Balance:          r.Balance,
		Status:           r.Status,
	}
}

func (r channelAggRow) toOneHour() dal.ChannelHealth1h {
	return dal.ChannelHealth1h{
		ChannelID:        r.ChannelID,
		BucketTS:         r.BucketTS,
		RequestCount:     r.RequestCount,
		ErrorCount:       r.ErrorCount,
		SuccessCount:     r.SuccessCount,
		PromptTokens:     r.PromptTokens,
		CompletionTokens: r.CompletionTokens,
		P50LatencyMs:     r.P50LatencyMs,
		P95LatencyMs:     r.P95LatencyMs,
		P99LatencyMs:     r.P99LatencyMs,
		TTFTP95Ms:        r.TTFTP95Ms,
		ErrorRate:        r.ErrorRate,
		Balance:          r.Balance,
		Status:           r.Status,
	}
}

// aggregateChannelHealthFromCache 从 cache_logs_summary_5min 读 N 个 1min 桶，汇总为 5min/1h 健康度
// 设计要点：
//   - 1 SQL 拉区间内所有 1min 桶；client 端按 channel_id SUM/AVG
//   - p50/p95/p99 分位不跨桶合并，取“请求量最大”的那个 1min 桶的分位（近似“5min/1h 内的最忙 1min”）
//   - balance/status 仍走 channels cache 表
//   - 限制：实时性损失 1min（cache 1min tick），接受度：monitor 1min tick 场景 100% 可接受
func aggregateChannelHealthFromCache(ctx context.Context, startTS, bucketTS int64) ([]channelAggRow, error) {
	if dal.OPS == nil {
		return nil, fmt.Errorf("OPS not initialized")
	}

	// 1) 拉区间内所有 1min 桶，1 SQL
	sql := `
SELECT
  channel_id,
  SUM(request_count) AS request_count,
  SUM(error_count) AS error_count,
  SUM(success_count) AS success_count,
  SUM(prompt_tokens) AS prompt_tokens,
  SUM(completion_tokens) AS completion_tokens
FROM cache_logs_summary_5min
WHERE bucket_ts >= ? AND bucket_ts <= ?
  AND channel_id > 0
GROUP BY channel_id`

	type sumRow struct {
		ChannelID        int   `gorm:"column:channel_id"`
		RequestCount     int64 `gorm:"column:request_count"`
		ErrorCount       int64 `gorm:"column:error_count"`
		SuccessCount     int64 `gorm:"column:success_count"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
	}
	var sums []sumRow
	if err := dal.OPS.WithContext(ctx).Raw(sql, startTS, bucketTS).Scan(&sums).Error; err != nil {
		return nil, fmt.Errorf("aggregate cache: %w", err)
	}
	if len(sums) == 0 {
		return nil, nil
	}

	// 2) 拉每个 channel 的所有 1min 桶详情，按 request_count 取最大那个的 p95/p99
	type detailRow struct {
		ChannelID    int   `gorm:"column:channel_id"`
		BucketTS     int64 `gorm:"column:bucket_ts"`
		RequestCount int64 `gorm:"column:request_count"`
		P50Ms        int   `gorm:"column:p50_latency_ms"`
		P95Ms        int   `gorm:"column:p95_latency_ms"`
		P99Ms        int   `gorm:"column:p99_latency_ms"`
		AvgMs        int   `gorm:"column:avg_latency_ms"`
	}
	var details []detailRow
	if err := dal.OPS.WithContext(ctx).
		Table("cache_logs_summary_5min").
		Select("channel_id, bucket_ts, request_count, p50_latency_ms, p95_latency_ms, p99_latency_ms, avg_latency_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ? AND channel_id > 0", startTS, bucketTS).
		Order("channel_id ASC, request_count DESC").
		Scan(&details).Error; err != nil {
		return nil, fmt.Errorf("aggregate cache detail: %w", err)
	}
	// 每个 channel 取第一个（request_count 最大的）
	topPerChannel := make(map[int]detailRow, len(details))
	for _, d := range details {
		if _, ok := topPerChannel[d.ChannelID]; !ok {
			topPerChannel[d.ChannelID] = d
		}
	}

	// 3) 拉 channels 取 balance/status（已 cache）
	chs, err := dal.ListChannels(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	chMap := make(map[int]dal.ChannelMirror, len(chs))
	for _, c := range chs {
		chMap[c.ID] = c
	}

	// 4) 汇总
	out := make([]channelAggRow, 0, len(sums))
	for _, s := range sums {
		if s.RequestCount == 0 {
			continue
		}
		errRate := float64(s.ErrorCount) / float64(s.RequestCount)
		top, hasTop := topPerChannel[s.ChannelID]
		ch, ok := chMap[s.ChannelID]
		balance := 0.0
		statusStr := "enabled"
		if ok {
			balance = ch.Balance
			statusStr = channelStatusStr(ch.Status)
		}
		row := channelAggRow{
			ChannelID:        s.ChannelID,
			BucketTS:         bucketTS,
			RequestCount:     s.RequestCount,
			ErrorCount:       s.ErrorCount,
			SuccessCount:     s.SuccessCount,
			PromptTokens:     s.PromptTokens,
			CompletionTokens: s.CompletionTokens,
			TTFTP95Ms:        0, // ttft 仍需 RoDB（perf_metrics 表），V2 再加
			ErrorRate:        errRate,
			Balance:          balance,
			Status:           statusStr,
		}
		if hasTop {
			row.P50LatencyMs = top.P50Ms
			row.P95LatencyMs = top.P95Ms
			row.P99LatencyMs = top.P99Ms
			// avg 不能 SUM，只能 count 加权（这里简化用 top 桶的 avg，足够反映趋势）
		}
		out = append(out, row)
	}
	return out, nil
}

// aggregateChannelHealthRaw 核心聚合 SQL
// 维度：每个 channel × bucket 内
// 指标：request_count / error_count / success_count / tokens / p50/p95/p99 latency / ttft_p95
// 余额 / 状态：实时从 channels 表取最新
func aggregateChannelHealthRaw(ctx context.Context, startTS, bucketTS int64) ([]channelAggRow, error) {
	// 检查 RO 是否可用
	if dal.RoDB() == nil {
		return nil, nil
	}

	// 1) 聚合 logs（用 percentile_cont 计算延迟分位）
	// 注意：use_time 单位是秒，×1000 转 ms
	sql := `
SELECT
  l.channel_id,
  COUNT(*) AS request_count,
  SUM(CASE WHEN l.type = ? THEN 1 ELSE 0 END) AS error_count,
  SUM(CASE WHEN l.type = ? THEN 1 ELSE 0 END) AS success_count,
  COALESCE(SUM(l.prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM(l.completion_tokens), 0) AS completion_tokens,
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY l.use_time) * 1000, 0)::int AS p50_ms,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY l.use_time) * 1000, 0)::int AS p95_ms,
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY l.use_time) * 1000, 0)::int AS p99_ms
FROM logs l
WHERE l.created_at >= ? AND l.created_at < ?
  AND l.channel_id > 0
GROUP BY l.channel_id
`
	type rawAgg struct {
		ChannelID        int   `gorm:"column:channel_id"`
		RequestCount     int64 `gorm:"column:request_count"`
		ErrorCount       int64 `gorm:"column:error_count"`
		SuccessCount     int64 `gorm:"column:success_count"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
		P50Ms            int   `gorm:"column:p50_ms"`
		P95Ms            int   `gorm:"column:p95_ms"`
		P99Ms            int   `gorm:"column:p99_ms"`
	}
	var raws []rawAgg
	if err := dal.RoDB().WithContext(ctx).Raw(sql,
		dal.LogTypeError, dal.LogTypeConsume, startTS, bucketTS).
		Scan(&raws).Error; err != nil {
		return nil, fmt.Errorf("aggregate logs: %w", err)
	}

	// 2) 取 channels 余额 + 状态
	chs, err := dal.ListChannels(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	chMap := make(map[int]dal.ChannelMirror, len(chs))
	for _, c := range chs {
		chMap[c.ID] = c
	}

	// 3) 组装结果
	out := make([]channelAggRow, 0, len(raws))
	for _, r := range raws {
		if r.RequestCount == 0 {
			continue
		}
		errRate := float64(r.ErrorCount) / float64(r.RequestCount)
		ch, ok := chMap[r.ChannelID]
		balance := 0.0
		statusStr := "enabled"
		if ok {
			balance = ch.Balance
			statusStr = channelStatusStr(ch.Status)
		}
		out = append(out, channelAggRow{
			ChannelID:        r.ChannelID,
			BucketTS:         bucketTS,
			RequestCount:     r.RequestCount,
			ErrorCount:       r.ErrorCount,
			SuccessCount:     r.SuccessCount,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			P50LatencyMs:     r.P50Ms,
			P95LatencyMs:     r.P95Ms,
			P99LatencyMs:     r.P99Ms,
			TTFTP95Ms:        0, // ttft 在 logs 表无字段；perf_metrics 表 join 留到 V2
			ErrorRate:        errRate,
			Balance:          balance,
			Status:           statusStr,
		})
	}
	return out, nil
}

// TickResult 一次 tick 的处理结果
type TickResult struct {
	Buckets5min int
	Buckets1h   int
	Skipped     bool
	Err         error
}

// RunTick 调度器调用的统一入口
//   - endTS 留 0 = now
//   - skipHourly = true 时只跑 5min（每小时 60 次中只 12 次跑 1h）
//   - 内部保证幂等：ON CONFLICT DO UPDATE 覆盖
func RunTick(ctx context.Context, endTS int64, skipHourly bool) TickResult {
	if endTS == 0 {
		endTS = time.Now().Unix()
	}
	if dal.RoDB() == nil {
		return TickResult{Skipped: true, Err: fmt.Errorf("no db available")}
	}

	res := TickResult{}
	n, err := AggregateChannelHealth5min(ctx, endTS)
	if err != nil {
		res.Err = err
		log.Printf("[monitor] 5min aggregate failed: %v", err)
		return res
	}
	res.Buckets5min = n

	if !skipHourly {
		n1h, err := AggregateChannelHealth1h(ctx, endTS)
		if err != nil {
			res.Err = err
			log.Printf("[monitor] 1h aggregate failed: %v", err)
			return res
		}
		res.Buckets1h = n1h
	}
	return res
}

// PingRO 健康检查（调试用）
func PingRO(ctx context.Context) error {
	if dal.RoDB() == nil {
		return fmt.Errorf("RO not initialized")
	}
	sqlDB, err := dal.RoDB().DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// silence unused import gorm
var _ = gorm.ErrRecordNotFound
