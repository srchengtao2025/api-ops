// 成本反推 + 上游对账聚合 (BILLING v3 上游对账用, PR #2 / 7, 2026-06-14)
//
// 公式 (RFC §2):
//  1. revenue (消耗)    = log.quota / 500000
//  2. group_ratio       = (log.other::jsonb->>'group_ratio')::numeric (默认 1.0)
//  3. 原价               = revenue / group_ratio
//  4. cost (累计成本)   = 原价 × channel_vendor_map.discount
//  5. profit_margin     = (revenue - cost) / cost
//
// 例 (user_alpha, group=mu-aws, group_ratio=0.64, 调 ch-2 provider_alpha discount=0.24):
//
//	quota=50000 → revenue=$0.1
//	原价 = $0.1 / 0.64 = $0.15625
//	cost  = $0.15625 × 0.24 = $0.0375
//	margin = ($0.1 - $0.0375) / $0.0375 = 166.7%
package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// UpstreamStatement 1 个 vendor 的对账单 (v3 RFC §2)
type UpstreamStatement struct {
	VendorCode  string `json:"vendor_code"`
	VendorName  string `json:"vendor_name"`
	PeriodStart int64  `json:"period_start"`
	PeriodEnd   int64  `json:"period_end"`

	// 汇总
	TotalRequestCount int64   `json:"total_request_count"`
	TotalRevenue      float64 `json:"total_revenue"`              // 客户消耗 USD
	TotalCost         float64 `json:"total_cost"`                 // 上游成本 USD (反推)
	TotalProfit       float64 `json:"total_profit"`               // revenue - cost
	ProfitRate        float64 `json:"profit_rate"`                // profit / cost (不是 / revenue, 财务看 "赚几倍")
	UnmatchedReason   string  `json:"unmatched_reason,omitempty"` // missing_pricing 等

	// 3 维度拆分
	ByDate    []UpstreamByDate    `json:"by_date"`
	ByChannel []UpstreamByChannel `json:"by_channel"`
	ByModel   []UpstreamByModel   `json:"by_model"`
}

// UpstreamByDate 按日期聚合
type UpstreamByDate struct {
	Date             string  `json:"date"` // YYYY-MM-DD
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"` // 单字段, 跟 v2 一致
	TotalCost        float64 `json:"total_cost"`
	TotalRevenue     float64 `json:"total_revenue"`
}

// UpstreamByChannel 按渠道聚合
type UpstreamByChannel struct {
	ChannelID        int64   `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	TotalCost        float64 `json:"total_cost"`
	TotalRevenue     float64 `json:"total_revenue"`
}

// UpstreamByModel 按模型聚合
type UpstreamByModel struct {
	ModelName        string  `json:"model_name"`
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	TotalCost        float64 `json:"total_cost"`
	TotalRevenue     float64 `json:"total_revenue"`
}

// UpstreamChannelDiscount 渠道折扣 (从 OPS.channel_vendor_map 拿)
type UpstreamChannelDiscount struct {
	ChannelID  int64   `json:"channel_id"`
	VendorCode string  `json:"vendor_code"`
	Discount   float64 `json:"discount"`
}

// UpstreamOverviewVendor 上游对账概览: 单 vendor 聚合 (handler overview / cache 用)
//
// 字段语义: vendor 维度汇总 + per-channel breakdown
// 数据源: cache hit → ops_upstream_summary_5min; cache miss → CalcUpstreamStatement.ByChannel
//
// PR #9 (2026-06-15): 从 internal/api 移到 internal/billing,
// 让 GetUpstreamOverviewCached (cache-aware) 能直接返回, handler 渲染时直接 map.
type UpstreamOverviewVendor struct {
	VendorCode   string                    `json:"vendor_code"`
	VendorName   string                    `json:"vendor_name"`
	RequestCount int64                     `json:"request_count"`
	TotalCost    float64                   `json:"total_cost"`
	TotalRevenue float64                   `json:"total_revenue"`
	TotalProfit  float64                   `json:"total_profit"`
	ProfitRate   float64                   `json:"profit_rate"`
	Channels     []UpstreamOverviewChannel `json:"channels"`
}

// UpstreamOverviewChannel 上游对账概览: 单 channel 聚合
type UpstreamOverviewChannel struct {
	ChannelID    int64   `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	Discount     float64 `json:"discount"`
	RequestCount int64   `json:"request_count"`
	TotalCost    float64 `json:"total_cost"`
	TotalRevenue float64 `json:"total_revenue"`
	TotalProfit  float64 `json:"total_profit"`
	ProfitRate   float64 `json:"profit_rate"`
}

// CalcLogCost 单 log 成本反推 (USD)
//
// 入参:
//   - quota: 内部额度 (newapi 内部 1/500000 = 1 USD)
//   - groupRatio: log.other->>'group_ratio', 默认 1.0
//   - channelDiscount: channel_vendor_map.discount (0-1)
//
// 出参: 累计成本 USD
func CalcLogCost(quota int64, groupRatio float64, channelDiscount float64) float64 {
	if quota <= 0 || channelDiscount <= 0 {
		return 0
	}
	if groupRatio <= 0 {
		groupRatio = 1.0 // 防御: 0 会除零
	}
	// 1. revenue (消耗 USD)
	revenue := float64(quota) / 500000.0
	// 2. 原价 (revenue / group_ratio)
	originalPrice := revenue / groupRatio
	// 3. 成本 = 原价 × 渠道折扣
	return originalPrice * channelDiscount
}

// CalcProfitMargin 利润率 = (消耗 - 成本) / 成本
//
// 返回 ratio (不是 %), 例: 0.5 = 50% 利润率, 2.0 = 200% 利润率
// cost <= 0 时返 0 (避免除零)
func CalcProfitMargin(revenue, cost float64) float64 {
	if cost <= 0 {
		return 0
	}
	return (revenue - cost) / cost
}

// IsImageGenerationModel 复用 v1 R2: 标记图片/视频生成类 model
// 跟 archive/v1-docs/BILLING-RULES.md §3 规则一致
func IsImageGenerationModel(modelName string) bool {
	if modelName == "" {
		return false
	}
	keywords := []string{"image", "sora", "midjourney", "mj-", "dalle"}
	lower := toLowerASCII(modelName)
	for _, k := range keywords {
		if containsSubstring(lower, k) {
			return true
		}
	}
	return false
}

func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b[i] = s[i] + 32
		} else {
			b[i] = s[i]
		}
	}
	return string(b)
}

func containsSubstring(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// loadChannelDiscounts 从 OPS 拿所有 (channel_id -> discount) 映射
func loadChannelDiscounts(ctx context.Context) (map[int64]float64, error) {
	mappings, err := dal.ListChannelVendors(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list channel vendors: %w", err)
	}
	out := make(map[int64]float64, len(mappings))
	for _, m := range mappings {
		out[int64(m.ChannelID)] = m.Discount
	}
	return out, nil
}

// CalcUpstreamStatement 给 1 个 vendor 生成上游对账单 (1 SQL + server 聚合)
//
// 流程:
//  1. RoDB 拉 period 范围内, channel_id IN (vendor 旗下) 的 logs
//  2. server 端 GROUP BY date/channel/model
//  3. 每个 log 用 CalcLogCost 反推 cost
//  4. 算汇总 + 3 维度
func CalcUpstreamStatement(ctx context.Context, vendorCode string, periodStart, periodEnd int64) (*UpstreamStatement, error) {
	if !dal.HasRoDB() {
		return nil, dal.ErrNoRoDB
	}

	// 1. 拿 vendor 旗下 channel_ids
	mappings, err := dal.ListChannelVendors(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list channel vendors: %w", err)
	}
	channelSet := make(map[int64]string)
	for _, m := range mappings {
		if m.VendorCode == vendorCode {
			channelSet[int64(m.ChannelID)] = vendorCode
		}
	}
	if len(channelSet) == 0 {
		return &UpstreamStatement{
			VendorCode: vendorCode, VendorName: vendorCode,
			PeriodStart: periodStart, PeriodEnd: periodEnd,
			UnmatchedReason: "no_channels",
		}, nil
	}

	channelDiscounts, err := loadChannelDiscounts(ctx)
	if err != nil {
		return nil, err
	}

	// 2. 拿 channel_id 列表
	channelIDs := make([]int64, 0, len(channelSet))
	for id := range channelSet {
		channelIDs = append(channelIDs, id)
	}

	// 3. RoDB 拉 logs
	type logRow struct {
		ID               int64
		ChannelID        int64
		ChannelName      string
		ModelName        string
		PromptTokens     int64
		CompletionTokens int64
		Quota            int64
		Other            string
		CreatedAt        int64
	}
	var logs []logRow
	if err := dal.RoDB().WithContext(ctx).Table("logs").
		Select("id, channel_id, channel_name, model_name, prompt_tokens, completion_tokens, quota, other, created_at").
		Where("type = ? AND created_at >= ? AND created_at <= ? AND channel_id IN ?",
			dal.LogTypeConsume, periodStart, periodEnd, channelIDs).
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}

	stmt := &UpstreamStatement{
		VendorCode:  vendorCode,
		VendorName:  vendorCode, // vendor 详情在另一个表 (upstream_vendors), 后续 PR 加
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
	}

	// 4. 聚合 3 维度
	byDate := make(map[string]*UpstreamByDate)
	byChannel := make(map[int64]*UpstreamByChannel)
	byModel := make(map[string]*UpstreamByModel)
	hasUnmatched := false

	for i := range logs {
		row := &logs[i]
		stmt.TotalRequestCount++

		// 反推 group_ratio (from other JSON)
		groupRatio := 1.0
		other, _ := dal.ParseOther(row.Other)
		if other != nil && other.GroupRatio > 0 {
			groupRatio = other.GroupRatio
		}

		// 渠道折扣
		discount, ok := channelDiscounts[row.ChannelID]
		if !ok || discount <= 0 {
			discount = 1.0
			hasUnmatched = true
		}

		// 单 log cost
		cost := CalcLogCost(row.Quota, groupRatio, discount)
		revenue := float64(row.Quota) / 500000.0
		stmt.TotalCost += cost
		stmt.TotalRevenue += revenue

		// 拆 cache tokens (跟 v2 一致, 用 log.other JSON 里的 cache_tokens)
		var cacheTokens int64
		if other != nil {
			cacheTokens = int64(other.CacheTokens)
		}

		// 按日期
		date := time.Unix(row.CreatedAt, 0).UTC().Format("2006-01-02")
		d, ok := byDate[date]
		if !ok {
			d = &UpstreamByDate{Date: date}
			byDate[date] = d
		}
		d.RequestCount++
		d.PromptTokens += row.PromptTokens
		d.CompletionTokens += row.CompletionTokens
		d.CacheTokens += cacheTokens
		d.TotalCost += cost
		d.TotalRevenue += revenue

		// 按渠道
		c, ok := byChannel[row.ChannelID]
		if !ok {
			c = &UpstreamByChannel{
				ChannelID:   row.ChannelID,
				ChannelName: row.ChannelName,
			}
			byChannel[row.ChannelID] = c
		}
		c.RequestCount++
		c.PromptTokens += row.PromptTokens
		c.CompletionTokens += row.CompletionTokens
		c.CacheTokens += cacheTokens
		c.TotalCost += cost
		c.TotalRevenue += revenue

		// 按模型
		modelName := row.ModelName
		if modelName == "" {
			modelName = "[unknown]"
		}
		m, ok := byModel[modelName]
		if !ok {
			m = &UpstreamByModel{ModelName: modelName}
			byModel[modelName] = m
		}
		m.RequestCount++
		m.PromptTokens += row.PromptTokens
		m.CompletionTokens += row.CompletionTokens
		m.CacheTokens += cacheTokens
		m.TotalCost += cost
		m.TotalRevenue += revenue
	}

	stmt.TotalProfit = stmt.TotalRevenue - stmt.TotalCost
	if stmt.TotalCost > 0 {
		stmt.ProfitRate = stmt.TotalProfit / stmt.TotalCost
	}
	if hasUnmatched {
		stmt.UnmatchedReason = "missing_channel_discount"
	}

	// 4. 转 [] 排序
	for _, d := range byDate {
		stmt.ByDate = append(stmt.ByDate, *d)
	}
	for _, c := range byChannel {
		stmt.ByChannel = append(stmt.ByChannel, *c)
	}
	for _, m := range byModel {
		stmt.ByModel = append(stmt.ByModel, *m)
	}

	return stmt, nil
}
