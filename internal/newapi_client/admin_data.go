// newapi Admin API: 数据聚合接口（/api/data/ /api/data/users）
//
// 用途：把"近 X 天" / "TopX" 这类聚合查询转移到 admin API，
// 替代直接对 RoDB logs 表做大 GROUP BY 的路径。
//
// 数据源：newapi 自己的 quota_data 表（不是 logs）
//   - quota_data 是 newapi 内部维护的"小时级预聚合表"
//   - 每 5min 由 UpdateQuotaData() 后台 goroutine 滚到 quota_data
//   - 5min 延迟（dashboard 主动刷新场景可接受）
//
// 接口清单：
//   - GetQuotaDataAll  /api/data/         按 model_name + 小时 聚合 (count, quota, token_used)
//   - GetQuotaDataUsers /api/data/users   按 username + 小时 聚合   (count, quota, token_used)
//
// 注意：这两个接口 都不支持 channel 维度！channel 维度的聚合仍要走 RoDB。
package newapi_client

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// QuotaDataRow 单条聚合行（小时粒度）
// 对应 quota_data 表：(username|model_name, created_at 小时对齐, count, quota, token_used)
type QuotaDataRow struct {
	Username  string `json:"username"`   // 仅 users 接口
	ModelName string `json:"model_name"` // 仅 all 接口
	HourTS    int64  `json:"created_at"` // unix 秒，按小时对齐
	Count     int64  `json:"count"`
	Quota     int64  `json:"quota"`      // 内部额度（newapi 内部计价单位）
	TokenUsed int64  `json:"token_used"` // 实际 token 数
}

// quotaDataResp 通用响应结构
type quotaDataResp struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    []QuotaDataRow `json:"data"`
}

// GetQuotaDataAll 调用 /api/data/ 返回 model_name + 小时聚合
//
//	startTS/endTS unix 秒（newapi 端转成小时对齐）
//	默认返回 ≤几个月的数据（newapi 端保留期限）
func (c *Client) GetQuotaDataAll(ctx context.Context, startTS, endTS int64) ([]QuotaDataRow, error) {
	q := url.Values{}
	q.Set("start_date", time.Unix(startTS, 0).UTC().Format("2006-01-02"))
	q.Set("end_date", time.Unix(endTS, 0).UTC().Format("2006-01-02"))
	var resp quotaDataResp
	if err := c.Do(ctx, "/api/data/", q, &resp); err != nil {
		return nil, fmt.Errorf("GetQuotaDataAll: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("GetQuotaDataAll: success=false, msg=%s", resp.Message)
	}
	return resp.Data, nil
}

// GetQuotaDataUsers 调用 /api/data/users 返回 username + 小时聚合
func (c *Client) GetQuotaDataUsers(ctx context.Context, startTS, endTS int64) ([]QuotaDataRow, error) {
	q := url.Values{}
	q.Set("start_date", time.Unix(startTS, 0).UTC().Format("2006-01-02"))
	q.Set("end_date", time.Unix(endTS, 0).UTC().Format("2006-01-02"))
	var resp quotaDataResp
	if err := c.Do(ctx, "/api/data/users", q, &resp); err != nil {
		return nil, fmt.Errorf("GetQuotaDataUsers: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("GetQuotaDataUsers: success=false, msg=%s", resp.Message)
	}
	return resp.Data, nil
}
