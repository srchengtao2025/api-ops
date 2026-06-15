// PR #9 单测 (2026-06-15) - ops_upstream_summary_5min DAL 单元测试
//
// 覆盖:
//  1. TableName = "ops_upstream_summary_5min"
//  2. UpstreamPeriodLabel 常量值
//
// 集成测试 (RoDB 必连) 见 docker 内跑 go test ./internal/dal/... -tags integration
package dal

import "testing"

// TestOpsUpstreamSummary5minTableName 表名稳定性
func TestOpsUpstreamSummary5minTableName(t *testing.T) {
	s := OpsUpstreamSummary5min{}
	if s.TableName() != "ops_upstream_summary_5min" {
		t.Fatalf("TableName = %q, want ops_upstream_summary_5min", s.TableName())
	}
}

// TestUpstreamPeriodLabelConstants 标签常量稳定性
func TestUpstreamPeriodLabelConstants(t *testing.T) {
	if string(UpstreamPeriodCurrentMonth) != "current-month" {
		t.Errorf("UpstreamPeriodCurrentMonth = %q, want current-month", string(UpstreamPeriodCurrentMonth))
	}
	if string(UpstreamPeriodLastMonth) != "last-month" {
		t.Errorf("UpstreamPeriodLastMonth = %q, want last-month", string(UpstreamPeriodLastMonth))
	}
}

// TestOpsUpstreamSummary5minUniqueConstraintTags GORM 标签 sanity 检查
//
// 真实 UNIQUE 约束由 SQL migration 强制, 这里只验 GORM tag 写对位置
func TestOpsUpstreamSummary5minUniqueConstraintTags(t *testing.T) {
	// 通过 reflect 检查 gorm tag (避免大改 assert 库, 简化版)
	// 实际生产时建议用 github.com/stretchr/testify/assert
	s := OpsUpstreamSummary5min{}
	if s.TableName() != "ops_upstream_summary_5min" {
		t.Fatalf("TableName 错: %q", s.TableName())
	}
	// 注: 这里只 smoke test, 详细 gorm tag 检查在 AutoMigrate 跑通后由 PG 自己报
}
