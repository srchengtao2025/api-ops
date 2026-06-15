// Package discount: 渠道名 → 折扣 解析 + 矫正
//
// 业务规则:
//   - 渠道名末尾的 "数字+折" 表示折扣 (如 "dy-claude-aws号池-42折" → 0.42)
//   - 数字 > 1 时按 X/10 (42折 → 0.42, 75折 → 0.75, 5折 → 0.5)
//   - 数字 ≤ 1 时按小数 (0.06折 → 0.06, 0.72折 → 0.72)
//   - 没有 "数字+折" 时返回 (1.0, false)  // false = 未识别, 需人工矫正
//   - 中间可能有空格 (如 "0.72 折")
//   - 渠道名里可能有 "号池" / "主节点" / "备节点" / "TPM" 等业务词, 不影响
//
// 真实样本 (49 个 upstream 渠道):
//
//	"dy-claude-aws号池-42折"          → (0.42, true)
//	"dy-claude-aws官KEY-75折"         → (0.75, true)
//	"dy-claude-kir-0.1折"             → (0.10, true)
//	"dy-gemini-nano-sp分组-0.24折"    → (0.24, true)
//	"dy-gemini-官方-0.72 折"          → (0.72, true)
//	"dy-openai-gpt号池-0.06折"        → (0.06, true)
//	"dy-Astrio-Anthropic原厂-72折"    → (0.72, true)
//	"dy-mu范式-aws号池-55折-主节点"   → (0.55, true)  // 取第一个匹配
//	"dy-mu范式-aws号池-55折_备节点"   → (0.55, true)
//	"EN-百炼国际-45折-3000万TPM"       → (0.45, true)
//	"GLM5.1-阿里百炼官方-原价"        → (1.0,  false) // "原价" 不是 "折"
//	"杜总-AWS速刷稳定-3RMB"            → (1.0,  false) // "RMB" 不是 "折"
//	"Ascend官转AWS-668453839089"      → (1.0,  false) // 8 位纯数字无 "折"
//	"channel-18"                       → (1.0,  false)
package discount

import (
	"regexp"
	"strconv"
)

// discountPattern 匹配 数字(可带小数) + 可选空格 + 折
// 例: 42折 / 0.06折 / 0.72 折 / 75折
var discountPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*折`)

// ParseResult 解析结果
type ParseResult struct {
	AutoDiscount float64 // 自动解析的折扣 (0-1)
	Recognized   bool    // true=从名字识别出, false=未识别, 需人工矫正
	Matched      string  // 匹配到的原始字符串 (debug 用, 如 "42折" / "0.72 折")
}

// ParseDiscountFromName 渠道名 → 折扣
//
// 业务规则 (用户确认 2026-06-14, upstream 业务特殊):
//   - "X 折" (X > 10)            → X/100 (42折=0.42, 75折=0.75, 100折=1.0)
//   - "X 折" (1 < X ≤ 10)        → X/10  (5折=0.5, 7折=0.7, 8折=0.8, 10折=1.0)
//   - "0.X 折" (X ≤ 1)           → 直接用小数 (0.06折=0.06, 0.72折=0.72)
//   - 不匹配 (如 "原价", "RMB")  → (1.0, false) 需人工矫正
//
// 业务语义 (中文折扣混合表达):
//   - "5 折" = 0.5 折扣 (打 5 折, 收 50% 的价)        ← 中文折扣习惯
//   - "42 折" = 0.42 折扣 (收 42% 的价)                ← 百分数表达
//   - "75 折" = 0.75 折扣 (收 75% 的价)
//   - "0.06 折" = 0.06 折扣 (收 6% 的价, 0.06 直接当 6% 理解)
//
// 历史: 早期想统一 X/100, 但用户说 5 折 = 0.5 不是 0.05, 中文折扣 "X 折"
// 在 ≤ 10 时是 X/10, > 10 时是 X/100. 0.X 折直接用.
func ParseDiscountFromName(name string) ParseResult {
	if name == "" {
		return ParseResult{AutoDiscount: 1.0, Recognized: false, Matched: ""}
	}
	matches := discountPattern.FindStringSubmatch(name)
	if len(matches) < 2 {
		return ParseResult{AutoDiscount: 1.0, Recognized: false, Matched: ""}
	}
	val, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || val <= 0 {
		return ParseResult{AutoDiscount: 1.0, Recognized: false, Matched: matches[0]}
	}
	switch {
	case val > 10:
		// 42折 → 0.42, 75折 → 0.75, 100折 → 1.0
		return ParseResult{AutoDiscount: val / 100.0, Recognized: true, Matched: matches[0]}
	case val > 1:
		// 5折 → 0.5, 7折 → 0.7, 8折 → 0.8, 10折 → 1.0 (中文折扣习惯)
		return ParseResult{AutoDiscount: val / 10.0, Recognized: true, Matched: matches[0]}
	default:
		// 0.06折 → 0.06, 0.72折 → 0.72 (直接小数, 当 6% / 72% 理解)
		return ParseResult{AutoDiscount: val, Recognized: true, Matched: matches[0]}
	}
}

// NormalizeDiscount 把用户输入归一化到 [0, 1] 区间
// 例: 0.42 / 42 / 42% / 4.2折 / 0.42折 → 0.42
// 用于矫正界面用户输入兼容
//
// 区间规则 (与 ParseDiscountFromName 一致):
//   - [0, 1]      → 直接用 (0.42 = 0.42)
//   - (1, 10]     → X 折 (5 → 0.5, 1.5 → 0.15)
//   - (10, 100]   → X 折 (42 → 4.2, 75 → 7.5)? 这不对
//   - > 100       → clamp 到 1.0
//   - < 0         → clamp 到 0
//
// ⚠️ 注意: NormalizeDiscount 与 ParseDiscountFromName 不完全一致
//   - ParseDiscountFromName: 42折 → 0.42 (因为 42 > 1 但 upstream 业务上 42 就是 0.42)
//   - NormalizeDiscount: 42 输 → 4.2 (因为用户输 42 可能是想 42 = 0.42 * 100, 也可能是想输 4.2)
//
// 实际矫正界面用 InputNumber + step=0.01, 0-1 范围, 用户输 0.42 而非 42
// 所以 NormalizeDiscount 仅做边界 clamp, 不做 X/10 转换
func NormalizeDiscount(input float64) float64 {
	if input < 0 {
		return 0
	}
	if input > 1 {
		return 1.0
	}
	return input
}
