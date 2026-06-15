// newapi Admin API SDK
// 用环境变量里的 API_OPS_ADMIN_TOKEN 调用 newapi 后台接口
// 主要是为了在 api-ops 不可直连 newapi DB 时做兜底；正常情况下我们优先直连 DB
//
// newapi 鉴权说明（实测）：
//   - 所有 API 都需要 `Authorization: Bearer <token>`
//   - 操作类 API（/api/channel/ /api/user/ /api/token/ 等）还需要 `New-Api-User: <user_id>`
//   - user_id 必须等于 token 自身的 user_id，否则被拒
//   - admin token 默认对应的 user_id 是 1（root）
package newapi_client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/api-ops/api-ops/internal/config"
)

const (
	// DefaultAdminUserID 默认 admin token 对应的 New-Api-User id（root 用户通常是 1）
	DefaultAdminUserID = "1"
	// PageSizeMax newapi 单页上限
	PageSizeMax = 100
)

type Client struct {
	BaseURL string
	Token   string
	UserID  string // 对应 New-Api-User header；默认 "1"
	HTTP    *http.Client
}

func New(cfg *config.Config) *Client {
	uid := cfg.UpstreamAdminUserID
	if uid == "" {
		uid = DefaultAdminUserID
	}
	return &Client{
		BaseURL: cfg.UpstreamAdminBaseURL,
		Token:   cfg.UpstreamAdminToken,
		UserID:  uid,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWith 直接构造（用于测试或特殊场景）
func NewWith(baseURL, token, userID string) *Client {
	if userID == "" {
		userID = DefaultAdminUserID
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		UserID:  userID,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Do 通用 GET 请求；返回 map（newapi 后台返回的格式）
// 自动带上 Authorization + New-Api-User 两个 header
func (c *Client) Do(ctx context.Context, path string, query url.Values, out interface{}) error {
	u := c.BaseURL + path
	if query != nil {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("New-Api-User", c.UserID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("newapi http %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

// DoRaw GET 请求 + 返 raw bytes (用于 stat 等非列表端点, 自解析 data map)
func (c *Client) DoRaw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.BaseURL + path
	if query != nil {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("New-Api-User", c.UserID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("newapi http %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// ===== 日志 stat =====

// LogsStat 仿 newapi model.Stat 结构
type LogsStat struct {
	Quota int `json:"quota"` // 成功 type=2 总消耗 (newapi quota 整数 = USD × 500000)
	RPM   int `json:"rpm"`   // 60s 滑窗内 type=2 请求数
	TPM   int `json:"tpm"`   // 60s 滑窗内 type=2 tokens (prompt + completion)
}

// GetLogsStat 调用 admin /api/log/stat
//
// 支持的过滤参数 (按 newapi SumUsedQuota 顺序):
//   - logType  int    // 0=全 type, 5=只 type=5 (但 newapi stat 硬编码 type=2, 这个参数实际被忽略)
//   - startTimestamp int64
//   - endTimestamp   int64
//   - modelName      string
//   - username       string
//   - tokenName      string
//   - channel        int
//   - group          string
func (c *Client) GetLogsStat(ctx context.Context, logType int, startTS, endTS int64, filters ...StatFilter) (LogsStat, error) {
	q := url.Values{}
	if logType > 0 {
		q.Set("type", strconv.Itoa(logType))
	}
	if startTS > 0 {
		q.Set("start_timestamp", strconv.FormatInt(startTS, 10))
	}
	if endTS > 0 {
		q.Set("end_timestamp", strconv.FormatInt(endTS, 10))
	}
	// 应用 filters (取第一个非零 StatFilter)
	for _, f := range filters {
		if f.Username != "" {
			q.Set("username", f.Username)
		}
		if f.ModelName != "" {
			q.Set("model_name", f.ModelName)
		}
		if f.TokenName != "" {
			q.Set("token_name", f.TokenName)
		}
		if f.Channel > 0 {
			q.Set("channel", strconv.Itoa(f.Channel))
		}
		if f.Group != "" {
			q.Set("group", f.Group)
		}
		if f != (StatFilter{}) {
			break // 只用第一个非空 filter
		}
	}
	raw, err := c.DoRaw(ctx, "/api/log/stat", q)
	if err != nil {
		return LogsStat{}, err
	}
	var wrap struct {
		Data LogsStat `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return LogsStat{}, err
	}
	return wrap.Data, nil
}

// StatFilter /api/log/stat 的可选过滤参数 (newapi 按 username/model/channel 等)
type StatFilter struct {
	Username  string
	ModelName string
	TokenName string
	Channel   int
	Group     string
}

// ===== 通用分页响应 =====

// PageResp 通用分页响应结构（newapi 多数列表接口都用这格式）
type PageResp[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Items    []T   `json:"items"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
	} `json:"data"`
}

// ===== 渠道 =====

type ChannelResp struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	Type               int     `json:"type"`
	Status             int     `json:"status"`
	Priority           int     `json:"priority"`
	Weight             int     `json:"weight"`
	Models             string  `json:"models"`
	Group              string  `json:"group"`
	BaseURL            string  `json:"base_url"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"`
	BalanceUpdatedTime int64   `json:"balance_updated_time"`
	UsedQuota          int64   `json:"used_quota"`
	ResponseTime       int     `json:"response_time"`
	TestTime           int64   `json:"test_time"`
	CreatedTime        int64   `json:"created_time"`
}

// ListChannelsAll 全量拉取所有渠道（自动翻页）
// new-api 分页是 1-based：GetStartIdx() = (p-1)*page_size
// p=1 → OFFSET 0, p=2 → OFFSET page_size, ...
// p=0 会产生 OFFSET -100（非法），new-api 内部有兼容逻辑但不可靠
// page_size 上限 100（new-api GetPageQuery 强制限制）
func (c *Client) ListChannelsAll(ctx context.Context) ([]ChannelResp, error) {
	var all []ChannelResp
	p := 1
	for {
		page, total, err := c.listChannelsPage(ctx, p, PageSizeMax)
		if err != nil {
			return nil, fmt.Errorf("list channels p=%d: %w", p, err)
		}
		all = append(all, page...)
		if int64(len(all)) >= total || len(page) == 0 {
			break
		}
		p++
		if p > 1000 {
			return nil, fmt.Errorf("list channels: too many pages (>1000), abort")
		}
	}
	return all, nil
}

func (c *Client) listChannelsPage(ctx context.Context, page, pageSize int) ([]ChannelResp, int64, error) {
	q := url.Values{}
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	var resp PageResp[ChannelResp]
	if err := c.Do(ctx, "/api/channel/", q, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data.Items, resp.Data.Total, nil
}

// ===== 日志 =====

type LogResp struct {
	ID                int64  `json:"id"`
	UserID            int    `json:"user_id"`
	Username          string `json:"username"`
	TokenName         string `json:"token_name"`
	ModelName         string `json:"model_name"`
	Quota             int    `json:"quota"`
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	UseTime           int    `json:"use_time"`
	ChannelID         int    `json:"channel"`
	Group             string `json:"group"`
	IP                string `json:"ip"`
	RequestID         string `json:"request_id"`
	UpstreamRequestID string `json:"upstream_request_id"`
	CreatedAt         int64  `json:"created_at"`
	Type              int    `json:"type"`
	Content           string `json:"content"`
	Other             string `json:"other"`
}

type ListLogsResp struct {
	Data []LogResp `json:"data"`
}

// SearchLogs 调用 /api/log/；参数参考 newapi/controller/log.go
// 注意：newapi 单页最多 100 条，需要循环翻页才能拉全量
func (c *Client) SearchLogs(ctx context.Context, start, end int64, username string, modelName string, channel int, logType int, page, pageSize int) ([]LogResp, error) {
	q := url.Values{}
	q.Set("start_timestamp", fmt.Sprintf("%d", start))
	q.Set("end_timestamp", fmt.Sprintf("%d", end))
	if username != "" {
		q.Set("username", username)
	}
	if modelName != "" {
		q.Set("model_name", modelName)
	}
	if channel > 0 {
		q.Set("channel", fmt.Sprintf("%d", channel))
	}
	if logType > 0 {
		q.Set("type", fmt.Sprintf("%d", logType))
	}
	q.Set("p", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))

	var resp ListLogsResp
	if err := c.Do(ctx, "/api/log/", q, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ===== 用户 =====

type UserResp struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Role         int    `json:"role"`
	Status       int    `json:"status"`
	Email        string `json:"email"`
	GitHubID     string `json:"github_id"`
	DiscordID    string `json:"discord_id"`
	OIDCID       string `json:"oidc_id"`
	WeChatID     string `json:"wechat_id"`
	TelegramID   string `json:"telegram_id"`
	Group        string `json:"group"`
	Quota        int64  `json:"quota"`
	UsedQuota    int64  `json:"used_quota"`
	RequestCount int    `json:"request_count"`
	AffCode      string `json:"aff_code"`
	AffCount     int    `json:"aff_count"`
	InviterID    int    `json:"inviter_id"`
}

// ListUsersAll 全量拉取所有用户（自动翻页）
// new-api 分页 1-based，page_size 上限 100
func (c *Client) ListUsersAll(ctx context.Context) ([]UserResp, error) {
	var all []UserResp
	p := 1
	for {
		page, total, err := c.listUsersPage(ctx, p, PageSizeMax)
		if err != nil {
			return nil, fmt.Errorf("list users p=%d: %w", p, err)
		}
		all = append(all, page...)
		if int64(len(all)) >= total || len(page) == 0 {
			break
		}
		p++
		if p > 1000 {
			return nil, fmt.Errorf("list users: too many pages (>1000), abort")
		}
	}
	return all, nil
}

func (c *Client) listUsersPage(ctx context.Context, page, pageSize int) ([]UserResp, int64, error) {
	q := url.Values{}
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	var resp PageResp[UserResp]
	if err := c.Do(ctx, "/api/user/", q, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data.Items, resp.Data.Total, nil
}

// ===== Token =====

type TokenResp struct {
	ID                 int    `json:"id"`
	UserID             int    `json:"user_id"`
	Key                string `json:"key"`
	Status             int    `json:"status"`
	Name               string `json:"name"`
	CreatedTime        int64  `json:"created_time"`
	AccessedTime       int64  `json:"accessed_time"`
	ExpiredTime        int64  `json:"expired_time"`
	RemainQuota        int64  `json:"remain_quota"`
	UnlimitedQuota     bool   `json:"unlimited_quota"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
	AllowIPs           string `json:"allow_ips"`
	UsedQuota          int64  `json:"used_quota"`
	Group              string `json:"group"`
	CrossGroupRetry    bool   `json:"cross_group_retry"`
}

// ListTokensAll 全量拉取所有 token（自动翻页）
// 注意：new-api /api/token/ 用的是 user-auth（不是 admin-auth），
// 但 admin token + New-Api-User=admin_id 也是合法 user-auth。
// 分页 1-based，page_size 上限 100。
func (c *Client) ListTokensAll(ctx context.Context) ([]TokenResp, error) {
	var all []TokenResp
	p := 1
	for {
		page, total, err := c.listTokensPage(ctx, p, PageSizeMax)
		if err != nil {
			return nil, fmt.Errorf("list tokens p=%d: %w", p, err)
		}
		all = append(all, page...)
		if int64(len(all)) >= total || len(page) == 0 {
			break
		}
		p++
		if p > 1000 {
			return nil, fmt.Errorf("list tokens: too many pages (>1000), abort")
		}
	}
	return all, nil
}

func (c *Client) listTokensPage(ctx context.Context, page, pageSize int) ([]TokenResp, int64, error) {
	q := url.Values{}
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	var resp PageResp[TokenResp]
	if err := c.Do(ctx, "/api/token/", q, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data.Items, resp.Data.Total, nil
}

// ===== 健康检查 =====

func (c *Client) Ping(ctx context.Context) error {
	var resp map[string]interface{}
	return c.Do(ctx, "/api/status", nil, &resp)
}
