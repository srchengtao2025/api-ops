package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/api-ops/api-ops/internal/auth"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
)

// LoginRequest POST /api/auth/login
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse
type LoginResponse struct {
	Token       string `json:"token"`
	TokenType   string `json:"token_type"` // "Bearer"
	ExpiresIn   int64  `json:"expires_in"` // seconds
	UserID      uint64 `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// loginHandler 公开 (不走 authMiddleware)
// 注册到 /api/auth/login (no auth)
func (s *Server) loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return
	}
	ctx := c.Request.Context()
	u, err := auth.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed: " + err.Error()})
		return
	}
	if u.Status != dal.OpsUserStatusActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "user disabled"})
		return
	}
	if !s.authSvc.VerifyPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}
	// 签发 JWT
	tok, err := s.authSvc.IssueToken(u.ID, u.Username, string(u.Role), u.PasswordChangedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "issue token: " + err.Error()})
		return
	}
	_ = auth.TouchLastLogin(ctx, u.ID)
	// 审计: 登录成功
	if s.audit != nil {
		_ = s.audit.Log(c, "auth.login", "user", u.Username, "login ok", map[string]interface{}{
			"user_id": u.ID, "role": u.Role,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": LoginResponse{
			Token:       tok,
			TokenType:   "Bearer",
			ExpiresIn:   int64(s.authSvc.TokenTTL.Seconds()),
			UserID:      u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        string(u.Role),
		},
	})
}

// meHandler GET /api/auth/me
// 需要 JWT
func (s *Server) meHandler(c *gin.Context) {
	uidAny, _ := c.Get("auth_user_id")
	uid, _ := uidAny.(uint64)
	if uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not logged in"})
		return
	}
	u, err := auth.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":       u.ID,
			"username":      u.Username,
			"display_name":  u.DisplayName,
			"email":         u.Email,
			"role":          u.Role,
			"last_login_at": u.LastLoginAt,
		},
	})
}

// logoutHandler POST /api/auth/logout
// 前端清 localStorage, 服务端无需真撤 (因 24h 短 + 改密撤销机制)
// 但仍记 audit, 便于追溯
func (s *Server) logoutHandler(c *gin.Context) {
	uidAny, _ := c.Get("auth_user_id")
	uid, _ := uidAny.(uint64)
	usernameAny, _ := c.Get("auth_username")
	username, _ := usernameAny.(string)
	if s.audit != nil {
		_ = s.audit.Log(c, "auth.logout", "user", username, "logout", map[string]interface{}{
			"user_id": uid,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"logged_out": true}})
}

// changePasswordHandler POST /api/auth/change-password
// 已登录用户改自己密码
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

func (s *Server) changePasswordHandler(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	uidAny, _ := c.Get("auth_user_id")
	uid, _ := uidAny.(uint64)
	if uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not logged in"})
		return
	}
	u, err := auth.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if !s.authSvc.VerifyPassword(u.PasswordHash, req.OldPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "old password incorrect"})
		return
	}
	newHash, err := s.authSvc.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := auth.UpdatePassword(c.Request.Context(), uid, newHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed: " + err.Error()})
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "auth.change_password", "user", u.Username, "password changed", nil)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"password_changed": true}})
}
