// Package notifier: 飞书机器人 webhook 推送
//
// 关键能力（Q3 决策体现）：
//   - 飞书机器人在企业生产环境作为"必到"通知通道（与 PRD §11.1 一致）
//   - 运行时配置（C6 决策体现）：URL/secret 从 system_config 表读，
//     启动时缓存一份（带短 TTL），admin 改配置后下一次推送前自动 reload
//   - 飞书签名校验：HMAC-SHA256(timestamp + "\n" + secret) → base64 → URL encode
//     （飞书官方加签算法；2022 年后机器人默认开启）
//   - 3 步重试：1s / 3s / 10s 指数退避，4xx 不重试
//   - 幂等：同 (alert_history_id, channel) 走 Redis SET NX，防重复发送
//   - URL 空时优雅降级：仅写日志，不报错（不影响告警主流程）
package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// ===== Config =====

// FeishuConfig 飞书机器人配置（从 system_config.Value 解析）
type FeishuConfig struct {
	URL    string `json:"url"`    // webhook URL（空 = 未配置）
	Secret string `json:"secret"` // 加签 secret（空 = 不加签）
}

// Card 飞书消息卡片（interactive card）
type Card struct {
	MsgType string   `json:"msg_type"` // "interactive"
	Card    CardBody `json:"card"`
}

// CardBody 卡片主体
type CardBody struct {
	Config   CardConfig    `json:"config,omitempty"`
	Header   CardHeader    `json:"header"`
	Elements []CardElement `json:"elements"`
}

// CardConfig 卡片配置：是否 @ 所有人
type CardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

// CardHeader 卡片头（决定颜色）
type CardHeader struct {
	Title CardTitle `json:"title"`
}

// CardTitle 标题
type CardTitle struct {
	Tag     string `json:"tag"`     // "plain_text"
	Content string `json:"content"` // 标题文本
}

// CardElement 卡片元素
type CardElement struct {
	Tag    string      `json:"tag"` // "div" / "hr" / "note"
	Text   *CardText   `json:"text,omitempty"`
	Note   *CardText   `json:"note,omitempty"`
	Href   *CardHref   `json:"href,omitempty"`
	Fields []CardField `json:"fields,omitempty"`
}

// CardText 文本节点
type CardText struct {
	Tag     string `json:"tag"` // "plain_text" / "lark_md"
	Content string `json:"content"`
}

// CardHref 链接
type CardHref struct {
	URL string `json:"url"`
}

// CardField 字段（key-value 表格）
type CardField struct {
	IsShort bool     `json:"is_short"`
	Text    CardText `json:"text"`
}

// SeverityColor severity → header 颜色（飞书 header 颜色：red/orange/yellow/blue/green）
func SeverityColor(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "red"
	case "high":
		return "orange"
	case "warning":
		return "yellow"
	case "info":
		return "blue"
	default:
		return "grey"
	}
}

// BuildAlertCard 构造告警卡片
//   - subjectType/channelId 决定卡片副标题
//   - subjectName / message / severity 决定内容
//   - ruleName 作为 header 标题
func BuildAlertCard(ruleName, severity, subjectType, subjectName, message string, ts int64) Card {
	tsStr := time.Unix(ts, 0).Format("2006-01-02 15:04:05")
	header := CardHeader{
		Title: CardTitle{
			Tag:     "plain_text",
			Content: fmt.Sprintf("[%s] %s", strings.ToUpper(severity), ruleName),
		},
	}
	elems := []CardElement{
		{
			Tag: "div",
			Fields: []CardField{
				{IsShort: true, Text: CardText{Tag: "lark_md", Content: "**主体**\n" + subjectName}},
				{IsShort: true, Text: CardText{Tag: "lark_md", Content: "**类型**\n" + subjectType}},
			},
		},
		{
			Tag:  "div",
			Text: &CardText{Tag: "lark_md", Content: "**详情**\n" + message},
		},
		{
			Tag: "hr",
		},
		{
			Tag:  "note",
			Note: &CardText{Tag: "plain_text", Content: "api-ops · " + tsStr},
		},
	}
	return Card{
		MsgType: "interactive",
		Card: CardBody{
			Config:   CardConfig{WideScreenMode: true},
			Header:   header,
			Elements: elems,
		},
	}
}

// ===== Notifier =====

// Notifier 飞书机器人推送器
type Notifier struct {
	cfg     *FeishuConfig // 缓存：feishu_webhook_alert 的 url/secret
	cfgKey  string        // system_config.key，默认 "feishu_webhook_alert"
	cfgTS   time.Time     // 缓存加载时间
	cfgTTL  time.Duration // 缓存 TTL（默认 30s）
	http    *http.Client
	channel string // 写入 alert_action 时的 channel 名（"feishu"）
}

// New 创建 Notifier（默认读 system_config['feishu_webhook_alert']）
func New() *Notifier {
	return &Notifier{
		cfgKey:  "feishu_webhook_alert",
		cfgTTL:  30 * time.Second,
		http:    &http.Client{Timeout: 8 * time.Second},
		channel: "feishu",
	}
}

// WithKey 修改配置 key（feishu_webhook_report 复用同一份实现）
func (n *Notifier) WithKey(key string) *Notifier {
	n.cfgKey = key
	return n
}

// loadConfig 从 system_config 读最新值，缓存 TTL 过期则刷新
// URL 为空返回 (cfg, false) —— 走降级
func (n *Notifier) loadConfig(ctx context.Context) (*FeishuConfig, bool) {
	now := time.Now()
	if n.cfg != nil && now.Sub(n.cfgTS) < n.cfgTTL {
		return n.cfg, n.cfg.URL != ""
	}

	if dal.OPS == nil {
		return nil, false
	}
	var row dal.SystemConfig
	err := dal.OPS.WithContext(ctx).Where("key = ?", n.cfgKey).First(&row).Error
	if err != nil {
		log.Printf("[notifier] load %s failed: %v", n.cfgKey, err)
		if n.cfg != nil {
			return n.cfg, n.cfg.URL != ""
		}
		return nil, false
	}

	cfg := &FeishuConfig{}
	if row.Value != "" {
		_ = json.Unmarshal([]byte(row.Value), cfg)
	}
	n.cfg = cfg
	n.cfgTS = now
	if cfg.URL == "" {
		log.Printf("[notifier] %s 未配置 url，发送会走降级（仅日志）", n.cfgKey)
	}
	return cfg, cfg.URL != ""
}

// Reload 强制清缓存（admin 改 config 后调用）
func (n *Notifier) Reload() {
	n.cfg = nil
	n.cfgTS = time.Time{}
}

// Send 发送一条告警到飞书
//   - alertID  : AlertHistory.ID（用于 alert_action 记录 + 幂等 key）
//   - ruleName : 告警规则名（卡片标题）
//   - severity : critical / high / warning / info
//   - subjectType / subjectName / message : 卡片正文
//   - ts : 告警时间（unix 秒）
//
// 返回 (sent bool, err error)：
//   - sent=false, err=nil：URL 未配置（降级，仅写日志）
//   - sent=true,  err=nil：已发送（含重试成功）
//   - sent=true,  err!=nil：4xx 终态失败（不重试）
//   - sent=true,  err!=nil：5xx/网络 3 次重试都失败
func (n *Notifier) Send(ctx context.Context, alertID uint64, ruleName, severity, subjectType, subjectName, message string, ts int64) (bool, error) {
	cfg, ok := n.loadConfig(ctx)
	if !ok {
		log.Printf("[notifier] degraded: url empty, alert_id=%d rule=%s message=%s",
			alertID, ruleName, truncate(message, 80))
		return false, nil
	}

	// 幂等：Redis SET NX（key = feishu:send:{alert_id}）—— 避免评估器重入导致重发
	if dal.RDB != nil {
		idemKey := fmt.Sprintf("feishu:send:%d", alertID)
		ok, err := dal.RDB.SetNX(ctx, idemKey, "1", 24*time.Hour).Result()
		if err == nil && !ok {
			log.Printf("[notifier] dedup: alert_id=%d already sent", alertID)
			return false, nil
		}
	}

	card := BuildAlertCard(ruleName, severity, subjectType, subjectName, message, ts)
	body, err := json.Marshal(card)
	if err != nil {
		return false, fmt.Errorf("marshal card: %w", err)
	}

	// 3 步重试：1s / 3s / 10s
	backoff := []time.Duration{0, 1 * time.Second, 3 * time.Second, 10 * time.Second}
	var lastErr error
	for i := 0; i < len(backoff); i++ {
		if backoff[i] > 0 {
			time.Sleep(backoff[i])
		}
		status, err := n.post(ctx, cfg, body)
		if err == nil && status >= 200 && status < 300 {
			log.Printf("[notifier] sent alert_id=%d status=%d", alertID, status)
			return true, nil
		}
		lastErr = err
		// 4xx 终态：飞书 / 客户端错误，不重试
		if status >= 400 && status < 500 {
			log.Printf("[notifier] 4xx stop: alert_id=%d status=%d err=%v", alertID, status, err)
			return true, fmt.Errorf("feishu 4xx: status=%d", status)
		}
		log.Printf("[notifier] retry: alert_id=%d attempt=%d status=%d err=%v", alertID, i+1, status, err)
	}
	return true, fmt.Errorf("feishu retries exhausted: %w", lastErr)
}

// post 真正 POST 到 webhook
func (n *Notifier) post(ctx context.Context, cfg *FeishuConfig, body []byte) (int, error) {
	fullURL := cfg.URL
	if cfg.Secret != "" {
		// 加签：timestamp = 当前秒，sign = base64(hmac-sha256(key=secret, data=timestamp+"\n"+secret))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sign := signFeishu(cfg.Secret, ts)
		fullURL = cfg.URL + "&timestamp=" + ts + "&sign=" + sign
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := n.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// signFeishu 飞书加签算法
//
//	sign = base64(hmac-sha256(key=secret, data=timestamp + "\n" + secret))
//	然后 URL-encode
func signFeishu(secret, ts string) string {
	stringToSign := ts + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
}

// ===== 通知入口（被 alert_engine 调） =====

// SendForAlert 把 AlertHistory 翻译成飞书 Card 并发送
//   - 同时 upsert 一条 alert_action 记录（status=sent / failed / degraded）
//   - 当 URL 未配置时记 status=degraded（不报错）
//   - 这是 alert_engine.firing 的回调
func (n *Notifier) SendForAlert(ctx context.Context, h *dal.AlertHistory) {
	ts := h.CreatedAt.Unix()
	if ts == 0 {
		ts = time.Now().Unix()
	}
	sent, err := n.Send(ctx, h.ID, h.RuleName, h.Severity, h.SubjectType, h.SubjectName, h.Message, ts)

	now := time.Now()
	action := &dal.AlertAction{
		AlertHistoryID: h.ID,
		Channel:        n.channel,
		Target:         "system_config:" + n.cfgKey,
	}
	switch {
	case !sent && err == nil:
		// 降级：URL 未配置
		action.Status = "degraded"
		action.Response = "feishu_webhook url empty, log-only"
	case err == nil:
		action.Status = "sent"
		action.SentAt = &now
		action.Response = "ok"
	default:
		action.Status = "failed"
		action.Error = err.Error()
		nowCopy := now
		action.SentAt = &nowCopy
	}
	_ = dal.CreateAlertAction(ctx, action)
}

// truncate 截断长字符串（仅日志用）
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
