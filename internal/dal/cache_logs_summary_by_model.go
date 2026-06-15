// api-ops 缓存：newapi logs 按 model × channel × 1min 桶聚合
//
// 用途：
//   - 替代 billing 对账（GenerateCustomerStatement / GenerateUpstreamStatement / AnalyzeProfit）
//     原实现"5万行 logs in-memory 聚合" + "逐条 CalcLogCost"
//   - 1min 滑窗：每分钟覆盖一桶（按 bucket_ts 对齐到分钟）
//   - 实时性：最差 1min 延迟（billing 是批处理场景，完全可接受）
//
// 数据流：
//
//	newapi logs (RoDB) ──(1min tick)──► cache_logs_summary_by_model_5min (OPS)
//
// 设计要点：
//   - UNIQUE(bucket_ts, channel_id, model_name) → ON CONFLICT DO UPDATE 幂等
//   - vendor_code 在 sync 时通过 channel_vendor_map 映射好（避免读 OPS.channel_vendor_map N 次）
//   - 含 token 拆分：prompt_tokens / completion_tokens / cache_tokens / cache_creation_tokens_5m/1h
//     → billing 算上游成本时直接用 cache 行 × pricing，不再遍历 Other JSON
//   - 边缘字段（image_tokens / audio_* / web_search / file_search）暂不存，量级 <1% 成本影响
//
// 性能对比（用户对账 N 用户 × 30 天）：
//   - 旧：N × 5万行 logs in-memory = N×5万次 CalcLogCost（每条要 parse Other JSON + 查 pricing）
//   - 新：1 SQL 拉 cache 行（≈ N × 几百桶 cache）+ 几百次 CalcLogCost（按 model 加和）
//   - 收益：~100x 加速，月账单场景从 OOM/超时 → 秒级返回
package dal

import (
	"context"
	"time"
)

// LogsSummaryByModel5min logs 1min 摘要缓存（按 channel × model 分组）
type LogsSummaryByModel5min struct {
	ID                    uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	BucketTS              int64     `gorm:"uniqueIndex:idx_ls5bm_ch_model_bucket,priority:3;not null" json:"bucket_ts"` // 对齐到分钟（unix 秒）
	ChannelID             int       `gorm:"uniqueIndex:idx_ls5bm_ch_model_bucket,priority:1;not null;default:0" json:"channel_id"`
	ModelName             string    `gorm:"size:128;uniqueIndex:idx_ls5bm_ch_model_bucket,priority:2;default:''" json:"model_name"`
	VendorCode            string    `gorm:"size:64;index:idx_ls5bm_vendor" json:"vendor_code"` // sync 时通过 channel_vendor_map 映射好
	RequestCount          int64     `gorm:"default:0" json:"request_count"`
	ErrorCount            int64     `gorm:"default:0" json:"error_count"`
	SuccessCount          int64     `gorm:"default:0" json:"success_count"`
	RefundCount           int64     `gorm:"default:0" json:"refund_count"`
	Quota                 int64     `gorm:"default:0" json:"quota"` // 财务：consume+refund 的 quota 加和
	PromptTokens          int64     `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens      int64     `gorm:"default:0" json:"completion_tokens"`
	CacheTokens           int64     `gorm:"default:0" json:"cache_tokens"`                                             // cache read 命中（Claude prompt cache / OpenAI cached）
	CacheCreationTokens5m int64     `gorm:"column:cache_creation_tokens_5m;default:0" json:"cache_creation_tokens_5m"` // Claude 5min TTL 写
	CacheCreationTokens1h int64     `gorm:"column:cache_creation_tokens_1h;default:0" json:"cache_creation_tokens_1h"` // Claude 1h TTL 写（1h 写入价 ≈ 5min 的 1.25x，存两个字段保留定价信息）
	UpdatedAt             time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	CreatedAt             time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (LogsSummaryByModel5min) TableName() string { return "cache_logs_summary_by_model_5min" }

// ===== 写入 =====

// UpsertLogsSummaryByModel 按 (bucket_ts, channel_id, model_name) 幂等写入
func UpsertLogsSummaryByModel(ctx context.Context, rows []LogsSummaryByModel5min) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"bucket_ts", "channel_id", "model_name"},
		[]string{
			"vendor_code",
			"request_count", "error_count", "success_count", "refund_count",
			"quota",
			"prompt_tokens", "completion_tokens",
			"cache_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h",
			"updated_at",
		},
	)).CreateInBatches(rows, 200).Error
}

// ===== 读取 =====

// SummaryByModelQuery 查询参数
type SummaryByModelQuery struct {
	ChannelIDs []int  // 空 = 全部 channel
	VendorCode string // 空 = 全部 vendor
	ModelName  string // 空 = 全部 model
	StartTS    int64  // 包含
	EndTS      int64  // 包含
	Limit      int    // 0 = 不限
}

// ListLogsSummaryByModel 通用查询
// billing 对账场景典型用法：按 channel 集合 + 时间窗拉所有 model 桶
func ListLogsSummaryByModel(ctx context.Context, q SummaryByModelQuery) ([]LogsSummaryByModel5min, error) {
	var rows []LogsSummaryByModel5min
	db := OPS.WithContext(ctx).Model(&LogsSummaryByModel5min{}).Order("bucket_ts ASC")
	if len(q.ChannelIDs) > 0 {
		db = db.Where("channel_id IN ?", q.ChannelIDs)
	}
	if q.VendorCode != "" {
		db = db.Where("vendor_code = ?", q.VendorCode)
	}
	if q.ModelName != "" {
		db = db.Where("model_name = ?", q.ModelName)
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

// ListLogsSummaryByModelByChannel 按 channel 列表拉 + 配套的 (channel_id → vendor_code) 映射
// 便利方法：返回的 map 用于 billing 算 cost 时按 channel 找 pricing
func ListLogsSummaryByModelByChannel(ctx context.Context, channelIDs []int, startTS, endTS int64) ([]LogsSummaryByModel5min, error) {
	return ListLogsSummaryByModel(ctx, SummaryByModelQuery{
		ChannelIDs: channelIDs,
		StartTS:    startTS,
		EndTS:      endTS,
	})
}

// PruneOldSummaryByModel 删除 N 天前的 model 摘要（默认 7 天）
func PruneOldSummaryByModel(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	cutoff := time.Now().Unix() - int64(retentionDays)*86400
	res := OPS.WithContext(ctx).
		Where("bucket_ts < ?", cutoff).
		Delete(&LogsSummaryByModel5min{})
	return res.RowsAffected, res.Error
}
