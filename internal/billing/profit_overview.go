// 利润分析聚合 (BILLING v4 PR #2 / 6, 2026-06-14)
//
// CalcProfitOverview 给定时间范围, 1 SQL 拿所有用户的 (revenue + 4 token) +
// server 端用 v3 CalcLogCost 反推 cost, 算汇总 + 趋势 + 3 维度拆分.
//
// 数据源:
//   - RoDB (newapi.logs): revenue, prompt, completion, cache, request_count, group, channel_id
//   - OPS.channel_vendor_map.discount: cost 反推
//
// 例: user_alpha (uid=47) 5 月
//
//	revenue = 1,002,849 calls × quota / 500000 = ~$70,226.82
//	cost = (revenue / 0.64) × 0.24 = ~$26,335
//	profit = ~$43,891
//	margin = 1.667 (166.7%)
package billing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// ProfitOverview 利润分析总览 (1 端点返完整数据)
type ProfitOverview struct {
	PeriodStart  int64            `json:"period_start"`
	PeriodEnd    int64            `json:"period_end"`
	UserCount    int              `json:"user_count"`
	TotalRevenue float64          `json:"total_revenue"`
	TotalCost    float64          `json:"total_cost"`
	TotalProfit  float64          `json:"total_profit"`
	ProfitRate   float64          `json:"profit_rate"` // profit / cost
	ByDay        []ProfitByDay    `json:"by_day"`
	ByUser       []ProfitByUser   `json:"by_user"`   // 27 客户, 按 profit 降序
	ByVendor     []ProfitByVendor `json:"by_vendor"` // 5 vendor, 按 profit 降序
	ByModel      []ProfitByModel  `json:"by_model"`  // top 10 model
}

// ProfitByDay 30 天趋势
type ProfitByDay struct {
	Date         string  `json:"date"`
	Revenue      float64 `json:"revenue"`
	Cost         float64 `json:"cost"`
	Profit       float64 `json:"profit"`
	RequestCount int64   `json:"request_count"`
}

// ProfitByUser 27 客户
type ProfitByUser struct {
	UserID           int64   `json:"user_id"`
	Username         string  `json:"username"`
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	Revenue          float64 `json:"revenue"`
	Cost             float64 `json:"cost"`
	Profit           float64 `json:"profit"`
	ProfitRate       float64 `json:"profit_rate"`
}

// ProfitByVendor 5 vendor (复用 v3 CalcUpstreamStatement)
type ProfitByVendor struct {
	VendorCode   string  `json:"vendor_code"`
	VendorName   string  `json:"vendor_name"`
	RequestCount int64   `json:"request_count"`
	Revenue      float64 `json:"revenue"`
	Cost         float64 `json:"cost"`
	Profit       float64 `json:"profit"`
	ProfitRate   float64 `json:"profit_rate"`
}

// ProfitByModel top 10 model
type ProfitByModel struct {
	ModelName    string  `json:"model_name"`
	RequestCount int64   `json:"request_count"`
	Revenue      float64 `json:"revenue"`
	Cost         float64 `json:"cost"`
	Profit       float64 `json:"profit"`
	ProfitRate   float64 `json:"profit_rate"`
}

// CalcProfitOverview 计算 profit overview
//
// 流程:
//  1. 1 SQL 拿所有 type=2 logs (按 user + channel 聚合)
//  2. server 端用 v3 CalcLogCost 反推 cost
//  3. 按 user 聚合 (27 行)
//  4. 复用 v3 CalcUpstreamStatement 按 vendor 聚合 (5 行)
//  5. 按 day + model 聚合 (30 天 + top 10)
func CalcProfitOverview(ctx context.Context, startTS, endTS int64) (*ProfitOverview, error) {
	if !dal.HasRoDB() {
		return nil, dal.ErrNoRoDB
	}

	// 1) 1 SQL 拿所有 logs (按 user + channel 聚合)
	type chRow struct {
		UserID           int64
		Username         string
		ChannelID        int64
		ChannelName      string
		ModelName        string
		GroupName        string
		CreatedAt        int64
		GroupRatio       float64
		RequestCount     int64
		PromptTokens     int64
		CompletionTokens int64
		CacheTokens      int64
		Quota            int64
	}
	var rows []chRow
	if err := dal.RoDB().WithContext(ctx).Table("logs").
		Select(`user_id, COALESCE(MAX(username), '') AS username,
		        channel_id, COALESCE(MAX(channel_name), '') AS channel_name,
		        COALESCE(MAX(model_name), '') AS model_name,
		        COALESCE(MAX("group"), '') AS group_name,
		        created_at,
		        COALESCE(MAX((other::jsonb->>'group_ratio')::numeric)::float8, 1.0) AS group_ratio,
		        COUNT(*) AS request_count,
		        COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
		        COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
		        COALESCE(SUM((other::jsonb->>'cache_tokens')::bigint), 0) AS cache_tokens,
		        COALESCE(SUM(quota), 0) AS quota`).
		Where("type = 2 AND created_at >= ? AND created_at < ?", startTS, endTS).
		Group("user_id, channel_id, model_name, created_at").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}

	// 2) 拿 channel_vendor_map (cache)
	discounts, err := loadChannelDiscountsForProfit(ctx)
	if err != nil {
		return nil, err
	}

	// 3) 拿 vendor name map
	vendors, _ := dal.ListVendors(ctx)
	vendorNameMap := make(map[string]string, len(vendors))
	for _, v := range vendors {
		vendorNameMap[v.Code] = v.Name
	}

	// 4) 聚合 4 维度
	overview := &ProfitOverview{
		PeriodStart: startTS,
		PeriodEnd:   endTS,
	}
	userMap := make(map[int64]*ProfitByUser)
	vendorMap := make(map[string]*ProfitByVendor)
	modelMap := make(map[string]*ProfitByModel)
	dayMap := make(map[string]*ProfitByDay)
	userSet := make(map[int64]bool)

	for _, r := range rows {
		// 单 log 维度的 cost
		cd, ok := discounts[r.ChannelID]
		if !ok {
			cd.Discount = 1.0 // R6 兜底
		}
		cost := CalcLogCost(r.Quota, r.GroupRatio, cd.Discount)
		revenue := float64(r.Quota) / 500000.0

		overview.TotalCost += cost
		overview.TotalRevenue += revenue
		overview.TotalProfit += revenue - cost
		userSet[r.UserID] = true

		// 按 user
		u, ok := userMap[r.UserID]
		if !ok {
			u = &ProfitByUser{UserID: r.UserID, Username: r.Username}
			userMap[r.UserID] = u
		}
		u.RequestCount += r.RequestCount
		u.PromptTokens += r.PromptTokens
		u.CompletionTokens += r.CompletionTokens
		u.CacheTokens += r.CacheTokens
		u.Revenue += revenue
		u.Cost += cost
		u.Profit = u.Revenue - u.Cost
		if u.Cost > 0 {
			u.ProfitRate = u.Profit / u.Cost
		}

		// 按 vendor
		if cd.VendorCode != "" {
			v, ok := vendorMap[cd.VendorCode]
			if !ok {
				v = &ProfitByVendor{
					VendorCode: cd.VendorCode,
					VendorName: vendorNameMap[cd.VendorCode],
				}
				vendorMap[cd.VendorCode] = v
			}
			v.RequestCount += r.RequestCount
			v.Revenue += revenue
			v.Cost += cost
			v.Profit = v.Revenue - v.Cost
			if v.Cost > 0 {
				v.ProfitRate = v.Profit / v.Cost
			}
		}

		// 按 model
		if r.ModelName != "" {
			m, ok := modelMap[r.ModelName]
			if !ok {
				m = &ProfitByModel{ModelName: r.ModelName}
				modelMap[r.ModelName] = m
			}
			m.RequestCount += r.RequestCount
			m.Revenue += revenue
			m.Cost += cost
			m.Profit = m.Revenue - m.Cost
			if m.Cost > 0 {
				m.ProfitRate = m.Profit / m.Cost
			}
		}

		// 按天
		date := time.Unix(r.CreatedAt, 0).UTC().Format("2006-01-02")
		d, ok := dayMap[date]
		if !ok {
			d = &ProfitByDay{Date: date}
			dayMap[date] = d
		}
		d.RequestCount += r.RequestCount
		d.Revenue += revenue
		d.Cost += cost
		d.Profit = d.Revenue - d.Cost
	}

	overview.UserCount = len(userSet)
	if overview.TotalCost > 0 {
		overview.ProfitRate = overview.TotalProfit / overview.TotalCost
	}

	// 5) 转 [] + 排序
	for _, u := range userMap {
		overview.ByUser = append(overview.ByUser, *u)
	}
	sort.Slice(overview.ByUser, func(i, j int) bool {
		return overview.ByUser[i].Profit > overview.ByUser[j].Profit
	})

	for _, v := range vendorMap {
		overview.ByVendor = append(overview.ByVendor, *v)
	}
	sort.Slice(overview.ByVendor, func(i, j int) bool {
		return overview.ByVendor[i].Profit > overview.ByVendor[j].Profit
	})

	// by_model top 10
	for _, m := range modelMap {
		overview.ByModel = append(overview.ByModel, *m)
	}
	sort.Slice(overview.ByModel, func(i, j int) bool {
		return overview.ByModel[i].Profit > overview.ByModel[j].Profit
	})
	if len(overview.ByModel) > 10 {
		overview.ByModel = overview.ByModel[:10]
	}

	// by_day 按日期升序
	for _, d := range dayMap {
		overview.ByDay = append(overview.ByDay, *d)
	}
	sort.Slice(overview.ByDay, func(i, j int) bool {
		return overview.ByDay[i].Date < overview.ByDay[j].Date
	})

	return overview, nil
}

// loadChannelDiscountsForProfit 拿 (channel_id -> {vendor_code, discount})
func loadChannelDiscountsForProfit(ctx context.Context) (map[int64]dal.ChannelVendorMap, error) {
	rows, err := dal.ListChannelVendors(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]dal.ChannelVendorMap, len(rows))
	for _, r := range rows {
		out[int64(r.ChannelID)] = r
	}
	return out, nil
}
