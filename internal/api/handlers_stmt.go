// Dashboard handlers + system config
//
// 历史 (2026-06-14 前): 这个文件还含 13 个 v1 billing handler (客户对账单 / 上游对账单 / 利润分析).
// 2026-06-14 v1 下线, 13 个 handler 全删. 移到 archive/ 分支 (commit history 保留).
//
// 当前文件仅 4 个 dashboard handler + 1 个 system config handler.
// 账单 v2 handler 在 handlers_billing_v2.go.
package api

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// ===== Dashboard =====

type DashboardToday struct {
	Date       string  `json:"date"`
	RevenueUSD float64 `json:"revenue_usd"` // 今日 cost (USD)
	RPM        int     `json:"rpm"`         // 60s 滑窗 rpm (newapi stat)
	TPM        int     `json:"tpm"`         // 60s 滑窗 tpm (newapi stat)
	// 砍掉的字段: RequestCount, SuccessCount, ErrorCount, PromptTokens, CompletionTokens, CacheTokens, AvgLatencyMs
}

// dashboardTodayStat 通过 newapi admin API /api/log/stat 拿今日 stat
//
// admin API 返 3 字段: quota (cost 总额) / rpm (60s 滑窗) / tpm (60s 滑窗)
// 硬编码 type=LogTypeConsume (= 成功 type=2), 所以 success_count 我们用 stat 返的 "quota / 单次平均价"
// 简化: 直接把 "今日 calls 成功数" 等于 "今日有 cost 的请求数" (按 type=2 stat 算)
//
// 砍掉的字段 (admin API 不直接给):
//   - error_count       (新api 不返 type=5)
//   - avg_latency_ms    (新api 不返)
//   - prompt_tokens     (新api 不返)
//   - completion_tokens (新api 不返)
//   - cache_tokens      (新api 不返)
func (s *Server) dashboardToday(c *gin.Context) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
	endOfDay := startOfDay + 86400 - 1

	if s.nac == nil {
		errResp(c, 503, "admin API client not configured (set upstream_ADMIN_BASE_URL/API_OPS_ADMIN_TOKEN)", nil)
		return
	}

	// 严格走 admin /api/log/stat 1 次全平台 (用户决策 2026-06-14: 总览模块全 admin API)
	// admin 限流 18次/5min, SPA 5s 刷新会触发. 限流时返 429 给前端展示.
	stat, err := s.nac.GetLogsStat(c.Request.Context(), 0, startOfDay, endOfDay)
	if err != nil {
		errResp(c, 502, "admin API stat failed: "+err.Error(), nil)
		return
	}
	d := &DashboardToday{
		Date:       now.Format("2006-01-02"),
		RevenueUSD: s.cfg.QuotaToUSD(int64(stat.Quota)),
		RPM:        stat.RPM,
		TPM:        stat.TPM,
	}
	c.Header("X-Data-Source", "admin_api_stat_global")
	ok(c, d)
}

// dashboardTrend 已删除 (2026-06-14):
//   - newapi admin API 不给按天趋势 (/api/data/ 准实时聚合表 = 空, DataExportEnabled=false)
//   - 总览模块按用户指示砍掉趋势功能
//   - 如需重新开启, 需 newapi 那边开 DataExportEnabled 或回 RoDB 翻页

// ===== 7d trend (PR #1, 2026-06-15) =====
//
// 用户需求 2026-06-15: 总览加 7 天曲线 (不含今天).
//
// 数据源策略:
//   - admin /api/log/stat 一次性 7 次串行调用 (D-7 ~ D-1)
//   - 后端 5min sync.Map cache, key="dashboard:trend7d"
//   - SPA 5s tick 只调 /api/dashboard/trend-7d, 命中后端 cache, 0 admin API 调用
//   - admin 限流 18次/5min: 7 次/5min = 7 占用, 余 11 额度 (供其他端点用)
//
// 不含今天: 今天数据由 dashboardToday (60s 滑窗) 单独展示, 不进 7d 趋势.

type DashboardTrend7dItem struct {
	Date       string  `json:"date"`        // "2026-06-08"
	RevenueUSD float64 `json:"revenue_usd"` // 当日 cost (USD), admin stat quota 换算
}

type DashboardTrend7d struct {
	Items        []DashboardTrend7dItem `json:"items"`
	GeneratedAt  int64                  `json:"generated_at"`  // unix 秒, 数据生成时间
	SourceCached bool                   `json:"source_cached"` // true=后端 cache 命中
}

const dashboardTrend7dCacheTTL = 5 * time.Minute

var (
	dashboardTrend7dCache    atomic.Pointer[DashboardTrend7d]
	dashboardTrend7dCacheExp atomic.Int64 // unix nano
)

func (s *Server) dashboardTrend7d(c *gin.Context) {
	if s.nac == nil {
		errResp(c, 503, "admin API client not configured", nil)
		return
	}

	// 检查 cache
	now := time.Now()
	if cached := dashboardTrend7dCache.Load(); cached != nil {
		if exp := dashboardTrend7dCacheExp.Load(); exp > now.UnixNano() {
			resp := *cached
			resp.SourceCached = true
			ok(c, resp)
			return
		}
	}

	// 7 个 24h 窗口, 串行 (admin 限流 18次/5min, 7 次安全)
	// D-7 = 7 天前 00:00 ~ 23:59:59
	// D-1 = 昨天 00:00 ~ 23:59:59
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	items := make([]DashboardTrend7dItem, 0, 7)
	for i := 7; i >= 1; i-- {
		dayStart := today.AddDate(0, 0, -i).Unix()
		dayEnd := dayStart + 86400 - 1
		stat, err := s.nac.GetLogsStat(c.Request.Context(), 0, dayStart, dayEnd)
		if err != nil {
			errResp(c, 502, "admin API stat failed at day -"+strconv.Itoa(i)+": "+err.Error(), nil)
			return
		}
		dayDate := today.AddDate(0, 0, -i)
		items = append(items, DashboardTrend7dItem{
			Date:       dayDate.Format("2006-01-02"),
			RevenueUSD: s.cfg.QuotaToUSD(int64(stat.Quota)),
		})
	}

	resp := DashboardTrend7d{
		Items:        items,
		GeneratedAt:  now.Unix(),
		SourceCached: false,
	}
	dashboardTrend7dCache.Store(&resp)
	dashboardTrend7dCacheExp.Store(now.Add(dashboardTrend7dCacheTTL).UnixNano())
	c.Header("X-Data-Source", "admin_api_stat_7calls")
	ok(c, resp)
}

type TopEntry struct {
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	RevenueUSD float64 `json:"revenue_usd"` // cost (USD) — admin API 拿到的唯一准数字
}

// ===== TopX 三个端点已禁用 (2026-06-14, 用户决策: 全 admin API + 砍 TopX 卡片) =====
//
// 用户原话: "总览模块严格走 admin API". 而 admin API 限制:
//   - /api/log/stat: 全平台 stat (quota/rpm/tpm) 不带任何分组维度, 不能拆 user/channel/model
//   - /api/data/ + /api/data/users: 准实时聚合表, newapi DataExportEnabled=false 时为空
//   - 18次/5min 限流: 107 user × 1 stat 串行直接触发 429
//
// 因此 TopCustomers/TopChannels/TopModels 三个端点撤掉, 路由注释, SPA 卡片删除.
// 恢复路径 (任一):
//   1. upstream 那边开 DataExportEnabled → /api/data/ 拿按 hour 聚合
//   2. 走 RoDB GROUP BY (1 SQL, 但要全表扫 178 万行 ≈ 5s, 不适合 dashboard)
//   3. 走 cache_logs_summary_5min 扩展 model_name/user_name 维度 (1min tick 预聚合)
//
// 当前所有 3 个 handler 都返 503 "TopX 不可用", 等用户决定恢复路径.

func (s *Server) dashboardTopCustomers(c *gin.Context) {
	errResp(c, 503, "Top Customers 卡片已撤掉 (admin API 不带 user 维度). 恢复路径见代码注释", nil)
}

func (s *Server) dashboardTopModels(c *gin.Context) {
	errResp(c, 503, "Top Models 卡片已撤掉 (admin /api/data/ 准实时表空, DataExportEnabled=false). 恢复路径见代码注释", nil)
}

func (s *Server) dashboardTopChannels(c *gin.Context) {
	errResp(c, 503, "Top Channels 卡片已撤掉 (admin API 不带 channel 维度). 恢复路径见代码注释", nil)
}

func (s *Server) getConfig(c *gin.Context) {
	ok(c, gin.H{
		"quota_per_unit":           s.cfg.QuotaPerUnit,
		"usd_cny_rate":             s.cfg.USDCNYRate,
		"display_currency":         s.cfg.DisplayCurrency,
		"upstream_import_max_rows": s.cfg.UpstreamImportMaxRows,
		"daily_stmt_cron":          s.cfg.DailyStmtCron,
	})
}
