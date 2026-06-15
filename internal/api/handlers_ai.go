// P3 AI 错误解读 API handlers
//
//	POST /api/ai/diagnose                  body: {cluster_id} 或 {channel_id, model_name, pattern, sample_content}
//	GET  /api/ai/reports?type=&start=&end=
//	GET  /api/ai/reports/:id
//	POST /api/ai/customer-health/:user_id  异步生成
//	POST /api/ai/cluster/tick              手动跑一次 1h 聚类（调试）
//	POST /api/ai/report/daily              手动跑一次日报
package api

import (
	"log"
	"strconv"
	"time"

	"github.com/api-ops/api-ops/internal/ai"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

type diagnoseRequest struct {
	ClusterID     uint64 `json:"cluster_id"`
	ChannelID     int    `json:"channel_id"`
	ModelName     string `json:"model_name"`
	Pattern       string `json:"pattern"`
	SampleContent string `json:"sample_content"`
	Count         int64  `json:"count"`
}

// aiDiagnose 同步诊断
func (s *Server) aiDiagnose(c *gin.Context) {
	var req diagnoseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp(c, 400, "invalid body: "+err.Error(), nil)
		return
	}
	var cluster *dal.AIErrorCluster
	if req.ClusterID > 0 {
		row, err := dal.GetAIErrorCluster(c.Request.Context(), req.ClusterID)
		if err != nil {
			errResp(c, 500, "get cluster failed", err.Error())
			return
		}
		if row == nil {
			errResp(c, 404, "cluster not found", nil)
			return
		}
		cluster = row
	} else if req.ChannelID > 0 && req.Pattern != "" {
		cluster = &dal.AIErrorCluster{
			ChannelID: req.ChannelID, ModelName: req.ModelName,
			Pattern: req.Pattern, SampleContent: req.SampleContent, Count: req.Count,
		}
	} else {
		errResp(c, 400, "cluster_id or (channel_id+pattern) 必填", nil)
		return
	}
	d, err := ai.Diagnose(c.Request.Context(), cluster)
	if err != nil {
		errResp(c, 500, "diagnose failed", err.Error())
		return
	}
	ok(c, gin.H{"diagnosis": d})
}

// aiListReports GET /api/ai/reports
func (s *Server) aiListReports(c *gin.Context) {
	typ := c.Query("type")
	startTS, endTS := dayRangeFromQuery(c)
	limit := queryLimit(c, 20, 200)
	offset := queryOffset(c)
	rows, err := dal.ListAIReports(c.Request.Context(), typ, limit)
	if err != nil {
		errResp(c, 500, "list reports failed", err.Error())
		return
	}
	filtered := make([]dal.AIReport, 0, len(rows))
	for _, r := range rows {
		if startTS > 0 && r.PeriodEnd < startTS {
			continue
		}
		if endTS > 0 && r.PeriodStart > endTS {
			continue
		}
		filtered = append(filtered, r)
	}
	if offset > 0 && offset < len(filtered) {
		filtered = filtered[offset:]
	}
	ok(c, gin.H{"total": len(filtered), "items": filtered})
}

// aiGetReport GET /api/ai/reports/:id
func (s *Server) aiGetReport(c *gin.Context) {
	id := parseUint(c.Param("id"))
	r, err := dal.GetAIReport(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get report failed", err.Error())
		return
	}
	if r == nil {
		errResp(c, 404, "report not found", nil)
		return
	}
	ok(c, gin.H{"report": r})
}

// aiCustomerHealth POST /api/ai/customer-health/:user_id  异步生成
func (s *Server) aiCustomerHealth(c *gin.Context) {
	uid := parseUint(c.Param("user_id"))
	if uid == 0 {
		errResp(c, 400, "user_id 必填", nil)
		return
	}
	go func() {
		ctx := c.Copy().Request.Context()
		rep, err := ai.GenerateCustomerHealthReport(ctx, uid)
		if err != nil {
			log.Printf("[ai] customer health failed user=%d: %v", uid, err)
		} else {
			log.Printf("[ai] customer health done user=%d report_id=%d", uid, rep.ID)
		}
	}()
	ok(c, gin.H{"user_id": uid, "status": "queued"})
}

// aiClusterTick POST /api/ai/cluster/tick  手动跑一次 1h 聚类
func (s *Server) aiClusterTick(c *gin.Context) {
	n, err := ai.ClusterOneHour(c.Request.Context())
	if err != nil {
		errResp(c, 500, "cluster failed", err.Error())
		return
	}
	ok(c, gin.H{"clusters_upserted": n})
}

// aiDailyReport POST /api/ai/report/daily  手动跑一次日报
func (s *Server) aiDailyReport(c *gin.Context) {
	endTS := parseInt64(c.DefaultQuery("end", strconv.FormatInt(time.Now().Unix(), 10)))
	rep, err := ai.GenerateErrorDailyReport(c.Request.Context(), endTS)
	if err != nil {
		errResp(c, 500, "daily report failed", err.Error())
		return
	}
	ok(c, gin.H{"report_id": rep.ID, "title": rep.Title})
}
