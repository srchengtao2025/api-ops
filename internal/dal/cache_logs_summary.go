// api-ops 缓存：newapi logs 摘要（由 internal/sync 周期性从 RoDB 同步）
//
// 用途：
//   - 替代 dashboard / monitor / alert / billing 等场景的"大聚合 SQL 直读 RoDB"
//   - 1min 滑窗：每分钟覆盖一桶（bucket_ts 对齐到分钟），monitor 读最近 5 桶做 5min 聚合
//   - 实时性：最差 1min 延迟（满足 monitor 5min 健康度的 1min 误差容忍）
//
// 数据流：
//
//	newapi logs (RoDB) ──(1min tick)──► cache_logs_summary_5min (OPS)
//
// 设计要点：
//   - channel_id=0 是全局汇总（一条/分钟），其余按 channel_id 分组
//   - UNIQUE(channel_id, bucket_ts) → ON CONFLICT DO UPDATE 幂等
//   - 数据量：100 channel × 1440 分钟/天 × 7 天 = ~100w 行（保留 7 天）
package dal

import (
	"context"
	"time"
)

// LogsSummary5min logs 1min 摘要缓存
// 注意：命名沿用 "5min" 是为了与 channel_health_5min 保持语义一致（5min 健康度读 5 个 1min 桶汇总）
type LogsSummary5min struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	ChannelID    int    `gorm:"uniqueIndex:idx_ls5_ch_bucket,priority:1;not null;default:0" json:"channel_id"` // 0=global
	BucketTS     int64  `gorm:"uniqueIndex:idx_ls5_ch_bucket,priority:2;not null" json:"bucket_ts"`            // 对齐到分钟（unix 秒）
	RequestCount int64  `gorm:"default:0" json:"request_count"`
	ErrorCount   int64  `gorm:"default:0" json:"error_count"`
	SuccessCount int64  `gorm:"default:0" json:"success_count"`
	// 财务（仅成功的类型 consume=2 / refund=6 才累加）
	Quota            int64 `gorm:"default:0" json:"quota"`
	PromptTokens     int64 `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens int64 `gorm:"default:0" json:"completion_tokens"`
	// 延迟分位（percentile_cont 1min 窗重算）
	P50LatencyMs int       `gorm:"default:0" json:"p50_latency_ms"`
	P95LatencyMs int       `gorm:"default:0" json:"p95_latency_ms"`
	P99LatencyMs int       `gorm:"default:0" json:"p99_latency_ms"`
	AvgLatencyMs int       `gorm:"default:0" json:"avg_latency_ms"`
	ErrorRate    float64   `gorm:"type:numeric(6,4);default:0" json:"error_rate"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (LogsSummary5min) TableName() string { return "cache_logs_summary_5min" }

// ===== 写入 =====

// UpsertLogsSummary 按 (channel_id, bucket_ts) 幂等写入
func UpsertLogsSummary(ctx context.Context, rows []LogsSummary5min) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"channel_id", "bucket_ts"},
		[]string{
			"request_count", "error_count", "success_count",
			"quota",
			"prompt_tokens", "completion_tokens",
			"p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
			"avg_latency_ms", "error_rate",
			"updated_at",
		},
	)).CreateInBatches(rows, 100).Error
}

// ===== 读取 =====

// SummaryQuery 查询参数
type SummaryQuery struct {
	ChannelID int   // 0 = 任意（不限 channel）；>0 = 精确匹配
	StartTS   int64 // 包含
	EndTS     int64 // 包含
	Limit     int
}

// ListLogsSummary 通用查询
func ListLogsSummary(ctx context.Context, q SummaryQuery) ([]LogsSummary5min, error) {
	var rows []LogsSummary5min
	db := OPS.WithContext(ctx).Model(&LogsSummary5min{}).Order("bucket_ts ASC")
	if q.ChannelID > 0 {
		db = db.Where("channel_id = ?", q.ChannelID)
	}
	if q.StartTS > 0 {
		db = db.Where("bucket_ts >= ?", q.StartTS)
	}
	if q.EndTS > 0 {
		db = db.Where("bucket_ts <= ?", q.EndTS)
	}
	if q.Limit > 0 {
		db = db.Limit(q.Limit)
	}
	return rows, db.Find(&rows).Error
}

// ListLatestGlobalSummary 取全局最新 N 个 1min 桶（monitor/dashboard 算 5min 聚合用）
func ListLatestGlobalSummary(ctx context.Context, count int) ([]LogsSummary5min, error) {
	if count <= 0 {
		count = 5
	}
	var rows []LogsSummary5min
	err := OPS.WithContext(ctx).
		Where("channel_id = 0").
		Order("bucket_ts DESC").
		Limit(count).
		Find(&rows).Error
	return rows, err
}

// ListLatestChannelSummary 取每个 channel 最新 N 个 1min 桶
// 实现：DISTINCT ON 等价（PG 9.x 兼容写法：子查询取每 channel 的 max bucket_ts）
func ListLatestChannelSummary(ctx context.Context, countPerChannel int) (map[int][]LogsSummary5min, error) {
	if countPerChannel <= 0 {
		countPerChannel = 5
	}
	// 1) 取每个 channel 的最新 bucket_ts
	type chBucket struct {
		ChannelID int   `gorm:"column:channel_id"`
		BucketTS  int64 `gorm:"column:bucket_ts"`
	}
	var tops []chBucket
	err := OPS.WithContext(ctx).
		Table("cache_logs_summary_5min").
		Select("channel_id, MAX(bucket_ts) AS bucket_ts").
		Where("channel_id > 0").
		Group("channel_id").
		Find(&tops).Error
	if err != nil {
		return nil, err
	}
	if len(tops) == 0 {
		return map[int][]LogsSummary5min{}, nil
	}
	// 2) 一次性拉所有相关 (channel_id, bucket_ts >= top - (countPerChannel-1)*60) 的行
	var minTS int64 = -1
	for _, t := range tops {
		threshold := t.BucketTS - int64(countPerChannel-1)*60
		if minTS == -1 || threshold < minTS {
			minTS = threshold
		}
	}
	var rows []LogsSummary5min
	err = OPS.WithContext(ctx).
		Where("channel_id > 0 AND bucket_ts >= ?", minTS).
		Order("channel_id ASC, bucket_ts ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	// 3) 按 channel_id 聚合，截断到 countPerChannel
	result := make(map[int][]LogsSummary5min, len(tops))
	bucketSet := make(map[int]map[int64]bool, len(tops))
	for _, t := range tops {
		bucketSet[t.ChannelID] = make(map[int64]bool)
	}
	for _, t := range tops {
		// 从 top 往下取 countPerChannel 个
		for i := 0; i < countPerChannel; i++ {
			bucketSet[t.ChannelID][t.BucketTS-int64(i)*60] = true
		}
	}
	for _, r := range rows {
		if set, ok := bucketSet[r.ChannelID]; ok && set[r.BucketTS] {
			result[r.ChannelID] = append(result[r.ChannelID], r)
		}
	}
	return result, nil
}

// PruneOldSummary 删除 N 天前的摘要（默认 7 天）
// 由调用方在 sync 完成后调度（每天跑 1 次即可）
func PruneOldSummary(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	cutoff := time.Now().Unix() - int64(retentionDays)*86400
	res := OPS.WithContext(ctx).
		Where("bucket_ts < ?", cutoff).
		Delete(&LogsSummary5min{})
	return res.RowsAffected, res.Error
}
