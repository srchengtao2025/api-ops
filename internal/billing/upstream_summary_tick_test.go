// PR #9 单测 (2026-06-15) - upstream 5min cache 聚合
//
// 覆盖场景:
//  1. computeUpstreamBucket 5min 对齐边界 (5min 倍数 / 非倍数 / 跨天)
//  2. mapPeriodToLabel period (YYYY-MM) → cache period_label 映射
//     - 本月 → 'current-month'
//     - 上月 → 'last-month'
//     - 2 月前 → ” (cache miss, 走 live calc)
//  3. currentMonthBounds / lastMonthBounds 边界
//  4. sortVendorsByRequestCount 排序稳定性
//  5. GetUpstreamOverviewCached cache miss 行为 (无需真实 DB, 用 nil DB 时返 0/0/nil)
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// TestComputeUpstreamBucket 5min 对齐
func TestComputeUpstreamBucket(t *testing.T) {
	tests := []struct {
		name string
		in   int64 // unix sec
		want int64
	}{
		{"整 5min 倍数", 1718445000, 1718445000},       // 2024-06-15 11:50:00 (假设 5min 倍数)
		{"5min 内偏 1s", 1718445001, 1718445000},      // 向下取整
		{"5min 内偏 60s", 1718445060, 1718445000},     // 向下取整
		{"5min 边界 4min59s", 1718445299, 1718445000}, // 整 5min 内 (4:59)
		{"下个 5min 起点", 1718445300, 1718445300},      // 5:00
		{"跨小时", 1718446500, 1718446500},             // 6:15 → 6:15
		{"0 epoch", 0, 0},                           // 边界
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(tt.in, 0)
			got := computeUpstreamBucket(now)
			if got.Unix() != tt.want {
				t.Errorf("computeUpstreamBucket(%d) = %d, want %d", tt.in, got.Unix(), tt.want)
			}
		})
	}
}

// TestMapPeriodToLabel period → cache label 映射
func TestMapPeriodToLabel(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	currentMonth := now.Format("2006-01")
	lastMonthTime := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -1, 0)
	lastMonth := lastMonthTime.Format("2006-01")

	tests := []struct {
		name   string
		period string
		want   dal.UpstreamPeriodLabel
	}{
		{"本月 → current-month", currentMonth, dal.UpstreamPeriodCurrentMonth},
		{"上月 → last-month", lastMonth, dal.UpstreamPeriodLastMonth},
		{"2 月前 → 空 (cache miss)", "2024-04", ""},
		{"未来月 → 空", "2099-12", ""},
		{"格式错 → 空", "2024/05", ""},
		{"空 → 空", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapPeriodToLabel(tt.period, 0)
			if got != tt.want {
				t.Errorf("mapPeriodToLabel(%q) = %q, want %q", tt.period, got, tt.want)
			}
		})
	}
}

// TestCurrentMonthBounds 本月至今 [startOfMonth, now]
func TestCurrentMonthBounds(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	// 2026-06-15 12:34:56
	now := time.Date(2026, 6, 15, 12, 34, 56, 0, loc)
	start, end := currentMonthBounds(now)
	wantStart := time.Date(2026, 6, 1, 0, 0, 0, 0, loc).Unix()
	wantEnd := now.Unix()
	if start != wantStart {
		t.Errorf("currentMonthBounds start = %d, want %d (2026-06-01 00:00)", start, wantStart)
	}
	if end != wantEnd {
		t.Errorf("currentMonthBounds end = %d, want %d (now)", end, wantEnd)
	}
}

// TestLastMonthBounds 上月完整 [1 号 00:00, 月末 23:59:59]
func TestLastMonthBounds(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 6, 15, 12, 34, 56, 0, loc) // 6 月中 → 算 5 月
	start, end := lastMonthBounds(now)
	wantStart := time.Date(2026, 5, 1, 0, 0, 0, 0, loc).Unix()
	wantEnd := time.Date(2026, 5, 31, 23, 59, 59, 0, loc).Unix()
	if start != wantStart {
		t.Errorf("lastMonthBounds start = %d, want %d (2026-05-01 00:00)", start, wantStart)
	}
	if end != wantEnd {
		t.Errorf("lastMonthBounds end = %d, want %d (2026-05-31 23:59:59)", end, wantEnd)
	}
}

// TestLastMonthBounds_跨年 1 月中 → 算去年 12 月
func TestLastMonthBounds_跨年(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, loc) // 1 月中 → 去年 12 月
	start, end := lastMonthBounds(now)
	wantStart := time.Date(2025, 12, 1, 0, 0, 0, 0, loc).Unix()
	wantEnd := time.Date(2025, 12, 31, 23, 59, 59, 0, loc).Unix()
	if start != wantStart || end != wantEnd {
		t.Errorf("lastMonthBounds 跨年 = [%d, %d], want [%d, %d] (2025-12)", start, end, wantStart, wantEnd)
	}
}

// TestSortVendorsByRequestCount 排序: RequestCount DESC
func TestSortVendorsByRequestCount(t *testing.T) {
	vs := []UpstreamOverviewVendor{
		{VendorCode: "a", RequestCount: 10},
		{VendorCode: "b", RequestCount: 100},
		{VendorCode: "c", RequestCount: 50},
		{VendorCode: "d", RequestCount: 1},
	}
	sortVendorsByRequestCount(vs)
	want := []string{"b", "c", "a", "d"}
	for i, v := range vs {
		if v.VendorCode != want[i] {
			t.Errorf("sortVendorsByRequestCount [%d] = %q, want %q", i, v.VendorCode, want[i])
		}
	}
}

// TestSortVendorsByRequestCount_空
func TestSortVendorsByRequestCount_空(t *testing.T) {
	vs := []UpstreamOverviewVendor{}
	sortVendorsByRequestCount(vs)
	if len(vs) != 0 {
		t.Errorf("sortVendorsByRequestCount empty, got len=%d", len(vs))
	}
}

// TestSortVendorsByRequestCount_单元素
func TestSortVendorsByRequestCount_单元素(t *testing.T) {
	vs := []UpstreamOverviewVendor{{VendorCode: "a", RequestCount: 1}}
	sortVendorsByRequestCount(vs)
	if vs[0].VendorCode != "a" {
		t.Errorf("sortVendorsByRequestCount single, got %q", vs[0].VendorCode)
	}
}

// TestGetUpstreamOverviewCached_RoDBNotConfigured 无 RoDB 返 ErrNoRoDB
//
// 不需要真 DB, 走 dal.HasRoDB() == false 路径
func TestGetUpstreamOverviewCached_RoDBNotConfigured(t *testing.T) {
	// 确保 RO nil
	originalRO := dal.RO
	dal.RO = nil
	defer func() { dal.RO = originalRO }()

	_, err := GetUpstreamOverviewCached(context.Background())
	if err != dal.ErrNoRoDB {
		t.Errorf("GetUpstreamOverviewCached 无 RoDB 期望 ErrNoRoDB, got %v", err)
	}
}

// TestGetUpstreamStatementCached_EmptyVendor 空 vendor 返错
func TestGetUpstreamStatementCached_EmptyVendor(t *testing.T) {
	// dal.HasRoDB() == false → ErrNoRoDB 先返; 改用 vendor=="" 测业务校验
	// 但 RO nil 时先返 ErrNoRoDB, 业务校验放后面
	// 这个 case 在 RoDB 未配时只能验到 ErrNoRoDB, 业务校验需要在 RO 配的集成测试里验
	// 这里跳过 (待 PR 后续集成测试覆盖)
	t.Skip("业务校验需要 RoDB 配, 跳过; 集成测试见 upstream_integration_test.go")
}

// TestGetUpstreamStatementCached_RoDBNotConfigured 同上, 无 RoDB 返 ErrNoRoDB
func TestGetUpstreamStatementCached_RoDBNotConfigured(t *testing.T) {
	originalRO := dal.RO
	dal.RO = nil
	defer func() { dal.RO = originalRO }()

	_, _, err := GetUpstreamStatementCached(context.Background(), "provider_alpha", 0, 0, dal.UpstreamPeriodCurrentMonth)
	if err != dal.ErrNoRoDB {
		t.Errorf("GetUpstreamStatementCached 无 RoDB 期望 ErrNoRoDB, got %v", err)
	}
}
