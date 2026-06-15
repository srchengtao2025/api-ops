// api-ops 自有 DB 的 CRUD repo（UpstreamVendor / UpstreamPricing / Import / Statement）
package dal

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ===== UpstreamVendor =====

func CreateVendor(ctx context.Context, v *UpstreamVendor) error {
	return OPS.WithContext(ctx).Create(v).Error
}

func UpdateVendor(ctx context.Context, v *UpstreamVendor) error {
	return OPS.WithContext(ctx).Save(v).Error
}

func DeleteVendor(ctx context.Context, id uint64) error {
	return OPS.WithContext(ctx).Delete(&UpstreamVendor{}, id).Error
}

func ListVendors(ctx context.Context) ([]UpstreamVendor, error) {
	var rows []UpstreamVendor
	err := OPS.WithContext(ctx).Order("code ASC").Find(&rows).Error
	return rows, err
}

func GetVendorByCode(ctx context.Context, code string) (*UpstreamVendor, error) {
	var v UpstreamVendor
	err := OPS.WithContext(ctx).Where("code = ?", code).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}

// ===== UpstreamPricing 已下线 (2026-06-14) =====
// v3 PR #2 之后, cost 反推改用 channel_vendor_map.discount, 价目表 0 引用
// 7 个函数 (UpsertPricing/GetPricingAt/ListPricing/DeletePricing/CreateImport/UpdateImport/GetImport) 全删
// 表移到 archive schema (migrations/2026-06-14-upstream-pricing-archive.sql)

// ===== ChannelVendorMap =====

func UpsertChannelVendor(ctx context.Context, m *ChannelVendorMap) error {
	var existing ChannelVendorMap
	err := OPS.WithContext(ctx).
		Where("channel_id = ? AND vendor_code = ?", m.ChannelID, m.VendorCode).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(m).Error
	}
	if err != nil {
		return err
	}
	m.ID = existing.ID
	return OPS.WithContext(ctx).Save(m).Error
}

func ListChannelVendors(ctx context.Context, channelID int) ([]ChannelVendorMap, error) {
	var rows []ChannelVendorMap
	q := OPS.WithContext(ctx).Order("vendor_code ASC")
	if channelID > 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	err := q.Find(&rows).Error
	return rows, err
}

func DeleteChannelVendor(ctx context.Context, id uint64) error {
	return OPS.WithContext(ctx).Delete(&ChannelVendorMap{}, id).Error
}

// GetChannelVendorByChannelID 1 渠道最多 1 行 (业务 1:1 约束)
func GetChannelVendorByChannelID(ctx context.Context, channelID int) (*ChannelVendorMap, error) {
	var m ChannelVendorMap
	err := OPS.WithContext(ctx).Where("channel_id = ?", channelID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// UpdateChannelVendorDiscount 矫正折扣 (set discount_override=true)
func UpdateChannelVendorDiscount(ctx context.Context, id uint64, discount float64, remark string) error {
	updates := map[string]interface{}{
		"discount":          discount,
		"discount_override": true,
		"updated_at":        gorm.Expr("NOW()"),
	}
	if remark != "" {
		updates["remark"] = remark
	}
	return OPS.WithContext(ctx).Model(&ChannelVendorMap{}).
		Where("id = ?", id).Updates(updates).Error
}

// UpdateChannelVendorAssignment 改 vendor_code (整行删除重建, 因 1:1 关系换供应商 = 换记录)
func UpdateChannelVendorAssignment(ctx context.Context, channelID int, newVendorCode string, discount float64) error {
	// 先查现有
	existing, err := GetChannelVendorByChannelID(ctx, channelID)
	if err != nil {
		return err
	}
	if existing == nil {
		// 创建新记录
		m := &ChannelVendorMap{
			ChannelID:        channelID,
			VendorCode:       newVendorCode,
			Discount:         discount,
			AutoDiscount:     discount,
			AutoRecognized:   true,
			AutoMatched:      "(manual create)",
			DiscountOverride: false,
		}
		return OPS.WithContext(ctx).Create(m).Error
	}
	// 更新现有
	return OPS.WithContext(ctx).Model(&ChannelVendorMap{}).
		Where("id = ?", existing.ID).
		Updates(map[string]interface{}{
			"vendor_code": newVendorCode,
			"discount":    discount,
			"updated_at":  gorm.Expr("NOW()"),
		}).Error
}

// ===== UpstreamPricingImport 已下线 (2026-06-14) =====
// 3 个函数 (CreateImport/UpdateImport/GetImport) 全删
// 表移到 archive schema (migrations/2026-06-14-upstream-pricing-archive.sql)

// ===== BillingStatement =====

func CreateStatement(ctx context.Context, s *BillingStatement) error {
	return OPS.WithContext(ctx).Create(s).Error
}

func CreateStatementLines(ctx context.Context, lines []BillingStatementLine) error {
	if len(lines) == 0 {
		return nil
	}
	// 分批 200 行写入，防止单次 INSERT 过大
	const batch = 200
	for i := 0; i < len(lines); i += batch {
		end := i + batch
		if end > len(lines) {
			end = len(lines)
		}
		if err := OPS.WithContext(ctx).Create(lines[i:end]).Error; err != nil {
			return err
		}
	}
	return nil
}

type StatementQuery struct {
	StatementType string
	SubjectType   string
	SubjectID     string
	PeriodStart   int64
	PeriodEnd     int64
	Status        string
	Limit         int
	Offset        int
}

func ListStatements(ctx context.Context, q StatementQuery) ([]BillingStatement, int64, error) {
	db := OPS.WithContext(ctx).Model(&BillingStatement{})
	if q.StatementType != "" {
		db = db.Where("statement_type = ?", q.StatementType)
	}
	if q.SubjectType != "" {
		db = db.Where("subject_type = ?", q.SubjectType)
	}
	if q.SubjectID != "" {
		db = db.Where("subject_id = ?", q.SubjectID)
	}
	if q.PeriodStart > 0 {
		db = db.Where("period_end >= ?", q.PeriodStart)
	}
	if q.PeriodEnd > 0 {
		db = db.Where("period_start <= ?", q.PeriodEnd)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	var rows []BillingStatement
	err := db.Order("period_start DESC, id DESC").
		Limit(q.Limit).Offset(q.Offset).Find(&rows).Error
	return rows, total, err
}

func GetStatement(ctx context.Context, id uint64) (*BillingStatement, error) {
	var s BillingStatement
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

func GetStatementLines(ctx context.Context, statementID uint64) ([]BillingStatementLine, error) {
	var rows []BillingStatementLine
	err := OPS.WithContext(ctx).Where("statement_id = ?", statementID).
		Order("model_name ASC, channel_id ASC").Find(&rows).Error
	return rows, err
}

func ConfirmStatement(ctx context.Context, id uint64, by string) error {
	now := time.Now()
	return OPS.WithContext(ctx).Model(&BillingStatement{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       "confirmed",
			"confirmed_at": now,
			"confirmed_by": by,
		}).Error
}

func MarkStatementExported(ctx context.Context, id uint64) error {
	now := time.Now()
	return OPS.WithContext(ctx).Model(&BillingStatement{}).
		Where("id = ?", id).
		Update("exported_at", now).Error
}

// ===== AlertRule / AlertHistory（占位，P1 使用） =====

func ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	var rows []AlertRule
	err := OPS.WithContext(ctx).Order("id ASC").Find(&rows).Error
	return rows, err
}

func ListEnabledAlertRules(ctx context.Context) ([]AlertRule, error) {
	var rows []AlertRule
	err := OPS.WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&rows).Error
	return rows, err
}

func GetAlertRule(ctx context.Context, id uint64) (*AlertRule, error) {
	var r AlertRule
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func CreateAlertRule(ctx context.Context, r *AlertRule) error {
	return OPS.WithContext(ctx).Create(r).Error
}

func UpsertAlertRule(ctx context.Context, r *AlertRule) error {
	if r.ID == 0 {
		return OPS.WithContext(ctx).Create(r).Error
	}
	return OPS.WithContext(ctx).Save(r).Error
}

func CreateAlertHistory(ctx context.Context, h *AlertHistory) error {
	return OPS.WithContext(ctx).Create(h).Error
}

// AlertHistoryQuery 告警列表查询
type AlertHistoryQuery struct {
	Status      string
	Severity    string
	SubjectType string
	SubjectID   string
	Limit       int
	Offset      int
}

func ListAlertHistories(ctx context.Context, q AlertHistoryQuery) ([]AlertHistory, int64, error) {
	db := OPS.WithContext(ctx).Model(&AlertHistory{})
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.Severity != "" {
		db = db.Where("severity = ?", q.Severity)
	}
	if q.SubjectType != "" {
		db = db.Where("subject_type = ?", q.SubjectType)
	}
	if q.SubjectID != "" {
		db = db.Where("subject_id = ?", q.SubjectID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	var rows []AlertHistory
	err := db.Order("created_at DESC").
		Limit(q.Limit).Offset(q.Offset).Find(&rows).Error
	return rows, total, err
}

func GetAlertHistory(ctx context.Context, id uint64) (*AlertHistory, error) {
	var h AlertHistory
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &h, err
}

func UpdateAlertHistoryStatus(ctx context.Context, id uint64, status, by string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"status": status,
	}
	switch status {
	case "acknowledged":
		updates["acked_at"] = &now
		updates["acked_by"] = by
	case "resolved":
		updates["resolved_at"] = &now
	}
	return OPS.WithContext(ctx).Model(&AlertHistory{}).
		Where("id = ?", id).Updates(updates).Error
}

// CreateAlertAction 记录一次通知动作
func CreateAlertAction(ctx context.Context, a *AlertAction) error {
	return OPS.WithContext(ctx).Create(a).Error
}

func ListAlertActions(ctx context.Context, alertHistoryID uint64) ([]AlertAction, error) {
	var rows []AlertAction
	err := OPS.WithContext(ctx).Where("alert_history_id = ?", alertHistoryID).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

// ===== ChannelHealth5min / ChannelHealth1h =====

// UpsertChannelHealth5min 按 (channel_id, bucket_ts) 幂等写入
func UpsertChannelHealth5min(ctx context.Context, rows []ChannelHealth5min) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"channel_id", "bucket_ts"},
		[]string{
			"request_count", "error_count", "success_count",
			"prompt_tokens", "completion_tokens",
			"p50_latency_ms", "p95_latency_ms", "p99_latency_ms", "ttftp95_ms",
			"error_rate", "balance", "status", "created_at",
		},
	)).CreateInBatches(rows, 100).Error
}

// UpsertChannelHealth1h 按 (channel_id, bucket_ts) 幂等写入
func UpsertChannelHealth1h(ctx context.Context, rows []ChannelHealth1h) error {
	if len(rows) == 0 {
		return nil
	}
	return OPS.WithContext(ctx).Clauses(OnConflictUpsert(
		[]string{"channel_id", "bucket_ts"},
		[]string{
			"request_count", "error_count", "success_count",
			"prompt_tokens", "completion_tokens",
			"p50_latency_ms", "p95_latency_ms", "p99_latency_ms", "ttftp95_ms",
			"error_rate", "balance", "status", "created_at",
		},
	)).CreateInBatches(rows, 100).Error
}

type ChannelHealthQuery struct {
	ChannelID int
	StartTS   int64
	EndTS     int64
	Limit     int
}

func ListChannelHealth5min(ctx context.Context, q ChannelHealthQuery) ([]ChannelHealth5min, error) {
	var rows []ChannelHealth5min
	db := OPS.WithContext(ctx).Model(&ChannelHealth5min{}).Order("bucket_ts ASC")
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

func ListChannelHealth1h(ctx context.Context, q ChannelHealthQuery) ([]ChannelHealth1h, error) {
	var rows []ChannelHealth1h
	db := OPS.WithContext(ctx).Model(&ChannelHealth1h{}).Order("bucket_ts ASC")
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

// GetChannelHealthLatest 取每个 channel 最新的 5min 桶（dashboard 用）
type ChannelLatestHealth struct {
	ChannelID    int     `gorm:"column:channel_id" json:"channel_id"`
	BucketTS     int64   `gorm:"column:bucket_ts" json:"bucket_ts"`
	RequestCount int64   `gorm:"column:request_count" json:"request_count"`
	ErrorCount   int64   `gorm:"column:error_count" json:"error_count"`
	ErrorRate    float64 `gorm:"column:error_rate" json:"error_rate"`
	P95LatencyMs int     `gorm:"column:p95_latency_ms" json:"p95_latency_ms"`
	Balance      float64 `gorm:"column:balance" json:"balance"`
	Status       string  `gorm:"column:status" json:"status"`
}

func ListLatestChannelHealth(ctx context.Context) ([]ChannelLatestHealth, error) {
	sql := `
SELECT channel_id, bucket_ts, request_count, error_count, error_rate,
       p95_latency_ms, balance, status
FROM channel_health_5min h
WHERE bucket_ts = (
  SELECT MAX(bucket_ts) FROM channel_health_5min WHERE channel_id = h.channel_id
)
ORDER BY channel_id ASC`
	var rows []ChannelLatestHealth
	err := OPS.WithContext(ctx).Raw(sql).Scan(&rows).Error
	return rows, err
}

// Channel24hSummary 24h 内每渠道聚合
// 用于渠道健康页: 只显示 24h 内有业务请求的渠道
type Channel24hSummary struct {
	ChannelID    int     `gorm:"column:channel_id" json:"channel_id"`
	RequestCount int64   `gorm:"column:request_count" json:"request_count"`   // 业务请求总数 (type IN 2,5,6)
	SuccessCount int64   `gorm:"column:success_count" json:"success_count"`   // 业务成功 (type=2)
	ErrorCount   int64   `gorm:"column:error_count" json:"error_count"`       // 独立错误 (type=5 AND use_channel.length=1, 排除中间重试失败)
	ErrorRate    float64 `gorm:"column:error_rate" json:"error_rate"`         // 错误率 = error_count / request_count
	P95LatencyMs int     `gorm:"column:p95_latency_ms" json:"p95_latency_ms"` // 走 channel_health_5min MAX(最新桶)
	LastBucketTS int64   `gorm:"column:last_bucket_ts" json:"last_bucket_ts"`
}

// ListChannel24hSummary 返回 24h 内每渠道业务请求聚合
//
// 口径 (用户决策 2026-06-15 09:43):
//   - 分母 (RequestCount) = SUM(type IN (2, 5, 6)) 业务请求 (消费+系统错误+退款)
//   - 分子 (ErrorCount)   = SUM(type=5 AND use_channel.length = 1) 独立错误
//     (use_channel 长度 > 1 是被 retry 中间失败的, 不算独立错误)
//   - 错误率 (ErrorRate)   = ErrorCount / RequestCount
//   - 延迟 (P95)           = MAX(channel_health_5min.p95_latency_ms) 跨 24h
//
// 性能: RoDB 走 idx_created_at_type 复合索引, 24h 178万行扫 ~6500 行, 7ms 内完成.
// 不会拖垮 DB (用户 2026-06-15 09:43 关注).
func ListChannel24hSummary(ctx context.Context, sinceTS int64) ([]Channel24hSummary, error) {
	if RoDB() == nil {
		// demo 模式无 RoDB, 返空 (前端会显示"近 24h 无活跃渠道")
		return nil, nil
	}
	// Step 1: 从 RoDB logs 算 24h 业务请求 + 错误率
	// idx_created_at_type (created_at, type) 索引完美命中
	// jsonb_array_length + other::jsonb 不走索引, 但 WHERE 已经过滤到 ~6500 行, 无所谓
	sql := `
SELECT
  channel_id,
  SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) AS success_count,
  SUM(CASE WHEN type = 5 AND jsonb_array_length(COALESCE((other::jsonb)->'admin_info'->'use_channel', '[]'::jsonb)) = 1 THEN 1 ELSE 0 END) AS error_count,
  SUM(CASE WHEN type IN (2, 5, 6) THEN 1 ELSE 0 END) AS request_count
FROM logs
WHERE created_at >= ?
  AND type IN (2, 5, 6)
GROUP BY channel_id
HAVING SUM(CASE WHEN type IN (2, 5, 6) THEN 1 ELSE 0 END) > 0`
	var rows []Channel24hSummary
	if err := RoDB().WithContext(ctx).Raw(sql, sinceTS).Scan(&rows).Error; err != nil {
		return nil, err
	}

	// Step 2: P95 走 channel_health_5min (避免 RoDB percentile_cont 慢)
	// 跨 24h MAX(最新一桶的 P95), 简单且快
	p95Map := make(map[int]int, len(rows))
	p95LastBucket := make(map[int]int64, len(rows))
	p95Rows, err := OPS.WithContext(ctx).
		Table("channel_health_5min").
		Select("channel_id, MAX(p95_latency_ms) AS p95, MAX(bucket_ts) AS last_ts").
		Where("bucket_ts >= ?", sinceTS).
		Group("channel_id").
		Rows()
	if err == nil {
		defer p95Rows.Close()
		for p95Rows.Next() {
			var ch int
			var p95 int
			var lastTS int64
			if err := p95Rows.Scan(&ch, &p95, &lastTS); err == nil {
				p95Map[ch] = p95
				p95LastBucket[ch] = lastTS
			}
		}
	}

	// 合并
	for i := range rows {
		rows[i].P95LatencyMs = p95Map[rows[i].ChannelID]
		rows[i].LastBucketTS = p95LastBucket[rows[i].ChannelID]
		// 错误率实时算 (不靠 RoDB 算避免精度问题)
		if rows[i].RequestCount > 0 {
			rows[i].ErrorRate = float64(rows[i].ErrorCount) / float64(rows[i].RequestCount)
		}
	}
	return rows, nil
}

// GetUpstreamVendorByCode 查供应商档案 (名字/联系方式), 给渠道健康页展示
func GetUpstreamVendorByCode(ctx context.Context, code string) (*UpstreamVendor, error) {
	var v UpstreamVendor
	err := OPS.WithContext(ctx).Where("code = ?", code).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}

// ===== AIReport（占位，P3 使用） =====

func CreateAIReport(ctx context.Context, r *AIReport) error {
	return OPS.WithContext(ctx).Create(r).Error
}

func ListAIReports(ctx context.Context, reportType string, limit int) ([]AIReport, error) {
	var rows []AIReport
	q := OPS.WithContext(ctx).Order("id DESC")
	if reportType != "" {
		q = q.Where("report_type = ?", reportType)
	}
	if limit <= 0 {
		limit = 20
	}
	err := q.Limit(limit).Find(&rows).Error
	return rows, err
}

func GetAIReport(ctx context.Context, id uint64) (*AIReport, error) {
	var r AIReport
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

// ===== AIErrorCluster =====

// UpsertAIErrorCluster 按 (pattern, channel_id, model_name) 幂等
func UpsertAIErrorCluster(ctx context.Context, c *AIErrorCluster) error {
	var existing AIErrorCluster
	err := OPS.WithContext(ctx).
		Where("pattern = ? AND channel_id = ? AND model_name = ?", c.Pattern, c.ChannelID, c.ModelName).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	c.ID = existing.ID
	return OPS.WithContext(ctx).Save(c).Error
}

func ListAIErrorClusters(ctx context.Context, limit int) ([]AIErrorCluster, error) {
	var rows []AIErrorCluster
	q := OPS.WithContext(ctx).Order("count DESC, id DESC")
	if limit <= 0 {
		limit = 50
	}
	err := q.Limit(limit).Find(&rows).Error
	return rows, err
}

func GetAIErrorCluster(ctx context.Context, id uint64) (*AIErrorCluster, error) {
	var c AIErrorCluster
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// ===== AIDiagnosis =====

func CreateAIDiagnosis(ctx context.Context, d *AIDiagnosis) error {
	return OPS.WithContext(ctx).Create(d).Error
}

func GetAIDiagnosis(ctx context.Context, id uint64) (*AIDiagnosis, error) {
	var d AIDiagnosis
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &d, err
}

// ===== UserTier =====

func UpsertUserTier(ctx context.Context, u *UserTier) error {
	var existing UserTier
	err := OPS.WithContext(ctx).Where("user_id = ?", u.UserID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(u).Error
	}
	if err != nil {
		return err
	}
	u.UserID = existing.UserID
	u.AssignedAt = existing.AssignedAt
	return OPS.WithContext(ctx).Save(u).Error
}

// ===== TierThreshold =====

func UpsertTierThreshold(ctx context.Context, t *TierThreshold) error {
	var existing TierThreshold
	err := OPS.WithContext(ctx).Where("tier = ?", t.Tier).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(t).Error
	}
	if err != nil {
		return err
	}
	t.ID = existing.ID
	return OPS.WithContext(ctx).Save(t).Error
}

// ===== ErrorKBEntry =====

func UpsertErrorKB(ctx context.Context, e *ErrorKBEntry) error {
	var existing ErrorKBEntry
	err := OPS.WithContext(ctx).
		Where("vendor = ? AND error_code = ?", e.Vendor, e.ErrorCode).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(e).Error
	}
	if err != nil {
		return err
	}
	e.ID = existing.ID
	return OPS.WithContext(ctx).Save(e).Error
}

// ===== SystemConfig =====

func UpsertSystemConfig(ctx context.Context, s *SystemConfig) error {
	var existing SystemConfig
	err := OPS.WithContext(ctx).Where("key = ?", s.Key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OPS.WithContext(ctx).Create(s).Error
	}
	if err != nil {
		return err
	}
	return OPS.WithContext(ctx).Model(&SystemConfig{}).
		Where("key = ?", s.Key).
		Updates(map[string]interface{}{
			"value":       s.Value,
			"description": s.Description,
			"updated_by":  s.UpdatedBy,
		}).Error
}

func GetSystemConfig(ctx context.Context, key string) (*SystemConfig, error) {
	var s SystemConfig
	err := OPS.WithContext(ctx).Where("key = ?", key).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

func ListSystemConfigs(ctx context.Context) ([]SystemConfig, error) {
	var rows []SystemConfig
	err := OPS.WithContext(ctx).Order("key ASC").Find(&rows).Error
	return rows, err
}

// ===== AuditLog =====

func CreateAuditLog(ctx context.Context, e *AuditLog) error {
	return OPS.WithContext(ctx).Create(e).Error
}

type AuditLogQuery struct {
	UserID       uint64
	Username     string
	Action       string
	ResourceType string
	Start        int64
	End          int64
	Limit        int
	Offset       int
}

func ListAuditLogs(ctx context.Context, q AuditLogQuery) ([]AuditLog, int64, error) {
	db := OPS.WithContext(ctx).Model(&AuditLog{})
	if q.UserID > 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.Username != "" {
		db = db.Where("username = ?", q.Username)
	}
	if q.Action != "" {
		db = db.Where("action LIKE ?", q.Action+"%")
	}
	if q.ResourceType != "" {
		db = db.Where("resource_type = ?", q.ResourceType)
	}
	if q.Start > 0 {
		db = db.Where("created_at >= ?", q.Start)
	}
	if q.End > 0 {
		db = db.Where("created_at <= ?", q.End)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	var rows []AuditLog
	err := db.Order("created_at DESC, id DESC").
		Limit(q.Limit).Offset(q.Offset).Find(&rows).Error
	return rows, total, err
}

func GetAuditLog(ctx context.Context, id uint64) (*AuditLog, error) {
	var e AuditLog
	err := OPS.WithContext(ctx).Where("id = ?", id).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &e, err
}

// ===== BillingExportTask (BILLING v2 异步任务, 2026-06-14) =====

// CreateBillingExportTask 创建新任务 (status=pending)
func CreateBillingExportTask(ctx context.Context, t *BillingExportTask) error {
	if t.Status == "" {
		t.Status = "pending"
	}
	return OPS.WithContext(ctx).Create(t).Error
}

// GetBillingExportTaskByTaskID 按 task_id (uuid) 查
func GetBillingExportTaskByTaskID(ctx context.Context, taskID string) (*BillingExportTask, error) {
	var t BillingExportTask
	err := OPS.WithContext(ctx).Where("task_id = ?", taskID).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// UpdateBillingExportTask 改任意字段
func UpdateBillingExportTask(ctx context.Context, t *BillingExportTask) error {
	return OPS.WithContext(ctx).Save(t).Error
}

// CountBillingExportTasksRunningByUser 查某 user_id 已 running 数 (限流用: 每用户 ≤ 2)
func CountBillingExportTasksRunningByUser(ctx context.Context, userID int) (int64, error) {
	var n int64
	err := OPS.WithContext(ctx).
		Model(&BillingExportTask{}).
		Where("user_id = ? AND status = ?", userID, "running").
		Count(&n).Error
	return n, err
}

// ListBillingExportTasks 列表 (任务中心 / 单用户历史)
//
// BILLING v3 (PR #4, 2026-06-14) 加 2 过滤字段:
//   - Kind: "customer" / "upstream" / "" (全部)
//   - VendorCode: v3 上游对账时填, 客户对账任务为空
type BillingExportTaskQuery struct {
	UserID     int    // 0 = 全部
	Kind       string // "" = 全部, "customer" / "upstream"
	VendorCode string // "" = 全部
	Status     string // "" = 全部
	Limit      int
	Offset     int
}

func ListBillingExportTasks(ctx context.Context, q BillingExportTaskQuery) ([]BillingExportTask, int64, error) {
	var rows []BillingExportTask
	var total int64
	db := OPS.WithContext(ctx).Model(&BillingExportTask{})
	if q.UserID > 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.Kind != "" {
		db = db.Where("kind = ?", q.Kind)
	}
	if q.VendorCode != "" {
		db = db.Where("vendor_code = ?", q.VendorCode)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	err := db.Order("created_at DESC").Limit(q.Limit).Offset(q.Offset).Find(&rows).Error
	return rows, total, err
}

// CancelPendingBillingExportTask 取消 pending 任务 (返回受影响行数)
func CancelPendingBillingExportTask(ctx context.Context, taskID, operator string) (int64, error) {
	now := time.Now()
	res := OPS.WithContext(ctx).
		Model(&BillingExportTask{}).
		Where("task_id = ? AND status = ?", taskID, "pending").
		Updates(map[string]interface{}{
			"status":      "cancelled",
			"finished_at": now,
			"error_msg":   "cancelled by " + operator,
		})
	return res.RowsAffected, res.Error
}

// AppendBillingExportTaskLog 写一条进度日志
func AppendBillingExportTaskLog(ctx context.Context, taskID, level, msg string) error {
	log := &BillingExportTaskLog{
		TaskID: taskID,
		Level:  level,
		Msg:    msg,
	}
	return OPS.WithContext(ctx).Create(log).Error
}

// PruneExpiredBillingExportTasks 清理 30 天前任务 (PR #7 调度器调用)
func PruneExpiredBillingExportTasks(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	// 先删 logs (FK 级联) 不会自动触发, 因为没外键约束
	if err := OPS.WithContext(ctx).Where("ts < ?", cutoff).Delete(&BillingExportTaskLog{}).Error; err != nil {
		return 0, err
	}
	res := OPS.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&BillingExportTask{})
	return res.RowsAffected, res.Error
}
