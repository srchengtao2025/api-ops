// PR #2 单测 - 成本反推 + 上游对账 (BILLING v3)
package billing

import (
	"testing"
)

// TestCalcLogCost 单 log 成本反推
func TestCalcLogCost(t *testing.T) {
	tests := []struct {
		name            string
		quota           int64
		groupRatio      float64
		channelDiscount float64
		expected        float64
	}{
		{
			name:  "正常情况 - user_alpha mu-aws 0.64, ch-2 provider_alpha 0.24",
			quota: 50000, groupRatio: 0.64, channelDiscount: 0.24,
			// revenue = 50000/500000 = 0.1 USD
			// 原价 = 0.1 / 0.64 = 0.15625
			// cost = 0.15625 * 0.24 = 0.0375
			expected: 0.0375,
		},
		{
			name:  "group_ratio=1.0 default",
			quota: 50000, groupRatio: 1.0, channelDiscount: 0.5,
			// revenue = 0.1, 原价 = 0.1, cost = 0.05
			expected: 0.05,
		},
		{
			name:  "group_ratio=0 (边界, 防御性返 1.0)",
			quota: 50000, groupRatio: 0.0, channelDiscount: 0.5,
			// 防除零, 当 1.0 算
			expected: 0.05,
		},
		{
			name:  "quota=0 返 0",
			quota: 0, groupRatio: 0.5, channelDiscount: 0.5,
			expected: 0,
		},
		{
			name:  "channelDiscount=0 返 0 (上游白送)",
			quota: 50000, groupRatio: 0.5, channelDiscount: 0.0,
			expected: 0,
		},
		{
			name:  "channelDiscount=1.0 满额 (无折扣)",
			quota: 1000000, groupRatio: 0.5, channelDiscount: 1.0,
			// revenue = 2.0, 原价 = 4.0, cost = 4.0
			expected: 4.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcLogCost(tt.quota, tt.groupRatio, tt.channelDiscount)
			if !floatEqual(got, tt.expected, 0.0001) {
				t.Errorf("CalcLogCost(%d, %f, %f) = %f, want %f",
					tt.quota, tt.groupRatio, tt.channelDiscount, got, tt.expected)
			}
		})
	}
}

// TestCalcProfitMargin 利润率
func TestCalcProfitMargin(t *testing.T) {
	tests := []struct {
		name     string
		revenue  float64
		cost     float64
		expected float64
	}{
		{
			name:    "正常 - revenue=0.1 cost=0.0375, margin=166.7%",
			revenue: 0.1, cost: 0.0375,
			expected: 1.667, // (0.1 - 0.0375) / 0.0375 = 1.666...
		},
		{
			name:    "revenue=cost margin=0",
			revenue: 1.0, cost: 1.0,
			expected: 0.0,
		},
		{
			name:    "cost=0 返 0 (防除零)",
			revenue: 1.0, cost: 0.0,
			expected: 0.0,
		},
		{
			name:    "revenue=0 cost=0.5 亏 100%",
			revenue: 0.0, cost: 0.5,
			expected: -1.0, // (0 - 0.5) / 0.5 = -1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcProfitMargin(tt.revenue, tt.cost)
			if !floatEqual(got, tt.expected, 0.01) {
				t.Errorf("CalcProfitMargin(%f, %f) = %f, want %f",
					tt.revenue, tt.cost, got, tt.expected)
			}
		})
	}
}

// TestIsImageGenerationModel 图片生成模型识别
func TestIsImageGenerationModel(t *testing.T) {
	yes := []string{
		"gpt-image-2", "gpt-image-2-sp", "sora-2-pro", "sora-2",
		"midjourney-v6", "mj-pro-1", "dalle-3",
		"GPT-IMAGE-2", // 大小写不敏感
	}
	no := []string{
		"llm-model-a", "llm-model-a-mini", "llm-model-b-large",
		"llm-model-c", "llm-model-b", "",
	}
	for _, m := range yes {
		if !IsImageGenerationModel(m) {
			t.Errorf("IsImageGenerationModel(%q) = false, want true", m)
		}
	}
	for _, m := range no {
		if IsImageGenerationModel(m) {
			t.Errorf("IsImageGenerationModel(%q) = true, want false", m)
		}
	}
}

func floatEqual(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}
