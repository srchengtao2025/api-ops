package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/api-ops/api-ops/internal/auth"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ===== Admin: 用户管理 (admin role only) =====

// listUsersHandler GET /api/admin/users
func (s *Server) listUsersHandler(c *gin.Context) {
	us, err := auth.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    us,
		"total":   len(us),
	})
}

// createUserHandler POST /api/admin/users
type CreateUserRequest struct {
	Username    string `json:"username" binding:"required,min=3,max=64"`
	Password    string `json:"password" binding:"required,min=8"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role" binding:"required,oneof=admin finance viewer"`
	Remark      string `json:"remark"`
}

func (s *Server) createUserHandler(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 重复检查
	if _, err := auth.GetUserByUsername(c.Request.Context(), req.Username); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		return
	}
	hash, err := s.authSvc.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().Unix()
	u := &dal.OpsUser{
		Username:          req.Username,
		PasswordHash:      hash,
		DisplayName:       req.DisplayName,
		Email:             req.Email,
		Role:              dal.OpsUserRole(req.Role),
		Status:            dal.OpsUserStatusActive,
		PasswordChangedAt: now,
		Remark:            req.Remark,
	}
	if err := auth.CreateUser(c.Request.Context(), u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "admin.user.create", "user", req.Username, "user created", map[string]interface{}{
			"role": req.Role,
		})
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": u})
}

// updateUserHandler PUT /api/admin/users/:id
// 不改密码 (走 reset-password 端点)
type UpdateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	Role        *string `json:"role" binding:"omitempty,oneof=admin finance viewer"`
	Status      *int    `json:"status" binding:"omitempty,oneof=-1 0 1"`
	Remark      *string `json:"remark"`
}

func (s *Server) updateUserHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := auth.GetUserByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	updates := map[string]interface{}{"updated_at": gorm.Expr("NOW()")}
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Role != nil {
		updates["role"] = *req.Role
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Remark != nil {
		updates["remark"] = *req.Remark
	}
	if err := dal.OPS.WithContext(c.Request.Context()).Model(&dal.OpsUser{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "admin.user.update", "user", u.Username, "user updated", updates)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"updated": true}})
}

// adminResetPasswordHandler POST /api/admin/users/:id/reset-password
// 不需要老密码, 强制重置 (撤销老 token)
type AdminResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

func (s *Server) adminResetPasswordHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req AdminResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := auth.GetUserByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	hash, err := s.authSvc.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := auth.UpdatePassword(c.Request.Context(), id, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "admin.user.reset_password", "user", u.Username, "password reset by admin", nil)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"password_reset": true}})
}

// deleteUserHandler DELETE /api/admin/users/:id
// 软删除 (status = -1)
func (s *Server) deleteUserHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	// 防自删
	uidAny, _ := c.Get("auth_user_id")
	uid, _ := uidAny.(uint64)
	if id == uid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete yourself"})
		return
	}
	u, err := auth.GetUserByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if err := dal.OPS.WithContext(c.Request.Context()).Model(&dal.OpsUser{}).
		Where("id = ?", id).Updates(map[string]interface{}{
		"status":     dal.OpsUserStatusDeleted,
		"updated_at": gorm.Expr("NOW()"),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "admin.user.delete", "user", u.Username, "user soft-deleted", nil)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}
