// BILLING v2 账单生成器 (2026-06-14 RFC PR #3)
//
// 4 token 字段 × 2 维度 (按天/按模型) 聚合 SQL
// 数据源: RoDB 直连 (per RFC §1.5)
//
// 安全:
//   - 5s statement timeout (PR #7 加 ctx.WithTimeout)
//   - LIMIT 100000 防全表扫 (单账号 1 月最多 5w 行)
//   - 4 索引已建 (idx_logs_user_id, idx_logs_created_at 等)
package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// StatementSummary 账单汇总 (4 token + USD)
//
// newapi logs 实际字段 (PR #8 部署发现):
//   - prompt_tokens: 标准列
//   - completion_tokens: 标准列
//   - cache_tokens: 单字段, 从 other JSON 提取 (Anthropic prompt caching 统一存)
//
// 注: 之前 PR #3 假设 cache_creation_tokens / cache_read_tokens 拆开 (Anthropic 标准),
//
//	但 newapi 没拆, 统一存 other->>'cache_tokens'. 这里改回单字段.
type StatementSummary struct {
	PeriodStart      int64   `json:"period_start"`
	PeriodEnd        int64   `json:"period_end"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	RevenueUSD       float64 `json:"revenue_usd"`
	RequestCount     int64   `json:"request_count"`
}

// StatementByDay 按天聚合行
type StatementByDay struct {
	Date             string  `json:"date"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	RevenueUSD       float64 `json:"revenue_usd"`
	RequestCount     int64   `json:"request_count"`
}

// StatementByModel 按模型聚合行
type StatementByModel struct {
	ModelName        string  `json:"model_name"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	RevenueUSD       float64 `json:"revenue_usd"`
	RequestCount     int64   `json:"request_count"`
}

// FullStatement 完整账单数据 (汇总 + 按天 + 按模型)
type FullStatement struct {
	UserID      int                `json:"user_id"`
	Username    string             `json:"username"`
	Period      string             `json:"period"` // '2026-05'
	Summary     StatementSummary   `json:"summary"`
	ByDay       []StatementByDay   `json:"by_day"`
	ByModel     []StatementByModel `json:"by_model"`
	GeneratedAt int64              `json:"generated_at"`
}

// StatementQueryParams 账单查询参数
type StatementQueryParams struct {
	UserID  int
	StartTS int64
	EndTS   int64 // 不含 (即 [StartTS, EndTS) 区间)
}

// PeriodBounds 把 'YYYY-MM' 转 unix 秒 [start, end)
//
// 例子: "2026-05" → 2026-05-01 00:00:00 UTC ~ 2026-06-01 00:00:00 UTC
// 注意: 跟用户业务习惯对齐, 上月 = 上个自然月 (不是 30 天滚动)
func PeriodBounds(period string) (int64, int64, error) {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid period (want YYYY-MM): %w", err)
	}
	start := t.Unix()
	end := t.AddDate(0, 1, 0).Unix()
	return start, end, nil
}

// QueryStatement 直连 RoDB 拉完整账单数据
//
// 3 个 SQL:
//  1. 汇总 (4 token + USD)
//  2. 按天 GROUP BY DATE
//  3. 按模型 GROUP BY model_name
//
// LIMIT 100000 兜底, 单账号 1 月通常 5w 行内
func QueryStatement(ctx context.Context, params StatementQueryParams) (*FullStatement, error) {
	if !dal.HasRoDB() {
		return nil, fmt.Errorf("RoDB not configured (set API_OPS_RO_DSN)")
	}
	if params.EndTS <= params.StartTS {
		return nil, fmt.Errorf("end_ts must be > start_ts")
	}

	// 1) 汇总
	var sum StatementSummary
	sum.PeriodStart = params.StartTS
	sum.PeriodEnd = params.EndTS
	summarySQL := `
SELECT
  COUNT(*) AS request_count,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM((other::jsonb->>'cache_tokens')::bigint), 0) AS cache_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  
  
  COALESCE(SUM(quota), 0) / 500000.0 AS revenue_usd
FROM logs
WHERE user_id = ? AND created_at >= ? AND created_at < ?
  AND type = 2
LIMIT 100000`
	row := dal.RoDB().WithContext(ctx).Raw(summarySQL, params.UserID, params.StartTS, params.EndTS).Row()
	if err := row.Scan(
		&sum.RequestCount,
		&sum.PromptTokens,
		&sum.CompletionTokens,
		&sum.CacheTokens,

		&sum.RevenueUSD,
	); err != nil {
		return nil, fmt.Errorf("summary query failed: %w", err)
	}

	// 2) 按天
	byDaySQL := `
SELECT
  TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM-DD') AS day,
  COUNT(*) AS request_count,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM((other::jsonb->>'cache_tokens')::bigint), 0) AS cache_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  
  
  COALESCE(SUM(quota), 0) / 500000.0 AS revenue_usd
FROM logs
WHERE user_id = ? AND created_at >= ? AND created_at < ?
  AND type = 2
GROUP BY day
ORDER BY day ASC
LIMIT 100000`
	byDayRows, err := dal.RoDB().WithContext(ctx).Raw(byDaySQL, params.UserID, params.StartTS, params.EndTS).Rows()
	if err != nil {
		return nil, fmt.Errorf("by_day query failed: %w", err)
	}
	defer byDayRows.Close()
	var byDay []StatementByDay
	for byDayRows.Next() {
		var r StatementByDay
		if err := byDayRows.Scan(
			&r.Date, &r.RequestCount,
			&r.PromptTokens, &r.CompletionTokens,
			&r.CacheTokens,
			&r.RevenueUSD,
		); err != nil {
			return nil, fmt.Errorf("by_day scan failed: %w", err)
		}
		byDay = append(byDay, r)
	}

	// 3) 按模型
	byModelSQL := `
SELECT
  model_name,
  COUNT(*) AS request_count,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM((other::jsonb->>'cache_tokens')::bigint), 0) AS cache_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  
  
  COALESCE(SUM(quota), 0) / 500000.0 AS revenue_usd
FROM logs
WHERE user_id = ? AND created_at >= ? AND created_at < ?
  AND type = 2
GROUP BY model_name
ORDER BY revenue_usd DESC
LIMIT 100000`
	byModelRows, err := dal.RoDB().WithContext(ctx).Raw(byModelSQL, params.UserID, params.StartTS, params.EndTS).Rows()
	if err != nil {
		return nil, fmt.Errorf("by_model query failed: %w", err)
	}
	defer byModelRows.Close()
	var byModel []StatementByModel
	for byModelRows.Next() {
		var r StatementByModel
		if err := byModelRows.Scan(
			&r.ModelName, &r.RequestCount,
			&r.PromptTokens, &r.CompletionTokens,
			&r.CacheTokens,
			&r.RevenueUSD,
		); err != nil {
			return nil, fmt.Errorf("by_model scan failed: %w", err)
		}
		byModel = append(byModel, r)
	}

	username := ""
	if u, _ := dal.GetUser(ctx, params.UserID); u != nil {
		username = u.Username
	}
	return &FullStatement{
		UserID:      params.UserID,
		Username:    username,
		Period:      time.Unix(params.StartTS, 0).UTC().Format("2006-01"),
		Summary:     sum,
		ByDay:       byDay,
		ByModel:     byModel,
		GeneratedAt: time.Now().Unix(),
	}, nil
}
