// BILLING v2 API 端点 (2026-06-14 RFC PR #4)
//
// 6 端点:
//
//	GET  /api/billing/v2/customer/current-month-overview     默认页: 27 用户当月累计
//	POST /api/billing/v2/customer/:uid/export-last-month    创建导出任务
//	GET  /api/billing/v2/customer/:uid/tasks                 单用户历史任务
//	GET  /api/billing/v2/export-tasks                        任务中心列表 (所有用户)
//	GET  /api/billing/v2/export-tasks/:task_id/download      下载 zip
//	POST /api/billing/v2/export-tasks/:task_id/cancel        取消 (仅 pending)
//
// RBAC:
//   - 读 (overview/tasks/列表/下载): admin/finance/viewer
//   - 写 (export/cancel): admin/finance
//
// 限流: 每用户 ≤ 2 running (在 EnqueueExportTask 内查 DB count, RFC §1 Q3)
package api

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/audit"
	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// ===== 1. GET /api/billing/v2/customer/current-month-overview =====
func (s *Server) billingV2CurrentMonthOverview(c *gin.Context) {
	if !dal.HasRoDB() {
		errResp(c, 503, "RoDB not configured", nil)
		return
	}
	// 本月 1 号 00:00 ~ 现在
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	endOfNow := now.Unix()

	// 1 SQL: GROUP BY user_id (4 token + USD)
	sql := `
SELECT
  user_id,
  COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
  COALESCE(SUM((other::jsonb->>'cache_tokens')::bigint), 0) AS cache_tokens,
  COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
  
  
  COALESCE(SUM(quota), 0) / 500000.0 AS revenue_usd,
  COUNT(*) AS request_count
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND type = 2
GROUP BY user_id
ORDER BY revenue_usd DESC
LIMIT 1000`
	rows, err := dal.RoDB().WithContext(c.Request.Context()).Raw(sql, startOfMonth, endOfNow).Rows()
	if err != nil {
		errResp(c, 500, "query failed: "+err.Error(), nil)
		return
	}
	defer rows.Close()

	type row struct {
		UserID           int
		Username         string `json:"username"`
		PromptTokens     int64  `json:"prompt_tokens"`
		CompletionTokens int64  `json:"completion_tokens"`
		CacheTokens      int64  `json:"cache_tokens"`

		RevenueUSD   float64 `json:"revenue_usd"`
		RequestCount int64   `json:"request_count"`
	}

	// 缓存 username map (避免 1000 次 DB 查询, 走 upstream_user_cache 5min sync 拉的)
	usernameMap := map[int]string{}
	if rows2, err := dal.OPS.WithContext(c.Request.Context()).
		Table("upstream_user_cache").
		Select("id, username").
		Rows(); err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var id int
			var name string
			if err := rows2.Scan(&id, &name); err == nil {
				usernameMap[id] = name
			}
		}
	}

	var out []row
	totalRevenue := 0.0
	totalTokens := int64(0)
	for rows.Next() {
		var r row
		if err := rows.Scan(
			&r.UserID, &r.PromptTokens, &r.CompletionTokens,
			&r.CacheTokens, &r.RevenueUSD, &r.RequestCount,
		); err != nil {
			errResp(c, 500, "scan failed: "+err.Error(), nil)
			return
		}
		r.Username = usernameMap[r.UserID]
		if r.Username == "" {
			r.Username = fmt.Sprintf("user#%d", r.UserID)
		}
		totalRevenue += r.RevenueUSD
		totalTokens += r.PromptTokens + r.CompletionTokens
		out = append(out, r)
	}
	if out == nil {
		out = []row{}
	}

	c.Header("X-Data-Source", "direct_db_logs_groupby_user")
	ok(c, gin.H{
		"period_start":  startOfMonth,
		"period_end":    endOfNow,
		"user_count":    len(out),
		"total_revenue": totalRevenue,
		"total_tokens":  totalTokens,
		"items":         out,
	})
}

// ===== 2. POST /api/billing/v2/customer/:uid/export-last-month =====
func (s *Server) billingV2ExportLastMonth(c *gin.Context) {
	uidStr := c.Param("uid")
	uid, err := strconv.Atoi(uidStr)
	if err != nil || uid <= 0 {
		errResp(c, 400, "invalid uid", nil)
		return
	}
	operator := getAuthUsername(c)
	role := getAuthRole(c)
	if role != "admin" && role != "finance" {
		errResp(c, 403, "insufficient role (admin/finance required)", nil)
		return
	}

	// body: { formats: "html" | "xlsx" | "html,xlsx" }
	var body struct {
		Formats string `json:"formats"`
	}
	// body 可选, 缺省 html,xlsx
	if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		errResp(c, 400, "invalid body: "+err.Error(), nil)
		return
	}
	if body.Formats == "" {
		body.Formats = "html,xlsx"
	}
	// 校验 formats
	want := strings.Split(body.Formats, ",")
	for _, f := range want {
		if f != "html" && f != "xlsx" {
			errResp(c, 400, "invalid format: "+f+" (want html/xlsx/html,xlsx)", nil)
			return
		}
	}

	// 查 username
	u, err := dal.GetUser(c.Request.Context(), uid)
	if err != nil || u == nil {
		errResp(c, 404, "user not found", nil)
		return
	}

	// 上月 = 上个自然月
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	lastMonth := now.AddDate(0, -1, 0)
	period := lastMonth.Format("2006-01")

	// 入队 (内部限流: 每用户 ≤ 2 running)
	taskID, err := billing.EnqueueExportTask(c.Request.Context(), uid, u.Username, period, body.Formats, "customer", "", operator)
	if err != nil {
		// 限流报错
		errResp(c, 429, err.Error(), nil)
		return
	}

	// audit
	_ = audit.NewLogger().Log(c, "billing.export.create", "billing_export_task", taskID,
		"create export task for user "+u.Username+" period="+period,
		map[string]interface{}{
			"user_id":  uid,
			"period":   period,
			"formats":  body.Formats,
			"operator": operator,
		})

	c.Header("X-Data-Source", "billing_v2_worker_pool")
	ok(c, gin.H{
		"task_id": taskID,
		"user_id": uid,
		"period":  period,
		"formats": body.Formats,
		"status":  "pending",
	})
}

// ===== 3. GET /api/billing/v2/customer/:uid/tasks =====
func (s *Server) billingV2CustomerTasks(c *gin.Context) {
	uidStr := c.Param("uid")
	uid, _ := strconv.Atoi(uidStr)
	if uid <= 0 {
		errResp(c, 400, "invalid uid", nil)
		return
	}
	limit := parseInt(c.Query("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	status := c.Query("status")
	rows, total, err := dal.ListBillingExportTasks(c.Request.Context(), dal.BillingExportTaskQuery{
		UserID: uid, Status: status, Limit: limit,
	})
	if err != nil {
		errResp(c, 500, "list failed: "+err.Error(), nil)
		return
	}
	c.Header("X-Data-Source", "ops_billing_export_tasks")
	ok(c, gin.H{
		"items": rows,
		"total": total,
		"limit": limit,
	})
}

// ===== 4. GET /api/billing/v2/export-tasks (任务中心) =====
func (s *Server) billingV2ExportTasks(c *gin.Context) {
	limit := parseInt(c.Query("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	status := c.Query("status")
	rows, total, err := dal.ListBillingExportTasks(c.Request.Context(), dal.BillingExportTaskQuery{
		Status: status, Limit: limit,
	})
	if err != nil {
		errResp(c, 500, "list failed: "+err.Error(), nil)
		return
	}
	c.Header("X-Data-Source", "ops_billing_export_tasks")
	ok(c, gin.H{
		"items": rows,
		"total": total,
		"limit": limit,
	})
}

// ===== 5. GET /api/billing/v2/export-tasks/:task_id/download =====
func (s *Server) billingV2Download(c *gin.Context) {
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
	if t.Status != "success" {
		errResp(c, 400, "task not ready, status="+t.Status, nil)
		return
	}
	if t.FilePath == "" {
		errResp(c, 500, "file_path is empty", nil)
		return
	}
	// 检查文件存在
	info, err := os.Stat(t.FilePath)
	if err != nil {
		errResp(c, 500, "file not found on disk: "+err.Error(), nil)
		return
	}
	// 流式下载
	filename := fmt.Sprintf("%s_%s_statement.zip", t.Username, t.Period)
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	c.Header("X-Task-ID", taskID)
	c.File(t.FilePath)
}

// ===== 6. POST /api/billing/v2/export-tasks/:task_id/cancel =====
func (s *Server) billingV2Cancel(c *gin.Context) {
	role := getAuthRole(c)
	if role != "admin" && role != "finance" {
		errResp(c, 403, "insufficient role (admin/finance required)", nil)
		return
	}
	taskID := c.Param("task_id")
	operator := getAuthUsername(c)
	affected, err := dal.CancelPendingBillingExportTask(c.Request.Context(), taskID, operator)
	if err != nil {
		errResp(c, 500, "cancel failed: "+err.Error(), nil)
		return
	}
	if affected == 0 {
		errResp(c, 400, "task not pending (already running/success/failed/cancelled)", nil)
		return
	}
	_ = audit.NewLogger().Log(c, "billing.export.cancel", "billing_export_task", taskID,
		"cancel pending task by "+operator,
		map[string]interface{}{
			"operator": operator,
		})
	ok(c, gin.H{
		"task_id":  taskID,
		"status":   "cancelled",
		"affected": affected,
	})
}

// ===== helpers =====

func getAuthUsername(c *gin.Context) string {
	if v, ok := c.Get("auth_username"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getAuthRole(c *gin.Context) string {
	if v, ok := c.Get("auth_role"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// 防止 http 包 unused 警告
var _ = http.StatusOK
