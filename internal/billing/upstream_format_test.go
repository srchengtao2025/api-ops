// PR #3 单测 - 上游对账生成器 (BILLING v3)
package billing

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// sampleUpstreamStatement 1 个真实场景的测试数据
// provider_alpha 5 月 2026-05-01 ~ 2026-05-31
// 假设:
//   - 2 个渠道 (ch-2, ch-7) 都是 0.24 折扣
//   - 1 个模型 llm-model-a
//   - 30 天每天 100 调用
func sampleUpstreamStatement() *UpstreamStatement {
	stmt := &UpstreamStatement{
		VendorCode:  "provider_alpha",
		VendorName:  "DataEyes",
		PeriodStart: 1746057600, // 2026-05-01 00:00:00 UTC
		PeriodEnd:   1748735999, // 2026-05-31 23:59:59 UTC
	}

	// 2 个渠道
	stmt.ByChannel = []UpstreamByChannel{
		{ChannelID: 2, ChannelName: "ch-2-provider_alpha",
			RequestCount: 1800, PromptTokens: 3600000, CompletionTokens: 1800000, CacheTokens: 900000,
			TotalCost: 21.6, TotalRevenue: 90.0},
		{ChannelID: 7, ChannelName: "ch-7-provider_alpha",
			RequestCount: 1200, PromptTokens: 2400000, CompletionTokens: 1200000, CacheTokens: 600000,
			TotalCost: 14.4, TotalRevenue: 60.0},
	}
	stmt.TotalRequestCount = 3000
	stmt.TotalCost = 36.0
	stmt.TotalRevenue = 150.0
	stmt.TotalProfit = 114.0
	stmt.ProfitRate = 3.167

	// 1 个模型
	stmt.ByModel = []UpstreamByModel{
		{ModelName: "llm-model-a",
			RequestCount: 3000, PromptTokens: 6000000, CompletionTokens: 3000000, CacheTokens: 1500000,
			TotalCost: 36.0, TotalRevenue: 150.0},
	}

	// 30 天 (生成示意)
	stmt.ByDate = make([]UpstreamByDate, 0, 30)
	for i := 0; i < 30; i++ {
		stmt.ByDate = append(stmt.ByDate, UpstreamByDate{
			Date:         time.Unix(1746057600+int64(i*86400), 0).UTC().Format("2006-01-02"),
			RequestCount: 100,
			PromptTokens: 200000, CompletionTokens: 100000, CacheTokens: 50000,
			TotalCost: 1.2, TotalRevenue: 5.0,
		})
	}
	return stmt
}

// TestRenderUpstreamHTML_NotEmpty
func TestRenderUpstreamHTML_NotEmpty(t *testing.T) {
	stmt := sampleUpstreamStatement()
	htmlBytes, err := RenderUpstreamHTML(stmt, time.Now().Unix())
	if err != nil {
		t.Fatalf("RenderUpstreamHTML: %v", err)
	}
	if len(htmlBytes) == 0 {
		t.Fatal("html bytes empty")
	}
	html := string(htmlBytes)
	// 关键字段校验
	wantSub := []string{
		"upstream 上游对账单",
		"DataEyes",
		"provider_alpha",
		"按渠道拆分",
		"按模型拆分",
		"按天拆分",
		"ch-2-provider_alpha",
		"ch-7-provider_alpha",
		"llm-model-a",
		"$150.00", // total revenue
		"$36.00",  // total cost
		"$114.00", // total profit
	}
	for _, sub := range wantSub {
		if !strings.Contains(html, sub) {
			t.Errorf("html missing %q", sub)
		}
	}
}

// TestRenderUpstreamHTML_EmptyByDay
func TestRenderUpstreamHTML_EmptyByDay(t *testing.T) {
	stmt := &UpstreamStatement{
		VendorCode: "test", VendorName: "Test",
		PeriodStart: 1746057600, PeriodEnd: 1748735999,
	}
	htmlBytes, err := RenderUpstreamHTML(stmt, time.Now().Unix())
	if err != nil {
		t.Fatalf("RenderUpstreamHTML: %v", err)
	}
	html := string(htmlBytes)
	if !strings.Contains(html, "Test") {
		t.Error("html missing vendor name")
	}
	// 空 ByDate 时按天拆分表头不应出现
	if strings.Contains(html, "按天拆分") {
		t.Error("html should not show ByDate section when empty")
	}
}

// TestRenderUpstreamXLSX_4Sheets
func TestRenderUpstreamXLSX_4Sheets(t *testing.T) {
	stmt := sampleUpstreamStatement()
	xlsxBytes, err := RenderUpstreamXLSX(stmt)
	if err != nil {
		t.Fatalf("RenderUpstreamXLSX: %v", err)
	}
	if len(xlsxBytes) == 0 {
		t.Fatal("xlsx bytes empty")
	}

	// XLSX 是 zip, 解开验证 4 sheet
	zr, err := zip.NewReader(bytes.NewReader(xlsxBytes), int64(len(xlsxBytes)))
	if err != nil {
		t.Fatalf("xlsx zip reader: %v", err)
	}
	sheets := []string{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/") {
			sheets = append(sheets, f.Name)
		}
	}
	wantSheets := []string{"sheet1.xml", "sheet2.xml", "sheet3.xml", "sheet4.xml"}
	if len(sheets) != len(wantSheets) {
		t.Errorf("xlsx sheets = %d, want 4 (got %v)", len(sheets), sheets)
	}
}

// TestPackUpstreamZip_HTMLAndXLSX
func TestPackUpstreamZip_HTMLAndXLSX(t *testing.T) {
	stmt := sampleUpstreamStatement()
	htmlBytes, _ := RenderUpstreamHTML(stmt, time.Now().Unix())
	xlsxBytes, _ := RenderUpstreamXLSX(stmt)

	// 写到 /tmp 避免污染 /data
	taskID := "test-upstream-pack-1"
	t.Cleanup(func() {
		_ = os.Remove("/tmp/data/billing-exports/" + taskID + ".zip")
	})
	defer os.RemoveAll("/tmp/data") // 清测试残留

	// 临时改 dir 路径: 写之前覆盖 dir
	// 这里直接测 PackUpstreamZip 写到 /data, 如果没 /data 写 ./data
	if _, err := os.Stat("/data"); os.IsNotExist(err) {
		// 写到 ./data, 测试完后清
		t.Cleanup(func() { os.RemoveAll("./data") })
	}

	path, size, err := PackUpstreamZip(taskID, stmt, time.Now().Unix(), "html,xlsx", htmlBytes, xlsxBytes)
	if err != nil {
		t.Fatalf("PackUpstreamZip: %v", err)
	}
	if size <= 0 {
		t.Errorf("zip size = %d, want > 0", size)
	}

	// 验证 zip 包含 3 文件 (README + html + xlsx)
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{"README.txt", "statement.html", "statement.xlsx"}
	if len(names) != 3 {
		t.Errorf("zip files = %d, want 3 (got %v)", len(names), names)
	}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("zip missing %s", w)
		}
	}
}

// TestPackUpstreamZip_HTMLOnly
func TestPackUpstreamZip_HTMLOnly(t *testing.T) {
	stmt := sampleUpstreamStatement()
	htmlBytes, _ := RenderUpstreamHTML(stmt, time.Now().Unix())

	taskID := "test-upstream-pack-2"
	if _, err := os.Stat("/data"); os.IsNotExist(err) {
		t.Cleanup(func() { os.RemoveAll("./data") })
	} else {
		t.Cleanup(func() { os.Remove("/data/billing-exports/" + taskID + ".zip") })
	}

	path, _, err := PackUpstreamZip(taskID, stmt, time.Now().Unix(), "html", htmlBytes, nil)
	if err != nil {
		t.Fatalf("PackUpstreamZip: %v", err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	if len(zr.File) != 2 { // README + statement.html
		t.Errorf("zip files = %d, want 2", len(zr.File))
	}
}
