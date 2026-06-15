// BILLING v4 利润分析 API handlers (PR #4 / 6, 2026-06-14)
//
// 1 端点: GET /api/billing/v4/profit/overview
//   - 1 SQL 拿 27 user 当月聚合
//   - server 端 CalcProfitOverview 算 cost 反推 + 汇总 + 趋势 + 3 维度拆分
//   - 1 端点返完整数据
package api

import (
	"time"

	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// billingV4ProfitOverview 1 端点返完整利润分析
//
// query params:
//   - start (unix 秒, 默认本月 1 号 00:00)
//   - end   (unix 秒, 默认 now)
func (s *Server) billingV4ProfitOverview(c *gin.Context) {
	if !dal.HasRoDB() {
		errResp(c, 503, "RoDB not configured", nil)
		return
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)

	// 默认本月 1 号 00:00 ~ now
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	endOfNow := now.Unix()

	// 支持 ?start=...&end=... 覆盖
	if s := c.Query("start"); s != "" {
		if ts := parseInt64(s); ts > 0 {
			startOfMonth = ts
		}
	}
	if e := c.Query("end"); e != "" {
		if ts := parseInt64(e); ts > 0 {
			endOfNow = ts
		}
	}

	overview, err := billing.CalcProfitOverview(c.Request.Context(), startOfMonth, endOfNow)
	if err != nil {
		errResp(c, 500, "calc profit overview: "+err.Error(), nil)
		return
	}
	ok(c, overview)
}
