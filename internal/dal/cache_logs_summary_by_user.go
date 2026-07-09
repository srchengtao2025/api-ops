// api-ops 缓存: newapi logs 按 user × 1min 桶聚合
//
// 用途 (api-ops 开源版, 2026-07-09 同步自 rezeai-ops):
//   - 客户健康度模块 overview / detail 走 user 维度 48h 聚合
//   - 替代原 handler 端 SELECT FROM logs GROUP BY user_id
//   - 1min tick 预聚合, 避免大数据量场景慢查询
//
// 数据流:
//
//	newapi logs (RoDB) ──(1min tick)──► cache_logs_summary_by_user_5min (OPS)
//
// 跟 rezeai-ops 区别:
//   - api-ops 单站架构, 无 site 字段 (always intl)
//   - 原始 rezeai-ops 7 commit (PR-A 到 PR-G) 加 Site 字段, api-ops 脱敏版去掉
//   - 后续 api-ops 用户接入多站时, 仿照加回 Site 字段即可
//
// 设计要点:
//   - UNIQUE(bucket_ts, user_id) → ON CONFLICT DO UPDATE 幂等
//   - username 在 sync 时通过 user cache 映射好 (避免 handler 端再查)
//   - 含 token 拆分: prompt / completion / cache + cache_creation
//   - 数据量: N user × 1440 分钟/天 × 7 天 = 轻量
package dal

import (
	"context"
	"time"
)

// LogsSummaryByUser5min logs 1min 摘要缓存 (按 user 维度, 客户健康度用)
//
// api-ops 单站版 (无 Site 字段). 多站用户参考 rezeai-ops 加回.
type LogsSummaryByUser5min struct {
	ID                     uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	BucketTS               int64     `gorm:"uniqueIndex:idx_ls5bu_user_bucket,priority:2;not null" json:"bucket_ts"`
	UserID                 int       `gorm:"uniqueIndex:idx_ls5bu_user_bucket,priority:1;not null;default:0" json:"user_id"`
	Username               string    `gorm:"size:64;index:idx_ls5bu_username" json:"username"`
	RequestCount           int64     `gorm:"default:0" json:"request_count"`
	ErrorCount             int64     `gorm:"default:0" json:"error_count"`
	SuccessCount           int64     `gorm:"default:0" json:"success_count"`
	Quota                  int64     `gorm:"default:0" json:"quota"`
	PromptTokens           int64     `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens       int64     `gorm:"default:0" json:"completion_tokens"`
	CacheTokens            int64     `gorm:"default:0" json:"cache_tokens"`
	CacheCreationTokens5m int64     `gorm:"column:cache_creation_tokens_5m;default:0" json:"cache_creation_tokens_5m"`
	CacheCreationTokens1h int64     `gorm:"column:cache_creation_tokens_1h;default:0" json:"cache_creation_tokens_1h"`
	UpdatedAt              time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	CreatedAt              time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (LogsSummaryByUser5min) TableName() string { return "cache_logs_summary_by_user_5min" }

// ===== 写入 =====

// UpsertLogsSummaryByUser 按 (bucket_ts, user_id) 幂等写入
func UpsertLogsSummaryByUser(ctx context.Context, rows []LogsSummaryByUser5min) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"bucket_ts", "user_id"},
		[]string{
			"username",
			"request_count", "error_count", "success_count",
			"quota",
			"prompt_tokens", "completion_tokens",
			"cache_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h",
			"updated_at",
		},
	)).CreateInBatches(rows, 200).Error
}

// ===== 读取 =====

// SummaryByUserQuery 查询参数
//
// api-ops 单站版, 无 Site 字段
type SummaryByUserQuery struct {
	UserID  int   // 0 = 任意; >0 = 精确匹配
	StartTS int64 // 包含
	EndTS   int64 // 包含
	Limit   int
}

// ListLogsSummaryByUser 通用查询 (健康度 detail 典型用法: 按时间窗拉所有 user 桶, handler 端按 user_id 聚合)
func ListLogsSummaryByUser(ctx context.Context, q SummaryByUserQuery) ([]LogsSummaryByUser5min, error) {
	var rows []LogsSummaryByUser5min
	db := OPS.WithContext(ctx).Model(&LogsSummaryByUser5min{}).Order("bucket_ts ASC")
	if q.UserID > 0 {
		db = db.Where("user_id = ?", q.UserID)
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

// AggregateUserSummaryInRange 按 user 维度聚合一段时间窗 (handler 端单 SQL 出结果)
//
// 健康度 overview 专用, 1 SQL 走 cache (OPS PG), 跨整窗秒返
func AggregateUserSummaryInRange(ctx context.Context, startTS, endTS int64) ([]LogsSummaryByUser5min, error) {
	var rows []LogsSummaryByUser5min
	err := OPS.WithContext(ctx).
		Table("cache_logs_summary_by_user_5min").
		Select(`user_id, MAX(username) AS username,
			SUM(request_count) AS request_count,
			SUM(error_count) AS error_count,
			SUM(success_count) AS success_count,
			SUM(quota) AS quota,
			SUM(prompt_tokens) AS prompt_tokens,
			SUM(completion_tokens) AS completion_tokens,
			SUM(cache_tokens) AS cache_tokens,
			SUM(cache_creation_tokens_5m) AS cache_creation_tokens_5m,
			SUM(cache_creation_tokens_1h) AS cache_creation_tokens_1h`).
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTS, endTS).
		Group("user_id").
		Order("quota DESC").
		Limit(1000).
		Scan(&rows).Error
	return rows, err
}

// PruneOldSummaryByUser 删除 N 天前的 user 摘要
func PruneOldSummaryByUser(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	cutoff := time.Now().Unix() - int64(retentionDays)*86400
	res := OPS.WithContext(ctx).
		Where("bucket_ts < ?", cutoff).
		Delete(&LogsSummaryByUser5min{})
	return res.RowsAffected, res.Error
}
