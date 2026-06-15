// 管理员运行时配置 API
// 路由：
//
//	GET /api/admin/config          列出所有 system_config（含飞书 webhook 是否配置）
//	PUT /api/admin/config          修改/新建一项（key + value）
//
// 关键点（Q5 决策体现）：
//   - admin 改自己系统的配置也要审计 —— PUT 请求本身走 audit middleware
//   - feishu_webhook_* 这类 key 改完后，notifier 缓存需要清掉
//     （下次 Send 重新 load）；这里通过 ReloadConfig callback 通知 notifier
package api

import (
	"encoding/json"
	"log"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// ReloadNotifierFunc 通知 notifier 清缓存的回调（避免 api 包 import notifier 形成环）
type ReloadNotifierFunc func()

// AdminConfigGetter 注入（cmd/server/main.go 初始化时 wire）
var ReloadNotifier ReloadNotifierFunc = func() {
	// 默认 no-op；server 启动时会被覆盖
}

// listAdminConfig GET /api/admin/config
// 返回所有 system_config，并把飞书 webhook 的 url 是否为空翻译为 feishu_configured bool
func (s *Server) listAdminConfig(c *gin.Context) {
	rows, err := dal.ListSystemConfigs(c.Request.Context())
	if err != nil {
		errResp(c, 500, "list system_config failed", err.Error())
		return
	}
	feishuConfigured := false
	for _, r := range rows {
		if r.Key == "feishu_webhook_alert" || r.Key == "feishu_webhook_report" {
			// value 是 JSON {"url":"...","secret":"..."}; url 非空才算配置
			var m map[string]string
			if err := json.Unmarshal([]byte(r.Value), &m); err == nil {
				if m["url"] != "" {
					feishuConfigured = true
					break
				}
			}
		}
	}
	ok(c, gin.H{
		"items":             rows,
		"feishu_configured": feishuConfigured,
	})
}

// upsertAdminConfigRequest PUT /api/admin/config body
type upsertAdminConfigRequest struct {
	Key         string `json:"key" binding:"required"`
	Value       string `json:"value" binding:"required"`
	Description string `json:"description"`
	UpdatedBy   string `json:"updated_by"` // 可选：admin 名称
}

// upsertAdminConfig PUT /api/admin/config
//   - upsert 到 system_config
//   - 如果是 feishu_webhook_* key，触发 notifier reload
func (s *Server) upsertAdminConfig(c *gin.Context) {
	var req upsertAdminConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp(c, 400, "invalid body: "+err.Error(), nil)
		return
	}
	if req.UpdatedBy == "" {
		req.UpdatedBy = "admin"
	}
	row := &dal.SystemConfig{
		Key:         req.Key,
		Value:       req.Value,
		Description: req.Description,
		UpdatedBy:   req.UpdatedBy,
	}
	if err := dal.UpsertSystemConfig(c.Request.Context(), row); err != nil {
		errResp(c, 500, "upsert system_config failed", err.Error())
		return
	}
	// 通知 notifier reload（如果是飞书相关 key）
	if req.Key == "feishu_webhook_alert" || req.Key == "feishu_webhook_report" {
		if ReloadNotifier != nil {
			ReloadNotifier()
		}
		log.Printf("[admin] feishu config %s updated → notifier reloaded", req.Key)
	}
	ok(c, gin.H{"key": req.Key, "updated_by": req.UpdatedBy})
}
