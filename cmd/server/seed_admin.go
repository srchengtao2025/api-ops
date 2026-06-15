package main

import (
	"context"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/auth"
	"github.com/api-ops/api-ops/internal/config"
	"github.com/api-ops/api-ops/internal/dal"
)

// seedBootstrapAdmin 启动时若 admin 用户不存在, 用 cfg.AdminPassword 创建
//
// 逻辑:
//  1. 若 cfg.AdminPassword 为空 → skip (dev/demo 模式, 用户用 SQL 手动建)
//  2. 若 ops_users 表为空 → 必建 (生产首次启动)
//  3. 若 admin username 不存在 → 建
//  4. 若 cfg.AdminPasswordChange=true 且 admin 已存在 → 改密
//  5. 重复 username / DB error → log warning 不 fatal
func seedBootstrapAdmin(cfg *config.Config) {
	if cfg.AdminPassword == "" {
		log.Println("[seed] ADMIN_PASSWORD empty, skip bootstrap admin")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	us, err := auth.ListUsers(ctx)
	if err != nil {
		log.Printf("[seed] WARN: list users failed: %v (skip bootstrap)", err)
		return
	}

	// 计算 password hash
	svc := auth.NewService(cfg.JWTSecret, 24*time.Hour)
	hash, err := svc.HashPassword(cfg.AdminPassword)
	if err != nil {
		log.Printf("[seed] WARN: hash password failed: %v", err)
		return
	}

	// 找同名用户
	var existing *dal.OpsUser
	for i := range us {
		if us[i].Username == cfg.AdminUsername {
			existing = &us[i]
			break
		}
	}

	now := time.Now().Unix()
	if existing == nil {
		// 创建
		u := &dal.OpsUser{
			Username:          cfg.AdminUsername,
			PasswordHash:      hash,
			DisplayName:       "Bootstrap Admin",
			Email:             "",
			Role:              dal.OpsUserRoleAdmin,
			Status:            dal.OpsUserStatusActive,
			PasswordChangedAt: now,
			Remark:            "bootstrap (created on first startup)",
		}
		if err := auth.CreateUser(ctx, u); err != nil {
			log.Printf("[seed] WARN: create bootstrap admin failed: %v", err)
			return
		}
		log.Printf("[seed] bootstrap admin created: username=%s role=admin", cfg.AdminUsername)
		return
	}

	// 已存在: 看是否需要强制改密
	if cfg.AdminPasswordChange {
		if err := auth.UpdatePassword(ctx, existing.ID, hash); err != nil {
			log.Printf("[seed] WARN: reset admin password failed: %v", err)
			return
		}
		log.Printf("[seed] admin password reset: username=%s (ADMIN_PASSWORD_CHANGE=true)", cfg.AdminUsername)
		return
	}
	log.Printf("[seed] admin exists: username=%s (use ADMIN_PASSWORD_CHANGE=true to force reset)", cfg.AdminUsername)
}
