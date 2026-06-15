// upstream/newapi 镜像表的本地 cache（用于无 SELECT 权限场景）
//
// 背景：
//   - api-ops 默认直连 newapi DB（RO_DSN）
//   - 当 newapi PG 账号 SELECT 权限不足时（例如只有 logs 表权限），其他表（channels/users/tokens）
//     无法通过 DB 访问
//   - 替代方案：用 newapi Admin API（Bearer token + New-Api-User header）拉取
//   - admin token + user_id=1 可拿到 channels/users/tokens 全量（数据量小）
//   - logs 数据量大（198万条），仍走 DB
//
// 同步策略：
//   - 启动时拉一次全量
//   - 之后每 5 分钟定时刷新一次
//   - handler 仍走 dal.ListChannels 等方法（DB），对应用层完全透明
//
// 表设计原则：
//   - 完全镜像 newapi 的字段集，便于后续字段扩展
//   - 不存敏感字段（如 token.key 明文）—— 安全考虑
//   - int64 字段不指定 type，让 GORM 用 bigint（避免 numeric 扫描失败）
//   - 显式 column tag 指定列名（避免 GORM 自动 CamelCase→snake_case 拆错）
package dal

import "time"

// ===== 渠道 cache =====

// UpstreamChannelCache newapi channels 表的本地镜像
type UpstreamChannelCache struct {
	ID                 int       `gorm:"column:id;primaryKey" json:"id"`
	Name               string    `gorm:"column:name;size:128;index" json:"name"`
	Type               int       `gorm:"column:type" json:"type"`
	Status             int       `gorm:"column:status;index" json:"status"`
	Priority           int       `gorm:"column:priority" json:"priority"`
	Weight             int       `gorm:"column:weight" json:"weight"`
	Models             string    `gorm:"column:models;type:text" json:"models"`
	Group              string    `gorm:"column:group;size:255;index" json:"group"`
	BaseURL            string    `gorm:"column:base_url;size:512" json:"base_url"`
	Other              string    `gorm:"column:other;type:text" json:"other"`
	Balance            float64   `gorm:"column:balance;type:numeric(20,8);default:0" json:"balance"`
	BalanceUpdatedTime int64     `gorm:"column:balance_updated_time" json:"balance_updated_time"`
	UsedQuota          int64     `gorm:"column:used_quota;default:0" json:"used_quota"`
	ResponseTime       int       `gorm:"column:response_time" json:"response_time"`
	TestTime           int64     `gorm:"column:test_time" json:"test_time"`
	CreatedTime        int64     `gorm:"column:created_time" json:"created_time"`
	SyncedAt           time.Time `gorm:"column:synced_at;index" json:"synced_at"`
}

func (UpstreamChannelCache) TableName() string { return "upstream_channel_cache" }

// ===== 用户 cache =====

// UpstreamUserCache newapi users 表的本地镜像
type UpstreamUserCache struct {
	ID           int       `gorm:"column:id;primaryKey" json:"id"`
	Username     string    `gorm:"column:username;size:64;uniqueIndex" json:"username"`
	DisplayName  string    `gorm:"column:display_name;size:64" json:"display_name"`
	Role         int       `gorm:"column:role;index" json:"role"`     // 1=root/admin, 2=普通用户
	Status       int       `gorm:"column:status;index" json:"status"` // 1=enabled, 2=disabled
	Email        string    `gorm:"column:email;size:128" json:"email"`
	GitHubID     string    `gorm:"column:github_id;size:64" json:"github_id"`
	DiscordID    string    `gorm:"column:discord_id;size:64" json:"discord_id"`
	OIDCID       string    `gorm:"column:oidc_id;size:64" json:"oidc_id"`
	WeChatID     string    `gorm:"column:wechat_id;size:64" json:"wechat_id"`
	TelegramID   string    `gorm:"column:telegram_id;size:64" json:"telegram_id"`
	Group        string    `gorm:"column:group;size:128;index" json:"group"`
	Quota        int64     `gorm:"column:quota;default:0" json:"quota"`
	UsedQuota    int64     `gorm:"column:used_quota;default:0" json:"used_quota"`
	RequestCount int       `gorm:"column:request_count;default:0" json:"request_count"`
	AffCode      string    `gorm:"column:aff_code;size:32;index" json:"aff_code"`
	AffCount     int       `gorm:"column:aff_count;default:0" json:"aff_count"`
	InviterID    int       `gorm:"column:inviter_id" json:"inviter_id"`
	SyncedAt     time.Time `gorm:"column:synced_at;index" json:"synced_at"`
}

func (UpstreamUserCache) TableName() string { return "upstream_user_cache" }

// ===== Token cache =====

// UpstreamTokenCache newapi tokens 表的本地镜像
// 注意：key 字段不存！避免敏感凭据泄漏
type UpstreamTokenCache struct {
	ID                 int       `gorm:"column:id;primaryKey" json:"id"`
	UserID             int       `gorm:"column:user_id;index" json:"user_id"`
	Status             int       `gorm:"column:status;index" json:"status"`
	Name               string    `gorm:"column:name;size:128" json:"name"`
	KeyMasked          string    `gorm:"column:key_masked;size:64" json:"key_masked"` // 只存前缀 + 后缀 (e.g. "fV2e***H1gt")
	CreatedTime        int64     `gorm:"column:created_time" json:"created_time"`
	AccessedTime       int64     `gorm:"column:accessed_time" json:"accessed_time"`
	ExpiredTime        int64     `gorm:"column:expired_time" json:"expired_time"`
	RemainQuota        int64     `gorm:"column:remain_quota;default:0" json:"remain_quota"`
	UnlimitedQuota     bool      `gorm:"column:unlimited_quota;default:false" json:"unlimited_quota"`
	ModelLimitsEnabled bool      `gorm:"column:model_limits_enabled;default:false" json:"model_limits_enabled"`
	ModelLimits        string    `gorm:"column:model_limits;type:text" json:"model_limits"`
	AllowIPs           string    `gorm:"column:allow_ips;type:text" json:"allow_ips"`
	UsedQuota          int64     `gorm:"column:used_quota;default:0" json:"used_quota"`
	Group              string    `gorm:"column:group;size:128;index" json:"group"`
	CrossGroupRetry    bool      `gorm:"column:cross_group_retry;default:false" json:"cross_group_retry"`
	SyncedAt           time.Time `gorm:"column:synced_at;index" json:"synced_at"`
}

func (UpstreamTokenCache) TableName() string { return "upstream_token_cache" }

// MaskTokenKey 把 token key 脱敏成前缀+后缀
// 输入: "fV2eJKLJHLKJH12345H1gt"
// 输出: "fV2e***H1gt"
func MaskTokenKey(key string) string {
	const prefix = 4
	const suffix = 4
	if len(key) <= prefix+suffix {
		return "***"
	}
	return key[:prefix] + "***" + key[len(key)-suffix:]
}
