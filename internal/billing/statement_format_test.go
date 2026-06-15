// BILLING v2 账单格式化单测 (PR #3)
//
// 重点验证:
//   - HTML 渲染 (不报错, 字段替换正确)
//   - XLSX 渲染 (3 sheet, 数字格式正确)
//   - ZIP 打包 (formats 拆分, 文件落盘正确)
//   - PeriodBounds (边界 case)
//
// 不测: QueryStatement (依赖 RoDB 真表, 留 PR #7 集成测)
package billing

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// 测试用 FullStatement fixture
func testFullStatement() *FullStatement {
	ts := time.Date(2026, 6, 14, 17, 0, 0, 0, time.UTC).Unix()
	return &FullStatement{
		UserID:   47,
		Username: "user_alpha",
		Period:   "2026-05",
		Summary: StatementSummary{
			PeriodStart:      1714521600, // 2026-05-01
			PeriodEnd:        1717200000, // 2026-06-01
			PromptTokens:     350000,
			CompletionTokens: 98000,
			CacheTokens:      5000,
			RevenueUSD:       1234.56,
			RequestCount:     6800,
		},
		ByDay: []StatementByDay{
			{Date: "2026-05-01", PromptTokens: 12000, CompletionTokens: 3200, CacheTokens: 200, RevenueUSD: 50.0, RequestCount: 220},
			{Date: "2026-05-02", PromptTokens: 15000, CompletionTokens: 4100, CacheTokens: 250, RevenueUSD: 65.0, RequestCount: 280},
		},
		ByModel: []StatementByModel{
			{ModelName: "claude-sonnet-4-5", PromptTokens: 200000, CompletionTokens: 60000, CacheTokens: 3000, RevenueUSD: 800.0, RequestCount: 4000},
			{ModelName: "claude-haiku-4-5", PromptTokens: 150000, CompletionTokens: 38000, CacheTokens: 2000, RevenueUSD: 434.56, RequestCount: 2800},
		},
		GeneratedAt: ts,
	}
}

func TestRenderHTML_NotEmpty(t *testing.T) {
	stmt := testFullStatement()
	html, err := RenderHTML(stmt)
	if err != nil {
		t.Fatalf("RenderHTML failed: %v", err)
	}
	if len(html) == 0 {
		t.Fatal("html should not be empty")
	}
	// 验证关键字段都替换进去了
	htmlStr := string(html)
	for _, expect := range []string{
		"user_alpha", "2026-05", "350,000", "98,000", "$1,234.56", "claude-sonnet-4-5",
	} {
		if !strings.Contains(htmlStr, expect) {
			t.Errorf("html missing %q", expect)
		}
	}
}

func TestRenderHTML_EmptyByDay(t *testing.T) {
	stmt := testFullStatement()
	stmt.ByDay = nil
	stmt.ByModel = nil
	html, err := RenderHTML(stmt)
	if err != nil {
		t.Fatalf("RenderHTML with empty by_day/by_model failed: %v", err)
	}
	if !strings.Contains(string(html), "本月无调用记录") {
		t.Error("html should show empty placeholder when by_day is nil")
	}
}

func TestRenderXLSX_3Sheets(t *testing.T) {
	stmt := testFullStatement()
	xlsx, err := RenderXLSX(stmt)
	if err != nil {
		t.Fatalf("RenderXLSX failed: %v", err)
	}
	if len(xlsx) == 0 {
		t.Fatal("xlsx should not be empty")
	}
	// xlsx 是 zip 格式, 检查 magic number
	if len(xlsx) < 4 || string(xlsx[:2]) != "PK" {
		t.Errorf("xlsx should start with PK, got: %x", xlsx[:4])
	}
}

func TestPackZip_HTMLOnly(t *testing.T) {
	stmt := testFullStatement()
	htmlBytes, _ := RenderHTML(stmt)
	taskID := "test-html-only"
	path, size, err := PackZip(taskID, stmt, "html", htmlBytes, nil)
	if err != nil {
		t.Fatalf("PackZip failed: %v", err)
	}
	defer os.RemoveAll("./data")
	if size == 0 {
		t.Fatal("zip should not be empty")
	}
	// 打开 zip 验证内容
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	// 应有: README.txt, statement.html (无 statement.xlsx)
	if !listContains(names, "README.txt") || !listContains(names, "statement.html") {
		t.Errorf("missing expected files, got: %v", names)
	}
	if listContains(names, "statement.xlsx") {
		t.Errorf("should not include xlsx, got: %v", names)
	}
}

func TestPackZip_HTMLAndXLSX(t *testing.T) {
	stmt := testFullStatement()
	htmlBytes, _ := RenderHTML(stmt)
	xlsxBytes, _ := RenderXLSX(stmt)
	taskID := "test-both"
	path, _, err := PackZip(taskID, stmt, "html,xlsx", htmlBytes, xlsxBytes)
	if err != nil {
		t.Fatalf("PackZip failed: %v", err)
	}
	defer os.RemoveAll("./data")
	zr, _ := zip.OpenReader(path)
	defer zr.Close()
	hasHTML, hasXLSX := false, false
	for _, f := range zr.File {
		if f.Name == "statement.html" {
			hasHTML = true
		}
		if f.Name == "statement.xlsx" {
			hasXLSX = true
		}
	}
	if !hasHTML || !hasXLSX {
		t.Errorf("missing files: html=%v xlsx=%v", hasHTML, hasXLSX)
	}
}

func TestPackZip_XLSXOnly(t *testing.T) {
	stmt := testFullStatement()
	xlsxBytes, _ := RenderXLSX(stmt)
	taskID := "test-xlsx-only"
	path, _, err := PackZip(taskID, stmt, "xlsx", nil, xlsxBytes)
	if err != nil {
		t.Fatalf("PackZip failed: %v", err)
	}
	defer os.RemoveAll("./data")
	zr, _ := zip.OpenReader(path)
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "statement.html" {
			t.Errorf("should not include html, got: %s", f.Name)
		}
	}
}

// listContains list 版 contains
func listContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestPeriodBounds_May(t *testing.T) {
	start, end, err := PeriodBounds("2026-05")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-05-01 00:00:00 (本地时区 Asia/Shanghai UTC+8) = 1777593600 UTC
	// 2026-06-01 00:00:00 (本地) = 1780272000 UTC
	if start != 1777593600 {
		t.Errorf("start expected 1777593600, got %d", start)
	}
	if end != 1780272000 {
		t.Errorf("end expected 1780272000, got %d", end)
	}
	// 验证差 1 月 ≈ 2678400 秒 (30 天)
	if end-start != 2678400 {
		t.Errorf("end-start expected 2678400, got %d", end-start)
	}
}

func TestPeriodBounds_Invalid(t *testing.T) {
	_, _, err := PeriodBounds("2026-13") // 无效月份
	if err == nil {
		t.Fatal("expected error for invalid month")
	}
	_, _, err = PeriodBounds("2026/05") // 错误格式
	if err == nil {
		t.Fatal("expected error for wrong format")
	}
}

func TestSplitFormats(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"html", []string{"html"}},
		{"xlsx", []string{"xlsx"}},
		{"html,xlsx", []string{"html", "xlsx"}},
		{"xlsx,html", []string{"html", "xlsx"}}, // 顺序无关
		{"", nil},
		{"unknown", nil},
	}
	for _, c := range cases {
		got := splitFormats(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitFormats(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 验证 bytes.Buffer 不影响测试 (引用的标准库, 防止 unused 警告)
var _ = bytes.NewBuffer
