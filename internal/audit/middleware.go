// Package audit: 操作审计中间件（PRD §11.2 / Q5 决策体现）
//
// 设计：
//   - 拦截所有写操作：POST / PUT / DELETE / PATCH
//   - 异步写 audit_logs（不阻塞业务请求）
//   - 不记录业务失败（response 4xx/5xx）—— 避免 audit 自身拖死业务
//   - request_body truncate 到 1KB（避免大文件上传占满 DB）
//
// 写操作全覆盖（Q5 决策体现）：
//   - 账单确认 / 价目删除 / 告警 ACK / AI 报告生成 / 客户封禁 / 配置变更
//
// 字段：user_id / username / action / resource_type / resource_id / method /
//
//	path / ip / user_agent / request_body / response_status / duration_ms
package audit

import (
	"bytes"
	"context"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// BodyMaxBytes request_body 最大字节数（1KB）—— 超过则截断
const BodyMaxBytes = 1024

// Middleware Gin 中间件：拦截写操作
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		// 只审计写操作
		if method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
			c.Next()
			return
		}

		// 读 body 全量（不要 truncate，避免下游 handler 解析失败）。
		// audit 持久化时再单独截断到 1KB（见 auditPersist 逻辑）。
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			_ = c.Request.Body.Close()
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		start := time.Now()
		c.Next()
		dur := time.Since(start).Milliseconds()

		status := c.Writer.Status()
		// Q5 决策：失败不记录（避免 audit 自身拖死业务）
		if status >= 400 {
			return
		}

		// 写 audit 异步（不阻塞返回）
		entry := &dal.AuditLog{
			UserID:         pickUint64FromCtx(c, "auth_user_id", "user_id"),
			Username:       pickStringFromCtx(c, "auth_username", "username"),
			Action:         deriveAction(method, c.FullPath()),
			Method:         method,
			Path:           c.Request.URL.Path,
			ResourceType:   resourceTypeFromPath(c.Request.URL.Path),
			ResourceID:     resourceIDFromPath(c.Request.URL.Path),
			IP:             c.ClientIP(),
			UserAgent:      c.Request.UserAgent(),
			RequestBody:    truncateBody(bodyBytes, BodyMaxBytes),
			ResponseStatus: status,
			DurationMs:     int(dur),
		}
		go writeAudit(entry)
	}
}

// truncateBody body 截断（仅在 audit 持久化时截断，不影响下游 handler 看到的 body）
func truncateBody(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated " + strconv.Itoa(len(b)-max) + " bytes)"
}

// writeAudit 异步落库（单独 goroutine；失败仅 log，不影响业务）
func writeAudit(entry *dal.AuditLog) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[audit] panic: %v", r)
		}
	}()
	if dal.OPS == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := dal.CreateAuditLog(ctx, entry); err != nil {
		log.Printf("[audit] write failed: %v", err)
	}
}

// deriveAction 从 method + route path 派生 action 字符串
// 例：POST /api/vendors → "post vendors"
func deriveAction(method, routePath string) string {
	if routePath == "" {
		routePath = "(unknown)"
	}
	// 去前缀 /api/
	p := strings.TrimPrefix(routePath, "/api/")
	// /api/monitor/alerts/:id/ack → "monitor.alerts.:id.ack"
	parts := strings.Split(p, "/")
	return strings.ToLower(method) + " " + strings.Join(parts, ".")
}

func resourceTypeFromPath(path string) string {
	p := strings.TrimPrefix(path, "/api/")
	parts := strings.Split(p, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func resourceIDFromPath(path string) string {
	p := strings.TrimPrefix(path, "/api/")
	parts := strings.Split(p, "/")
	for _, seg := range parts {
		if isNumber(seg) {
			return seg
		}
	}
	return ""
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func uint64FromCtx(c *gin.Context, key string) uint64 {
	v, ok := c.Get(key)
	if !ok {
		return 0
	}
	if n, ok := v.(uint64); ok {
		return n
	}
	if n, ok := v.(int); ok {
		return uint64(n)
	}
	return 0
}

// pickUint64FromCtx 先试 primary key, fallback 到 secondary
// 用于 A 阶段: 新 middleware 写 "auth_user_id", 老代码可能写 "user_id"
func pickUint64FromCtx(c *gin.Context, primary, secondary string) uint64 {
	if v := uint64FromCtx(c, primary); v != 0 {
		return v
	}
	return uint64FromCtx(c, secondary)
}

func stringFromCtx(c *gin.Context, key string) string {
	v, ok := c.Get(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// pickStringFromCtx 同上
func pickStringFromCtx(c *gin.Context, primary, secondary string) string {
	if v := stringFromCtx(c, primary); v != "" {
		return v
	}
	return stringFromCtx(c, secondary)
}
