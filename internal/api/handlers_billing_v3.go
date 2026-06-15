// BILLING v3 上游对账 API handlers (PR #4 / 7, 2026-06-14)
//
// 5 端点 (RFC §5.1):
//  1. GET  /api/billing/v3/upstream/current-month-overview
//     vendor + channel 双层, 当月累计
//  2. POST /api/billing/v3/upstream/export-last-month
//     admin/finance 异步创建任务
//  3. GET  /api/billing/v3/upstream/:vendor_code/tasks
//     单 vendor 任务历史
//  4. GET  /api/billing/v3/export-tasks
//     全任务列表 (kind='upstream')
//  5. GET  /api/billing/v3/export-tasks/:task_id/download + cancel
//     复用 v2 download/cancel 逻辑 (kind 自动路由)
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// ===== 1. current-month-overview =====

type UpstreamOverviewResponse struct {
	PeriodStart  int64                            `json:"period_start"`
	PeriodEnd    int64                            `json:"period_end"`
	VendorCount  int                              `json:"vendor_count"`
	ChannelCount int                              `json:"channel_count"`
	TotalCost    float64                          `json:"total_cost"`
	TotalRevenue float64                          `json:"total_revenue"`
	TotalProfit  float64                          `json:"total_profit"`
	Items        []billing.UpstreamOverviewVendor `json:"items"`
	// PR #9 (2026-06-15) cache 字段: 透传 cache bucket ts + stale 标志
	CacheTS     int64  `json:"cache_ts"`     // 最新 cache bucket unix 秒, 0=全 fallback
	Stale       bool   `json:"stale"`        // true = 至少 1 vendor 走了 fallback
	StaleReason string `json:"stale_reason"` // 仅 stale=true 时填
	GeneratedAt int64  `json:"generated_at"` // 实际生成时间 (cache ts 或 实时)
}

// billingV3UpstreamCurrentMonthOverview cache 优先 + fallback 实时算 (PR #9, 2026-06-15)
//
// 设计:
//   - 走 GetUpstreamOverviewCached, 优先读 ops_upstream_summary_5min
//   - cache hit (5min 内有所有 vendor 数据) → 0 RoDB hit, stale=false
//   - cache miss (启动 5min 内, 或某 vendor 没数据) → 对该 vendor 走 CalcUpstreamStatement 实时算, stale=true
//   - 响应加 cache_ts / stale / stale_reason 字段, 前端可显示 "数据 X 分钟前更新"
//
// 跟现 handler 老逻辑对比:
//   - 老: 1 SQL GROUP BY channel_id (1.9M 行) → 内存 join channel_vendor_map, fast estimate
//   - 新: cache hit ≈ 0 RoDB hit; cache miss = N vendor × 1 SQL (跟 worker 一致), 但走的 CalcUpstreamStatement
//     (精确版, 走 group_ratio) — overview 数字更准
//   - 收益: 月对账 1-5 号 5 vendor 同时跑 → cache 命中 0 SQL, 不再有短时尖峰
func (s *Server) billingV3UpstreamCurrentMonthOverview(c *gin.Context) {
	if !dal.HasRoDB() {
		errResp(c, 503, "RoDB not configured", nil)
		return
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	endOfNow := now.Unix()

	// PR #9 (2026-06-15): 走 cache-aware getter
	//   - cache hit: 0 RoDB hit
	//   - cache miss: 走 CalcUpstreamStatement per-vendor 实时算
	//   - 详细: internal/billing/upstream_cache.go GetUpstreamOverviewCached
	cacheRes, err := billing.GetUpstreamOverviewCached(c.Request.Context())
	if err != nil {
		errResp(c, 500, "get upstream overview: "+err.Error(), nil)
		return
	}

	// 包装为 handler 响应 (PeriodStart/End, VendorCount, ChannelCount, TotalCost/Revenue/Profit)
	channelSet := make(map[int64]bool)
	resp := UpstreamOverviewResponse{
		PeriodStart: startOfMonth,
		PeriodEnd:   endOfNow,
		VendorCount: len(cacheRes.Vendors),
		Items:       cacheRes.Vendors,
		CacheTS:     cacheRes.CacheTS,
		Stale:       cacheRes.Stale,
		StaleReason: cacheRes.StaleReason,
		GeneratedAt: cacheRes.GeneratedAt,
	}
	for _, v := range cacheRes.Vendors {
		resp.TotalCost += v.TotalCost
		resp.TotalRevenue += v.TotalRevenue
		resp.TotalProfit += v.TotalProfit
		for _, ch := range v.Channels {
			channelSet[ch.ChannelID] = true
		}
	}
	resp.ChannelCount = len(channelSet)
	ok(c, resp)
}

// ===== 2. export-last-month =====

type billingV3UpstreamExportReq struct {
	VendorCode string `json:"vendor_code"` // 空 = 全部
	Formats    string `json:"formats"`
}

// billingV3UpstreamExportLastMonth 创建 v3 异步任务
//
// body: { vendor_code: "provider_alpha", formats: "html,xlsx" }
// vendor_code 空 = 全部 vendor 各创建 1 任务
func (s *Server) billingV3UpstreamExportLastMonth(c *gin.Context) {
	if !dal.HasRoDB() {
		errResp(c, 503, "RoDB not configured", nil)
		return
	}
	role := getAuthRole(c)
	if role != "admin" && role != "finance" {
		errResp(c, 403, "insufficient role (admin/finance required)", nil)
		return
	}
	operator := getAuthUsername(c)
	uidAny, _ := c.Get("auth_user_id")
	uid, _ := uidAny.(uint)
	// 兜底: legacy token 没 user_id, 用 admin 1 兜 (跟 auth middleware 写的 role=admin 一致)
	if uid == 0 {
		uid = 1
	}

	var body billingV3UpstreamExportReq
	_ = c.ShouldBindJSON(&body) // body 可选
	if body.Formats == "" {
		body.Formats = "html,xlsx"
	}
	// 算上月 period: 本月 1 号 - 1 天 = 上月 1 号
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	lastOfPrevMonth := firstOfThisMonth.Add(-time.Second)
	period := lastOfPrevMonth.Format("2006-01")

	// 决定要创建的 vendor 列表
	var vendorCodes []string
	if body.VendorCode != "" {
		vendorCodes = []string{body.VendorCode}
	} else {
		vendors, _ := dal.ListVendors(c.Request.Context())
		for _, v := range vendors {
			vendorCodes = append(vendorCodes, v.Code)
		}
	}

	// 给每个 vendor 创建一个任务
	created := []gin.H{}
	for _, vc := range vendorCodes {
		taskID, err := billing.EnqueueExportTask(c.Request.Context(), int(uid), operator, period, body.Formats, "upstream", vc, operator)
		if err != nil {
			errResp(c, 500, fmt.Sprintf("enqueue %s: %v", vc, err), nil)
			return
		}
		created = append(created, gin.H{
			"task_id":     taskID,
			"vendor_code": vc,
			"period":      period,
			"status":      "pending",
		})
	}
	ok(c, gin.H{
		"period":       period,
		"created":      created,
		"vendor_count": len(vendorCodes),
	})
}

// ===== 3. :vendor_code/tasks =====

// billingV3UpstreamVendorTasks 单 vendor 任务历史
func (s *Server) billingV3UpstreamVendorTasks(c *gin.Context) {
	vendorCode := c.Param("vendor_code")
	if vendorCode == "" {
		errResp(c, 400, "missing vendor_code", nil)
		return
	}
	limit := queryLimit(c, 50, 200)
	offset := queryOffset(c)
	status := c.Query("status")

	rows, total, err := dal.ListBillingExportTasks(c.Request.Context(), dal.BillingExportTaskQuery{
		Kind:       "upstream",
		VendorCode: vendorCode,
		Status:     status,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		errResp(c, 500, "list failed: "+err.Error(), nil)
		return
	}
	ok(c, gin.H{"total": total, "items": rows})
}

// ===== 4. v3 全任务列表 =====

// billingV3ExportTasks 列出 kind='upstream' 的全任务
// 复用 v2 download/cancel 端点 (按 kind 自动路由)
func (s *Server) billingV3ExportTasks(c *gin.Context) {
	limit := queryLimit(c, 50, 200)
	offset := queryOffset(c)
	status := c.Query("status")

	rows, total, err := dal.ListBillingExportTasks(c.Request.Context(), dal.BillingExportTaskQuery{
		Kind:   "upstream",
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		errResp(c, 500, "list failed: "+err.Error(), nil)
		return
	}
	ok(c, gin.H{"total": total, "items": rows})
}

// loadChannelDiscounts 拿 (channel_id -> ChannelVendorMap) 映射
func loadChannelDiscounts(ctx context.Context) (map[int64]dal.ChannelVendorMap, error) {
	rows, err := dal.ListChannelVendors(ctx, 0) // 0 = 全部
	if err != nil {
		return nil, err
	}
	out := make(map[int64]dal.ChannelVendorMap, len(rows))
	for _, r := range rows {
		out[int64(r.ChannelID)] = r
	}
	return out, nil
}
