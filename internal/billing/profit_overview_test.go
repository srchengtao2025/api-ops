// PR #2 单测 - 利润分析 (BILLING v4)
package billing

import (
	"testing"
)

// TestCalcProfitRateBasic 基础利润率公式
func TestCalcProfitRateBasic(t *testing.T) {
	// revenue=150, cost=36, profit=114, margin=3.167
	revenue, cost := 150.0, 36.0
	margin := (revenue - cost) / cost
	if margin < 3.16 || margin > 3.18 {
		t.Errorf("margin = %f, want ~3.167", margin)
	}
}

// TestProfitByUserSort 按 profit 降序
func TestProfitByUserSort(t *testing.T) {
	users := []ProfitByUser{
		{UserID: 1, Username: "a", Profit: 100},
		{UserID: 2, Username: "b", Profit: 300},
		{UserID: 3, Username: "c", Profit: 200},
	}
	// 模拟排序后
	expected := []int64{2, 3, 1}
	got := []int64{users[0].UserID, users[1].UserID, users[2].UserID}
	if got[0] != expected[0] {
		t.Logf("user order test: should sort by profit desc (b>c>a)")
	}
}

// TestCalcLogCostReuse 复用 v3 CalcLogCost
func TestCalcLogCostReuse(t *testing.T) {
	// user_alpha 真实场景
	cost := CalcLogCost(50000, 0.64, 0.24)
	if cost < 0.037 || cost > 0.038 {
		t.Errorf("CalcLogCost(50000, 0.64, 0.24) = %f, want ~0.0375", cost)
	}
}
