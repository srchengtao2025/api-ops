// upstream/newapi users / tokens 表只读访问
package dal

import (
	"context"

	"gorm.io/gorm"
)

// UserMirror 镜像 users 表
type UserMirror struct {
	ID           int    `gorm:"column:id;primaryKey"`
	Username     string `gorm:"column:username"`
	DisplayName  string `gorm:"column:display_name"`
	Email        string `gorm:"column:email"`
	Role         int    `gorm:"column:role"`
	Status       int    `gorm:"column:status"`
	Quota        int    `gorm:"column:quota"` // 当前钱包余额（quota）
	UsedQuota    int    `gorm:"column:used_quota"`
	RequestCount int    `gorm:"column:request_count"`
	Group        string `gorm:"column:group"`
	Remark       string `gorm:"column:remark"`
	CreatedAt    int64  `gorm:"column:created_at"`
	LastLoginAt  int64  `gorm:"column:last_login_at"`
}

func (UserMirror) TableName() string { return "users" }

// TokenMirror 镜像 tokens 表
type TokenMirror struct {
	ID                 int    `gorm:"column:id;primaryKey"`
	UserID             int    `gorm:"column:user_id"`
	Key                string `gorm:"column:key"`
	Name               string `gorm:"column:name"`
	Status             int    `gorm:"column:status"`
	Group              string `gorm:"column:group"`
	UsedQuota          int64  `gorm:"column:used_quota"`
	RemainQuota        int    `gorm:"column:remain_quota"`
	UnlimitedQuota     bool   `gorm:"column:unlimited_quota"`
	ModelLimitsEnabled bool   `gorm:"column:model_limits_enabled"`
	ModelLimits        string `gorm:"column:model_limits"`
	CreatedTime        int64  `gorm:"column:created_time"`
	AccessedTime       int64  `gorm:"column:accessed_time"`
	ExpiredTime        int64  `gorm:"column:expired_time"`
}

func (TokenMirror) TableName() string { return "tokens" }

// GetUser 按 ID 取用户
// 数据源：upstream_user_cache 表（由 internal/sync 周期性从 newapi Admin API 同步）
func GetUser(ctx context.Context, id int) (*UserMirror, error) {
	var u UserMirror
	err := OPS.WithContext(ctx).Table("upstream_user_cache").Where("id = ?", id).Take(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &u, err
}

// FindUserByUsername 按用户名取用户
func FindUserByUsername(ctx context.Context, username string) (*UserMirror, error) {
	var u UserMirror
	err := OPS.WithContext(ctx).Table("upstream_user_cache").Where("username = ?", username).Take(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &u, err
}

// ListTokensByUser 列出某用户的所有 token
func ListTokensByUser(ctx context.Context, userID int) ([]TokenMirror, error) {
	var rows []TokenMirror
	err := OPS.WithContext(ctx).Table("upstream_token_cache").Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error
	return rows, err
}
