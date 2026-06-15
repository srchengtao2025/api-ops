// logs summary cache schema 回归测试
//
// 背景：cache_logs_summary_5min 表是 1min tick 把 RoDB.logs 摘要写到 OPS，
// 替代 monitor/dashboard/billing 的"大聚合直读 RoDB"。
//
// 关键约束：
//   - UNIQUE(channel_id, bucket_ts) → ON CONFLICT DO UPDATE 幂等
//   - channel_id=0 是 global 摘要（一条/分钟）
//   - channel_id>0 是 per-channel 摘要（N 条/分钟）
//
// 本测试用 GORM schema reflection 验证：
//  1. 表名 = cache_logs_summary_5min
//  2. 主键 = id
//  3. channel_id + bucket_ts 是唯一索引（通过 tag 字符串验证）
//  4. DBName 映射正确
package dal

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestLogsSummary5min_Schema(t *testing.T) {
	s, err := schema.Parse(&LogsSummary5min{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("schema parse failed: %v", err)
	}

	// 1) 表名
	if s.Table != "cache_logs_summary_5min" {
		t.Fatalf("TableName = %q, want cache_logs_summary_5min", s.Table)
	}

	// 2) 主键字段
	pkFound := false
	for _, f := range s.PrimaryFields {
		if f.DBName == "id" {
			pkFound = true
			break
		}
	}
	if !pkFound {
		t.Fatal("primary key 'id' not found")
	}

	// 3) 通过字段 tag 验证 UNIQUE(channel_id, bucket_ts) 存在且顺序正确
	// （schema.Schema 不公开 Indexes，直接读 struct tag 最可靠）
	chField, ok := s.FieldsByName["ChannelID"]
	if !ok {
		t.Fatal("ChannelID field not found")
	}
	btField, ok := s.FieldsByName["BucketTS"]
	if !ok {
		t.Fatal("BucketTS field not found")
	}
	if !hasUniqIdx(chField.Tag, "idx_ls5_ch_bucket", 1) {
		t.Errorf("ChannelID tag %q missing uniqueIndex idx_ls5_ch_bucket priority 1", chField.Tag)
	}
	if !hasUniqIdx(btField.Tag, "idx_ls5_ch_bucket", 2) {
		t.Errorf("BucketTS tag %q missing uniqueIndex idx_ls5_ch_bucket priority 2", btField.Tag)
	}

	// 4) DBName 映射：关键字段
	wantDBNames := map[string]string{
		"ChannelID":        "channel_id",
		"BucketTS":         "bucket_ts",
		"RequestCount":     "request_count",
		"ErrorCount":       "error_count",
		"SuccessCount":     "success_count",
		"Quota":            "quota",
		"PromptTokens":     "prompt_tokens",
		"CompletionTokens": "completion_tokens",
		"P50LatencyMs":     "p50_latency_ms",
		"P95LatencyMs":     "p95_latency_ms",
		"P99LatencyMs":     "p99_latency_ms",
		"AvgLatencyMs":     "avg_latency_ms",
		"ErrorRate":        "error_rate",
	}
	for _, f := range s.Fields {
		if want, ok := wantDBNames[f.Name]; ok {
			if f.DBName != want {
				t.Errorf("field %s: DBName = %q, want %q", f.Name, f.DBName, want)
			}
		}
	}
}

// hasUniqIdx 检查 struct field tag 是否含 uniqueIndex:name,priority:N
func hasUniqIdx(tag reflect.StructTag, name string, priority int) bool {
	gormTag := tag.Get("gorm")
	if gormTag == "" {
		return false
	}
	// 简化：检查 "uniqueIndex:idx_ls5_ch_bucket" 和 "priority:N" 都在
	needle1 := "uniqueIndex:" + name
	needle2 := "priority:" + itoa(priority)
	return contains(gormTag, needle1) && contains(gormTag, needle2)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
