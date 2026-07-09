// BILLING 客户健康度模块单测 (2026-07-08)
//
// 重点验证:
//   - parseHealthPeriod: "48h"/"7d"/"30d" 解析 + 错误 case
//   - computeHealthLevel: 健康度等级阈值 (Q-D 决策 2026-07-08)
//   - levelOrder: 排序 (critical < warning < healthy)
//   - joinReasons: 拼接
//
// 跳过:
//   - HTML 渲染端到端测试 (依赖 RoDB, 等集成测)
//   - Handler httptest (需要 mock dal + auth, 等 PR #7)
package billing

import (
	"strings"
	"testing"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

func TestParseHealthPeriod_Valid(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		input        string
		wantDuration int64 // seconds
	}{
		{"48h", 48 * 3600},
		{"7d", 7 * 86400},
		{"30d", 30 * 86400},
		{"1h", 3600},
		{"90d", 90 * 86400},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			startTS, endTS, err := parseHealthPeriod(c.input)
			if err != nil {
				t.Fatalf("parseHealthPeriod(%q) err: %v", c.input, err)
			}
			if endTS < now-2 || endTS > now+2 {
				t.Fatalf("endTS should be ~now, got %d (now=%d)", endTS, now)
			}
			dur := endTS - startTS
			if dur != c.wantDuration {
				t.Fatalf("duration=%d, want %d", dur, c.wantDuration)
			}
		})
	}
}

func TestParseHealthPeriod_Invalid(t *testing.T) {
	cases := []struct {
		input string
		desc  string
	}{
		{"", "empty"},
		{"48", "no unit"},
		{"48x", "unknown unit"},
		{"abc", "not number"},
		{"0h", "zero"},
		{"-1h", "negative"},
		{"h", "no number"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			_, _, err := parseHealthPeriod(c.input)
			if err == nil {
				t.Fatalf("parseHealthPeriod(%q) should error", c.input)
			}
		})
	}
}

func TestComputeHealthLevel_Healthy(t *testing.T) {
	// 错误率 < 2% AND 缓存 >= 90% → healthy
	cases := []struct {
		name      string
		errRate   float64
		cacheRate float64
		wantLevel string
	}{
		{"perfect", 0.0, 1.0, HealthLevelHealthy},
		{"low err high cache", 0.01, 0.95, HealthLevelHealthy},
		{"just below err threshold", 0.019, 0.92, HealthLevelHealthy},
		{"just at cache threshold", 0.005, 0.90, HealthLevelHealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			level, _ := computeHealthLevel(c.errRate, c.cacheRate)
			if level != c.wantLevel {
				t.Fatalf("errRate=%f cacheRate=%f → level=%q, want %q",
					c.errRate, c.cacheRate, level, c.wantLevel)
			}
		})
	}
}

func TestComputeHealthLevel_Warning(t *testing.T) {
	// 2% <= 错误率 < 10% OR 70% <= 缓存 < 90% → warning
	cases := []struct {
		name      string
		errRate   float64
		cacheRate float64
	}{
		{"mid err low cache", 0.05, 0.85, }, // 双维度都黄
		{"mid err high cache", 0.05, 0.95, }, // 错误率黄, 缓存绿 → 整体黄
		{"low err low cache", 0.01, 0.85, }, // 错误率绿, 缓存黄 → 整体黄
		{"just at err threshold", 0.02, 0.95, },
		{"just below err critical", 0.099, 0.95, },
		{"just at cache warning", 0.01, 0.70, },
		{"just below cache healthy", 0.01, 0.899, },
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			level, reasons := computeHealthLevel(c.errRate, c.cacheRate)
			if level != HealthLevelWarning {
				t.Fatalf("errRate=%f cacheRate=%f → level=%q, want %q (reasons: %s)",
					c.errRate, c.cacheRate, level, HealthLevelWarning, reasons)
			}
		})
	}
}

func TestComputeHealthLevel_Critical(t *testing.T) {
	// 错误率 >= 10% OR 缓存 < 70% → critical
	cases := []struct {
		name      string
		errRate   float64
		cacheRate float64
	}{
		{"high err high cache", 0.15, 0.95, }, // 错误率红, 缓存绿 → 整体红
		{"low err very low cache", 0.01, 0.50, }, // 错误率绿, 缓存红 → 整体红
		{"high err low cache", 0.20, 0.60, }, // 双红
		{"just at err critical", 0.10, 0.95, },
		{"just below cache warning", 0.01, 0.699, },
		{"zero cache", 0.01, 0.0, },
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			level, reasons := computeHealthLevel(c.errRate, c.cacheRate)
			if level != HealthLevelCritical {
				t.Fatalf("errRate=%f cacheRate=%f → level=%q, want %q (reasons: %s)",
					c.errRate, c.cacheRate, level, HealthLevelCritical, reasons)
			}
		})
	}
}

func TestComputeHealthLevel_ReasonsNotEmpty(t *testing.T) {
	// reasons 永远非空, 用于前端 tooltip
	cases := []struct {
		errRate   float64
		cacheRate float64
	}{
		{0.0, 1.0},
		{0.05, 0.5},
		{0.20, 0.95},
	}
	for _, c := range cases {
		_, reasons := computeHealthLevel(c.errRate, c.cacheRate)
		if reasons == "" {
			t.Fatalf("reasons should not be empty for errRate=%f cacheRate=%f", c.errRate, c.cacheRate)
		}
	}
}

func TestLevelOrder(t *testing.T) {
	// critical < warning < healthy (排序时 critical 优先)
	if levelOrder(HealthLevelCritical) >= levelOrder(HealthLevelWarning) {
		t.Fatal("critical should come before warning")
	}
	if levelOrder(HealthLevelWarning) >= levelOrder(HealthLevelHealthy) {
		t.Fatal("warning should come before healthy")
	}
	// 未知等级 fallback
	if levelOrder("unknown") != 2 {
		t.Fatal("unknown level should fallback to 2")
	}
}

func TestJoinReasons(t *testing.T) {
	cases := []struct {
		input []string
		want  string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a; b"},
		{[]string{"错误率 5.00% ≥ 2%", "缓存复用率 80.00% < 90%"}, "错误率 5.00% ≥ 2%; 缓存复用率 80.00% < 90%"},
	}
	for _, c := range cases {
		got := joinReasons(c.input)
		if got != c.want {
			t.Fatalf("joinReasons(%v) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFormatTimeCN(t *testing.T) {
	// unix epoch 0 = 1970-01-01 00:00:00 UTC
	// 加 8h 时区 (CST) = 1970-01-01 08:00:00
	got := formatTimeCN(0)
	if got != "1970-01-01 08:00:00" {
		t.Fatalf("formatTimeCN(0) = %q, want %q", got, "1970-01-01 08:00:00")
	}
	// 再验证一个边界: 1752038400 (2025-07-09 13:20:00 CST, 已知输出)
	got = formatTimeCN(1752038400)
	if got != "2025-07-09 13:20:00" {
		t.Fatalf("formatTimeCN(1752038400) = %q, want %q", got, "2025-07-09 13:20:00")
	}
}

func TestHitRatePct(t *testing.T) {
	cases := []struct {
		prompt, cache int
		want          string
	}{
		{0, 0, "0.00%"},
		{100, 50, "50.00%"},
		{200, 100, "50.00%"},
		{3, 2, "66.67%"},
		{100, 100, "100.00%"},
	}
	for _, c := range cases {
		got := hitRatePct(c.prompt, c.cache)
		if got != c.want {
			t.Fatalf("hitRatePct(%d, %d) = %q, want %q", c.prompt, c.cache, got, c.want)
		}
	}
}

func TestHealthLevelLabel(t *testing.T) {
	cases := map[string]string{
		HealthLevelHealthy:  "健康",
		HealthLevelWarning:  "关注",
		HealthLevelCritical: "告警",
		"unknown":           "unknown", // fallback
	}
	for input, want := range cases {
		got := healthLevelLabel(input)
		if got != want {
			t.Fatalf("healthLevelLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestRenderErrorHTML_Regression: 2026-07-09 bug fix
// 之前 .ErrorRate 传 string ("23.64%") 给 template, 模板用 gt .ErrorRate 10.0 比较挂
// 修: 传 float64, 模板内 printf "%.2f%%" (mulf .ErrorRate 100)
func TestRenderErrorHTML_Regression(t *testing.T) {
	d := &CustomerHealthDetail{
		UserID:       47,
		Username:     "Phanthy",
		Period:       "48h",
		RequestCount: 1000,
		SuccessCount: 700,
		ErrorCount:   300,
		ErrorRate:    0.3, // 30%
		HealthLevel:  HealthLevelCritical,
		Errors: []CustomerHealthDetailItem{
			{ID: 1, CreatedAt: 1752038400, ModelName: "glm-5.2", ChannelID: 9, PromptTokens: 100, CompletionTokens: 50, Content: "test error"},
		},
		ErrorsTruncated: false,
	}
	task := &dal.BillingExportTask{
		TaskID: "test-task-id",
		Site:   "intl",
		Period: "48h",
	}
	html := renderErrorDetailHTML(d, task)
	// 关键验证: HTML 包含正确格式化的错误率 "30.00%" (不是 "render error:")
	if !strings.Contains(html, "30.00%") {
		t.Errorf("HTML should contain '30.00%%', got: %.200s", html)
	}
	if strings.Contains(html, "render error") {
		t.Errorf("HTML should not contain 'render error' (template failed), got: %.200s", html)
	}
	if !strings.Contains(html, "Phanthy") {
		t.Errorf("HTML should contain username 'Phanthy'")
	}
}

func TestRenderHitHTML_Regression(t *testing.T) {
	d := &CustomerHealthDetail{
		UserID:    47,
		Username:  "Phanthy",
		Period:    "48h",
		PromptTokens: 1000,
		CacheTokens: 950,
		CacheRate:    0.95, // 95%
		HealthLevel:  HealthLevelHealthy,
		Hits: []CustomerHealthDetailItem{
			{ID: 1, CreatedAt: 1752038400, ModelName: "glm-5.2", ChannelID: 9, PromptTokens: 100, CacheTokens: 95, CompletionTokens: 50},
		},
		HitsTruncated: false,
	}
	task := &dal.BillingExportTask{
		TaskID: "test-task-id",
		Site:   "intl",
		Period: "48h",
	}
	html := renderHitDetailHTML(d, task)
	if !strings.Contains(html, "95.00%") {
		t.Errorf("HTML should contain '95.00%%', got: %.200s", html)
	}
	if strings.Contains(html, "render error") {
		t.Errorf("HTML should not contain 'render error' (template failed), got: %.200s", html)
	}
}
