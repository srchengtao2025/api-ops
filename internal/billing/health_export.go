// BILLING · 客户健康度模块 (api-ops 开源版, 2026-07-09 同步自 rezeai-ops)
//
// 跟 rezeai-ops 区别:
//   - api-ops 单站架构, 无 Site 字段 (本文件函数都无 site 参数)
//   - 多站用户参考 rezeai-ops 内 internal/billing/health_export.go
//
// 复用 billing_export_tasks 表, kind = 'customer_health'
//   - Period 字段存时间窗: "48h" / "7d" / "30d" (字段 size:7 装得下, 不扩 schema)
//   - VendorCode 字段存导出类型: "errors" (错误详情) / "hits" (命中详情)
//   - Formats 字段: "html" (单一 HTML, 不打包)
//
// 数据源:
//   - 列表 (overview): cache_logs_summary_by_user_5min (1min tick 预聚合)
//   - 详情 (errors/hits): logs RoDB 直查 (LogQuery 过滤, 限 5000 条)
//
// 错误率口径 (跟 rezeai-ops 一致):
//   error_rate = error_count / (error_count + success_count)
//   退款 refund_count 不算分母 (退款是业务回滚, 不污染错误率)
//
// 缓存复用率口径 (B 公式, 跟 Anthropic / OpenAI 业内规范一致):
//   cache_rate = cache_tokens / prompt_tokens
//   prompt_tokens 字段已包含 cache 命中部分
//
// 健康度等级:
//   healthy:  error_rate < 2% AND cache_rate >= 90%
//   warning:  2% <= error_rate < 10%  OR  70% <= cache_rate < 90%
//   critical: error_rate >= 10%      OR  cache_rate < 70%
package billing

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// 健康度等级常量
const (
	HealthLevelHealthy  = "healthy"
	HealthLevelWarning  = "warning"
	HealthLevelCritical = "critical"
)

// 健康度等级阈值 (Q-D 决策, 2026-07-08)
const (
	// error_rate 阈值 (0-1)
	errorRateWarning  = 0.02
	errorRateCritical = 0.10
	// cache_rate 阈值 (0-1)
	cacheRateHealthy  = 0.90
	cacheRateWarning  = 0.70
	// 详情导出行数上限
	healthDetailLimit = 5000
)

// CustomerHealthOverviewItem 单客户健康度摘要 (handler 端用)
type CustomerHealthOverviewItem struct {
	UserID         int     `json:"user_id"`
	Username       string  `json:"username"`
	RequestCount   int64   `json:"request_count"`
	SuccessCount   int64   `json:"success_count"`
	ErrorCount     int64   `json:"error_count"`
	PromptTokens   int64   `json:"prompt_tokens"`
	CacheTokens    int64   `json:"cache_tokens"`
	Quota          int64   `json:"quota"`
	ErrorRate      float64 `json:"error_rate"`     // error / (error + success)
	CacheRate      float64 `json:"cache_rate"`     // cache / prompt
	HealthLevel    string  `json:"health_level"`   // healthy / warning / critical
	HealthReasons  string  `json:"health_reasons"` // 等级解释, 逗号分隔
}

// CustomerHealthDetailItem 单条详情 (errors 跟 hits 通用)
type CustomerHealthDetailItem struct {
	ID              int64  `json:"id"`
	CreatedAt       int64  `json:"created_at"`
	ModelName       string `json:"model_name"`
	ChannelID       int    `json:"channel_id"`
	PromptTokens    int    `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	CacheTokens     int    `json:"cache_tokens"`     // 命中数
	ErrorType       int    `json:"error_type,omitempty"`  // type=5 时填
	Content         string `json:"content,omitempty"`     // 错误堆栈 / 请求摘要
}

// CustomerHealthDetail 单客户详情
type CustomerHealthDetail struct {
	UserID         int                       `json:"user_id"`
	Username       string                    `json:"username"`
	Period         string                    `json:"period"`
	RequestCount   int64                     `json:"request_count"`
	SuccessCount   int64                     `json:"success_count"`
	ErrorCount     int64                     `json:"error_count"`
	PromptTokens   int64                     `json:"prompt_tokens"`
	CacheTokens    int64                     `json:"cache_tokens"`
	ErrorRate      float64                   `json:"error_rate"`
	CacheRate      float64                   `json:"cache_rate"`
	HealthLevel    string                    `json:"health_level"`
	HealthReasons  string                    `json:"health_reasons"`
	Errors         []CustomerHealthDetailItem `json:"errors"`
	Hits           []CustomerHealthDetailItem `json:"hits"`
	ErrorsTruncated bool                     `json:"errors_truncated"`
	HitsTruncated   bool                     `json:"hits_truncated"`
}

// parseHealthPeriod 解析 "48h" / "7d" / "30d" → [startTS, endTS)
//
// 跟 v2 "YYYY-MM" 不同, 健康度用滑动窗口
func parseHealthPeriod(period string) (int64, int64, error) {
	if len(period) < 2 {
		return 0, 0, fmt.Errorf("invalid period: %q (expect 48h/7d/30d)", period)
	}
	numStr := period[:len(period)-1]
	unit := period[len(period)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, 0, fmt.Errorf("invalid period: %q", period)
	}
	endTS := time.Now().Unix()
	var startTS int64
	switch unit {
	case 'h':
		startTS = endTS - int64(n)*3600
	case 'd':
		startTS = endTS - int64(n)*86400
	default:
		return 0, 0, fmt.Errorf("invalid period unit: %q (expect h/d)", string(unit))
	}
	return startTS, endTS, nil
}

// computeHealthLevel 算健康度等级
//
// 返回: (level, reasons 逗号分隔字符串)
func computeHealthLevel(errorRate, cacheRate float64) (string, string) {
	var reasons []string
	level := HealthLevelHealthy

	// error_rate 维度
	switch {
	case errorRate >= errorRateCritical:
		level = HealthLevelCritical
		reasons = append(reasons, fmt.Sprintf("错误率 %.2f%% ≥ 10%%", errorRate*100))
	case errorRate >= errorRateWarning:
		if level == HealthLevelHealthy {
			level = HealthLevelWarning
		}
		reasons = append(reasons, fmt.Sprintf("错误率 %.2f%% ≥ 2%%", errorRate*100))
	default:
		reasons = append(reasons, fmt.Sprintf("错误率 %.2f%% 正常", errorRate*100))
	}

	// cache_rate 维度
	switch {
	case cacheRate < cacheRateWarning:
		level = HealthLevelCritical
		reasons = append(reasons, fmt.Sprintf("缓存复用率 %.2f%% < 70%%", cacheRate*100))
	case cacheRate < cacheRateHealthy:
		if level == HealthLevelHealthy {
			level = HealthLevelWarning
		}
		reasons = append(reasons, fmt.Sprintf("缓存复用率 %.2f%% < 90%%", cacheRate*100))
	default:
		reasons = append(reasons, fmt.Sprintf("缓存复用率 %.2f%% 优秀", cacheRate*100))
	}

	return level, joinReasons(reasons)
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}

// CustomerHealthOverview 全部客户 48h 健康度列表
//
// 走 cache_logs_summary_by_user_5min (1min tick 预聚合), handler 端再 GROUP BY
// api-ops 单站: 走 RoDB() 默认 (国际站, 等同 rezeai-ops intl 站)
func CustomerHealthOverview(ctx context.Context, period string) ([]CustomerHealthOverviewItem, error) {
	startTS, endTS, err := parseHealthPeriod(period)
	if err != nil {
		return nil, err
	}

	// 走 cache_logs_summary_by_user_5min (跟 v2 客户对账同源, 避免 logs 全表扫)
	rows, err := dal.AggregateUserSummaryInRange(ctx, startTS, endTS)
	if err != nil {
		return nil, fmt.Errorf("aggregate user summary: %w", err)
	}

	out := make([]CustomerHealthOverviewItem, 0, len(rows))
	for _, r := range rows {
		// 错误率 B 公式: error / (error + success) 退款不算分母
		denom := r.ErrorCount + r.SuccessCount
		var errorRate float64
		if denom > 0 {
			errorRate = float64(r.ErrorCount) / float64(denom)
		}
		// 缓存复用率 B 公式: cache / prompt
		var cacheRate float64
		if r.PromptTokens > 0 {
			cacheRate = float64(r.CacheTokens) / float64(r.PromptTokens)
		}
		level, reasons := computeHealthLevel(errorRate, cacheRate)
		out = append(out, CustomerHealthOverviewItem{
			UserID:        r.UserID,
			Username:      r.Username,
			RequestCount:  r.RequestCount,
			SuccessCount:  r.SuccessCount,
			ErrorCount:    r.ErrorCount,
			PromptTokens:  r.PromptTokens,
			CacheTokens:   r.CacheTokens,
			Quota:         r.Quota,
			ErrorRate:     errorRate,
			CacheRate:     cacheRate,
			HealthLevel:   level,
			HealthReasons: reasons,
		})
	}
	// 排序: critical 优先, 然后按请求量
	sort.Slice(out, func(i, j int) bool {
		if out[i].HealthLevel != out[j].HealthLevel {
			return levelOrder(out[i].HealthLevel) < levelOrder(out[j].HealthLevel)
		}
		return out[i].RequestCount > out[j].RequestCount
	})
	return out, nil
}

func levelOrder(l string) int {
	switch l {
	case HealthLevelCritical:
		return 0
	case HealthLevelWarning:
		return 1
	default:
		return 2
	}
}

// CustomerHealthDetailByUser 单客户详情 (errors + hits, 各限 5000 条)
//
// api-ops 单站: 走默认 RoDB (国际站, 等同 rezeai-ops intl 站)
func CustomerHealthDetailByUser(ctx context.Context, period string, userID int) (*CustomerHealthDetail, error) {
	startTS, endTS, err := parseHealthPeriod(period)
	if err != nil {
		return nil, err
	}

	// 1) 走 cache 拿聚合数据
	cacheRows, err := dal.AggregateUserSummaryInRange(ctx, startTS, endTS)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", err)
	}
	var agg *dal.LogsSummaryByUser5min
	for i := range cacheRows {
		if cacheRows[i].UserID == userID {
			agg = &cacheRows[i]
			break
		}
	}
	if agg == nil {
		return nil, fmt.Errorf("user %d not found in 48h logs", userID)
	}

	denom := agg.ErrorCount + agg.SuccessCount
	var errorRate float64
	if denom > 0 {
		errorRate = float64(agg.ErrorCount) / float64(denom)
	}
	var cacheRate float64
	if agg.PromptTokens > 0 {
		cacheRate = float64(agg.CacheTokens) / float64(agg.PromptTokens)
	}
	level, reasons := computeHealthLevel(errorRate, cacheRate)

	// 2) 走 RoDB 拿错误详情 (type=5)
	//    api-ops 单站: 走默认 RoDB() (等同 rezeai-ops intl 站)
	//    多站用户参考 rezeai-ops: 改用 QueryLogsOnDB(ctx, dal.GetRO(site), q)
	errQ := dal.LogQuery{
		StartTime: startTS,
		EndTime:   endTS,
		UserID:    userID,
		OnlyError: true,
		Limit:     healthDetailLimit + 1, // +1 探测是否截断
		OrderDesc: true,
	}
	errRows, err := dal.QueryLogs(ctx, errQ)
	if err != nil {
		return nil, fmt.Errorf("query errors: %w", err)
	}
	errorsTruncated := false
	if len(errRows) > healthDetailLimit {
		errRows = errRows[:healthDetailLimit]
		errorsTruncated = true
	}

	// 3) 走 RoDB 拿命中详情 (cache_tokens > 0)
	hitQ := dal.LogQuery{
		StartTime: startTS,
		EndTime:   endTS,
		UserID:    userID,
		LogType:   dal.LogTypeConsume,
		Limit:     healthDetailLimit + 1,
		OrderDesc: true,
	}
	hitRows, err := dal.QueryLogs(ctx, hitQ)
	if err != nil {
		return nil, fmt.Errorf("query hits: %w", err)
	}
	hitsTruncated := false
	if len(hitRows) > healthDetailLimit {
		hitRows = hitRows[:healthDetailLimit]
		hitsTruncated = true
	}

	// 4) 过滤 hitRows: cache_tokens > 0 (handler 端 JSON 解析 Other)
	hits := make([]CustomerHealthDetailItem, 0, len(hitRows))
	for _, r := range hitRows {
		other, _ := dal.ParseOther(r.Other)
		if other == nil || other.CacheTokens <= 0 {
			continue
		}
		hits = append(hits, CustomerHealthDetailItem{
			ID:               r.ID,
			CreatedAt:        r.CreatedAt,
			ModelName:        r.ModelName,
			ChannelID:        r.ChannelID,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			CacheTokens:      other.CacheTokens,
		})
	}

	errors := make([]CustomerHealthDetailItem, 0, len(errRows))
	for _, r := range errRows {
		other, _ := dal.ParseOther(r.Other)
		var cacheTok int
		if other != nil {
			cacheTok = other.CacheTokens
		}
		errors = append(errors, CustomerHealthDetailItem{
			ID:               r.ID,
			CreatedAt:        r.CreatedAt,
			ModelName:        r.ModelName,
			ChannelID:        r.ChannelID,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			CacheTokens:      cacheTok,
			ErrorType:        r.Type,
			Content:          r.Content,
		})
	}

	return &CustomerHealthDetail{
		UserID:          userID,
		Username:        agg.Username,
		Period:          period,
		RequestCount:    agg.RequestCount,
		SuccessCount:    agg.SuccessCount,
		ErrorCount:      agg.ErrorCount,
		PromptTokens:    agg.PromptTokens,
		CacheTokens:     agg.CacheTokens,
		ErrorRate:       errorRate,
		CacheRate:       cacheRate,
		HealthLevel:     level,
		HealthReasons:   reasons,
		Errors:          errors,
		Hits:            hits,
		ErrorsTruncated: errorsTruncated,
		HitsTruncated:   hitsTruncated,
	}, nil
}

// ===== HTML 生成 (走 generateStatement 的 customer_health 分支) =====

// customerHealthExportDir 健康度导出文件目录
const customerHealthExportDir = "/data/customer-health-exports"

// generateCustomerHealthErrorHTML 错误详情 HTML
func generateCustomerHealthErrorHTML(ctx context.Context, task *dal.BillingExportTask) (string, int64, error) {
	detail, err := CustomerHealthDetailByUser(ctx, task.Period, task.UserID)
	if err != nil {
		return "", 0, fmt.Errorf("query detail: %w", err)
	}
	html := renderErrorDetailHTML(detail, task)
	path, size, err := writeHealthHTML(task.TaskID, html)
	if err != nil {
		return "", 0, fmt.Errorf("write html: %w", err)
	}
	_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
		fmt.Sprintf("error detail rendered, %d rows, size=%d", len(detail.Errors), size))
	return path, size, nil
}

// generateCustomerHealthHitHTML 命中详情 HTML
func generateCustomerHealthHitHTML(ctx context.Context, task *dal.BillingExportTask) (string, int64, error) {
	detail, err := CustomerHealthDetailByUser(ctx, task.Period, task.UserID)
	if err != nil {
		return "", 0, fmt.Errorf("query detail: %w", err)
	}
	html := renderHitDetailHTML(detail, task)
	path, size, err := writeHealthHTML(task.TaskID, html)
	if err != nil {
		return "", 0, fmt.Errorf("write html: %w", err)
	}
	_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
		fmt.Sprintf("hit detail rendered, %d rows, size=%d", len(detail.Hits), size))
	return path, size, nil
}

func writeHealthHTML(taskID, html string) (string, int64, error) {
	if err := os.MkdirAll(customerHealthExportDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir: %w", err)
	}
	path := filepath.Join(customerHealthExportDir, taskID+".html")
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		return "", 0, fmt.Errorf("write: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat: %w", err)
	}
	return path, st.Size(), nil
}

// ===== HTML 模板 =====

// renderErrorDetailHTML 错误详情 HTML
func renderErrorDetailHTML(d *CustomerHealthDetail, task *dal.BillingExportTask) string {
	data := map[string]interface{}{
		"Title":       fmt.Sprintf("客户错误详情 - %s (uid=%d)", d.Username, d.UserID),
		"GeneratedAt": time.Now().Format("2006-01-02 15:04:05 MST"),
		"Site":        "intl",
		"Period":      d.Period,
		"Username":    d.Username,
		"UserID":      d.UserID,
		"HealthLevel": healthLevelLabel(d.HealthLevel),
		"ErrorRate":   d.ErrorRate, // float64 (0-1), 模板内 printf 格式化 + gt 比较
		"ErrorCount":  d.ErrorCount,
		"SuccessCount": d.SuccessCount,
		"RequestCount": d.RequestCount,
		"Truncated":   d.ErrorsTruncated,
		"Errors":      d.Errors,
	}
	tpl := errorDetailTpl
	var buf []byte
	t := template.Must(template.New("error").Funcs(template.FuncMap{
		"formatTime": formatTimeCN,
		"mulf":       func(a, b float64) float64 { return a * b },
	}).Parse(tpl))
	if err := t.Execute(&writeBuffer{buf: &buf}, data); err != nil {
		log.Printf("[billing-health] render error html: %v", err)
		return "<html><body>render error: " + err.Error() + "</body></html>"
	}
	return string(buf)
}

// renderHitDetailHTML 命中详情 HTML
func renderHitDetailHTML(d *CustomerHealthDetail, task *dal.BillingExportTask) string {
	data := map[string]interface{}{
		"Title":       fmt.Sprintf("客户缓存命中详情 - %s (uid=%d)", d.Username, d.UserID),
		"GeneratedAt": time.Now().Format("2006-01-02 15:04:05 MST"),
		"Site":        "intl",
		"Period":      d.Period,
		"Username":    d.Username,
		"UserID":      d.UserID,
		"HealthLevel": healthLevelLabel(d.HealthLevel),
		"CacheRate":   d.CacheRate, // float64 (0-1), 模板内 printf 格式化
		"CacheTokens": d.CacheTokens,
		"PromptTokens": d.PromptTokens,
		"Truncated":   d.HitsTruncated,
		"Hits":        d.Hits,
	}
	tpl := hitDetailTpl
	var buf []byte
	t := template.Must(template.New("hit").Funcs(template.FuncMap{
		"formatTime": formatTimeCN,
		"mulf":       func(a, b float64) float64 { return a * b },
		"hitRate":    hitRatePct,
	}).Parse(tpl))
	if err := t.Execute(&writeBuffer{buf: &buf}, data); err != nil {
		log.Printf("[billing-health] render hit html: %v", err)
		return "<html><body>render error: " + err.Error() + "</body></html>"
	}
	return string(buf)
}

func healthLevelLabel(l string) string {
	switch l {
	case HealthLevelHealthy:
		return "健康"
	case HealthLevelWarning:
		return "关注"
	case HealthLevelCritical:
		return "告警"
	}
	return l
}

// writeBuffer 极简 io.Writer 实现 (避免引 bytes.Buffer)
type writeBuffer struct {
	buf *[]byte
}

func (w *writeBuffer) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// formatTimeCN unix 秒 → 2006-01-02 15:04:05 (CST)
func formatTimeCN(unixSec int64) string {
	return time.Unix(unixSec, 0).In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
}

// hitRatePct 算单行命中率 (cache / prompt * 100, 2 位小数 + %)
func hitRatePct(prompt, cache int) string {
	if prompt <= 0 {
		return "0.00%"
	}
	return fmt.Sprintf("%.2f%%", float64(cache)/float64(prompt)*100)
}

// ===== HTML 模板字符串 (内嵌, 避免外部资源依赖) =====

const errorDetailTpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f5f5;color:#1a202c;line-height:1.6;padding:24px;max-width:1280px;margin:0 auto}
h1{font-size:24px;margin:0 0 8px}
h2{font-size:18px;margin:24px 0 12px;padding-bottom:8px;border-bottom:2px solid #e2e8f0}
.meta{color:#718096;font-size:13px;margin-bottom:16px}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;margin:20px 0}
.kpi{background:#fff;padding:16px;border-radius:8px;border-left:4px solid #2b6cb0;box-shadow:0 1px 3px rgba(0,0,0,0.05)}
.kpi .label{font-size:12px;color:#718096;text-transform:uppercase;letter-spacing:0.5px}
.kpi .value{font-size:24px;font-weight:700;margin-top:4px}
.kpi.warn{border-left-color:#d69e2e}
.kpi.crit{border-left-color:#e53e3e}
table{width:100%;border-collapse:collapse;background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,0.05);font-size:13px}
th{background:#ebf4ff;color:#2b6cb0;padding:10px 12px;text-align:left;font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:0.3px}
td{padding:10px 12px;border-bottom:1px solid #e2e8f0;vertical-align:top}
tr:hover{background:#f7fafc}
.content{font-family:"SF Mono",Monaco,monospace;font-size:12px;max-width:400px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.tag{display:inline-block;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600}
.tag.healthy{background:#c6f6d5;color:#22543d}
.tag.warning{background:#fefcbf;color:#744210}
.tag.critical{background:#fed7d7;color:#742a2a}
.notice{background:#fef5e7;padding:12px 16px;border-radius:6px;margin:16px 0;font-size:14px;color:#744210}
.footer{text-align:center;color:#a0aec0;font-size:12px;margin-top:32px;padding-top:16px;border-top:1px solid #e2e8f0}
</style>
</head>
<body>
<h1>📊 {{.Title}}</h1>
<div class="meta">生成时间: {{.GeneratedAt}} · 站点: {{.Site}} · 时间窗: {{.Period}}</div>

<h2>健康度</h2>
<div class="kpis">
  <div class="kpi {{if eq .HealthLevel "告警"}}crit{{else if eq .HealthLevel "关注"}}warn{{end}}">
    <div class="label">等级</div>
    <div class="value">
      {{if eq .HealthLevel "健康"}}<span class="tag healthy">🟢 健康</span>
      {{else if eq .HealthLevel "关注"}}<span class="tag warning">🟡 关注</span>
      {{else}}<span class="tag critical">🔴 告警</span>{{end}}
    </div>
  </div>
  <div class="kpi {{if gt .ErrorRate 0.10}}crit{{else if gt .ErrorRate 0.02}}warn{{end}}">
    <div class="label">错误率</div>
    <div class="value">{{printf "%.2f%%" (mulf .ErrorRate 100)}}</div>
  </div>
  <div class="kpi">
    <div class="label">总请求数</div>
    <div class="value">{{.RequestCount}}</div>
  </div>
  <div class="kpi">
    <div class="label">成功数</div>
    <div class="value">{{.SuccessCount}}</div>
  </div>
  <div class="kpi crit">
    <div class="label">错误数</div>
    <div class="value">{{.ErrorCount}}</div>
  </div>
</div>

{{if .Truncated}}<div class="notice">⚠️ 数据量超过 5000 条, 已截断显示. 如需完整数据请缩小时间窗或联系运维.</div>{{end}}

<h2>错误明细 ({{len .Errors}} 条)</h2>
<table>
  <thead>
    <tr>
      <th style="width:150px">时间</th>
      <th style="width:140px">模型</th>
      <th style="width:80px">渠道</th>
      <th style="width:90px">输入</th>
      <th style="width:90px">输出</th>
      <th style="width:90px">缓存</th>
      <th>错误内容</th>
    </tr>
  </thead>
  <tbody>
  {{range .Errors}}
    <tr>
      <td>{{formatTime .CreatedAt}}</td>
      <td>{{.ModelName}}</td>
      <td>{{.ChannelID}}</td>
      <td style="text-align:right">{{.PromptTokens}}</td>
      <td style="text-align:right">{{.CompletionTokens}}</td>
      <td style="text-align:right">{{.CacheTokens}}</td>
      <td><div class="content" title="{{.Content}}">{{.Content}}</div></td>
    </tr>
  {{else}}
    <tr><td colspan="7" style="text-align:center;color:#a0aec0;padding:40px">无错误记录 🎉</td></tr>
  {{end}}
  </tbody>
</table>

<div class="footer">由 rezeai-ops 客户健康度模块生成 · 2026-07-08</div>
</body>
</html>`

const hitDetailTpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f5f5;color:#1a202c;line-height:1.6;padding:24px;max-width:1280px;margin:0 auto}
h1{font-size:24px;margin:0 0 8px}
h2{font-size:18px;margin:24px 0 12px;padding-bottom:8px;border-bottom:2px solid #e2e8f0}
.meta{color:#718096;font-size:13px;margin-bottom:16px}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;margin:20px 0}
.kpi{background:#fff;padding:16px;border-radius:8px;border-left:4px solid #2b6cb0;box-shadow:0 1px 3px rgba(0,0,0,0.05)}
.kpi .label{font-size:12px;color:#718096;text-transform:uppercase;letter-spacing:0.5px}
.kpi .value{font-size:24px;font-weight:700;margin-top:4px}
.kpi.good{border-left-color:#38a169}
table{width:100%;border-collapse:collapse;background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,0.05);font-size:13px}
th{background:#ebf4ff;color:#2b6cb0;padding:10px 12px;text-align:right;font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:0.3px}
th:first-child{text-align:left}
td{padding:10px 12px;border-bottom:1px solid #e2e8f0;text-align:right}
td:first-child{text-align:left}
tr:hover{background:#f7fafc}
.tag{display:inline-block;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600}
.tag.healthy{background:#c6f6d5;color:#22543d}
.tag.warning{background:#fefcbf;color:#744210}
.tag.critical{background:#fed7d7;color:#742a2a}
.notice{background:#fef5e7;padding:12px 16px;border-radius:6px;margin:16px 0;font-size:14px;color:#744210}
.footer{text-align:center;color:#a0aec0;font-size:12px;margin-top:32px;padding-top:16px;border-top:1px solid #e2e8f0}
</style>
</head>
<body>
<h1>⚡ {{.Title}}</h1>
<div class="meta">生成时间: {{.GeneratedAt}} · 站点: {{.Site}} · 时间窗: {{.Period}}</div>

<h2>健康度</h2>
<div class="kpis">
  <div class="kpi {{if eq .HealthLevel "告警"}}crit{{else if eq .HealthLevel "关注"}}warn{{end}}">
    <div class="label">等级</div>
    <div class="value">
      {{if eq .HealthLevel "健康"}}<span class="tag healthy">🟢 健康</span>
      {{else if eq .HealthLevel "关注"}}<span class="tag warning">🟡 关注</span>
      {{else}}<span class="tag critical">🔴 告警</span>{{end}}
    </div>
  </div>
  <div class="kpi good">
    <div class="label">缓存复用率</div>
    <div class="value">{{printf "%.2f%%" (mulf .CacheRate 100)}}</div>
  </div>
  <div class="kpi">
    <div class="label">总输入 tokens</div>
    <div class="value">{{.PromptTokens}}</div>
  </div>
  <div class="kpi good">
    <div class="label">缓存命中 tokens</div>
    <div class="value">{{.CacheTokens}}</div>
  </div>
</div>

{{if .Truncated}}<div class="notice">⚠️ 数据量超过 5000 条, 已截断显示. 如需完整数据请缩小时间窗或联系运维.</div>{{end}}

<h2>命中明细 ({{len .Hits}} 条)</h2>
<table>
  <thead>
    <tr>
      <th style="width:150px">时间</th>
      <th style="width:140px">模型</th>
      <th style="width:80px">渠道</th>
      <th style="width:110px">输入</th>
      <th style="width:110px">缓存命中</th>
      <th style="width:110px">输出</th>
      <th style="width:100px">命中率</th>
    </tr>
  </thead>
  <tbody>
  {{range .Hits}}
    <tr>
      <td>{{formatTime .CreatedAt}}</td>
      <td>{{.ModelName}}</td>
      <td>{{.ChannelID}}</td>
      <td>{{.PromptTokens}}</td>
      <td style="color:#38a169;font-weight:600">{{.CacheTokens}}</td>
      <td>{{.CompletionTokens}}</td>
      <td>{{hitRate .PromptTokens .CacheTokens}}</td>
    </tr>
  {{else}}
    <tr><td colspan="7" style="text-align:center;color:#a0aec0;padding:40px">无缓存命中记录</td></tr>
  {{end}}
  </tbody>
</table>

<div class="footer">由 rezeai-ops 客户健康度模块生成 · 2026-07-08</div>
</body>
</html>`
