// HTTP API 层：所有路由 + handlers
// 鉴权 (A 阶段)：JWT 账号系统 (3 角色) + 老 OPS_API_TOKEN 兼容 (过渡)
package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/audit"
	"github.com/api-ops/api-ops/internal/auth"
	"github.com/api-ops/api-ops/internal/config"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/newapi_client"
	"github.com/api-ops/api-ops/internal/realtime"
	"github.com/gin-gonic/gin"
)

// Server API 服务器
type Server struct {
	cfg     *config.Config
	gin     *gin.Engine
	wsSrv   *realtime.Server
	authSvc *auth.Service // JWT service
	audit   *audit.Logger // 显式注入 (老代码里 audit.Middleware() 隐式)
	// nac newapi Admin API client（可能 nil：环境变量未设时跳过 admin 数据路径）
	nac *newapi_client.Client
}

// SetNewapiClient 注入 newapi Admin API client（main.go 启动后调用）
func (s *Server) SetNewapiClient(c *newapi_client.Client) {
	s.nac = c
}

// SetAuthService 注入 JWT service（main.go 启动后调用）
func (s *Server) SetAuthService(svc *auth.Service) {
	s.authSvc = svc
}

// SetAuditLogger 注入显式 audit logger（供 handler 主动写 audit）
func (s *Server) SetAuditLogger(l *audit.Logger) {
	s.audit = l
}

// New 创建 API server
func New(cfg *config.Config) *Server {
	if cfg.GinMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/api/health", "/healthz"},
	}))
	r.Use(corsMiddleware(cfg))
	// Q5 决策体现：审计 middleware 拦截所有 /api/* 写操作
	r.Use(audit.Middleware())

	s := &Server{cfg: cfg, gin: r}
	s.registerRoutes()
	return s
}

// MountWebSocket 挂载 P2 WebSocket 路由（main.go 在启动后调用）
func (s *Server) MountWebSocket(hub *realtime.Hub) {
	s.wsSrv = realtime.NewServer(hub)
	s.wsSrv.Mount(s.gin, nil) // WS 暂不挂 auth 中间件（demo）
}

// Run 启动
func (s *Server) Run(addr string) error {
	return s.gin.Run(addr)
}

// Gin 返回 gin 引擎（用于测试）
func (s *Server) Gin() *gin.Engine {
	return s.gin
}

// ===== 路由注册 =====

func (s *Server) registerRoutes() {
	// 健康检查
	r := s.gin
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// 公开 auth 端点 (不走 authMiddleware, 自身处理 login)
	pubAuth := r.Group("/api/auth")
	{
		pubAuth.POST("/login", s.loginHandler)
	}

	// 受保护 API（JWT 优先，老 token 兼容）
	api := r.Group("/api")
	api.Use(authMiddleware(s))
	{
		// 上游供应商管理
		//   GET: 全部 3 角色可读
		//   POST/PUT/DELETE: admin only (创建/修改/删除 vendor 是核心配置, 财务可读不可改)
		api.GET("/vendors", s.listVendors)
		api.POST("/vendors", requireRole(string(dal.OpsUserRoleAdmin)), s.createVendor)
		api.PUT("/vendors/:id", requireRole(string(dal.OpsUserRoleAdmin)), s.updateVendor)
		api.DELETE("/vendors/:id", requireRole(string(dal.OpsUserRoleAdmin)), s.deleteVendor)
		api.GET("/vendors/:code/channels", s.listVendorChannels)

		// 上游价目表
		//   GET: 全部 3 角色可读
		//   DELETE/IMPORT: admin + finance (财务需要能修价目做月对账)

		// 渠道-供应商映射
		//   GET: 全部
		//   POST/DELETE: admin + finance
		api.GET("/channel-vendors", s.listChannelVendors)
		api.POST("/channel-vendors", requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)), s.upsertChannelVendor)
		api.DELETE("/channel-vendors/:id", requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)), s.deleteChannelVendor)

		// A 阶段: 供应商管理 (核心页面, 渠道 + 自动折扣解析 + 矫正)
		//   GET /api/channel-mappings                        全部 49 渠道视图 (auto + final)
		//   POST /api/channel-mappings                       分配渠道给供应商 (admin+finance)
		//   DELETE /api/channel-mappings/:channel_id         解除映射
		//   POST /api/channel-mappings/:channel_id/correct-discount  矫正折扣 (admin+finance)
		//   POST /api/channel-mappings/reparse               重跑全部 auto 解析 (admin only)
		api.GET("/channel-mappings", s.listChannelMappings)
		api.POST("/channel-mappings", requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)), s.assignChannelVendor)
		api.DELETE("/channel-mappings/:channel_id", requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)), s.unassignChannelVendor)
		api.POST("/channel-mappings/:channel_id/correct-discount", requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)), s.correctChannelDiscount)
		api.POST("/channel-mappings/reparse", requireRole(string(dal.OpsUserRoleAdmin)), s.reparseAllChannelsDiscounts)

		// upstream 渠道列表（只读，来自 newapi DB）
		api.GET("/upstream/channels", s.listupstreamChannels)
		api.GET("/upstream/channels/:id", s.getupstreamChannel)

		// BILLING v2 (PR #4 / 8, 2026-06-14) - 6 端点, 全 admin/finance 写
		//
		// v1 客户对账 + 上游对账 + 利润分析 18 端点已下线 (2026-06-14):
		//   - 路由: 全删 (server.go)
		//   - handler: handlers_stmt.go 13 函数全删, 文件重写为 dashboard only
		//   - DB 表: archive.billing_statements / archive.billing_statement_lines (归档 schema, 只读)
		//   - SPA: 3 页面 (CustomerStatements / UpstreamStatements / ProfitAnalysis) 全删
		//   - 文档: docs/BILLING-RULES.md / docs/MONTHLY-RECONCILIATION.md → archive/v1-docs/
		//   - 备份: 仓库 git history 保留所有 v1 代码, 查找 commit "PR: v1 billing 下线" 之前的 history
		api.GET("/billing/v2/customer/current-month-overview", s.billingV2CurrentMonthOverview)
		api.POST("/billing/v2/customer/:uid/export-last-month",
			requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)),
			s.billingV2ExportLastMonth)
		api.GET("/billing/v2/customer/:uid/tasks", s.billingV2CustomerTasks)
		api.GET("/billing/v2/export-tasks", s.billingV2ExportTasks)
		api.GET("/billing/v2/export-tasks/:task_id/download", s.billingV2Download)
		api.POST("/billing/v2/export-tasks/:task_id/cancel",
			requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)),
			s.billingV2Cancel)

		// ===== BILLING v3 上游对账 (PR #4 / 7, 2026-06-14) =====
		// 复用 v2 download/cancel 端点 (按 kind 路由: customer 走客户对账, upstream 走上游对账)
		api.GET("/billing/v3/upstream/current-month-overview", s.billingV3UpstreamCurrentMonthOverview)
		api.POST("/billing/v3/upstream/export-last-month",
			requireRole(string(dal.OpsUserRoleAdmin), string(dal.OpsUserRoleFinance)),
			s.billingV3UpstreamExportLastMonth)
		api.GET("/billing/v3/upstream/:vendor_code/tasks", s.billingV3UpstreamVendorTasks)
		api.GET("/billing/v3/export-tasks", s.billingV3ExportTasks)
		// download + cancel 复用 v2 端点 (handler 按 task.kind 路由 ZIP 路径)

		// ===== BILLING v4 利润分析 (PR #4 / 6, 2026-06-14) =====
		// 1 端点, 复用 v2 revenue + v3 cost
		api.GET("/billing/v4/profit/overview", s.billingV4ProfitOverview)

		// Dashboard（运营总览）—— 数据从 newapi logs 实时聚合
		api.GET("/dashboard/today", s.dashboardToday)
		// /dashboard/trend-7d 7 天曲线 (不含今天), admin 1 轮 7 调用 + 后端 cache 5min (2026-06-15 加回)
		api.GET("/dashboard/trend-7d", s.dashboardTrend7d)
		// /dashboard/trend 已删除 (2026-06-14): admin API 不给按天趋势
		// TopX 3 路由全部禁用 (2026-06-14): 用户决策 - 全 admin API + 砍 TopX 卡片.
		// admin /api/log/stat 不带分组维度, admin /api/data/ 表空, 18次/5min 限流.
		// 恢复: 见 handlers_stmt.go 中 3 个 handler 函数的注释.
		// api.GET("/dashboard/top-customers", s.dashboardTopCustomers)
		// api.GET("/dashboard/top-models", s.dashboardTopModels)
		// api.GET("/dashboard/top-channels", s.dashboardTopChannels)

		// P1 监控 + 告警
		api.GET("/monitor/channels", s.listMonitorChannels)
		api.GET("/monitor/channels/:id/health", s.channelHealth)
		api.GET("/monitor/alerts", s.listAlerts)
		api.GET("/monitor/alerts/:id", s.getAlert)
		api.POST("/monitor/alerts/:id/ack", s.ackAlert)
		api.POST("/monitor/alerts/:id/resolve", s.resolveAlert)
		api.GET("/monitor/rules", s.listAlertRules)
		api.POST("/monitor/tick", s.monitorTickNow)

		// 健康/检查
		api.GET("/config", s.getConfig)

		// 审计日志
		api.GET("/audit/logs", s.listAuditLogs)
		api.GET("/audit/logs/:id", s.getAuditLog)

		// 管理员运行时配置
		api.GET("/admin/config", s.listAdminConfig)
		api.PUT("/admin/config", s.upsertAdminConfig)

		// P3 AI 错误解读
		api.POST("/ai/diagnose", s.aiDiagnose)
		api.GET("/ai/reports", s.aiListReports)
		api.GET("/ai/reports/:id", s.aiGetReport)
		api.POST("/ai/customer-health/:user_id", s.aiCustomerHealth)
		api.POST("/ai/cluster/tick", s.aiClusterTick)
		api.POST("/ai/report/daily", s.aiDailyReport)
	}

	// auth: 需要 JWT 鉴权
	authAPI := r.Group("/api/auth")
	authAPI.Use(authMiddleware(s))
	{
		authAPI.GET("/me", s.meHandler)
		authAPI.POST("/logout", s.logoutHandler)
		authAPI.POST("/change-password", s.changePasswordHandler)
	}

	// admin: 用户管理 (admin only)
	adminAPI := r.Group("/api/admin/users")
	adminAPI.Use(authMiddleware(s), requireRole(string(dal.OpsUserRoleAdmin)))
	{
		adminAPI.GET("", s.listUsersHandler)
		adminAPI.POST("", s.createUserHandler)
		adminAPI.PUT("/:id", s.updateUserHandler)
		adminAPI.POST("/:id/reset-password", s.adminResetPasswordHandler)
		adminAPI.DELETE("/:id", s.deleteUserHandler)
	}

	// SPA 前端（React + Vite）—— 非 API 路径全部返回 index.html
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.File("./web/dist/index.html")
	})
	r.Static("/assets", "./web/dist/assets")
}

// ===== 中间件 =====

// allowedCORSOrigins CORS 白名单 (从 env CORS_ALLOWED_ORIGINS 解析, 逗号分隔)
// 默认允许本地 + 远程 IP + mock 静态文件
func allowedCORSOrigins(cfg *config.Config) []string {
	raw := ""
	if cfg != nil {
		raw = cfg.CORSAllowedOrigins
	}
	// 默认白名单: 本地开发 + 远程 IP + 同源
	defaults := []string{
		"http://localhost:8088",
		"http://127.0.0.1:8088",
		"http://api-ops.example.com:8088",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	}
	if raw == "" {
		return defaults
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return defaults
	}
	return out
}

// isOriginAllowed 检查 origin 是否在白名单 (支持前缀 * 通配, 如 https://*.example.com)
func isOriginAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	for _, a := range allowed {
		if a == "*" {
			return true
		}
		if a == origin {
			return true
		}
		// 简单子域通配: https://*.x.com 匹配 https://abc.x.com
		if strings.Contains(a, "*.") {
			// 把 *. 拆成 prefix
			prefix := strings.SplitN(a, "*.", 2)[0]
			suffix := strings.SplitN(a, "*.", 2)[1]
			if strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

func corsMiddleware(cfg *config.Config) gin.HandlerFunc {
	allowed := allowedCORSOrigins(cfg)
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && isOriginAllowed(origin, allowed) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}
		// 不再回 * (P0-3 修)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// authMiddleware 鉴权中间件
//
// 优先顺序:
//  1. JWT (Authorization: Bearer <jwt>)    ← 新账号系统, 推荐
//  2. 老 OPS_API_TOKEN (兼容过渡期)         ← 仅 bootstrap admin 还没建出来前用
//  3. ?api_token=xxx query (浏览器静态页 demo 用, 生产应关)
//
// 例外路径 (不需要 auth):
//   - /api/health, /healthz
//   - /api/auth/login (登录接口)
//   - OPTIONS (CORS preflight)
//   - /api/ws/* (WebSocket 自己鉴权)
func authMiddleware(s *Server) gin.HandlerFunc {
	expectedToken := ""
	if s.cfg != nil {
		expectedToken = s.cfg.OpsAPIToken
	}
	skipPaths := map[string]bool{
		"/api/health":     true,
		"/healthz":        true,
		"/api/auth/login": true, // 登录接口公开
	}
	return func(c *gin.Context) {
		if skipPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/ws/") {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		// 1) 拿 token
		authHeader := c.GetHeader("Authorization")
		var token string
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if c.Request.URL.Path != "/" && strings.HasPrefix(c.Request.URL.Path, "/api/") {
			if q := c.Query("api_token"); q != "" {
				token = q
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing token (Authorization: Bearer <jwt> or ?api_token=)",
			})
			return
		}

		// 2) 优先 JWT
		if s.authSvc != nil {
			if claims, err := s.authSvc.ParseToken(token); err == nil {
				// 校验 pwd_ts: 若 password_changed_at 已推进 → 撤销
				ctx := c.Request.Context()
				u, dbErr := auth.GetUserByID(ctx, claims.UserID)
				if dbErr == nil && u != nil {
					if u.Status != dal.OpsUserStatusActive {
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user disabled"})
						return
					}
					if u.PasswordChangedAt > claims.PwdTS {
						c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
							"error": "token revoked (password changed)",
						})
						return
					}
					c.Set("auth_ok", true)
					c.Set("auth_method", "jwt")
					c.Set("auth_user_id", u.ID)
					c.Set("auth_username", u.Username)
					c.Set("auth_role", string(u.Role))
					c.Set("auth_display_name", u.DisplayName)
					c.Next()
					return
				}
				// JWT 合法但 DB 查不到用户 (被删了) → 拒
				if errors.Is(dbErr, auth.ErrUserNotFound) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
					return
				}
				// DB 短暂错误: 回退到兼容老 token
			}
		}

		// 3) 回退到老 OPS_API_TOKEN (仅当 JWT 失败)
		if expectedToken == "" {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "server misconfigured: OPS_API_TOKEN not set",
			})
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("auth_ok", true)
		c.Set("auth_method", "bearer_token_legacy")
		c.Set("auth_role", string(dal.OpsUserRoleAdmin)) // 老 token 等效 admin
		c.Set("auth_username", "legacy_token")
		c.Next()
	}
}

// requireRole RBAC 装饰器
// 用法: api.POST("/vendors", requireRole("admin", "finance"), s.createVendor)
func requireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		r, ok := c.Get("auth_role")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		role, _ := r.(string)
		if !allowed[role] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "insufficient role (required: " + strings.Join(roles, "/") + ", got: " + role + ")",
			})
			return
		}
		c.Next()
	}
}

// ===== 通用辅助 =====

func parseUint(s string) uint64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func parseInt(s string) int {
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func dayRangeFromQuery(c *gin.Context) (int64, int64) {
	start := parseInt64(c.Query("start"))
	end := parseInt64(c.Query("end"))
	if end == 0 {
		end = time.Now().Unix()
	}
	if start == 0 {
		start = end - 7*86400 // 默认 7 天
	}
	return start, end
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

func errResp(c *gin.Context, status int, msg string, detail interface{}) {
	c.JSON(status, gin.H{
		"success": false,
		"error": gin.H{
			"message": msg,
			"detail":  detail,
		},
	})
}

func queryLimit(c *gin.Context, def, max int) int {
	v := parseInt(c.Query("limit"))
	if v <= 0 {
		v = def
	}
	if v > max {
		v = max
	}
	return v
}

func queryOffset(c *gin.Context) int {
	return parseInt(c.Query("offset"))
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
