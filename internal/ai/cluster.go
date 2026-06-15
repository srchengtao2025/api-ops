// Package ai: 错误聚类 —— 把 logs.content 归一化为 pattern
//   - SQL：REGEXP_REPLACE(content, [UUID|<TS>|<N>], 'g')  → pattern
//   - 按 (pattern, channel_id, model_name) 分组，1h 滑窗
//   - 写入 ai_error_clusters（UNIQUE conflict: pattern+channel_id+model_name）
//   - 调度：scheduler 每小时 tick
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// ClusterOneHour 跑一次 1h 聚类（默认对最近 1h）
func ClusterOneHour(ctx context.Context) (int, error) {
	return ClusterWindow(ctx, time.Now().Add(-1*time.Hour).Unix(), time.Now().Unix())
}

// ClusterWindow 在 [startTS, endTS] 区间内聚类
// PG REGEXP_REPLACE 三步归一化：UUID → <UUID>; ISO-8601 时间戳 → <TS>; 裸数字 → <N>
func ClusterWindow(ctx context.Context, startTS, endTS int64) (int, error) {
	sql := `
SELECT REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(content,
  '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', '<UUID>', 'g'),
  '\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}', '<TS>', 'g'),
  '\b\d+\b', '<N>', 'g') AS pattern,
  channel_id,
  model_name,
  COUNT(*) AS count,
  ARRAY_AGG(DISTINCT username) AS affected_users,
  (ARRAY_AGG(content))[1] AS sample_content
FROM logs
WHERE type = ? AND created_at >= ? AND created_at <= ?
GROUP BY pattern, channel_id, model_name
ORDER BY count DESC LIMIT 200`

	type row struct {
		Pattern       string `gorm:"column:pattern"`
		ChannelID     int    `gorm:"column:channel_id"`
		ModelName     string `gorm:"column:model_name"`
		Count         int64  `gorm:"column:count"`
		AffectedUsers []byte `gorm:"column:affected_users"`
		SampleContent string `gorm:"column:sample_content"`
	}
	var rows []row
	err := dal.RoDB().WithContext(ctx).Raw(sql, dal.LogTypeError, startTS, endTS).Scan(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("cluster query: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	upserted := 0
	for _, r := range rows {
		c := &dal.AIErrorCluster{
			Pattern: r.Pattern, ChannelID: r.ChannelID, ModelName: r.ModelName,
			WindowStart: startTS, WindowEnd: endTS,
			Count:         r.Count,
			SampleContent: r.SampleContent,
			AffectedUsers: pgArrayToJSON(r.AffectedUsers),
		}
		if err := dal.UpsertAIErrorCluster(ctx, c); err != nil {
			log.Printf("[ai.cluster] upsert failed pattern=%q ch=%d: %v", r.Pattern, r.ChannelID, err)
			continue
		}
		upserted++
	}
	return upserted, nil
}

// pgArrayToJSON 把 PG 数组 text 格式 "{a,b,c}" → ["a","b","c"] JSON
func pgArrayToJSON(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return "[]"
	}
	parts := strings.Split(s, ",")
	out, _ := json.Marshal(parts)
	return string(out)
}
