// Package sync 周期性把 newapi Admin API 数据同步到 api-ops 自有 DB cache
//
// 设计动机：
//   - newapi PG 账号（billing）只有 logs 表 SELECT 权限
//   - channels/users/tokens 表需要数据，通过 admin API (Bearer token + New-Api-User)
//     拉到 api_ops 库的 cache 表
//   - handler 全部走 cache（dal 层已切到 OPS.*_cache 表），对应用层透明
//
// 数据流：
//
//	newapi Admin API ──(Bearer + New-Api-User)──► Client.ListXxxAll()
//	     │
//	     ▼
//	api-ops OPS DB (upstream_*_cache 表) ◄── upsert ──┘
//
// 调度：
//   - 启动时 RunOnce() 同步阻塞一次（保证 handler 起来就有数据）
//   - 之后启 goroutine + Ticker，每 5 分钟刷一次
package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/newapi_client"
	"gorm.io/gorm"
)

// DefaultInterval 默认同步间隔
const DefaultInterval = 5 * time.Minute

// upstreamSync 同步服务
type upstreamSync struct {
	client *newapi_client.Client
}

// New 创建 sync service（必须保证 client 已初始化）
func New(client *newapi_client.Client) *upstreamSync {
	if client == nil || client.Token == "" || client.BaseURL == "" {
		return nil
	}
	return &upstreamSync{client: client}
}

// RunOnce 同步一次（同步阻塞，用于启动时）
// 返回错误仅记录不致命（cache 表可空，handler 仍可用，只是数据空）
func (s *upstreamSync) RunOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}

	var errs []error

	if n, err := s.syncChannels(ctx); err != nil {
		errs = append(errs, fmt.Errorf("channels: %w", err))
		log.Printf("[sync] channels failed: %v", err)
	} else {
		log.Printf("[sync] channels: upserted=%d", n)
	}

	if n, err := s.syncUsers(ctx); err != nil {
		errs = append(errs, fmt.Errorf("users: %w", err))
		log.Printf("[sync] users failed: %v", err)
	} else {
		log.Printf("[sync] users: upserted=%d", n)
	}

	if n, err := s.syncTokens(ctx); err != nil {
		errs = append(errs, fmt.Errorf("tokens: %w", err))
		log.Printf("[sync] tokens failed: %v", err)
	} else {
		log.Printf("[sync] tokens: upserted=%d", n)
	}

	if len(errs) > 0 {
		return fmt.Errorf("sync partial failed: %d errors", len(errs))
	}
	return nil
}

// Start 启动后台定时同步
// 注意：调用方应保证传入 ctx 在退出时 cancel
func (s *upstreamSync) Start(ctx context.Context, interval time.Duration) {
	if s == nil {
		log.Println("[sync] disabled (no admin token / base URL)")
		return
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	log.Printf("[sync] started, interval=%s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[sync] stopped")
			return
		case <-ticker.C:
			if err := s.RunOnce(ctx); err != nil {
				log.Printf("[sync] periodic: %v", err)
			}
		}
	}
}

// ===== 内部：单表同步 =====

func (s *upstreamSync) syncChannels(ctx context.Context) (int, error) {
	channels, err := s.client.ListChannelsAll(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	rows := make([]dal.UpstreamChannelCache, 0, len(channels))
	for _, c := range channels {
		rows = append(rows, dal.UpstreamChannelCache{
			ID:                 c.ID,
			Name:               c.Name,
			Type:               c.Type,
			Status:             c.Status,
			Priority:           c.Priority,
			Weight:             c.Weight,
			Models:             c.Models,
			Group:              c.Group,
			BaseURL:            c.BaseURL,
			Other:              c.Other,
			Balance:            c.Balance,
			BalanceUpdatedTime: c.BalanceUpdatedTime,
			UsedQuota:          c.UsedQuota,
			ResponseTime:       c.ResponseTime,
			TestTime:           c.TestTime,
			CreatedTime:        c.CreatedTime,
			SyncedAt:           now,
		})
	}
	return upsertAll(dal.OPS, &dal.UpstreamChannelCache{}, rows, "id")
}

func (s *upstreamSync) syncUsers(ctx context.Context) (int, error) {
	users, err := s.client.ListUsersAll(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	rows := make([]dal.UpstreamUserCache, 0, len(users))
	for _, u := range users {
		rows = append(rows, dal.UpstreamUserCache{
			ID:           u.ID,
			Username:     u.Username,
			DisplayName:  u.DisplayName,
			Role:         u.Role,
			Status:       u.Status,
			Email:        u.Email,
			GitHubID:     u.GitHubID,
			DiscordID:    u.DiscordID,
			OIDCID:       u.OIDCID,
			WeChatID:     u.WeChatID,
			TelegramID:   u.TelegramID,
			Group:        u.Group,
			Quota:        u.Quota,
			UsedQuota:    u.UsedQuota,
			RequestCount: u.RequestCount,
			AffCode:      u.AffCode,
			AffCount:     u.AffCount,
			InviterID:    u.InviterID,
			SyncedAt:     now,
		})
	}
	return upsertAll(dal.OPS, &dal.UpstreamUserCache{}, rows, "id")
}

func (s *upstreamSync) syncTokens(ctx context.Context) (int, error) {
	tokens, err := s.client.ListTokensAll(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	rows := make([]dal.UpstreamTokenCache, 0, len(tokens))
	for _, t := range tokens {
		rows = append(rows, dal.UpstreamTokenCache{
			ID:                 t.ID,
			UserID:             t.UserID,
			Status:             t.Status,
			Name:               t.Name,
			KeyMasked:          dal.MaskTokenKey(t.Key),
			CreatedTime:        t.CreatedTime,
			AccessedTime:       t.AccessedTime,
			ExpiredTime:        t.ExpiredTime,
			RemainQuota:        t.RemainQuota,
			UnlimitedQuota:     t.UnlimitedQuota,
			ModelLimitsEnabled: t.ModelLimitsEnabled,
			ModelLimits:        t.ModelLimits,
			AllowIPs:           t.AllowIPs,
			UsedQuota:          t.UsedQuota,
			Group:              t.Group,
			CrossGroupRetry:    t.CrossGroupRetry,
			SyncedAt:           now,
		})
	}
	return upsertAll(dal.OPS, &dal.UpstreamTokenCache{}, rows, "id")
}

// upsertAll 通用 upsert：清空表 + 批量插入（数据量小，性能 OK）
// 数据量（channels=78, users=107, tokens=2）都很小，全量替换比增量 diff 简单可靠
func upsertAll(db *gorm.DB, model interface{}, rows interface{}, pk string) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("db is nil")
	}
	tx := db.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	// 先清空
	if err := tx.Where("1=1").Delete(model).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("delete old: %w", err)
	}
	// 批量插入
	if err := tx.Create(rows).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("insert: %w", err)
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	// 统计行数（reflect 看 rows 长度）
	count := reflectLen(rows)
	_ = pk
	return count, nil
}

func reflectLen(v interface{}) int {
	switch r := v.(type) {
	case []dal.UpstreamChannelCache:
		return len(r)
	case []dal.UpstreamUserCache:
		return len(r)
	case []dal.UpstreamTokenCache:
		return len(r)
	default:
		return 0
	}
}
