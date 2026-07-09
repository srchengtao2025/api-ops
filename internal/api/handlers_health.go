// 客户健康度模块 (2026-07-08)
//
// 路由:
//   GET  /api/customer-health/overview                    全部客户 48h 健康度
//   GET  /api/customer-health/:user_id                    单客户详情 (errors + hits, 各限 100 条前端预览)
//   POST /api/customer-health/:user_id/export-errors       异步导出"详细错误列表" HTML
//   POST /api/customer-health/:user_id/export-hits         异步导出"详细缓存命中列表" HTML
//   GET  /api/customer-health/export-tasks                任务中心列表 (kind=customer_health)
//   GET  /api/customer-health/export-tasks/:task_id/download  下载 HTML 文件
//
// 设计要点:
//   - overview 走 cache_logs_summary_by_user_5min (1min 预聚合, 多站)
//   - detail 走 RoDB logs 直查 (errors: type=5, hits: cache_tokens>0)
//   - 导出复用 v2 异步导出 worker pool, kind=customer_health, VendorCode 区分 errors/hits
//   - 文件落 /data/customer-health-exports/{task_id}.html, 跟 v2 zip 区分
//   - 多站点: site 字段贯穿, intl + cn 共享同一份代码
package api

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/dal"
)

// customerHealthOverview GET /api/customer-health/overview
//
// Query:
//   - period: "48h" / "7d" / "30d" (默认 "48h")
//   - level: "healthy" / "warning" / "critical" (可选, 过滤健康度等级)
//
// 读权限: 全部 3 角色 (admin/finance/viewer)
func (s *Server) customerHealthOverview(c *gin.Context) {
	site := "intl"
	period := c.DefaultQuery("period", "48h")
	level := c.Query("level")

	items, err := billing.CustomerHealthOverview(c.Request.Context(), period)
	if err != nil {
		errResp(c, 500, "overview failed: "+err.Error(), nil)
		return
	}

	// 可选 level 过滤
	if level != "" {
		filtered := make([]billing.CustomerHealthOverviewItem, 0, len(items))
		for _, it := range items {
			if it.HealthLevel == level {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}

	// 汇总统计
	stats := struct {
		Total     int `json:"total"`
		Healthy   int `json:"healthy"`
		Warning   int `json:"warning"`
		Critical  int `json:"critical"`
	}{
		Total: len(items),
	}
	for _, it := range items {
		switch it.HealthLevel {
		case billing.HealthLevelHealthy:
			stats.Healthy++
		case billing.HealthLevelWarning:
			stats.Warning++
		case billing.HealthLevelCritical:
			stats.Critical++
		}
	}

	c.Header("X-Data-Source", "cache_logs_summary_by_user_5min")
	ok(c, gin.H{
		"site":   site,
		"period": period,
		"stats":  stats,
		"items":  items,
	})
}

// customerHealthDetail GET /api/customer-health/:user_id
//
// 返回前 100 条 errors + 100 条 hits (前端预览用, 完整版走 export)
func (s *Server) customerHealthDetail(c *gin.Context) {
	uid := parseInt(c.Param("user_id"))
	if uid == 0 {
		errResp(c, 400, "user_id 必填", nil)
		return
	}
	site := "intl"
	period := c.DefaultQuery("period", "48h")

	// 复用 CustomerHealthDetailByUser (单客户完整版, errors/hits 各 5000)
	// 但前端预览只需要 100, 我们截断后再返回
	detail, err := billing.CustomerHealthDetailByUser(c.Request.Context(), period, uid)
	if err != nil {
		errResp(c, 500, "detail failed: "+err.Error(), nil)
		return
	}

	// 预览截断
	const previewLimit = 100
	if len(detail.Errors) > previewLimit {
		detail.Errors = detail.Errors[:previewLimit]
	}
	if len(detail.Hits) > previewLimit {
		detail.Hits = detail.Hits[:previewLimit]
	}

	c.Header("X-Data-Source", "cache_logs_summary_by_user_5min + RoDB logs")
	ok(c, gin.H{
		"site":   site,
		"detail": detail,
	})
}

// customerHealthExportErrors POST /api/customer-health/:user_id/export-errors
//
// 异步导出"详细错误列表" HTML, 复用 v2 异步导出 worker pool
// 写权限: admin + finance
func (s *Server) customerHealthExportErrors(c *gin.Context) {
	s.enqueueHealthExport(c, "errors")
}

// customerHealthExportHits POST /api/customer-health/:user_id/export-hits
//
// 异步导出"详细缓存命中列表" HTML
// 写权限: admin + finance
func (s *Server) customerHealthExportHits(c *gin.Context) {
	s.enqueueHealthExport(c, "hits")
}

// enqueueHealthExport 复用逻辑: 提交 customer_health 异步导出任务
func (s *Server) enqueueHealthExport(c *gin.Context, kind string) {
	uid := parseInt(c.Param("user_id"))
	if uid == 0 {
		errResp(c, 400, "user_id 必填", nil)
		return
	}
	period := c.DefaultQuery("period", "48h")

	// 找 username (走 cache_logs_summary_by_user_5min 拿, sync 已填好)
	username := getUsernameByID(c.Request.Context(), uid)
	if username == "" {
		username = fmt.Sprintf("user_%d", uid)
	}

	// 操作人
	operator, _ := c.Get("auth_username")
	opName, _ := operator.(string)
	if opName == "" {
		opName = "unknown"
	}

	taskID, err := billing.EnqueueExportTask(
		c.Request.Context(),
		uid, username, period,
		"html",            // formats (健康度只支持 html, 不打包)
		"customer_health", // kind (复用 v2 enum, 加新值)
		kind,              // vendor_code: "errors" / "hits"
		opName,
		// api-ops 单站: 不传 site (rezeai-ops 9 参数版, 我们是 8 参数)
	)
	if err != nil {
		errResp(c, 500, "enqueue failed: "+err.Error(), nil)
		return
	}

	log.Printf("[customer-health] export queued task_id=%s user=%d kind=%s operator=%s",
		taskID, uid, kind, opName)

	ok(c, gin.H{
		"task_id": taskID,
		"status":  "pending",
		"kind":    kind,
		"period":  period,
	})
}

// customerHealthExportTasks GET /api/customer-health/export-tasks
//
// 任务中心列表, 只看 customer_health 类型
func (s *Server) customerHealthExportTasks(c *gin.Context) {
	status := c.Query("status")
	limit := queryLimit(c, 20, 200)
	offset := queryOffset(c)

	rows, total, err := dal.ListBillingExportTasks(c.Request.Context(), dal.BillingExportTaskQuery{
		Site:   "",
		Kind:   "customer_health", // 只看健康度任务
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		errResp(c, 500, "list failed: "+err.Error(), nil)
		return
	}

	c.Header("X-Data-Source", "ops_billing_export_tasks")
	ok(c, gin.H{
		"total": total,
		"items": rows,
	})
}

// customerHealthDownload GET /api/customer-health/export-tasks/:task_id/download
//
// 健康度任务下载, 跟 v2 流程一样但 file_path 在 /data/customer-health-exports/
func (s *Server) customerHealthDownload(c *gin.Context) {
	taskID := c.Param("task_id")
	t, err := dal.GetBillingExportTaskByTaskID(c.Request.Context(), taskID)
	if err != nil {
		errResp(c, 500, "query failed: "+err.Error(), nil)
		return
	}
	if t == nil {
		errResp(c, 404, "task not found", nil)
		return
	}
	if t.Kind != "customer_health" {
		errResp(c, 400, "task kind != customer_health", nil)
		return
	}
	if t.Status != "success" {
		errResp(c, 400, "task not ready, status="+t.Status, nil)
		return
	}
	if t.FilePath == "" {
		errResp(c, 500, "file_path is empty", nil)
		return
	}
	// 文件存在性检查
	if _, err := os.Stat(t.FilePath); err != nil {
		errResp(c, 500, "file not found on disk: "+err.Error(), nil)
		return
	}
	// 推断下载文件名
	downloadName := fmt.Sprintf("health_%s_%s_%s.html", t.Username, t.VendorCode, t.Period)
	c.FileAttachment(t.FilePath, downloadName)
}

// getUsernameByID 走 cache_logs_summary_by_user_5min 拿 username (sync 已填好, 不再查 RoDB)
//
// 查不到返回空字符串, 让 caller fallback "user_N"
// api-ops 单站: 不传 site 字段
func getUsernameByID(ctx context.Context, uid int) string {
	rows, err := dal.ListLogsSummaryByUser(ctx, dal.SummaryByUserQuery{
		UserID: uid,
		Limit:  1,
	})
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Username
}
