// BILLING v3 上游对账生成器: HTML + XLSX + ZIP (PR #3 / 7, 2026-06-14)
//
// 复用 v2 framework (internal/billing/statement_format.go):
//   - findTemplatePath / splitFormats / writeZipEntry
//   - withThousandSep / withThousandSepFloat
//   - 模板路径: internal/billing/templates/upstream.html
//   - 复用 PackZip 路径 /data/billing-exports/{taskID}.zip
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

// RenderUpstreamHTML 渲染 UpstreamStatement 为 HTML 字节
//
// 模板在 internal/billing/templates/upstream.html
func RenderUpstreamHTML(stmt *UpstreamStatement, generatedAt int64) ([]byte, error) {
	tmplPath := findTemplatePath("upstream.html")
	tmpl, err := template.New("upstream.html").Funcs(template.FuncMap{
		"safe":           func(s string) template.HTML { return template.HTML(s) },
		"thousands":      func(n int64) string { return withThousandSep(n) },
		"thousandsFloat": func(f float64) string { return withThousandSepFloat(f) },
		"pct": func(f float64) string {
			// 利润率百分比, 2 位小数 + %
			return withThousandSepFloat(f*100) + "%"
		},
	}).ParseFiles(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", tmplPath, err)
	}

	// 包装数据 (模板用 .GeneratedAtRFC3339 + period "YYYY-MM" 字符串)
	data := struct {
		*UpstreamStatement
		GeneratedAtRFC3339 string
		PeriodStr          string
	}{
		UpstreamStatement:  stmt,
		GeneratedAtRFC3339: time.Unix(generatedAt, 0).Format("2006-01-02 15:04:05 MST"),
		PeriodStr: fmt.Sprintf("%s ~ %s",
			time.Unix(stmt.PeriodStart, 0).Format("2006-01-02"),
			time.Unix(stmt.PeriodEnd, 0).Format("2006-01-02")),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderUpstreamXLSX 渲染 UpstreamStatement 为 XLSX 字节
//
// 3 sheet:
//  1. 汇总 (vendor + 4 token + cost + revenue + profit + margin)
//  2. 按渠道 (channel 维度的 4 token + cost + revenue)
//  3. 按模型 (model 维度的 4 token + cost + revenue)
//  4. 按天   (date 维度的 4 token + cost + revenue)
func RenderUpstreamXLSX(stmt *UpstreamStatement) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

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
	summaryHeaders := []string{
		"上游 (vendor)", "周期", "调用次数",
		"输入 tokens", "输出 tokens", "缓存 tokens (合计)",
		"累计成本 (USD)", "客户消耗 (USD)", "毛利 (USD)", "利润率",
	}
	for i, h := range summaryHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summarySheet, cell, h)
		f.SetCellStyle(summarySheet, cell, cell, headerStyle)
	}
	// 汇总行 (从 ByChannel 聚合)
	var totalReq, totalPrompt, totalComp, totalCache int64
	for _, c := range stmt.ByChannel {
		totalReq += c.RequestCount
		totalPrompt += c.PromptTokens
		totalComp += c.CompletionTokens
		totalCache += c.CacheTokens
	}
	row := []interface{}{
		fmt.Sprintf("%s (%s)", stmt.VendorName, stmt.VendorCode),
		fmt.Sprintf("%s ~ %s",
			time.Unix(stmt.PeriodStart, 0).Format("2006-01-02"),
			time.Unix(stmt.PeriodEnd, 0).Format("2006-01-02")),
		totalReq, totalPrompt, totalComp, totalCache,
		stmt.TotalCost, stmt.TotalRevenue, stmt.TotalProfit, stmt.ProfitRate,
	}
	for i, v := range row {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue(summarySheet, cell, v)
	}

	// ===== Sheet 2: 按渠道 =====
	chSheet := "按渠道"
	f.NewSheet(chSheet)
	chHeaders := []string{
		"渠道 ID", "渠道名", "调用次数",
		"输入 tokens", "输出 tokens", "缓存 tokens",
		"累计成本 (USD)", "客户消耗 (USD)",
	}
	for i, h := range chHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(chSheet, cell, h)
		f.SetCellStyle(chSheet, cell, cell, headerStyle)
	}
	for i, c := range stmt.ByChannel {
		row := []interface{}{c.ChannelID, c.ChannelName, c.RequestCount,
			c.PromptTokens, c.CompletionTokens, c.CacheTokens,
			c.TotalCost, c.TotalRevenue}
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(chSheet, cell, v)
		}
	}
	// 合计行
	totalRow := len(stmt.ByChannel) + 2
	for i, h := range chHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow)
		f.SetCellValue(chSheet, cell, h)
		f.SetCellStyle(chSheet, cell, cell, totalStyle)
	}
	totalValues := []interface{}{"合计", "", totalReq, totalPrompt, totalComp, totalCache,
		stmt.TotalCost, stmt.TotalRevenue}
	for i, v := range totalValues {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow+1)
		f.SetCellValue(chSheet, cell, v)
		f.SetCellStyle(chSheet, cell, cell, totalStyle)
	}

	// ===== Sheet 3: 按模型 =====
	modelSheet := "按模型"
	f.NewSheet(modelSheet)
	modelHeaders := []string{
		"模型", "调用次数",
		"输入 tokens", "输出 tokens", "缓存 tokens",
		"累计成本 (USD)", "客户消耗 (USD)",
	}
	for i, h := range modelHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(modelSheet, cell, h)
		f.SetCellStyle(modelSheet, cell, cell, headerStyle)
	}
	for i, m := range stmt.ByModel {
		row := []interface{}{m.ModelName, m.RequestCount,
			m.PromptTokens, m.CompletionTokens, m.CacheTokens,
			m.TotalCost, m.TotalRevenue}
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
	totalValues = []interface{}{"合计", totalReq, totalPrompt, totalComp, totalCache,
		stmt.TotalCost, stmt.TotalRevenue}
	for i, v := range totalValues {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow+1)
		f.SetCellValue(modelSheet, cell, v)
		f.SetCellStyle(modelSheet, cell, cell, totalStyle)
	}

	// ===== Sheet 4: 按天 =====
	dateSheet := "按天"
	f.NewSheet(dateSheet)
	dateHeaders := []string{
		"日期", "调用次数",
		"输入 tokens", "输出 tokens", "缓存 tokens",
		"累计成本 (USD)", "客户消耗 (USD)",
	}
	for i, h := range dateHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(dateSheet, cell, h)
		f.SetCellStyle(dateSheet, cell, cell, headerStyle)
	}
	for i, d := range stmt.ByDate {
		row := []interface{}{d.Date, d.RequestCount,
			d.PromptTokens, d.CompletionTokens, d.CacheTokens,
			d.TotalCost, d.TotalRevenue}
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(dateSheet, cell, v)
		}
	}
	totalRow = len(stmt.ByDate) + 2
	for i, h := range dateHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow)
		f.SetCellValue(dateSheet, cell, h)
		f.SetCellStyle(dateSheet, cell, cell, totalStyle)
	}
	totalValues = []interface{}{"合计", totalReq, totalPrompt, totalComp, totalCache,
		stmt.TotalCost, stmt.TotalRevenue}
	for i, v := range totalValues {
		cell, _ := excelize.CoordinatesToCellName(i+1, totalRow+1)
		f.SetCellValue(dateSheet, cell, v)
		f.SetCellStyle(dateSheet, cell, cell, totalStyle)
	}

	// 列宽
	for _, sheet := range []string{summarySheet, chSheet, modelSheet, dateSheet} {
		f.SetColWidth(sheet, "A", "A", 28)
		f.SetColWidth(sheet, "B", "J", 18)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

// PackUpstreamZip 打包 HTML + XLSX (可选) 到 /data/billing-exports/{taskID}.zip
//
// 入参: formats = "html" / "xlsx" / "html,xlsx"
// 返: zip 路径, 文件大小, error
func PackUpstreamZip(taskID string, stmt *UpstreamStatement, generatedAt int64, formats string,
	htmlBytes, xlsxBytes []byte) (string, int64, error) {

	dir := "/data/billing-exports"
	// 兜底: 开发机没 /data, 写到 ./data/billing-exports
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

	// README.txt
	readme := fmt.Sprintf(
		"upstream 上游对账单\n"+
			"上游: %s (%s)\n"+
			"周期: %s ~ %s\n"+
			"生成时间: %s\n"+
			"包含文件:\n",
		stmt.VendorName, stmt.VendorCode,
		time.Unix(stmt.PeriodStart, 0).Format("2006-01-02"),
		time.Unix(stmt.PeriodEnd, 0).Format("2006-01-02"),
		time.Unix(generatedAt, 0).Format("2006-01-02 15:04:05 MST"),
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
			readme += "  - statement.xlsx (Excel 4 sheet, 财务处理用)\n"
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
