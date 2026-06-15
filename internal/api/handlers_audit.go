// 审计日志 API handlers
// 路由：
//
//	GET  /api/audit/logs         列表（user_id / action / start / end / limit / offset）
//	GET  /api/audit/logs/:id     详情
package api

import (
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// listAuditLogs GET /api/audit/logs
//
//	query: user_id / action / resource_type / start / end / limit=50&offset=0
func (s *Server) listAuditLogs(c *gin.Context) {
	q := dal.AuditLogQuery{
		UserID:       parseUint(c.Query("user_id")),
		Username:     c.Query("username"),
		Action:       c.Query("action"),
		ResourceType: c.Query("resource_type"),
		Start:        parseInt64(c.Query("start")),
		End:          parseInt64(c.Query("end")),
		Limit:        queryLimit(c, 50, 500),
		Offset:       queryOffset(c),
	}
	rows, total, err := dal.ListAuditLogs(c.Request.Context(), q)
	if err != nil {
		errResp(c, 500, "list audit logs failed", err.Error())
		return
	}
	ok(c, gin.H{"total": total, "items": rows})
}

// getAuditLog GET /api/audit/logs/:id
func (s *Server) getAuditLog(c *gin.Context) {
	id := parseUint(c.Param("id"))
	if id == 0 {
		errResp(c, 400, "id 必填", nil)
		return
	}
	e, err := dal.GetAuditLog(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get audit log failed", err.Error())
		return
	}
	if e == nil {
		errResp(c, 404, "audit log not found", nil)
		return
	}
	ok(c, e)
}
