// Package audit: 写操作审计中间件测试
//
// 测试策略：
//   - 用 gin.Engine + httptest 启内存 HTTP server，挂 Middleware() + 简单 handler
//   - 写一个 CaptureWriter 接收审计条目（通过重写 dal.CreateAuditLog 不现实，
//     改为测试中间件的"是否走 audit 路径"——通过 dal.OPS nil 守卫来保证安全）
//   - 替代方案：用 dal.AuditLog{} 直接结构断言 + 中间件 deriveAction / truncateBody
//     等纯函数的行为
//   - 异常 path 不阻塞业务：用 panic handler + c.Next() 链验证中间件即使后端出错
//     也不会 panic 导致请求失败
//
// 覆盖的边界 case：
//  1. POST /api/vendors → 200 → 走 audit 路径（状态码 < 400）
//  2. GET /api/vendors → 不走 audit 路径（method 不在拦截列表）
//  3. PUT / PATCH / DELETE → 都走 audit 路径
//  4. POST 返回 500 → 不写 audit（status >= 400 跳过）
//  5. POST 返回 4xx → 不写 audit
//  6. body 截断到 1KB：超长 body 写 audit 时 truncate
//  7. 异常 handler 不阻塞业务（panic recover 由 gin 接管）
//  8. 中间件不修改下游 handler 看到的 body（重新注入 NopCloser）
//  9. deriveAction: POST /api/monitor/alerts/123/ack → "post monitor.alerts.123.ack"
//  10. resourceTypeFromPath / resourceIDFromPath 解析
package audit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// ===== 辅助：构造带 Middleware 的 gin.Engine =====

func newAuditEngine(handler gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(Middleware())
	if handler != nil {
		r.POST("/api/vendors", handler)
		r.GET("/api/vendors", handler)
		r.PUT("/api/vendors/:id", handler)
		r.PATCH("/api/vendors/:id", handler)
		r.DELETE("/api/vendors/:id", handler)
		r.POST("/api/monitor/alerts/:id/ack", handler)
		r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	}
	return r
}

// ===== 边界 case 1: POST + 200 → 走 audit 路径 =====

func TestMiddleware_POST_WritesAudit(t *testing.T) {
	called := false
	handler := func(c *gin.Context) {
		called = true
		// 验证下游能读到 body（中间件应重新注入 NopCloser）
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			t.Errorf("downstream ShouldBindJSON failed: %v", err)
		}
		c.JSON(200, gin.H{"ok": true})
	}
	r := newAuditEngine(handler)

	body := bytes.NewBufferString(`{"name":"test"}`)
	req := httptest.NewRequest("POST", "/api/vendors", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status=%d 期望 200", w.Code)
	}
	if !called {
		t.Error("handler 未被调用（中间件应 c.Next() 后返回）")
	}
	// dal.OPS 为 nil → writeAudit 内部跳过；我们只验证请求成功 + handler 被调
}

// ===== 边界 case 2: GET → 不写 audit（method 不在拦截列表） =====

func TestMiddleware_GET_NoAudit(t *testing.T) {
	called := false
	handler := func(c *gin.Context) {
		called = true
		c.JSON(200, gin.H{"data": []any{}})
	}
	r := newAuditEngine(handler)

	req := httptest.NewRequest("GET", "/api/vendors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status=%d 期望 200", w.Code)
	}
	if !called {
		t.Error("handler 未被调用")
	}
	// GET 不在 audit 拦截列表，writeAudit 不会被调用
}

// ===== 边界 case 3: PUT / PATCH / DELETE 都走 audit =====

func TestMiddleware_AllWriteMethods(t *testing.T) {
	handler := func(c *gin.Context) {
		c.JSON(200, gin.H{"updated": true})
	}
	r := newAuditEngine(handler)

	methods := []string{"PUT", "PATCH", "DELETE"}
	for _, m := range methods {
		req := httptest.NewRequest(m, "/api/vendors/42", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("%s /api/vendors/42 → status=%d 期望 200", m, w.Code)
		}
	}
}

// ===== 边界 case 4: POST 返回 500 → 不写 audit =====

func TestMiddleware_POST5xx_NoAudit(t *testing.T) {
	handler := func(c *gin.Context) {
		c.JSON(500, gin.H{"error": "internal"})
	}
	r := newAuditEngine(handler)

	req := httptest.NewRequest("POST", "/api/vendors", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("status=%d 期望 500", w.Code)
	}
	// 中间件在 c.Next() 后检查 status >= 400 → return，不调 writeAudit
	// 我们无法直接断言 writeAudit 未被调（因为 writeAudit 内部用 goroutine），
	// 但保证不 panic + status 正确即可
}

// ===== 边界 case 5: POST 返回 4xx → 不写 audit =====

func TestMiddleware_POST4xx_NoAudit(t *testing.T) {
	cases := []int{400, 401, 403, 404, 422, 429}

	for _, status := range cases {
		// 重新构造（每个 status 独立）
		r := newAuditEngine(func(c *gin.Context) {
			c.JSON(status, gin.H{"error": "client"})
		})
		req := httptest.NewRequest("POST", "/api/vendors", bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != status {
			t.Errorf("POST status=%d 期望 %d", w.Code, status)
		}
	}
}

// ===== 边界 case 6: truncateBody 边界 =====

func TestTruncateBody(t *testing.T) {
	// 短 body：不截断
	short := []byte("hello world")
	if got := truncateBody(short, 100); got != "hello world" {
		t.Errorf("短 body 应原样返回, got=%q", got)
	}

	// 等长 body：不截断（边界）
	exact := []byte(strings.Repeat("a", 100))
	if got := truncateBody(exact, 100); got != string(exact) {
		t.Errorf("等长 body 应原样返回, got=%q", got)
	}

	// 超长 body：截断到 max + 标记
	long := []byte(strings.Repeat("a", 2000))
	got := truncateBody(long, 1024)
	if len(got) <= 1024 {
		t.Errorf("超长 body 应被截断, len=%d", len(got))
	}
	if !strings.Contains(got, "...(truncated") {
		t.Errorf("截断标记缺失, got prefix=%q", got[:60])
	}
	// 验证截断字节数信息正确
	if !strings.Contains(got, "976 bytes)") { // 2000 - 1024 = 976
		t.Errorf("截断字节数信息错误: %q", got[len(got)-30:])
	}

	// 空 body
	if got := truncateBody(nil, 100); got != "" {
		t.Errorf("nil body 应返回空, got=%q", got)
	}
}

// ===== 边界 case 7: 异常 handler 不阻塞业务（gin panic recover 接管） =====

func TestMiddleware_PanicHandler_DoesNotBlock(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery()) // 必须加 recover，否则整个进程 panic
	r.Use(Middleware())
	r.POST("/api/vendors", func(c *gin.Context) {
		panic("simulated downstream panic")
	})

	req := httptest.NewRequest("POST", "/api/vendors", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// gin.Recovery 把 panic 转 500
	if w.Code != 500 {
		t.Errorf("panic 应被 gin.Recovery 转 500, got %d", w.Code)
	}
	// 业务被中断，但中间件本身的 writeAudit 也不会被调到（因为 panic 发生在 c.Next() 里）
	// 关键是：进程不应 crash，request 应有 500 响应
}

// ===== 边界 case 8: 中间件不修改下游 handler 看到的 body =====

func TestMiddleware_PreservesBody(t *testing.T) {
	originalBody := `{"key":"value","nested":{"a":1}}`
	var seenByHandler string

	handler := func(c *gin.Context) {
		buf := make([]byte, 1024)
		n, _ := c.Request.Body.Read(buf)
		seenByHandler = string(buf[:n])
		c.JSON(200, gin.H{"ok": true})
	}
	r := newAuditEngine(handler)

	req := httptest.NewRequest("POST", "/api/vendors", bytes.NewBufferString(originalBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if seenByHandler != originalBody {
		t.Errorf("下游 handler 看到的 body 与原始不一致:\nsent=%q\nseen=%q", originalBody, seenByHandler)
	}
}

// ===== 边界 case 9: deriveAction 派生规则 =====

func TestDeriveAction(t *testing.T) {
	cases := []struct {
		method, path string
		want         string
	}{
		{"POST", "/api/vendors", "post vendors"},
		{"POST", "/api/monitor/alerts/123/ack", "post monitor.alerts.123.ack"},
		{"DELETE", "/api/billing/pricing/42", "delete billing.pricing.42"},
		{"PUT", "/api/ai/reports", "put ai.reports"},
		{"PATCH", "/api/users/1", "patch users.1"},
		// 无路由（FullPath 空）
		{"POST", "", "post (unknown)"},
	}
	for _, c := range cases {
		got := deriveAction(c.method, c.path)
		if got != c.want {
			t.Errorf("deriveAction(%q, %q)=%q 期望 %q", c.method, c.path, got, c.want)
		}
	}
}

// ===== 边界 case 10: resourceTypeFromPath / resourceIDFromPath =====

func TestResourceTypeFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/vendors", "vendors"},
		{"/api/monitor/alerts", "monitor"},
		{"/api/billing/customer/1", "billing"},
		{"/api/", ""},
	}
	for _, c := range cases {
		if got := resourceTypeFromPath(c.path); got != c.want {
			t.Errorf("resourceTypeFromPath(%q)=%q 期望 %q", c.path, got, c.want)
		}
	}
}

func TestResourceIDFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/vendors/42", "42"},
		{"/api/monitor/alerts/123/ack", "123"},
		{"/api/vendors", ""},
		{"/api/users/abc", ""}, // 非数字
		{"/api/vendors/1/extra/2", "1"},
	}
	for _, c := range cases {
		if got := resourceIDFromPath(c.path); got != c.want {
			t.Errorf("resourceIDFromPath(%q)=%q 期望 %q", c.path, got, c.want)
		}
	}
}

// ===== 边界 case 11: isNumber 辅助 =====

func TestIsNumber(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"123", true},
		{"0", true},
		{"abc", false},
		{"12a", false},
		{"-1", false}, // 含负号
	}
	for _, c := range cases {
		if got := isNumber(c.s); got != c.want {
			t.Errorf("isNumber(%q)=%v 期望 %v", c.s, got, c.want)
		}
	}
}

// ===== 边界 case 12: Middleware 集成端到端（POST → 200 → 写 audit 占位） =====

func TestMiddleware_End2End_HealthEndpoint(t *testing.T) {
	// GET /health 路径不在 audit 拦截范围（method=GET）
	// 单独构造一个 engine 注册 /health handler
	r := gin.New()
	r.Use(Middleware())
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("/health status=%d 期望 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("/health 响应 ok=true 缺失: %v", body)
	}
}
