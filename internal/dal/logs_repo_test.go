// logs_repo schema 回归测试
//
// 背景：new-api logs 表 DB 列名是 channel_id（不是 channel）。
// 之前 LogMirror.ChannelID 的 gorm tag 误写为 column:channel，
// 会导致 GORM ORM 链式 Where 自动生成 SQL：WHERE channel = ...
// 在 PG 实际表上执行时报 "column \"channel\" does not exist"。
//
// 本测试用 GORM schema reflection 验证 ChannelID 字段映射的 DBName，
// 防止回归。
package dal

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestLogMirror_ChannelID_DBColumn(t *testing.T) {
	s, err := schema.Parse(&LogMirror{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("schema parse failed: %v", err)
	}
	for _, f := range s.Fields {
		if f.Name == "ChannelID" {
			if f.DBName != "channel_id" {
				t.Fatalf("LogMirror.ChannelID 映射到 DBName=%q，期望 channel_id（new-api logs 表实际列名）", f.DBName)
			}
			return
		}
	}
	t.Fatal("LogMirror 未找到 ChannelID 字段")
}
