// Package dal: upstream/newapi logs 表的只读数据访问
// 数据源：newapi/model/log.go 中的 Log 结构体
// 注意：本包只允许 SELECT，禁止写
package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// LogMirror 镜像 newapi logs 表的字段（按需读取，不持久化）
// 字段定义参考 newapi/model/log.go:34-56
type LogMirror struct {
	ID               int64  `gorm:"column:id;primaryKey"`
	UserID           int    `gorm:"column:user_id"`
	Username         string `gorm:"column:username"`
	TokenName        string `gorm:"column:token_name"`
	ModelName        string `gorm:"column:model_name"`
	Quota            int64  `gorm:"column:quota"` // 内部额度消耗（最重要的字段）
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
	UseTime          int    `gorm:"column:use_time"` // 耗时（秒）
	IsStream         bool   `gorm:"column:is_stream"`
	// new-api logs 表 DB 列名是 channel_id（不是 channel）。
	// GORM ORM 链式 Where/Find 走 struct tag → 必须显式 column:channel_id，
	// 否则生成的 SQL 是 WHERE channel = ... 触发 "column \"channel\" does not exist"
	ChannelID         int    `gorm:"column:channel_id"`
	TokenID           int    `gorm:"column:token_id"`
	Group             string `gorm:"column:group"` // 计费用 group
	IP                string `gorm:"column:ip"`
	RequestID         string `gorm:"column:request_id"`
	UpstreamRequestID string `gorm:"column:upstream_request_id"`
	CreatedAt         int64  `gorm:"column:created_at"` // unix 秒
	Type              int    `gorm:"column:type"`       // 2=consume, 5=error, 6=refund
	Content           string `gorm:"column:content"`    // 中文摘要 / 错误堆栈
	Other             string `gorm:"column:other"`      // JSON 字符串
}

func (LogMirror) TableName() string { return "logs" }

// LogTypeConsume / Error / Refund 与 newapi/model/log.go:59-67 保持一致
const (
	LogTypeConsume = 2
	LogTypeError   = 5
	LogTypeRefund  = 6
)

// OtherFields 是 Other JSON 里对账要关注的高频字段
type OtherFields struct {
	ModelRatio        float64 `json:"model_ratio"`
	GroupRatio        float64 `json:"group_ratio"`
	UserGroupRatio    float64 `json:"user_group_ratio"`
	CompletionRatio   float64 `json:"completion_ratio"`
	CacheRatio        float64 `json:"cache_ratio"`
	CacheTokens       int     `json:"cache_tokens"`
	ModelPrice        float64 `json:"model_price"`
	IsModelMapped     bool    `json:"is_model_mapped"`
	UpstreamModelName string  `json:"upstream_model_name"`

	// billingexpr 模式（参考 newapi/service/log_info_generate.go:268-284）
	BillingMode string `json:"billing_mode"` // "ratio" | "tiered_expr"
	ExprB64     string `json:"expr_b64"`
	MatchedTier string `json:"matched_tier"`

	// 缓存细分（Claude 5min/1h TTL）
	CacheCreationTokens   int `json:"cache_creation_tokens"`
	CacheCreationTokens5m int `json:"cache_creation_tokens_5m"`
	CacheCreationTokens1h int `json:"cache_creation_tokens_1h"`

	// 多模态
	ImageTokens       int     `json:"image"`
	ImageRatio        float64 `json:"image_ratio"`
	AudioInputTokens  int     `json:"audio_input"`
	AudioOutputTokens int     `json:"audio_output"`
	AudioRatio        float64 `json:"audio_ratio"`

	// 工具调用
	WebSearchCount  int `json:"web_search"`
	FileSearchCount int `json:"file_search"`
}

// ParseOther 解析 Other JSON
func ParseOther(other string) (*OtherFields, error) {
	if other == "" {
		return &OtherFields{}, nil
	}
	of := &OtherFields{}
	if err := json.Unmarshal([]byte(other), of); err != nil {
		return of, fmt.Errorf("parse other json: %w", err)
	}
	return of, nil
}

// LogQuery 通用查询参数
type LogQuery struct {
	StartTime   int64  // unix 秒，包含
	EndTime     int64  // unix 秒，包含
	Username    string // 用户名（精确）
	UserID      int    // 用户 ID
	ChannelID   int    // 渠道 ID
	ModelName   string // 模型名（精确）
	Group       string // 分组
	LogType     int    // 2/5/6，0 表示全部
	RequestID   string // 请求 ID
	OnlySuccess bool   // 仅成功
	OnlyError   bool   // 仅错误
	OnlyRefund  bool   // 仅退款
	Limit       int    // 默认 1000，最大 50000
	Offset      int    // 默认 0
	OrderDesc   bool   // 默认 true（最新在前）
}

func (q *LogQuery) apply(db *gorm.DB) *gorm.DB {
	if q.StartTime > 0 {
		db = db.Where("created_at >= ?", q.StartTime)
	}
	if q.EndTime > 0 {
		db = db.Where("created_at <= ?", q.EndTime)
	}
	if q.Username != "" {
		db = db.Where("username = ?", q.Username)
	}
	if q.UserID > 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.ChannelID > 0 {
		// new-api logs 表 DB 列名是 channel_id（Go struct ChannelId → channel_id）
		// JSON tag 是 "channel"（前端用），但 SQL 必须用 channel_id
		db = db.Where("channel_id = ?", q.ChannelID)
	}
	if q.ModelName != "" {
		db = db.Where("model_name = ?", q.ModelName)
	}
	if q.Group != "" {
		db = db.Where(`"group" = ?`, q.Group)
	}
	if q.LogType > 0 {
		db = db.Where("type = ?", q.LogType)
	}
	if q.RequestID != "" {
		db = db.Where("request_id = ?", q.RequestID)
	}
	switch {
	case q.OnlySuccess:
		db = db.Where("type = ?", LogTypeConsume)
	case q.OnlyError:
		db = db.Where("type = ?", LogTypeError)
	case q.OnlyRefund:
		db = db.Where("type = ?", LogTypeRefund)
	}
	if q.Limit <= 0 {
		q.Limit = 1000
	}
	if q.Limit > 50000 {
		q.Limit = 50000
	}
	db = db.Limit(q.Limit).Offset(q.Offset)
	if q.OrderDesc {
		db = db.Order("id DESC")
	} else {
		db = db.Order("id ASC")
	}
	return db
}

// QueryLogs 通用查询
// A 阶段 (2026-06-14): RoDB 没配时直接返回 ErrNoRoDB, 不再 fallback 到 OPS 影子表
func QueryLogs(ctx context.Context, q LogQuery) ([]LogMirror, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var rows []LogMirror
	err := q.apply(RoDB().WithContext(ctx)).Find(&rows).Error
	return rows, err
}

// CountLogs 计数（不带 limit/offset）
func CountLogs(ctx context.Context, q LogQuery) (int64, error) {
	if !HasRoDB() {
		return 0, ErrNoRoDB
	}
	var n int64
	q.Limit = 0
	q.Offset = 0
	err := q.apply(RoDB().WithContext(ctx)).Model(&LogMirror{}).Count(&n).Error
	return n, err
}

// UserAggregate 用户聚合（按时间窗）
type UserAggregate struct {
	UserID         int     `gorm:"column:user_id"`
	Username       string  `gorm:"column:username"`
	Group          string  `gorm:"column:group"`
	RequestCount   int64   `gorm:"column:request_count"`
	SuccessCount   int64   `gorm:"column:success_count"`
	ErrorCount     int64   `gorm:"column:error_count"`
	RefundCount    int64   `gorm:"column:refund_count"`
	PromptTokens   int64   `gorm:"column:prompt_tokens"`
	CompTokens     int64   `gorm:"column:completion_tokens"`
	Quota          int64   `gorm:"column:quota"`        // 客户实付 (内部 quota)
	CacheTokens    int64   `gorm:"column:cache_tokens"` // 从 Other 解析汇总
	CacheCostQuota int64   `gorm:"column:cache_cost_quota"`
	TotalLatencyMs int64   `gorm:"column:total_latency_ms"`
	AvgLatencyMs   float64 `gorm:"column:avg_latency_ms"`
}

// AggregateByUser 按用户聚合（带 cache 等 Other 字段的特殊处理）
// 由于 Other 是 JSON，PG 可以用 jsonb_extract_path_text; newapi 这里是 TEXT, 我们用 application-level 二次处理
// 这里只做主表聚合（不含 cache），cache 需要在业务层 Read Other 后累加
func AggregateByUser(ctx context.Context, q LogQuery) ([]UserAggregate, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var rows []UserAggregate
	sql := `
  SELECT user_id, username, "group",
  COUNT(*) AS request_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS refund_count,
  COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens),0) AS completion_tokens,
  COALESCE(SUM(CASE WHEN type IN (?, ?) THEN quota ELSE 0 END),0) AS quota,
  COALESCE(SUM(use_time),0) AS total_latency_ms,
  COALESCE(AVG(use_time),0) AS avg_latency_ms
  FROM logs WHERE 1=1`
	args := []any{LogTypeConsume, LogTypeError, LogTypeRefund, LogTypeConsume, LogTypeRefund}
	if q.StartTime > 0 {
		sql += " AND created_at >= ?"
		args = append(args, q.StartTime)
	}
	if q.EndTime > 0 {
		sql += " AND created_at <= ?"
		args = append(args, q.EndTime)
	}
	sql += ` GROUP BY user_id, username, "group" ORDER BY quota DESC`
	err := RoDB().WithContext(ctx).Raw(sql, args...).Scan(&rows).Error
	return rows, err
}

// ModelAggregate 模型聚合
type ModelAggregate struct {
	ModelName    string  `gorm:"column:model_name"`
	RequestCount int64   `gorm:"column:request_count"`
	SuccessCount int64   `gorm:"column:success_count"`
	ErrorCount   int64   `gorm:"column:error_count"`
	Quota        int64   `gorm:"column:quota"`
	PromptTokens int64   `gorm:"column:prompt_tokens"`
	CompTokens   int64   `gorm:"column:completion_tokens"`
	AvgLatencyMs float64 `gorm:"column:avg_latency_ms"`
}

// AggregateByModel 按模型聚合
func AggregateByModel(ctx context.Context, q LogQuery) ([]ModelAggregate, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var rows []ModelAggregate
	sql := `
  SELECT model_name,
  COUNT(*) AS request_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_count,
  COALESCE(SUM(CASE WHEN type IN (?, ?) THEN quota ELSE 0 END),0) AS quota,
  COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens),0) AS completion_tokens,
  COALESCE(AVG(use_time),0) AS avg_latency_ms
  FROM logs WHERE 1=1`
	args := []any{LogTypeConsume, LogTypeError, LogTypeConsume, LogTypeRefund}
	if q.StartTime > 0 {
		sql += " AND created_at >= ?"
		args = append(args, q.StartTime)
	}
	if q.EndTime > 0 {
		sql += " AND created_at <= ?"
		args = append(args, q.EndTime)
	}
	sql += ` GROUP BY model_name ORDER BY quota DESC`
	err := RoDB().WithContext(ctx).Raw(sql, args...).Scan(&rows).Error
	return rows, err
}

// ChannelAggregate 渠道聚合
// ChannelID 映射列名必须 channel_id（与 new-api logs 表一致）
type ChannelAggregate struct {
	ChannelID    int     `gorm:"column:channel_id"`
	RequestCount int64   `gorm:"column:request_count"`
	SuccessCount int64   `gorm:"column:error_count"`
	Quota        int64   `gorm:"column:quota"`
	PromptTokens int64   `gorm:"column:prompt_tokens"`
	CompTokens   int64   `gorm:"column:completion_tokens"`
	AvgLatencyMs float64 `gorm:"column:avg_latency_ms"`
}

// AggregateByChannel 按渠道聚合
func AggregateByChannel(ctx context.Context, q LogQuery) ([]ChannelAggregate, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var rows []ChannelAggregate
	// new-api logs 表 DB 列名是 channel_id，直接 SELECT channel_id
	sql := `
  SELECT channel_id,
  COUNT(*) AS request_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_count,
  SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_count,
  COALESCE(SUM(CASE WHEN type IN (?, ?) THEN quota ELSE 0 END),0) AS quota,
  COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
  COALESCE(SUM(completion_tokens),0) AS completion_tokens,
  COALESCE(AVG(use_time),0) AS avg_latency_ms
  FROM logs WHERE 1=1`
	args := []any{LogTypeConsume, LogTypeError, LogTypeConsume, LogTypeRefund}
	if q.StartTime > 0 {
		sql += " AND created_at >= ?"
		args = append(args, q.StartTime)
	}
	if q.EndTime > 0 {
		sql += " AND created_at <= ?"
		args = append(args, q.EndTime)
	}
	sql += ` GROUP BY channel_id ORDER BY quota DESC`
	err := RoDB().WithContext(ctx).Raw(sql, args...).Scan(&rows).Error
	return rows, err
}

// DistinctUsernames 列出某时间段内有日志的用户
func DistinctUsernames(ctx context.Context, startTime, endTime int64) ([]string, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var names []string
	err := RoDB().WithContext(ctx).
		Model(&LogMirror{}).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Distinct("username").
		Order("username").
		Pluck("username", &names).Error
	return names, err
}

// DistinctModels 列出某时间段内出现过的模型
func DistinctModels(ctx context.Context, startTime, endTime int64) ([]string, error) {
	if !HasRoDB() {
		return nil, ErrNoRoDB
	}
	var models []string
	err := RoDB().WithContext(ctx).
		Model(&LogMirror{}).
		Where("created_at >= ? AND created_at <= ? AND model_name <> ''", startTime, endTime).
		Distinct("model_name").
		Order("model_name").
		Pluck("model_name", &models).Error
	return models, err
}

// MustUnixDay 把 unix 秒归一到当天 00:00:00 UTC+8
func MustUnixDay(t int64) int64 {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Unix(t, 0).In(loc).Truncate(24 * time.Hour).Unix()
}

// DateRange 生成 [start, end] 闭区间每一天的 unix 起止（按上海时区）
func DateRange(startUnix, endUnix int64) []DayRange {
	startDay := MustUnixDay(startUnix)
	endDay := MustUnixDay(endUnix)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	var days []DayRange
	for d := startDay; d <= endDay; d += 86400 {
		next := d + 86400 - 1 // 当天 23:59:59
		days = append(days, DayRange{
			Day:     d,
			Date:    time.Unix(d, 0).In(loc).Format("2006-01-02"),
			StartTs: d,
			EndTs:   next,
		})
	}
	return days
}

// DayRange 单日时间窗
type DayRange struct {
	Day     int64  `json:"day"`  // 起始 unix
	Date    string `json:"date"` // YYYY-MM-DD
	StartTs int64  `json:"start_ts"`
	EndTs   int64  `json:"end_ts"`
}

// InClause 把 []string 拼成 SQL IN (...) 子句（防止注入）
func InClause(vals []string) string {
	if len(vals) == 0 {
		return "''"
	}
	quoted := make([]string, 0, len(vals))
	for _, v := range vals {
		// 转义单引号
		v = strings.ReplaceAll(v, "'", "''")
		quoted = append(quoted, "'"+v+"'")
	}
	return strings.Join(quoted, ",")
}
