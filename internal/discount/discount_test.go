package discount

import "testing"

func TestParseDiscountFromName(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantDiscount   float64
		wantRecognized bool
	}{
		// 真实 49 渠道样本 (业务规则: X 折 = X/100, 0.X 折 = 直接用)
		{"42折", "dy-claude-aws号池-42折", 0.42, true},
		{"75折", "dy-claude-aws官KEY-75折", 0.75, true},
		{"0.1折", "dy-claude-kir-0.1折", 0.10, true},
		{"0.24折_带分组", "dy-gemini-nano-sp分组-0.24折", 0.24, true},
		{"0.72折_带空格", "dy-gemini-官方-0.72 折", 0.72, true},
		{"0.06折_小数", "dy-openai-gpt号池-0.06折", 0.06, true},
		{"72折_不带小数", "dy-Astrio-Anthropic原厂-72折", 0.72, true},
		{"55折_主节点后缀", "dy-mu范式-aws号池-55折-主节点", 0.55, true},
		{"55折_备节点后缀", "dy-mu范式-aws号池-55折_备节点", 0.55, true},
		{"45折_TPM后缀", "EN-百炼国际-45折-3000万TPM", 0.45, true},
		{"5折_单位数", "ez-claude-aws号池-5折", 0.5, true},
		{"0.32折_空号", "dy-Gemini-vip大并发号池-0.32折", 0.32, true},
		{"0.2折_空格", "dy-nano-临时号池-0.2 折", 0.20, true},
		{"0.62折_原厂", "dy-claude-原厂号池-0.62 折", 0.62, true},
		{"75折_纯数字开头", "dy-aws-官号key-75折", 0.75, true},
		{"0.38折_中文姓", "杜总-短期速刷aws号池-0.38折", 0.38, true},
		{"0.22折_姓2字", "刘总-provider_delta-0.22折", 0.22, true},
		{"0.6折_大写", "dy-Openai纯官方-0.6折", 0.6, true},
		// 未识别
		{"原价", "GLM5.1-阿里百炼官方-原价", 1.0, false},
		{"RMB后缀", "杜总-AWS速刷稳定-3RMB", 1.0, false},
		{"8位纯数字", "Ascend官转AWS-668453839089", 1.0, false},
		{"channel-N", "channel-18", 1.0, false},
		{"空字符串", "", 1.0, false},
		{"纯中文", "测试渠道", 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDiscountFromName(tt.input)
			if got.Recognized != tt.wantRecognized {
				t.Errorf("Recognized: got %v, want %v (input=%q, matched=%q)",
					got.Recognized, tt.wantRecognized, tt.input, got.Matched)
			}
			// 浮点比较用 epsilon
			if abs(got.AutoDiscount-tt.wantDiscount) > 0.001 {
				t.Errorf("AutoDiscount: got %v, want %v (input=%q, matched=%q)",
					got.AutoDiscount, tt.wantDiscount, tt.input, got.Matched)
			}
		})
	}
}

func TestNormalizeDiscount(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0.42, 0.42}, // 已是小数
		{0.0, 0.0},   // 边界
		{1.0, 1.0},   // 边界
		{0.05, 0.05}, // 极小
		{0.6, 0.6},   // 0.6 不变
		{1.5, 1.0},   // > 1 clamp 到 1.0
		{42, 1.0},    // > 1 clamp
		{100, 1.0},   // = 1
		{-1, 0.0},    // 负数
		{0.99, 0.99}, // 接近 1
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := NormalizeDiscount(tt.input); got != tt.want {
				t.Errorf("NormalizeDiscount(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
