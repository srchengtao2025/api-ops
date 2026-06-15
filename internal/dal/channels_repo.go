// upstream/newapi channels 表只读访问
// 字段定义参考 newapi/model/channel.go:23-60
package dal

import (
	"context"

	"gorm.io/gorm"
)

// ChannelMirror 镜像 channels 表
type ChannelMirror struct {
	ID                 int     `gorm:"column:id;primaryKey"`
	Type               int     `gorm:"column:type"`
	Key                string  `gorm:"column:key"`
	Name               string  `gorm:"column:name"`
	Status             int     `gorm:"column:status"` // 1=enabled 2=manual_disabled 3=auto_disabled
	Weight             *uint   `gorm:"column:weight"`
	Models             string  `gorm:"column:models"` // 逗号分隔
	Group              string  `gorm:"column:group"`  // 逗号分隔多 group
	UsedQuota          int64   `gorm:"column:used_quota"`
	Balance            float64 `gorm:"column:balance"`
	BalanceUpdatedTime int64   `gorm:"column:balance_updated_time"`
	ResponseTime       int     `gorm:"column:response_time"` // 最近响应耗时(ms)
	TestTime           int64   `gorm:"column:test_time"`
	Priority           *int64  `gorm:"column:priority"`
	Tag                *string `gorm:"column:tag"`
	Remark             *string `gorm:"column:remark"`
	CreatedTime        int64   `gorm:"column:created_time"`
}

func (ChannelMirror) TableName() string { return "channels" }

// ChannelStatusEnabled / ManuallyDisabled / AutoDisabled
const (
	ChannelStatusEnabled          = 1
	ChannelStatusManuallyDisabled = 2
	ChannelStatusAutoDisabled     = 3
)

// ListChannels 列出所有渠道
// 数据源：upstream_channel_cache 表（由 internal/sync 周期性从 newapi Admin API 同步）
// 以前走 newapi RO.channels 表；但 billing 用户 SELECT 权限不全。
// 改走 cache 后，handler 完全无感（字段集兼容）
func ListChannels(ctx context.Context, statusFilter int) ([]ChannelMirror, error) {
	var rows []ChannelMirror
	q := OPS.WithContext(ctx).Table("upstream_channel_cache").Order("id ASC")
	if statusFilter > 0 {
		q = q.Where("status = ?", statusFilter)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetChannel 单条
func GetChannel(ctx context.Context, id int) (*ChannelMirror, error) {
	var c ChannelMirror
	err := OPS.WithContext(ctx).Table("upstream_channel_cache").Where("id = ?", id).Take(&c).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &c, err
}

// ListEnabledChannels 只取 enabled 渠道（对账上游侧用）
func ListEnabledChannels(ctx context.Context) ([]ChannelMirror, error) {
	return ListChannels(ctx, ChannelStatusEnabled)
}

// ChannelIDMap 把渠道 ID → 名称 的 map（前端展示用）
func ChannelIDMap(ctx context.Context) (map[int]string, error) {
	chs, err := ListChannels(ctx, 0)
	if err != nil {
		return nil, err
	}
	m := make(map[int]string, len(chs))
	for _, c := range chs {
		m[c.ID] = c.Name
	}
	return m, nil
}
