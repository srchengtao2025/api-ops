package audit

import (
	"context"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// Logger 显式 logger (供 handler 内部主动记录用, 与 Middleware() 互补)
// 例: 登录成功 / 改密 / admin 操作 → 业务逻辑里显式 Log()
type Logger struct {
	// 留空: 全部用 package-level writeAudit (复用 middleware 同一路径)
}

func NewLogger() *Logger { return &Logger{} }

// Log 主动写一条 audit (gorm 异步, 不阻塞)
// 从 gin.Context 读 auth_user_id / auth_username (authMiddleware 已设)
func (l *Logger) Log(c *gin.Context, action, resourceType, resourceID, summary string, extra map[string]interface{}) error {
	uid := pickUint64FromCtx(c, "auth_user_id", "user_id")
	uname := pickStringFromCtx(c, "auth_username", "username")
	// 把 extra 序列化成 body? 简单起见塞 remark (但 AuditLog 没 remark 字段)
	// 走 path/method 不变, summary 进 action
	entry := &dal.AuditLog{
		UserID:         uid,
		Username:       uname,
		Action:         action,
		Method:         c.Request.Method,
		Path:           c.Request.URL.Path,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		IP:             c.ClientIP(),
		UserAgent:      c.Request.UserAgent(),
		RequestBody:    summary + formatExtra(extra),
		ResponseStatus: 0, // 主动 Log 不带 response
		DurationMs:     0,
	}
	// 异步写
	go func() {
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
			log.Printf("[audit] Log failed: %v", err)
		}
	}()
	return nil
}

func formatExtra(extra map[string]interface{}) string {
	if len(extra) == 0 {
		return ""
	}
	// 简易 JSON-ish 拼字符串 (避免拉 encoding/json)
	// 格式: " | extra: k1=v1, k2=v2"
	s := " | extra: "
	for k, v := range extra {
		s += k + "=" + toString(v) + ", "
	}
	return s
}

func toString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return intToStr(x)
	case int64:
		return int64ToStr(x)
	case uint64:
		return int64ToStr(int64(x))
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return "<unsupported>"
	}
}

func intToStr(i int) string     { return fmtInt(int64(i)) }
func int64ToStr(i int64) string { return fmtInt(i) }
func fmtInt(i int64) string     { return _itoa(i) }

// _itoa 简单整数转字符串
func _itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
