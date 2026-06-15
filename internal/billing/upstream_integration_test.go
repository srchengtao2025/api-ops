// PR #6 集成测 - 上游对账端到端 (BILLING v3)
//
// 测 RenderUpstreamHTML + RenderUpstreamXLSX + PackUpstreamZip 全链路
// (CalcUpstreamStatement 端到端在 PR #7 公网验证覆盖, 避免加 gorm.io/driver/sqlite 依赖)
package billing

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// TestIntegration_UpstreamFormatFullChain 数据层 + 渲染 + 打包全链路
//
// 跳过 CalcUpstreamStatement (需 RoDB mock), 复用 PR #2 测过的 CalcLogCost
// + sampleUpstreamStatement (PR #3) 模拟 CalcUpstreamStatement 输出
func TestIntegration_UpstreamFormatFullChain(t *testing.T) {
	stmt := sampleUpstreamStatement()

	// ==== 1. RenderUpstreamHTML ====
	htmlBytes, err := RenderUpstreamHTML(stmt, time.Now().Unix())
	if err != nil {
		t.Fatalf("RenderUpstreamHTML: %v", err)
	}
	html := string(htmlBytes)
	// 校验关键字段
	for _, want := range []string{
		"provider_alpha", "DataEyes",
		"ch-2-provider_alpha", "ch-7-provider_alpha", "llm-model-a",
		"按渠道拆分", "按模型拆分", "按天拆分",
		"缓存 tokens", "累计成本 (USD)", "客户消耗 (USD)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}

	// ==== 2. RenderUpstreamXLSX ====
	xlsxBytes, err := RenderUpstreamXLSX(stmt)
	if err != nil {
		t.Fatalf("RenderUpstreamXLSX: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(xlsxBytes), int64(len(xlsxBytes)))
	if err != nil {
		t.Fatalf("xlsx zip: %v", err)
	}
	sheetCount := 0
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/") {
			sheetCount++
		}
	}
	if sheetCount != 4 {
		t.Errorf("xlsx sheets = %d, want 4", sheetCount)
	}

	// ==== 3. PackUpstreamZip ====
	taskID := "test-integration-v3"
	if _, err := os.Stat("/data"); os.IsNotExist(err) {
		t.Cleanup(func() { os.RemoveAll("./data") })
	} else {
		t.Cleanup(func() { os.Remove("/data/billing-exports/" + taskID + ".zip") })
	}

	zipPath, zipSize, err := PackUpstreamZip(taskID, stmt, time.Now().Unix(), "html,xlsx", htmlBytes, xlsxBytes)
	if err != nil {
		t.Fatalf("PackUpstreamZip: %v", err)
	}
	if zipSize <= 0 {
		t.Errorf("zip size = %d, want > 0", zipSize)
	}

	// 验证 ZIP 3 文件
	zr2, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr2.Close()
	names := []string{}
	for _, f := range zr2.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"README.txt", "statement.html", "statement.xlsx"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("zip missing %s", want)
		}
	}

	// ==== 4. 业务公式校验 ====
	// sampleUpstreamStatement: 3000 调用, revenue=$150, cost=$36, profit=$114, margin=3.167
	if stmt.TotalRequestCount != 3000 {
		t.Errorf("TotalRequestCount = %d, want 3000", stmt.TotalRequestCount)
	}
	if stmt.TotalRevenue != 150.0 {
		t.Errorf("TotalRevenue = %f, want 150.0", stmt.TotalRevenue)
	}
	if stmt.TotalCost != 36.0 {
		t.Errorf("TotalCost = %f, want 36.0", stmt.TotalCost)
	}
	if stmt.TotalProfit != 114.0 {
		t.Errorf("TotalProfit = %f, want 114.0", stmt.TotalProfit)
	}
	if stmt.ProfitRate < 3.16 || stmt.ProfitRate > 3.18 {
		t.Errorf("ProfitRate = %f, want ~3.167 (316.7%%)", stmt.ProfitRate)
	}

	t.Logf("✓ 端到端成功: provider_alpha 5 月 3000 调用, $%.2f 消耗, $%.2f 成本, $%.2f 利润 (margin=%.1f%%)",
		stmt.TotalRevenue, stmt.TotalCost, stmt.TotalProfit, stmt.ProfitRate*100)
}

// TestIntegration_UpstreamPackFormats HTML/XLSX/两者/空 4 case
func TestIntegration_UpstreamPackFormats(t *testing.T) {
	stmt := sampleUpstreamStatement()
	htmlBytes, _ := RenderUpstreamHTML(stmt, time.Now().Unix())
	xlsxBytes, _ := RenderUpstreamXLSX(stmt)

	cases := []struct {
		name      string
		formats   string
		wantFiles int
		skipHTML  bool
		skipXLSX  bool
	}{
		{name: "HTML only", formats: "html", wantFiles: 2}, // README + html
		{name: "XLSX only", formats: "xlsx", wantFiles: 2}, // README + xlsx
		{name: "Both", formats: "html,xlsx", wantFiles: 3}, // README + html + xlsx
		{name: "Empty", formats: "", wantFiles: 1},         // only README
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			taskID := "test-fmt-" + tt.name
			if _, err := os.Stat("/data"); os.IsNotExist(err) {
				t.Cleanup(func() { os.RemoveAll("./data") })
			} else {
				t.Cleanup(func() { os.Remove("/data/billing-exports/" + taskID + ".zip") })
			}

			_, _, err := PackUpstreamZip(taskID, stmt, time.Now().Unix(), tt.formats, htmlBytes, xlsxBytes)
			if err != nil {
				t.Fatalf("PackUpstreamZip: %v", err)
			}
			// 验证文件数
			dir := "/data/billing-exports"
			if _, err := os.Stat("/data"); os.IsNotExist(err) {
				dir = "./data/billing-exports"
			}
			zr, err := zip.OpenReader(dir + "/" + taskID + ".zip")
			if err != nil {
				t.Fatalf("open zip: %v", err)
			}
			defer zr.Close()
			if len(zr.File) != tt.wantFiles {
				t.Errorf("%s: zip files = %d, want %d", tt.name, len(zr.File), tt.wantFiles)
			}
		})
	}
}
