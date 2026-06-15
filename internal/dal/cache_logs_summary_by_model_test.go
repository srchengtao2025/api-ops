// logs summary by-model cache schema 回归测试
//
// 背景：cache_logs_summary_by_model_5min 表是 1min tick 把 RoDB.logs 按
// (channel_id, model_name) 维度摘要写到 OPS，替代 billing 对账的"5万行 logs
// in-memory 聚合 + 逐条 CalcLogCost"。
//
// 关键约束：
//   - UNIQUE(bucket_ts, channel_id, model_name) → ON CONFLICT DO UPDATE 幂等
//   - vendor_code 在 sync 时通过 channel_vendor_map 映射好，避免 billing 算 cost 时再查
//
// 本测试用 GORM schema reflection 验证：
//  1. 表名 = cache_logs_summary_by_model_5min
//  2. 主键 = id
//  3. (bucket_ts, channel_id, model_name) 是唯一索引（通过 tag 字符串验证）
//  4. DBName 映射正确
package dal

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestLogsSummaryByModel5min_Schema(t *testing.T) {
	s, err := schema.Parse(&LogsSummaryByModel5min{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("schema parse failed: %v", err)
	}

	// 1) 表名
	if s.Table != "cache_logs_summary_by_model_5min" {
		t.Fatalf("TableName = %q, want cache_logs_summary_by_model_5min", s.Table)
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

	// 3) 通过字段 tag 验证 UNIQUE(bucket_ts, channel_id, model_name)
	// schema.Schema 不公开 Indexes，直接读 struct tag 最可靠
	chField, ok := s.FieldsByName["ChannelID"]
	if !ok {
		t.Fatal("ChannelID field not found")
	}
	mdField, ok := s.FieldsByName["ModelName"]
	if !ok {
		t.Fatal("ModelName field not found")
	}
	btField, ok := s.FieldsByName["BucketTS"]
	if !ok {
		t.Fatal("BucketTS field not found")
	}
	if !hasUniqIdx(chField.Tag, "idx_ls5bm_ch_model_bucket", 1) {
		t.Errorf("ChannelID tag %q missing uniqueIndex idx_ls5bm_ch_model_bucket priority 1", chField.Tag)
	}
	if !hasUniqIdx(mdField.Tag, "idx_ls5bm_ch_model_bucket", 2) {
		t.Errorf("ModelName tag %q missing uniqueIndex idx_ls5bm_ch_model_bucket priority 2", mdField.Tag)
	}
	if !hasUniqIdx(btField.Tag, "idx_ls5bm_ch_model_bucket", 3) {
		t.Errorf("BucketTS tag %q missing uniqueIndex idx_ls5bm_ch_model_bucket priority 3", btField.Tag)
	}

	// 4) DBName 映射：关键字段
	wantDBNames := map[string]string{
		"ChannelID":             "channel_id",
		"ModelName":             "model_name",
		"BucketTS":              "bucket_ts",
		"VendorCode":            "vendor_code",
		"RequestCount":          "request_count",
		"ErrorCount":            "error_count",
		"SuccessCount":          "success_count",
		"RefundCount":           "refund_count",
		"Quota":                 "quota",
		"PromptTokens":          "prompt_tokens",
		"CompletionTokens":      "completion_tokens",
		"CacheTokens":           "cache_tokens",
		"CacheCreationTokens5m": "cache_creation_tokens_5m",
		"CacheCreationTokens1h": "cache_creation_tokens_1h",
	}
	for _, f := range s.Fields {
		if want, ok := wantDBNames[f.Name]; ok {
			if f.DBName != want {
				t.Errorf("field %s: DBName = %q, want %q", f.Name, f.DBName, want)
			}
		}
	}
}
