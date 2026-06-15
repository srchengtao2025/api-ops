// Package monitor: 告警规则引擎
// 设计：
//   - 5 条内置规则（YAML 写死，seed 灌入 alert_rules 表；本文件保留 YAML 常量作为"代码即文档"）
//   - 评估器 EvaluateRules() 每 1min tick：
//     1) 读所有 enabled 规则
//     2) 按 rule.Type 拉取最近窗口的健康度 / 错误数 / 余额等数据
//     3) 比对 threshold → 命中则创建 AlertHistory
//     4) 抑制器：Redis key alert_fire:{rule_id}:{subject_id} TTL=max(duration, 1h)
//     5) 不发飞书（本期不实现通知，由 P2/T2 负责）
//   - 状态机：firing → acknowledged/resolved/suppressed/escalated
//   - API 暴露：list / get / ack / resolve（handler 在 internal/api/handlers_monitor.go）
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/realtime"
)

// ===== 默认 5 条规则（与 seed AlertRules 对齐） =====
//
// Q3 飞书独占体现：所有规则的 NotifyChannels 默认含 feishu
// 1) ch_high_error_rate (critical) —— error_rate > 20% 持续 10min
// 2) ch_balance_low (high)        —— balance < $5
// 3) ch_p95_degraded (high)       —— p95 > baseline × 1.5 持续 15min
// 4) vip_consecutive_errors (high) —— 5min 内 ≥ 10 次错误
// 5) svip_user_critical (critical) —— 5min 内 ≥ 3 次错误

// DefaultAlertRules 5 条内置告警规则（与 mock HTML alert-center.html 命名一致）
// 这是"代码即文档"：seed 阶段 seed/ops.go 用同样定义写入 DB
var DefaultAlertRules = []dal.AlertRule{
	{
		Name:           "渠道错误率过高 (critical)",
		Type:           "channel_error_rate",
		Target:         "all",
		Condition:      "error_rate>0.20 window=5m duration=10m",
		Severity:       "critical",
		NotifyChannels: `["feishu_ops","feishu_oncall"]`,
		Actions:        `["notify_feishu","auto_disable_channel","ai_diagnose"]`,
		YAMLFull:       chHighErrorRateYAML,
		Enabled:        true,
	},
	{
		Name:           "渠道余额低 (high)",
		Type:           "balance_low",
		Target:         "all",
		Condition:      "balance<5.0",
		Severity:       "high",
		NotifyChannels: `["feishu_finance"]`,
		Actions:        `["notify_feishu"]`,
		YAMLFull:       chBalanceLowYAML,
		Enabled:        true,
	},
	{
		Name:           "渠道 P95 延迟劣化 (high)",
		Type:           "p95_degraded",
		Target:         "all",
		Condition:      "p95>baseline*1.5 window=15m",
		Severity:       "high",
		NotifyChannels: `["feishu_ops"]`,
		Actions:        `["notify_feishu","ai_diagnose"]`,
		YAMLFull:       chP95DegradedYAML,
		Enabled:        true,
	},
	{
		Name:           "VIP 客户连续错误 (high)",
		Type:           "user_consecutive_error",
		Target:         "group=vip",
		Condition:      "errors_in_5m>=10",
		Severity:       "high",
		NotifyChannels: `["feishu_cs"]`,
		Actions:        `["notify_feishu"]`,
		YAMLFull:       vipConsecutiveErrorsYAML,
		Enabled:        true,
	},
	{
		Name:           "SVIP 客户 critical",
		Type:           "user_consecutive_error",
		Target:         "group=svip",
		Condition:      "errors_in_5m>=3",
		Severity:       "critical",
		NotifyChannels: `["feishu_ops","feishu_oncall"]`,
		Actions:        `["notify_feishu","at_ops_manager","ai_diagnose"]`,
		YAMLFull:       svipUserCriticalYAML,
		Enabled:        true,
	},
}

const (
	chHighErrorRateYAML = `id: ch_high_error_rate
name: 渠道错误率过高
target: channel
metric: error_rate
window: 5m
duration: 10m
condition: "ratio > 0.20"
severity: critical
actions:
  - notify_feishu
  - auto_disable_channel
  - ai_diagnose
feishu:
  webhook_secret: "SECxxxx"
  at_mobiles: ["13800138000"]
  at_user_ids: ["ou_xxxx"]`

	chBalanceLowYAML = `id: ch_balance_low
name: 渠道余额低
target: channel
metric: balance
condition: "balance < 5.0"
severity: high
actions: [notify_feishu_finance]`

	chP95DegradedYAML = `id: ch_p95_degraded
name: 渠道 P95 延迟劣化
target: channel
metric: p95_latency
window: 15m
condition: "p95 > baseline_p95 * 1.5"
severity: high
actions: [notify_feishu_ops, ai_diagnose]`

	vipConsecutiveErrorsYAML = `id: vip_consecutive_errors
name: VIP 客户连续错误
target: user
target_tier: vip
metric: consecutive_errors
window: 5m
condition: ">10"
severity: high
actions: [notify_feishu_cs]`

	svipUserCriticalYAML = `id: svip_user_critical
name: SVIP 客户 critical
target: user
target_tier: svip
metric: error_count
window: 5m
condition: ">=3"
duration: 1m
severity: critical
actions:
  - notify_feishu
  - at_ops_manager
  - ai_diagnose`
)

// ===== 评估器 =====

// EvaluateRules 1min tick 调一次：遍历所有 enabled 规则，匹配则创建 AlertHistory
// 返回本次触发的 AlertHistory 数（不计 suppressed）
func EvaluateRules(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rules, err := dal.ListEnabledAlertRules(ctx)
	if err != nil {
		return 0, fmt.Errorf("list rules: %w", err)
	}

	nowUnix := now.Unix()
	triggered := 0

	for _, rule := range rules {
		switch rule.Type {
		case "channel_error_rate":
			n, err := evalChannelErrorRate(ctx, &rule, nowUnix)
			if err != nil {
				log.Printf("[alert] %s eval failed: %v", rule.Name, err)
				continue
			}
			triggered += n

		case "balance_low":
			n, err := evalBalanceLow(ctx, &rule, nowUnix)
			if err != nil {
				log.Printf("[alert] %s eval failed: %v", rule.Name, err)
				continue
			}
			triggered += n

		case "p95_degraded":
			n, err := evalP95Degraded(ctx, &rule, nowUnix)
			if err != nil {
				log.Printf("[alert] %s eval failed: %v", rule.Name, err)
				continue
			}
			triggered += n

		case "user_consecutive_error":
			n, err := evalUserErrors(ctx, &rule, nowUnix)
			if err != nil {
				log.Printf("[alert] %s eval failed: %v", rule.Name, err)
				continue
			}
			triggered += n

		default:
			// 未知 rule.type：跳过（保留扩展点）
			continue
		}
	}

	// 收尾：把满足 resolved 条件的 firing 告警标记 resolved
	if err := resolveFiredAlerts(ctx); err != nil {
		log.Printf("[alert] resolve fired failed: %v", err)
	}

	return triggered, nil
}

// threshold 从 condition 字符串解析 "metric>0.20" / "balance<5.0" / "errors_in_5m>=10" / "p95>baseline*1.5"
type threshold struct {
	Metric  string  // error_rate / balance / p95 / errors_in_5m
	Op      string  // > / < / >= / <= / ==
	Value   float64 // 数值
	RawExpr string  // 原始表达式
}

func parseThreshold(cond string) (threshold, error) {
	t := threshold{RawExpr: cond}
	for _, op := range []string{">=", "<=", "==", ">", "<"} {
		if idx := strings.Index(cond, op); idx > 0 {
			t.Metric = strings.TrimSpace(cond[:idx])
			t.Op = op
			vStr := strings.TrimSpace(cond[idx+len(op):])
			// 处理 p95>baseline*1.5 → 抽出倍数
			if strings.HasPrefix(vStr, "baseline*") {
				m, err := strconv.ParseFloat(strings.TrimPrefix(vStr, "baseline*"), 64)
				if err != nil {
					return t, fmt.Errorf("invalid baseline multiplier: %q", vStr)
				}
				t.Value = m
			} else {
				v, err := strconv.ParseFloat(vStr, 64)
				if err != nil {
					return t, fmt.Errorf("invalid threshold value: %q", vStr)
				}
				t.Value = v
			}
			return t, nil
		}
	}
	return t, fmt.Errorf("no operator in condition: %q", cond)
}

func (t threshold) hit(actual float64) bool {
	switch t.Op {
	case ">":
		return actual > t.Value
	case "<":
		return actual < t.Value
	case ">=":
		return actual >= t.Value
	case "<=":
		return actual <= t.Value
	case "==":
		return actual == t.Value
	}
	return false
}

// extractDuration 从 condition 字符串里抽 window=5m / duration=10m
// 简单实现：找 "window=5m" 取 5，duration 类似
func extractDuration(cond, key string) time.Duration {
	idx := strings.Index(cond, key+"=")
	if idx < 0 {
		return 0
	}
	rest := cond[idx+len(key)+1:]
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		end = len(rest)
	}
	vStr := rest[:end]
	if len(vStr) < 2 {
		return 0
	}
	num, err := strconv.Atoi(vStr[:len(vStr)-1])
	if err != nil {
		return 0
	}
	unit := vStr[len(vStr)-1]
	switch unit {
	case 's':
		return time.Duration(num) * time.Second
	case 'm':
		return time.Duration(num) * time.Minute
	case 'h':
		return time.Duration(num) * time.Hour
	}
	return 0
}

// alertKey 抑制器 key
func alertKey(ruleID uint64, subjectID string) string {
	return fmt.Sprintf("alert_fire:%d:%s", ruleID, subjectID)
}

// shouldSuppress 检查 Redis 抑制器；存在则返回 true
// 没 Redis 时走 DB 兜底：查 alert_histories 最近 max(duration,1h) 内同 rule+subject 的 firing/ack 记录
func shouldSuppress(ctx context.Context, ruleID uint64, subjectID, condition string) (bool, error) {
	if dal.RDB != nil {
		key := alertKey(ruleID, subjectID)
		exists, err := dal.RDB.Exists(ctx, key).Result()
		if err == nil {
			if exists > 0 {
				return true, nil
			}
			return false, nil
		}
		// Redis 故障 → 降级到 DB
	}
	// DB 兜底：查最近 1h 内同 rule+subject 的 firing/ack
	dur := extractDuration(condition, "duration")
	if dur < time.Hour {
		dur = time.Hour
	}
	since := time.Now().Add(-dur).Unix()
	var n int64
	err := dal.OPS.WithContext(ctx).Model(&dal.AlertHistory{}).
		Where("rule_id = ? AND subject_id = ? AND status IN ('firing','acknowledged','escalated') AND created_at >= ?",
			ruleID, subjectID, since).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// markSuppressed 把 suppress 状态写回 Redis（TTL=duration 或 1h）
func markSuppressed(ctx context.Context, ruleID uint64, subjectID, condition string) {
	dur := extractDuration(condition, "duration")
	if dur < time.Hour {
		dur = time.Hour
	}
	if dal.RDB != nil {
		key := alertKey(ruleID, subjectID)
		_ = dal.RDB.Set(ctx, key, "1", dur).Err()
	}
}

// fireAlert 创建 AlertHistory + AlertAction 记录
// 飞书通知走 notifier.SendForAlert（异步；不存在时仅写 alert_action 记录）
func fireAlert(ctx context.Context, rule *dal.AlertRule, subjectType, subjectID, subjectName, message string) error {
	now := time.Now()
	severity := rule.Severity
	if severity == "" {
		severity = "warning"
	}
	hist := &dal.AlertHistory{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		Severity:    severity,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		SubjectName: subjectName,
		Message:     message,
		Status:      "firing",
		NotifiedAt:  &now,
	}
	if err := dal.CreateAlertHistory(ctx, hist); err != nil {
		return fmt.Errorf("create alert history: %w", err)
	}

	// 解析 NotifyChannels（JSON 数组）→ 写 alert_actions 记录
	channels := []string{}
	if rule.NotifyChannels != "" {
		_ = json.Unmarshal([]byte(rule.NotifyChannels), &channels)
	}
	for _, ch := range channels {
		if ch == "" {
			continue
		}
		_ = dal.CreateAlertAction(ctx, &dal.AlertAction{
			AlertHistoryID: hist.ID,
			Channel:        ch,
			Status:         "pending", // notifier.SendForAlert 异步填充为 sent / failed / degraded
			Target:         "(pending-config)",
		})
	}

	// 异步触发飞书通知（Q3 决策体现）
	// 使用独立 context，避免请求 ctx 取消导致发送中断
	if Notifier != nil {
		go Notifier.SendForAlert(context.Background(), hist)
	}
	// P2 实时面板：同步推送 alert 帧到 global
	if AlertBroadcaster != nil {
		AlertBroadcaster(realtime.AlertPayload{
			RuleID: hist.RuleID, RuleName: hist.RuleName, Severity: hist.Severity,
			SubjectType: hist.SubjectType, SubjectID: hist.SubjectID,
			SubjectName: hist.SubjectName, Message: hist.Message,
		})
	}
	return nil
}

// ===== 规则评估实现 =====

// evalChannelErrorRate: 读 channel_health_5min 最后一桶 → error_rate>0.20 → firing
func evalChannelErrorRate(ctx context.Context, rule *dal.AlertRule, _ int64) (int, error) {
	thr, err := parseThreshold(stripPrefix(rule.Condition, "ratio "))
	if err != nil {
		thr, err = parseThreshold(rule.Condition)
		if err != nil {
			return 0, fmt.Errorf("parse threshold: %w", err)
		}
	}
	_ = thr

	// 直接用 latest 的 5min 数据（不强制 duration=10m 持续时长；简化为看到就触发，抑制器兜底）
	rows, err := dal.ListLatestChannelHealth(ctx)
	if err != nil {
		return 0, err
	}
	t, _ := parseThreshold(rule.Condition)
	count := 0
	for _, r := range rows {
		if r.RequestCount == 0 {
			continue
		}
		if !t.hit(r.ErrorRate) {
			continue
		}
		// 抑制？
		sup, err := shouldSuppress(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		if err != nil {
			return count, err
		}
		if sup {
			continue
		}
		// 命中
		msg := fmt.Sprintf("渠道 #%d 错误率 %.1f%% 超过阈值 (最近 5min，请求=%d 错误=%d)",
			r.ChannelID, r.ErrorRate*100, r.RequestCount, r.ErrorCount)
		// 取 channel name
		chName := fmt.Sprintf("Channel-%d", r.ChannelID)
		if ch, _ := dal.GetChannel(ctx, r.ChannelID); ch != nil {
			chName = ch.Name
		}
		if err := fireAlert(ctx, rule, "channel", fmt.Sprintf("%d", r.ChannelID), chName, msg); err != nil {
			return count, err
		}
		markSuppressed(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		count++
	}
	return count, nil
}

// evalBalanceLow: 读 channels.balance < 5.0 → firing
func evalBalanceLow(ctx context.Context, rule *dal.AlertRule, _ int64) (int, error) {
	rows, err := dal.ListLatestChannelHealth(ctx)
	if err != nil {
		return 0, err
	}
	t, err := parseThreshold(rule.Condition)
	if err != nil {
		return 0, fmt.Errorf("parse threshold: %w", err)
	}
	count := 0
	for _, r := range rows {
		if !t.hit(r.Balance) {
			continue
		}
		sup, err := shouldSuppress(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		if err != nil {
			return count, err
		}
		if sup {
			continue
		}
		chName := fmt.Sprintf("Channel-%d", r.ChannelID)
		if ch, _ := dal.GetChannel(ctx, r.ChannelID); ch != nil {
			chName = ch.Name
		}
		msg := fmt.Sprintf("渠道 #%d 余额 $%.2f 低于阈值 (status=%s)", r.ChannelID, r.Balance, r.Status)
		if err := fireAlert(ctx, rule, "channel", fmt.Sprintf("%d", r.ChannelID), chName, msg); err != nil {
			return count, err
		}
		markSuppressed(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		count++
	}
	return count, nil
}

// evalP95Degraded: 读 channel_health_5min 最近 15min 平均 p95 vs baseline (取 24h 平均 p95) × 1.5 → firing
// 简化版：取最新 5min 桶 p95 vs 24h 内的均值 × 1.5
func evalP95Degraded(ctx context.Context, rule *dal.AlertRule, _ int64) (int, error) {
	t, err := parseThreshold(rule.Condition)
	if err != nil {
		return 0, err
	}
	mult := t.Value
	if mult <= 0 {
		mult = 1.5
	}
	// baseline = 24h 1h 聚合的 avg(p95)
	now := time.Now().Unix()
	baselineRows, err := dal.ListChannelHealth1h(ctx, dal.ChannelHealthQuery{
		StartTS: now - 24*3600, EndTS: now,
	})
	if err != nil {
		return 0, err
	}
	baseline := make(map[int]float64, len(baselineRows))
	counts := make(map[int]int, len(baselineRows))
	for _, r := range baselineRows {
		if r.P95LatencyMs <= 0 {
			continue
		}
		baseline[r.ChannelID] += float64(r.P95LatencyMs)
		counts[r.ChannelID]++
	}
	for chID, sum := range baseline {
		if c := counts[chID]; c > 0 {
			baseline[chID] = sum / float64(c)
		}
	}

	// 当前 5min p95
	latest, err := dal.ListLatestChannelHealth(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range latest {
		base, ok := baseline[r.ChannelID]
		if !ok || base <= 0 {
			continue
		}
		threshold := base * mult
		if float64(r.P95LatencyMs) <= threshold {
			continue
		}
		sup, err := shouldSuppress(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		if err != nil {
			return count, err
		}
		if sup {
			continue
		}
		chName := fmt.Sprintf("Channel-%d", r.ChannelID)
		if ch, _ := dal.GetChannel(ctx, r.ChannelID); ch != nil {
			chName = ch.Name
		}
		msg := fmt.Sprintf("渠道 #%d P95 %dms > baseline %.0fms × %.2f (阈值 %.0fms)",
			r.ChannelID, r.P95LatencyMs, base, mult, threshold)
		if err := fireAlert(ctx, rule, "channel", fmt.Sprintf("%d", r.ChannelID), chName, msg); err != nil {
			return count, err
		}
		markSuppressed(ctx, rule.ID, fmt.Sprintf("%d", r.ChannelID), rule.Condition)
		count++
	}
	return count, nil
}

// evalUserErrors: SVIP/VIP 客户在 5min 内错误数 ≥ 阈值 → firing
// 简化：直接查 logs 表最近 5min，按 user_id 聚合错误数
// 客户分级用 user_tier 表（rule.Target = "group=svip" / "group=vip"）
func evalUserErrors(ctx context.Context, rule *dal.AlertRule, _ int64) (int, error) {
	// 解析 target tier
	tier := ""
	if rule.Target != "" && strings.HasPrefix(rule.Target, "group=") {
		tier = strings.TrimPrefix(rule.Target, "group=")
	}
	// 把 svip / vip 映射到 user_tier 表里的实际值
	userTierFilter := tier
	if tier == "vip" {
		userTierFilter = "vip-1" // seed 里 vip 客户的 tier = "vip-1"
	}

	thr, err := parseThreshold(rule.Condition)
	if err != nil {
		return 0, err
	}
	_ = thr

	// 1) 取该 tier 的用户
	var userIDs []uint64
	q := dal.OPS.WithContext(ctx).Model(&dal.UserTier{}).Where("tier = ?", userTierFilter)
	if err := q.Pluck("user_id", &userIDs).Error; err != nil {
		return 0, err
	}
	if len(userIDs) == 0 {
		return 0, nil
	}

	// 2) 5min 窗口，按 user_id 聚合错误数
	endTS := time.Now().Unix()
	startTS := endTS - 5*60
	type userErr struct {
		UserID   int    `gorm:"column:user_id"`
		Username string `gorm:"column:username"`
		ErrCount int64  `gorm:"column:err_count"`
	}
	var errs []userErr
	sql := `
SELECT user_id, MAX(username) AS username,
       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS err_count
FROM logs
WHERE created_at >= ? AND created_at < ? AND type = ?
GROUP BY user_id
HAVING SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) >= ?
`
	if err := dal.RoDB().WithContext(ctx).Raw(sql,
		dal.LogTypeError, startTS, endTS, dal.LogTypeError, dal.LogTypeError, int(thr.Value)).
		Scan(&errs).Error; err != nil {
		return 0, err
	}

	// 3) 过滤到本 tier 的用户
	tierSet := make(map[uint64]bool, len(userIDs))
	for _, id := range userIDs {
		tierSet[id] = true
	}
	count := 0
	for _, e := range errs {
		if !tierSet[uint64(e.UserID)] {
			continue
		}
		subjectID := fmt.Sprintf("%d", e.UserID)
		sup, err := shouldSuppress(ctx, rule.ID, subjectID, rule.Condition)
		if err != nil {
			return count, err
		}
		if sup {
			continue
		}
		subjectName := e.Username
		if subjectName == "" {
			subjectName = fmt.Sprintf("user-%d", e.UserID)
		}
		msg := fmt.Sprintf("%s 客户 %s 在 5min 内 %d 次错误 ≥ 阈值 %d",
			strings.ToUpper(tier), subjectName, e.ErrCount, int(thr.Value))
		if err := fireAlert(ctx, rule, "user", subjectID, subjectName, msg); err != nil {
			return count, err
		}
		markSuppressed(ctx, rule.ID, subjectID, rule.Condition)
		count++
	}
	return count, nil
}

// stripPrefix 去掉 condition 前缀（如 "ratio "），便于复用 parseThreshold
func stripPrefix(s, prefix string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), prefix)
}

// resolveFiredAlerts 把条件已不再满足的 firing 告警 → resolved
// 简化版：仅在 eval 后无新触发时按 status+firing 列表逐条检查
// 这里只做最小实现：定期扫描 status=firing 的告警，重新跑对应规则的 evaluator，
// 若对应 metric 已恢复，则置为 resolved。
// 完整版需要保留 rule_type+subject_id 的索引（本任务范围内用最小实现）
func resolveFiredAlerts(ctx context.Context) error {
	rows, _, err := dal.ListAlertHistories(ctx, dal.AlertHistoryQuery{
		Status: "firing", Limit: 500,
	})
	if err != nil {
		return err
	}
	for _, h := range rows {
		// 简化：调用对应 evaluator 不再 fire → 标记 resolved
		// 这里我们用时间窗（30min 内未重复触发 = resolved）做兜底
		// 实际规则应按 subject_type 重新跑 metrics —— 本期时间窗近似即可
		if h.CreatedAt.Before(time.Now().Add(-30 * time.Minute)) {
			_ = dal.UpdateAlertHistoryStatus(ctx, h.ID, "resolved", "auto-resolver")
		}
	}
	return nil
}

// ===== Handler 层调用的 service-level 操作 =====

// AcknowledgeAlert 告警 ACK
func AcknowledgeAlert(ctx context.Context, id uint64, by string) error {
	if by == "" {
		by = "ops"
	}
	return dal.UpdateAlertHistoryStatus(ctx, id, "acknowledged", by)
}

// ResolveAlert 告警 resolve
func ResolveAlert(ctx context.Context, id uint64) error {
	return dal.UpdateAlertHistoryStatus(ctx, id, "resolved", "manual")
}

// ListAlerts 包装 dal.ListAlertHistories
func ListAlerts(ctx context.Context, status, severity string, limit, offset int) ([]dal.AlertHistory, int64, error) {
	return dal.ListAlertHistories(ctx, dal.AlertHistoryQuery{
		Status: status, Severity: severity,
		Limit: limit, Offset: offset,
	})
}
