package auth

import (
	"context"
	"errors"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"gorm.io/gorm"
)

// ErrUserNotFound 用户不存在
var ErrUserNotFound = errors.New("user not found")

// ErrUserDisabled 用户被锁/删
var ErrUserDisabled = errors.New("user disabled")

// GetUserByUsername
func GetUserByUsername(ctx context.Context, username string) (*dal.OpsUser, error) {
	var u dal.OpsUser
	if err := dal.OPS.WithContext(ctx).Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByID
func GetUserByID(ctx context.Context, id uint64) (*dal.OpsUser, error) {
	var u dal.OpsUser
	if err := dal.OPS.WithContext(ctx).First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CreateUser
func CreateUser(ctx context.Context, u *dal.OpsUser) error {
	if u.Status == 0 {
		u.Status = dal.OpsUserStatusActive
	}
	return dal.OPS.WithContext(ctx).Create(u).Error
}

// UpdatePassword 更新密码 + 推进 password_changed_at (撤销老 token)
func UpdatePassword(ctx context.Context, userID uint64, newHash string) error {
	now := time.Now().Unix()
	return dal.OPS.WithContext(ctx).Model(&dal.OpsUser{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"password_hash":       newHash,
			"password_changed_at": now,
			"updated_at":          gorm.Expr("NOW()"),
		}).Error
}

// TouchLastLogin 登录成功后写
func TouchLastLogin(ctx context.Context, userID uint64) error {
	now := time.Now().Unix()
	return dal.OPS.WithContext(ctx).Model(&dal.OpsUser{}).
		Where("id = ?", userID).
		Update("last_login_at", now).Error
}

// ListUsers 管理员后台
func ListUsers(ctx context.Context) ([]dal.OpsUser, error) {
	var us []dal.OpsUser
	if err := dal.OPS.WithContext(ctx).Order("id ASC").Find(&us).Error; err != nil {
		return nil, err
	}
	return us, nil
}
