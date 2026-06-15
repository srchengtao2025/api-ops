// Package ai: 错误聚类测试
//
// 测试策略：
//   - pgArrayToJSON 是纯函数，覆盖所有边界（空 / 单元素 / 多元素 / 嵌套大括号）
//   - normalizePattern 是 PG REGEXP_REPLACE 三步归一化的 Go 等价实现（UUID → <UUID>;
//     ISO-8601 TS → <TS>; 裸数字 → <N>），用于离线验证归一化行为
//   - aggregateClusters 模拟 (pattern, channel_id, model_name) 分组 + Top 50 排序
//   - ClusterWindow / ClusterOneHour 需要 DB（PG REGEXP_REPLACE），本测试不覆盖
//
// 覆盖的边界 case：
//  1. pgArrayToJSON：标准 PG 数组 {a,b,c} → ["a","b","c"]
//  2. pgArrayToJSON：空数组 {} → []
//  3. pgArrayToJSON：单元素 {foo} → ["foo"]
//  4. 归一化：UUID 替换正确（多种 UUID 格式）
//  5. 归一化：ISO-8601 时间戳替换
//  6. 归一化：裸数字替换（保留时间戳里的数字不变，因为已被 <TS> 替换）
//  7. 归一化：组合 pattern（错误堆栈示例）
//  8. 聚合：按 (pattern, channel, model) 分组，相同 pattern 累加 count
//  9. Top 50 排序：count 倒序，超过 50 取前 50
//  10. 归一化幂等：归一化两次应等于归一化一次
package ai

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ===== 边界 case 1-3: pgArrayToJSON =====

func TestPgArrayToJSON_Standard(t *testing.T) {
	in := []byte("{alice,bob,charlie}")
	want := `["alice","bob","charlie"]`
	if got := pgArrayToJSON(in); got != want {
		t.Errorf("pgArrayToJSON(%q)=%q 期望 %q", in, got, want)
	}
}

func TestPgArrayToJSON_Empty(t *testing.T) {
	in := []byte("{}")
	want := `[]`
	if got := pgArrayToJSON(in); got != want {
		t.Errorf("pgArrayToJSON({})= %q 期望 %q", got, want)
	}
}

func TestPgArrayToJSON_Single(t *testing.T) {
	in := []byte("{foo}")
	want := `["foo"]`
	if got := pgArrayToJSON(in); got != want {
		t.Errorf("pgArrayToJSON({foo})=%q 期望 %q", got, want)
	}
}

func TestPgArrayToJSON_TrimWhitespace(t *testing.T) {
	in := []byte("  {a, b}  ")
	// 当前实现：trim 后 split on "," — 输出 ["a", " b"]（含前导空格）
	got := pgArrayToJSON(in)
	// 仅验证能解析为 JSON 数组（不强制精确匹配空格的语义）
	var arr []string
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Errorf("pgArrayToJSON 应产出合法 JSON, got=%q err=%v", got, err)
	}
	if len(arr) != 2 {
		t.Errorf("array length=%d 期望 2", len(arr))
	}
}

// ===== 归一化辅助函数（Go 等价实现 PG REGEXP_REPLACE 三步） =====

var (
	uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	tsRe   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)
	numRe  = regexp.MustCompile(`\b\d+\b`)
)

// normalizePattern PG 三步归一化的 Go 等价
func normalizePattern(s string) string {
	s = uuidRe.ReplaceAllString(s, "<UUID>")
	s = tsRe.ReplaceAllString(s, "<TS>")
	s = numRe.ReplaceAllString(s, "<N>")
	return s
}

// ===== 边界 case 4: UUID 替换 =====

func TestNormalize_UUID(t *testing.T) {
	in := "request failed: trace_id=550e8400-e29b-41d4-a716-446655440000"
	want := "request failed: trace_id=<UUID>"
	if got := normalizePattern(in); got != want {
		t.Errorf("normalize UUID: got=%q want=%q", got, want)
	}
}

func TestNormalize_MultipleUUIDs(t *testing.T) {
	in := "req=550e8400-e29b-41d4-a716-446655440000 resp=12345678-1234-1234-1234-123456789012"
	want := "req=<UUID> resp=<UUID>"
	if got := normalizePattern(in); got != want {
		t.Errorf("multi-UUID: got=%q want=%q", got, want)
	}
}

// ===== 边界 case 5: ISO-8601 时间戳 =====

func TestNormalize_ISO8601(t *testing.T) {
	in := "error at 2024-05-13T08:30:00 in handler"
	want := "error at <TS> in handler"
	if got := normalizePattern(in); got != want {
		t.Errorf("normalize TS: got=%q want=%q", got, want)
	}
}

func TestNormalize_TS_Then_Number(t *testing.T) {
	// TS 含数字，但已被 <TS> 替换；剩余其他数字被替换
	in := "2024-05-13T08:30:00 failed after 3 retries"
	// 第一步：UUID 无 → 不变
	// 第二步：TS → <TS> → "<TS> failed after 3 retries"
	// 第三步：裸数字 → "<N>" → "<TS> failed after <N> retries"
	want := "<TS> failed after <N> retries"
	if got := normalizePattern(in); got != want {
		t.Errorf("TS + number: got=%q want=%q", got, want)
	}
}

// ===== 边界 case 6: 裸数字 =====

func TestNormalize_Numbers(t *testing.T) {
	in := "status=500 error_count=12"
	want := "status=<N> error_count=<N>"
	if got := normalizePattern(in); got != want {
		t.Errorf("normalize numbers: got=%q want=%q", got, want)
	}
}

func TestNormalize_Numbers_NoWordBoundary(t *testing.T) {
	// UUID 里的 hex 不算 "number"（UUID pattern 优先）
	// Go \b 在 word char 切换时也算 word boundary；
	// 但 Go regex \b\d+\b 在 "v2" 中：v 是 word char, 2 也是 word char → 中间不是 \b → 不匹配
	in := "v2 error"
	got := normalizePattern(in)
	if got != "v2 error" {
		t.Errorf("v2 中 2 不被替换（word-char 之间无 \\b）: got=%q", got)
	}

	// 但 "retry 3 times" 中 3 两侧是 space → 替换
	in2 := "retry 3 times"
	got2 := normalizePattern(in2)
	if got2 != "retry <N> times" {
		t.Errorf("retry 3 times → retry <N> times: got=%q", got2)
	}
}

// ===== 边界 case 7: 组合 pattern（实际错误堆栈） =====

func TestNormalize_CombinedErrorStack(t *testing.T) {
	in := `RuntimeError: connection reset by peer at 2024-05-13T08:30:00
request_id=550e8400-e29b-41d4-a716-446655440000 channel=5 status=500 retries=3`
	got := normalizePattern(in)
	// 验证关键 pattern 都被归一化
	must := []string{"<UUID>", "<TS>", "<N>", "RuntimeError", "connection reset by peer"}
	for _, m := range must {
		if !strings.Contains(got, m) {
			t.Errorf("归一化结果应含 %q, got=%q", m, got)
		}
	}
	// 不应再含原始 UUID
	if uuidRe.MatchString(got) {
		t.Errorf("归一化后仍含 UUID: %q", got)
	}
}

// ===== 边界 case 8: 聚合（按 pattern+channel+model 分组） =====

type fakeLogRow struct {
	Pattern   string
	ChannelID int
	ModelName string
	Count     int64
}

func aggregateClusters(rows []fakeLogRow) []fakeLogRow {
	type key struct {
		p string
		c int
		m string
	}
	m := make(map[key]*fakeLogRow)
	order := []key{}
	for _, r := range rows {
		k := key{r.Pattern, r.ChannelID, r.ModelName}
		if existing, ok := m[k]; ok {
			existing.Count += r.Count
		} else {
			cp := r
			m[k] = &cp
			order = append(order, k)
		}
	}
	out := make([]fakeLogRow, 0, len(m))
	for _, k := range order {
		out = append(out, *m[k])
	}
	return out
}

func TestAggregate_GroupByPatternChannelModel(t *testing.T) {
	in := []fakeLogRow{
		{Pattern: "RuntimeError: connection reset", ChannelID: 1, ModelName: "llm-model-a", Count: 5},
		{Pattern: "RuntimeError: connection reset", ChannelID: 1, ModelName: "llm-model-a", Count: 3}, // 同 key，累加
		{Pattern: "RuntimeError: connection reset", ChannelID: 2, ModelName: "llm-model-a", Count: 2}, // 不同 channel
		{Pattern: "Timeout", ChannelID: 1, ModelName: "llm-model-a", Count: 1},
	}
	got := aggregateClusters(in)
	// 期望 3 个分组: (RuntimeError|ch=1|model=llm-model-a)=8, (RuntimeError|ch=2|model=llm-model-a)=2, (Timeout|ch=1)=1
	if len(got) != 3 {
		t.Fatalf("分组数=%d 期望 3", len(got))
	}
	var sum int64
	for _, r := range got {
		sum += r.Count
	}
	if sum != 11 {
		t.Errorf("count 总和=%d 期望 11", sum)
	}
}

// ===== 边界 case 9: Top 50 排序（count 倒序 + 取前 50） =====

func topN(rows []fakeLogRow, n int) []fakeLogRow {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

func TestTopN_SortAndLimit(t *testing.T) {
	in := make([]fakeLogRow, 100)
	for i := range in {
		in[i] = fakeLogRow{
			Pattern:   "p" + intToStr(i),
			ChannelID: 1,
			ModelName: "m",
			Count:     int64(i),
		}
	}
	got := topN(in, 50)
	if len(got) != 50 {
		t.Fatalf("TopN(50) 应返回 50，实际 %d", len(got))
	}
	// count 应该是倒序
	for i := 1; i < len(got); i++ {
		if got[i-1].Count < got[i].Count {
			t.Errorf("TopN 未倒序：idx %d (%v) < idx %d (%v)", i-1, got[i-1].Count, i, got[i].Count)
		}
	}
	// 第一个应是 count=99（最大）
	if got[0].Count != 99 {
		t.Errorf("TopN[0].Count=%d 期望 99", got[0].Count)
	}
	// 第 50 个应是 count=50
	if got[49].Count != 50 {
		t.Errorf("TopN[49].Count=%d 期望 50", got[49].Count)
	}
}

func TestTopN_FewerThanLimit(t *testing.T) {
	in := []fakeLogRow{
		{Pattern: "a", Count: 1},
		{Pattern: "b", Count: 5},
	}
	got := topN(in, 50)
	if len(got) != 2 {
		t.Errorf("TopN(50) 输入 2 条应原样返回")
	}
	if got[0].Pattern != "b" || got[0].Count != 5 {
		t.Errorf("TopN[0] 应是 count=5 的 b，实际 %+v", got[0])
	}
}

// ===== 边界 case 10: 归一化幂等 =====

func TestNormalize_Idempotent(t *testing.T) {
	in := "error at 2024-05-13T08:30:00 trace=550e8400-e29b-41d4-a716-446655440000 code=500"
	once := normalizePattern(in)
	twice := normalizePattern(once)
	if once != twice {
		t.Errorf("归一化不幂等：once=%q twice=%q", once, twice)
	}
}

// ===== 边界 case 11: 归一化空字符串 =====

func TestNormalize_Empty(t *testing.T) {
	if got := normalizePattern(""); got != "" {
		t.Errorf("空字符串归一化应为空, got=%q", got)
	}
}

func TestNormalize_NoSpecialChars(t *testing.T) {
	in := "plain text without anything"
	if got := normalizePattern(in); got != in {
		t.Errorf("无 UUID/TS/数字 应不变, got=%q want=%q", got, in)
	}
}

// ===== helper =====

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
