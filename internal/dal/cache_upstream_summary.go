// api-ops 缓存：BILLING v3 上游对账 5min 聚合 (scheduler 5min tick 写)
//
// 用途：
//   - 替代 handler / worker 的"按需 CalcUpstreamStatement 全量重算" → cache miss fallback
//   - 月对账 (1-5 号) 5 vendor 同时重算 → 短时 5 SQL 直读 logs 1.9M 行 尖峰
//   - 新: scheduler 5min tick 预算"本月至今 + 上月" 2 period, 写 cache
//   - 读 (handler / worker / 报表) 优先读 cache, cache miss fallback 实时算
//
// 数据流 (每 5min)：
//
//	newapi logs (RoDB) ──(CalcUpstreamStatement 1 vendor 1 period)──► ops_upstream_summary_5min (OPS)
//
// 设计要点：
//   - PRIMARY KEY: (vendor_code, period_label, ts_bucket) → UNIQUE 幂等 UPSERT
//   - 5 vendor × 2 period × 12 tick/h × 24h = 2880 行/天, 30 天保留 ≈ 86k 行
//   - 实时性: 最差 5min 延迟 (跟监控 cache_logs_summary_5min 模式一致, 满足月对账 1-5 号场景)
package dal

import (
	"context"
	"time"
)

// UpstreamPeriodLabel period 标签 (跟 cache 表 period_label 字段对齐)
type UpstreamPeriodLabel string

const (
	// UpstreamPeriodCurrentMonth 本月至今 (1 号 → now)
	UpstreamPeriodCurrentMonth UpstreamPeriodLabel = "current-month"
	// UpstreamPeriodLastMonth 上月完整 (1 号 → 月末 23:59:59)
	UpstreamPeriodLastMonth UpstreamPeriodLabel = "last-month"
)

// ===== 写入 =====

// UpsertUpstreamSummary 按 (vendor_code, period_label, ts_bucket) 幂等写入
// tsBucket 应对齐到 5min (即 now - (now % 300))
func UpsertUpstreamSummary(ctx context.Context, rows []OpsUpstreamSummary5min) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"vendor_code", "period_label", "ts_bucket"},
		[]string{
			"period_start", "period_end",
			"request_count", "revenue", "cost", "profit",
			"updated_at",
		},
	)).CreateInBatches(rows, 100).Error
}

// ===== 读取 =====

// UpstreamSummaryQuery 查询参数
type UpstreamSummaryQuery struct {
	VendorCode  string              // 必填
	PeriodLabel UpstreamPeriodLabel // 必填 (current-month | last-month)
	// MaxAgeSeconds: 仅返回 bucket 距 now ≤ MaxAgeSeconds 的最新行
	//   = 0  → 不限, 取最新 1 桶 (handler 走这条)
	//   > 0  → 限制 (调用方想做"5min 内才算新鲜")
	MaxAgeSeconds int64
}

// GetLatestUpstreamSummary 取 (vendor_code, period_label) 最新 1 桶 cache
//
// 返回:
//   - row != nil: cache 命中
//   - row == nil: cache miss (该 vendor 还没被 tick 写过, 或表为空)
//   - err != nil: DB 错
func GetLatestUpstreamSummary(ctx context.Context, q UpstreamSummaryQuery) (*OpsUpstreamSummary5min, error) {
	if q.VendorCode == "" || q.PeriodLabel == "" {
		return nil, nil
	}
	db := OPS.WithContext(ctx).
		Where("vendor_code = ? AND period_label = ?", q.VendorCode, string(q.PeriodLabel))
	if q.MaxAgeSeconds > 0 {
		cutoff := time.Now().Add(-time.Duration(q.MaxAgeSeconds) * time.Second)
		db = db.Where("ts_bucket >= ?", cutoff)
	}
	var row OpsUpstreamSummary5min
	if err := db.Order("ts_bucket DESC").Limit(1).First(&row).Error; err != nil {
		// gorm.ErrRecordNotFound → 返 nil (cache miss)
		return nil, nil
	}
	return &row, nil
}

// ListLatestUpstreamSummaryAllVendors 1 次拉所有 vendor × period 最新 1 桶
// handler 概览页 / overview 用: 避免 N 次单查
//
// 返回: map[vendor_code]map[period_label]*OpsUpstreamSummary5min
func ListLatestUpstreamSummaryAllVendors(ctx context.Context, maxAgeSeconds int64) (map[string]map[UpstreamPeriodLabel]*OpsUpstreamSummary5min, error) {
	// 1) 取每个 (vendor, period) 最新 ts_bucket
	type vpKey struct {
		VendorCode  string
		PeriodLabel string
		TSBucket    time.Time
	}
	var tops []vpKey
	db := OPS.WithContext(ctx).
		Table("ops_upstream_summary_5min").
		Select("vendor_code, period_label, MAX(ts_bucket) AS ts_bucket").
		Group("vendor_code, period_label")
	if maxAgeSeconds > 0 {
		cutoff := time.Now().Add(-time.Duration(maxAgeSeconds) * time.Second)
		db = db.Where("ts_bucket >= ?", cutoff)
	}
	if err := db.Find(&tops).Error; err != nil {
		return nil, err
	}
	if len(tops) == 0 {
		return map[string]map[UpstreamPeriodLabel]*OpsUpstreamSummary5min{}, nil
	}
	// 2) 一次拉所有 (vendor, period, ts_bucket) 行
	// 简化: 拉每个 (vendor, period) 最新 1 行
	out := make(map[string]map[UpstreamPeriodLabel]*OpsUpstreamSummary5min, len(tops))
	for _, t := range tops {
		var row OpsUpstreamSummary5min
		err := OPS.WithContext(ctx).
			Where("vendor_code = ? AND period_label = ? AND ts_bucket = ?",
				t.VendorCode, t.PeriodLabel, t.TSBucket).
			First(&row).Error
		if err != nil {
			// 找不到 (并发删除/数据漂移) 跳过
			continue
		}
		m, ok := out[t.VendorCode]
		if !ok {
			m = make(map[UpstreamPeriodLabel]*OpsUpstreamSummary5min, 2)
			out[t.VendorCode] = m
		}
		m[UpstreamPeriodLabel(t.PeriodLabel)] = &row
	}
	return out, nil
}

// PruneOldUpstreamSummary 删除 N 天前的 cache (默认 30 天, 跟 billing_export_tasks 一致)
//
// 由调用方在 scheduler tick 完成后调度 (每天跑 1 次即可)
func PruneOldUpstreamSummary(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	res := OPS.WithContext(ctx).
		Where("ts_bucket < ?", cutoff).
		Delete(&OpsUpstreamSummary5min{})
	return res.RowsAffected, res.Error
}
