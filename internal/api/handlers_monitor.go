// P1 监控 API handlers
// 路由：
//
//	GET  /api/monitor/channels
//	GET  /api/monitor/channels/:id/health?range=24h
//	GET  /api/monitor/alerts?status=firing&severity=&limit=50&offset=0
//	GET  /api/monitor/alerts/:id
//	POST /api/monitor/alerts/:id/ack       body: {"acked_by":"ops_alice"}
//	POST /api/monitor/alerts/:id/resolve
//	GET  /api/monitor/rules                （列出所有 alert_rules）
package api

import (
	"fmt"
	"strconv"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/monitor"
	"github.com/gin-gonic/gin"
)

// ===== /api/monitor/channels =====

// listMonitorChannels 返回"近 24h 活跃 + 启用"的渠道 + 24h 健康度聚合
//
// 规则 (用户决策 2026-06-15 09:18):
//  1. 24h 内 request_count = 0 的渠道 → 不显示
//  2. status != enabled (1) 的渠道 → 不显示
//  3. 展示 24h SUM(request/error) + 实时算 error_rate (比最新 1 桶更准)
//  4. join channel_vendor_map + upstream_vendors 拿供应商名字
func (s *Server) listMonitorChannels(c *gin.Context) {
	// 1) 只取 enabled 渠道
	chs, err := dal.ListChannels(c.Request.Context(), dal.ChannelStatusEnabled)
	if err != nil {
		errResp(c, 500, "list channels failed", err.Error())
		return
	}

	// 2) 24h 内活跃渠道 (request_count > 0)
	sinceTS := time.Now().Add(-24 * time.Hour).Unix()
	summary24h, err := dal.ListChannel24hSummary(c.Request.Context(), sinceTS)
	if err != nil {
		errResp(c, 500, "list 24h health failed", err.Error())
		return
	}
	summaryMap := make(map[int]dal.Channel24hSummary, len(summary24h))
	for _, s := range summary24h {
		summaryMap[s.ChannelID] = s
	}

	out := make([]gin.H, 0, len(summary24h))
	// 索引: channel_id → ch
	chMap := make(map[int]dal.ChannelMirror, len(chs))
	for _, c := range chs {
		chMap[c.ID] = c
	}

	for _, s := range summary24h {
		// 拿供应商
		vendorName := ""
		vendorCode := ""
		if vm, err := dal.GetChannelVendorByChannelID(c.Request.Context(), s.ChannelID); err == nil && vm != nil {
			vendorCode = vm.VendorCode
			if v, err := dal.GetUpstreamVendorByCode(c.Request.Context(), vm.VendorCode); err == nil && v != nil {
				vendorName = v.Name
			}
		}

		// 找原始 ch (cache 可能慢, 找不到仍显示)
		var chItem gin.H
		if c, ok := chMap[s.ChannelID]; ok {
			chItem = gin.H{
				"id":                 c.ID,
				"name":               c.Name,
				"type":               c.Type,
				"status":             c.Status,
				"models":             c.Models,
				"group":              c.Group,
				"used_quota":         c.UsedQuota,
				"balance":            c.Balance,
				"response_time":      c.ResponseTime,
				"balance_updated_at": c.BalanceUpdatedTime,
			}
		} else {
			chItem = gin.H{"id": s.ChannelID, "name": "(cache 未同步)", "status": 1}
		}

		chItem["vendor_code"] = vendorCode
		chItem["vendor_name"] = vendorName
		chItem["health_24h"] = gin.H{
			"request_count":  s.RequestCount, // 业务请求总数 (type IN 2,5,6)
			"success_count":  s.SuccessCount, // 业务成功 (type=2)
			"error_count":    s.ErrorCount,   // 独立错误 (type=5 AND use_channel.length=1, 排除中间重试失败)
			"error_rate":     s.ErrorRate,    // 错误率 = error_count / request_count
			"p95_latency_ms": s.P95LatencyMs, // 跨 24h MAX(最新桶 P95)
			"last_bucket_ts": s.LastBucketTS, // P95 取自哪一桶
		}
		out = append(out, chItem)
	}
	ok(c, gin.H{"total": len(out), "items": out})
}

// channelHealth 返回指定 channel 的 5min 滑窗序列
// query: range=24h（默认 24h，最大 7d）
func (s *Server) channelHealth(c *gin.Context) {
	id := parseInt(c.Param("id"))
	if id == 0 {
		errResp(c, 400, "id 必填", nil)
		return
	}
	rangeStr := c.DefaultQuery("range", "24h")
	dur, err := time.ParseDuration(rangeStr)
	if err != nil || dur <= 0 {
		dur = 24 * time.Hour
	}
	if dur > 7*24*time.Hour {
		dur = 7 * 24 * time.Hour
	}
	endTS := time.Now().Unix()
	startTS := endTS - int64(dur.Seconds())

	// 优先 5min；range > 6h 用 1h 节省带宽
	useHourly := dur > 6*time.Hour
	if useHourly {
		rows, err := dal.ListChannelHealth1h(c.Request.Context(), dal.ChannelHealthQuery{
			ChannelID: id, StartTS: startTS, EndTS: endTS, Limit: 500,
		})
		if err != nil {
			errResp(c, 500, "list 1h health failed", err.Error())
			return
		}
		ok(c, gin.H{
			"channel_id":  id,
			"granularity": "1h",
			"start":       startTS,
			"end":         endTS,
			"count":       len(rows),
			"items":       rows,
		})
		return
	}
	rows, err := dal.ListChannelHealth5min(c.Request.Context(), dal.ChannelHealthQuery{
		ChannelID: id, StartTS: startTS, EndTS: endTS, Limit: 500,
	})
	if err != nil {
		errResp(c, 500, "list 5min health failed", err.Error())
		return
	}
	ok(c, gin.H{
		"channel_id":  id,
		"granularity": "5m",
		"start":       startTS,
		"end":         endTS,
		"count":       len(rows),
		"items":       rows,
	})
}

// ===== /api/monitor/alerts =====

func (s *Server) listAlerts(c *gin.Context) {
	status := c.Query("status")
	severity := c.Query("severity")
	limit := queryLimit(c, 50, 500)
	offset := queryOffset(c)
	rows, total, err := monitor.ListAlerts(c.Request.Context(), status, severity, limit, offset)
	if err != nil {
		errResp(c, 500, "list alerts failed", err.Error())
		return
	}
	ok(c, gin.H{"total": total, "items": rows})
}

func (s *Server) getAlert(c *gin.Context) {
	id := parseUint(c.Param("id"))
	h, err := dal.GetAlertHistory(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get alert failed", err.Error())
		return
	}
	if h == nil {
		errResp(c, 404, "alert not found", nil)
		return
	}
	actions, _ := dal.ListAlertActions(c.Request.Context(), id)
	ok(c, gin.H{"alert": h, "actions": actions})
}

type ackReq struct {
	AckedBy string `json:"acked_by"`
}

func (s *Server) ackAlert(c *gin.Context) {
	id := parseUint(c.Param("id"))
	var req ackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		// body 可选，默认 ops
		req.AckedBy = ""
	}
	// 确认存在
	h, err := dal.GetAlertHistory(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get alert failed", err.Error())
		return
	}
	if h == nil {
		errResp(c, 404, "alert not found", nil)
		return
	}
	if h.Status == "resolved" {
		errResp(c, 400, "alert already resolved", nil)
		return
	}
	if err := monitor.AcknowledgeAlert(c.Request.Context(), id, req.AckedBy); err != nil {
		errResp(c, 500, "ack failed", err.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": "acknowledged", "acked_by": req.AckedBy})
}

func (s *Server) resolveAlert(c *gin.Context) {
	id := parseUint(c.Param("id"))
	h, err := dal.GetAlertHistory(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get alert failed", err.Error())
		return
	}
	if h == nil {
		errResp(c, 404, "alert not found", nil)
		return
	}
	if err := monitor.ResolveAlert(c.Request.Context(), id); err != nil {
		errResp(c, 500, "resolve failed", err.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": "resolved"})
}

// ===== /api/monitor/rules =====

func (s *Server) listAlertRules(c *gin.Context) {
	rows, err := dal.ListAlertRules(c.Request.Context())
	if err != nil {
		errResp(c, 500, "list rules failed", err.Error())
		return
	}
	ok(c, gin.H{"total": len(rows), "items": rows})
}

// ===== 调试 / 自检 =====

// monitorTickNow 立刻跑一次 5min 聚合 + 1h 聚合 + 规则评估（调试用）
func (s *Server) monitorTickNow(c *gin.Context) {
	skipHourly, _ := strconv.ParseBool(c.DefaultQuery("skip_hourly", "false"))
	res := monitor.RunTick(c.Request.Context(), 0, skipHourly)
	if res.Err != nil {
		errResp(c, 500, "tick failed", res.Err.Error())
		return
	}
	fired, err := monitor.EvaluateRules(c.Request.Context(), time.Now())
	if err != nil {
		errResp(c, 500, "evaluate failed", err.Error())
		return
	}
	ok(c, gin.H{
		"buckets_5min": res.Buckets5min,
		"buckets_1h":   res.Buckets1h,
		"alerts_fired": fired,
		"skipped":      res.Skipped,
		"now":          fmt.Sprintf("%d", time.Now().Unix()),
	})
}
