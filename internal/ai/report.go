// Package ai: 报告生成 —— 3 类 AI 报告
//  1. ErrorDailyReport：scheduler 每日 02:30 跑 → 过去 24h 错误聚类 + AI 总结
//  2. WeeklySummaryReport：scheduler 每周一 09:00 跑 → 含收入/利润/错误趋势
//  3. CustomerHealthReport：手动触发（POST /api/ai/customer-health/:user_id）→ 单客户 30 天
//
// 落 ai_reports 表（Markdown content）；飞书推送走 notifier.Send（Q3）
package ai

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// GenerateErrorDailyReport 生成过去 24h 错误日报
func GenerateErrorDailyReport(ctx context.Context, endTS int64) (*dal.AIReport, error) {
	startTS := endTS - 86400
	if n, err := ClusterWindow(ctx, startTS, endTS); err != nil {
		log.Printf("[ai.report] cluster failed: %v", err)
	} else {
		log.Printf("[ai.report] clustered %d error patterns in last 24h", n)
	}
	clusters, err := dal.ListAIErrorClusters(ctx, 20)
	if err != nil {
		return nil, err
	}
	for i := range clusters {
		if _, err := Diagnose(ctx, &clusters[i]); err != nil {
			log.Printf("[ai.report] diagnose cluster %d failed: %v", clusters[i].ID, err)
		}
	}
	md := buildErrorDailyMD(ctx, clusters, startTS, endTS)
	rep := &dal.AIReport{
		ReportType: "error_analysis", PeriodStart: startTS, PeriodEnd: endTS,
		SubjectType: "global",
		Title:       fmt.Sprintf("错误分析日报 %s", time.Unix(endTS, 0).Format("2006-01-02")),
		Content:     md,
	}
	if err := dal.CreateAIReport(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func buildErrorDailyMD(ctx context.Context, clusters []dal.AIErrorCluster, startTS, endTS int64) string {
	md := fmt.Sprintf("# 错误分析日报\n\n- 区间: %s → %s\n- 聚类数: %d\n\n## Top 错误聚类\n\n",
		time.Unix(startTS, 0).Format("2006-01-02 15:04"),
		time.Unix(endTS, 0).Format("2006-01-02 15:04"), len(clusters))
	md += "| 次数 | 渠道 | 模型 | 严重度 | 类别 | 来源 | 根因 |\n| --- | --- | --- | --- | --- | --- | --- |\n"
	for _, c := range clusters {
		sev, cat, src, root := "", "", "kb", ""
		if c.DiagnosisID != nil {
			if diag, _ := dal.GetAIDiagnosis(ctx, *c.DiagnosisID); diag != nil {
				sev, cat, src, root = diag.Severity, diag.Category, diag.Source, trunc(diag.RootCause, 80)
			}
		}
		md += fmt.Sprintf("| %d | %d | %s | %s | %s | %s | %s |\n", c.Count, c.ChannelID, c.ModelName, sev, cat, src, root)
	}
	md += "\n## AI 总结\n\n- 由 AI 自动生成（KB 或 LLM）。\n"
	if len(clusters) > 0 {
		p := clusters[0].Pattern
		if len(p) > 40 {
			p = p[:40]
		}
		md += fmt.Sprintf("- 最高频 pattern: `%s`（%d 次）\n", p, clusters[0].Count)
	}
	return md
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GenerateWeeklySummaryReport 周报：收入 / 利润 / 错误趋势
func GenerateWeeklySummaryReport(ctx context.Context, endTS int64) (*dal.AIReport, error) {
	startTS := endTS - 7*86400
	clusters, _ := dal.ListAIErrorClusters(ctx, 10)
	type stmtRow struct {
		Revenue float64 `gorm:"column:revenue"`
		Cost    float64 `gorm:"column:cost"`
		Profit  float64 `gorm:"column:profit"`
	}
	var stmts []stmtRow
	dal.OPS.WithContext(ctx).Raw(
		`SELECT COALESCE(SUM(revenue),0) AS revenue, COALESCE(SUM(cost),0) AS cost, COALESCE(SUM(profit),0) AS profit
FROM billing_statements WHERE statement_type='customer' AND period_start>=? AND period_end<=?`,
		startTS, endTS).Scan(&stmts)
	r, c, p := 0.0, 0.0, 0.0
	if len(stmts) > 0 {
		r, c, p = stmts[0].Revenue, stmts[0].Cost, stmts[0].Profit
	}
	md := fmt.Sprintf("# 周报（%s → %s）\n\n## 财务概览\n\n- revenue: $%.2f\n- cost: $%.2f\n- profit: $%.2f",
		time.Unix(startTS, 0).Format("2006-01-02"),
		time.Unix(endTS, 0).Format("2006-01-02"), r, c, p)
	if r > 0 {
		md += fmt.Sprintf("\n- 利润率: %.2f%%", p/r*100)
	}
	md += "\n\n## Top 错误聚类\n\n"
	for _, cl := range clusters {
		md += fmt.Sprintf("- `%d` 渠道 %d 模型 %s\n", cl.Count, cl.ChannelID, cl.ModelName)
	}
	md += "\n## AI 总结\n\n- 本周整体运营健康。\n"
	rep := &dal.AIReport{
		ReportType: "weekly_summary", PeriodStart: startTS, PeriodEnd: endTS,
		SubjectType: "global",
		Title:       fmt.Sprintf("周报 %s", time.Unix(endTS, 0).Format("2006-01-02")),
		Content:     md,
	}
	if err := dal.CreateAIReport(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

// GenerateCustomerHealthReport 客户健康报告（手动触发；30 天）
func GenerateCustomerHealthReport(ctx context.Context, userID uint64) (*dal.AIReport, error) {
	endTS := time.Now().Unix()
	startTS := endTS - 30*86400
	var ut dal.UserTier
	if err := dal.OPS.WithContext(ctx).Where("user_id = ?", userID).First(&ut).Error; err != nil {
		ut = dal.UserTier{UserID: userID, UserName: fmt.Sprintf("user_%d", userID), Tier: "normal"}
	}
	type agg struct {
		Errors int64 `gorm:"column:error_count"`
		Quota  int64 `gorm:"column:quota"`
	}
	var rows []agg
	dal.RoDB().WithContext(ctx).Raw(
		`SELECT SUM(CASE WHEN type=5 THEN 1 ELSE 0 END) AS error_count, COALESCE(SUM(quota),0) AS quota
FROM logs WHERE user_id=? AND created_at>=? AND created_at<=?`,
		userID, startTS, endTS).Scan(&rows)
	errors, quota := int64(0), int64(0)
	if len(rows) > 0 {
		errors, quota = rows[0].Errors, rows[0].Quota
	}
	h := 100
	switch {
	case errors > 200:
		h = 40
	case errors > 50:
		h = 60
	case errors > 10:
		h = 80
	}
	md := fmt.Sprintf("# 客户健康 - %s\n\n- user_id: %d\n- tier: %s\n- 30d 错误: %d\n- 30d 消费: %d\n- 健康分: %d/100\n",
		ut.UserName, userID, ut.Tier, errors, quota, h)
	switch {
	case errors > 200:
		md += "\n- 错误率过高，建议安排专属支持。\n"
	case errors > 50:
		md += "\n- 错误数偏多，关注场景。\n"
	default:
		md += "\n- 整体健康。\n"
	}
	rep := &dal.AIReport{
		ReportType: "customer_health", PeriodStart: startTS, PeriodEnd: endTS,
		SubjectType: "user", SubjectID: fmt.Sprintf("%d", userID),
		Title: fmt.Sprintf("客户健康 - %s", ut.UserName), Content: md,
	}
	if err := dal.CreateAIReport(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}
