// BILLING v2 账单格式化: HTML + XLSX + ZIP (PR #3 / 8, 2026-06-14 RFC)
//
// 调用链: QueryStatement → RenderHTML / RenderXLSX → PackZip
package billing

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/xuri/excelize/v2"
)

// RenderHTML 渲染 FullStatement 为 HTML 字节
//
// 模板在 internal/billing/templates/statement.html
// 自动 escape 字段值防 XSS (text/template 而非 html/template)
func RenderHTML(stmt *FullStatement) ([]byte, error) {
	// 路径基于 cwd (开发时) 或 . (二进制部署时相对路径)
	tmplPath := findTemplatePath("statement.html")
	tmpl, err := template.New("statement.html").Funcs(template.FuncMap{
		"safe":           func(s string) template.HTML { return template.HTML(s) },
		"thousands":      func(n int64) string { return withThousandSep(n) },
		"thousandsFloat": func(f float64) string { return withThousandSepFloat(f) },
	}).ParseFiles(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", tmplPath, err)
	}

	// 数据包装 (模板需要 .GeneratedAtRFC3339 字段)
	data := struct {
		*FullStatement
		GeneratedAtRFC3339 string
	}{
		FullStatement:      stmt,
		GeneratedAtRFC3339: time.Unix(stmt.GeneratedAt, 0).Format("2006-01-02 15:04:05 MST"),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// findTemplatePath 找模板路径 (兼容开发 + 部署)
//
// 部署位置: /app/internal/billing/templates/ (Dockerfile COPY 进去的)
// 开发位置: ./internal/billing/templates/ (从 cwd)
// 旧版兼容: 顶层 templates/ (v1 exporter 时代)
func findTemplatePath(name string) string {
	candidates := []string{
		"internal/billing/templates/" + name,
		"./internal/billing/templates/" + name,
		"/app/internal/billing/templates/" + name,
		"templates/" + name,
		"./templates/" + name,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0] // 失败也返第一个, 让 ParseFiles 报错
}

// RenderXLSX 渲染 FullStatement 为 XLSX 字节
//
// 3 sheet:
//  1. 汇总 (4 token + USD)
//  2. 按天 (30 行)
//  3. 按模型 (N 个 model)
func RenderXLSX(stmt *FullStatement) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	// 表头样式
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#1677FF"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	totalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "CF1322"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFF7E6"}, Pattern: 1},
	})

	// ===== Sheet 1: 汇总 =====
	summarySheet := "汇总"
	f.SetSheetName("Sheet1", summarySheet)
	headers := []string{"客户", "周期", "调用次数", "输入 tokens", "输出 tokens", "缓存 tokens", "合计金额 (USD)"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summarySheet, cell, h)
		f.SetCellStyle(summarySheet, cell, cell, headerStyle)
	}
	row := []interface{}{
		stmt.Username, stmt.Period, stmt.Summary.RequestCount,
		stmt.Summary.PromptTokens, stmt.Summary.CompletionTokens,
		stmt.Summary.CacheTokens,
		stmt.Summary.RevenueUSD,
	}
	for i, v := range row {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue(summarySheet, cell, v)
	}

	// ===== Sheet 2: 按天 =====
	daySheet := "按天"
	f.NewSheet(daySheet)
	dayHeaders := []string{"日期", "调用次数", "输入 tokens", "输出 tokens", "缓存 tokens", "金额 (USD)"}
	for i, h := range dayHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(daySheet, cell, h)
		f.SetCellStyle(daySheet, cell, cell, headerStyle)
	}
	for i, d := range stmt.ByDay {
		row := []interface{}{d.Date, d.RequestCount, d.PromptTokens, d.CompletionTokens, d.CacheTokens, d.RevenueUSD}
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(daySheet, cell, v)
		}
	}
	// 合计行
	totalRow := len(stmt.ByDay) + 2
	for i, h := range dayHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow)
		f.SetCellValue(daySheet, cell, h)
		f.SetCellStyle(daySheet, cell, cell, totalStyle)
	}
	totalValues := []interface{}{"合计", stmt.Summary.RequestCount, stmt.Summary.PromptTokens, stmt.Summary.CompletionTokens, stmt.Summary.CacheTokens, stmt.Summary.RevenueUSD}
	for i, v := range totalValues {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow+1)
		f.SetCellValue(daySheet, cell, v)
		f.SetCellStyle(daySheet, cell, cell, totalStyle)
	}

	// ===== Sheet 3: 按模型 =====
	modelSheet := "按模型"
	f.NewSheet(modelSheet)
	modelHeaders := []string{"模型", "调用次数", "输入 tokens", "输出 tokens", "缓存 tokens", "金额 (USD)"}
	for i, h := range modelHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(modelSheet, cell, h)
		f.SetCellStyle(modelSheet, cell, cell, headerStyle)
	}
	for i, m := range stmt.ByModel {
		row := []interface{}{m.ModelName, m.RequestCount, m.PromptTokens, m.CompletionTokens, m.CacheTokens, m.RevenueUSD}
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(modelSheet, cell, v)
		}
	}
	totalRow = len(stmt.ByModel) + 2
	for i, h := range modelHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow)
		f.SetCellValue(modelSheet, cell, h)
		f.SetCellStyle(modelSheet, cell, cell, totalStyle)
	}
	totalValues = []interface{}{"合计", stmt.Summary.RequestCount, stmt.Summary.PromptTokens, stmt.Summary.CompletionTokens, stmt.Summary.CacheTokens, stmt.Summary.RevenueUSD}
	for i, v := range totalValues {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow+1)
		f.SetCellValue(modelSheet, cell, v)
		f.SetCellStyle(modelSheet, cell, cell, totalStyle)
	}

	// 列宽自适应
	for _, sheet := range []string{summarySheet, daySheet, modelSheet} {
		f.SetColWidth(sheet, "A", "A", 32)
		f.SetColWidth(sheet, "B", "G", 18)
	}

	// 写 buffer
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

// PackZip 把 HTML + XLSX (可选) 打包到 /data/billing-exports/{taskID}.zip
//
// 输入: formats = "html" / "xlsx" / "html,xlsx"
// 返回: 写入的 zip 路径, 文件大小, error
func PackZip(taskID string, stmt *FullStatement, formats string, htmlBytes, xlsxBytes []byte) (string, int64, error) {
	dir := "/data/billing-exports"
	// 兜底: 如果 /data 不存在 (开发机), 写到当前目录
	if _, err := os.Stat("/data"); os.IsNotExist(err) {
		dir = "./data/billing-exports"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, taskID+".zip")
	f, err := os.Create(path)
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// README.txt (说明包含的文件)
	readme := fmt.Sprintf(
		"upstream 客户对账单\n"+
			"客户: %s (ID: %d)\n"+
			"周期: %s\n"+
			"生成时间: %s\n"+
			"包含文件:\n",
		stmt.Username, stmt.UserID, stmt.Period,
		time.Unix(stmt.GeneratedAt, 0).Format("2006-01-02 15:04:05 MST"),
	)
	wantHTML := false
	wantXLSX := false
	for _, f := range splitFormats(formats) {
		switch f {
		case "html":
			wantHTML = true
			readme += "  - statement.html (人类可读, 浏览器打开)\n"
		case "xlsx":
			wantXLSX = true
			readme += "  - statement.xlsx (Excel 多 sheet, 财务处理用)\n"
		}
	}
	if err := writeZipEntry(zw, "README.txt", []byte(readme)); err != nil {
		return "", 0, err
	}
	if wantHTML && len(htmlBytes) > 0 {
		if err := writeZipEntry(zw, "statement.html", htmlBytes); err != nil {
			return "", 0, err
		}
	}
	if wantXLSX && len(xlsxBytes) > 0 {
		if err := writeZipEntry(zw, "statement.xlsx", xlsxBytes); err != nil {
			return "", 0, err
		}
	}

	if err := zw.Close(); err != nil {
		return "", 0, fmt.Errorf("close zip: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return path, info.Size(), nil
}

func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	fw, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("zip create %s: %w", name, err)
	}
	if _, err := fw.Write(data); err != nil {
		return fmt.Errorf("zip write %s: %w", name, err)
	}
	return nil
}

func splitFormats(formats string) []string {
	var out []string
	for _, f := range []string{"html", "xlsx"} {
		if contains(formats, f) {
			out = append(out, f)
		}
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 千分位格式化 (不依赖 golang.org/x/text, 纯 stdlib)
func withThousandSep(n int64) string {
	negative := n < 0
	if negative {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	// 从右往左每 3 位插逗号
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}

func withThousandSepFloat(f float64) string {
	// 简化: 整数部分千分位 + 小数部分保留 2 位
	// 用 math.Round 避免 1234.56 → 1234.55 浮点精度问题
	negative := f < 0
	if negative {
		f = -f
	}
	whole := int64(f)
	frac := int64((f-float64(whole))*100 + 0.5) // 0.5 修正浮点
	if frac >= 100 {                            // 进位
		whole++
		frac -= 100
	}
	result := withThousandSep(whole) + fmt.Sprintf(".%02d", frac)
	if negative {
		return "-" + result
	}
	return result
}
