// Package llm: LLM Provider 抽象层
//   - Provider interface：Diagnose(ctx, ErrorSample) → Diagnosis
//   - GatewayProvider / DirectOpenAI / DirectAnthropic
//   - Factory：读 system_config.ai_provider 字段创建，空/未配置 → return nil（降级走纯 KB）
//   - 5min Redis 缓存：key = ai:llm:{ch}:{model}:{pattern_hash}
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// ErrorSample 错误样本（Diagnose 输入）
type ErrorSample struct {
	Pattern       string `json:"pattern"`
	SampleContent string `json:"sample_content"`
	ChannelID     int    `json:"channel_id"`
	ModelName     string `json:"model_name"`
	Count         int64  `json:"count"`
}

// Diagnosis LLM 输出
type Diagnosis struct {
	Pattern    string  `json:"pattern"`
	Source     string  `json:"source"`     // kb | llm | kb_fallback
	Confidence float64 `json:"confidence"` // 0.0 - 1.0
	Category   string  `json:"category"`
	Severity   string  `json:"severity"`
	RootCause  string  `json:"root_cause"`
	Action     string  `json:"action"`
	DocURL     string  `json:"doc_url"`
	Tokens     int     `json:"tokens"`
	Provider   string  `json:"provider"`
}

// Provider LLM Provider 接口
type Provider interface {
	Name() string
	Diagnose(ctx context.Context, s ErrorSample) (*Diagnosis, error)
}

// Factory creates a Provider from system_config.ai_provider
//
//	"gateway" | "upstream" → GatewayProvider
//	"openai"   → DirectOpenAI
//	"anthropic"→ DirectAnthropic
//	"" / "none" / "kb_only" → return nil
func Factory(ctx context.Context) Provider {
	cfg, err := dal.GetSystemConfig(ctx, "ai_provider")
	if err != nil || cfg == nil {
		return nil
	}
	var v struct {
		Vendor string `json:"vendor"`
	}
	if err := json.Unmarshal([]byte(cfg.Value), &v); err != nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(v.Vendor)) {
	case "", "none", "kb_only", "empty":
		return nil
	case "gateway", "upstream_gateway", "upstream":
		return &GatewayProvider{}
	case "openai":
		return &DirectOpenAI{}
	case "anthropic":
		return &DirectAnthropic{}
	default:
		log.Printf("[llm] unknown ai_provider vendor=%q, fallback to KB-only", v.Vendor)
		return nil
	}
}

const cacheTTL = 5 * time.Minute

// CacheKey ai:llm:{ch}:{model}:{pattern_hash}
func CacheKey(s ErrorSample) string {
	h := sha256.Sum256([]byte(s.Pattern))
	m := s.ModelName
	m = strings.ReplaceAll(m, " ", "_")
	if len(m) > 32 {
		m = m[:32]
	}
	return fmt.Sprintf("ai:llm:%d:%s:%s", s.ChannelID, m, hex.EncodeToString(h[:8]))
}

// GetCache 读缓存
func GetCache(ctx context.Context, key string) (*Diagnosis, bool) {
	if dal.RDB == nil {
		return nil, false
	}
	v, err := dal.RDB.Get(ctx, key).Result()
	if err != nil {
		return nil, false
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(v), &d); err != nil {
		return nil, false
	}
	return &d, true
}

// SetCache 写缓存
func SetCache(ctx context.Context, key string, d *Diagnosis) {
	if dal.RDB == nil {
		return
	}
	b, _ := json.Marshal(d)
	_ = dal.RDB.Set(ctx, key, b, cacheTTL).Err()
}

// DiagnoseWithCache 先查缓存，miss 则调 Provider 并写回
func DiagnoseWithCache(ctx context.Context, p Provider, s ErrorSample) (*Diagnosis, error) {
	key := CacheKey(s)
	if d, ok := GetCache(ctx, key); ok {
		return d, nil
	}
	d, err := p.Diagnose(ctx, s)
	if err != nil {
		return nil, err
	}
	SetCache(ctx, key, d)
	return d, nil
}

// BuildPrompt 构造 LLM 提示词
func BuildPrompt(s ErrorSample) string {
	var b strings.Builder
	b.WriteString("你是资深 SRE/AI 网关运维工程师，分析下面归一化后的错误聚类，返回最可能的根因/严重度/处置建议。\n\n")
	fmt.Fprintf(&b, "pattern: %s\n", s.Pattern)
	if s.SampleContent != "" && s.SampleContent != s.Pattern {
		fmt.Fprintf(&b, "原始样例: %s\n", trunc(s.SampleContent, 500))
	}
	fmt.Fprintf(&b, "次数: %d\n", s.Count)
	if s.ChannelID > 0 {
		fmt.Fprintf(&b, "渠道: %d\n", s.ChannelID)
	}
	if s.ModelName != "" {
		fmt.Fprintf(&b, "模型: %s\n", s.ModelName)
	}
	b.WriteString("\n严格用 JSON 返回（不要其他文字）：\n")
	b.WriteString(`{"category":"限流|认证|客户端|服务端|计费|其他","severity":"info|warning|high|critical","confidence":0.0-1.0,"root_cause":"...","action":"1)... 2)... 3)...","doc_url":"https://..."}`)
	return b.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// PostJSON POST JSON 到 url，附带 Bearer token
func PostJSON(ctx context.Context, url, apiKey string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(buf))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// parseDiagnosisJSON 解析 LLM 返回的 JSON（容错：可能含 ```json 包裹）
func parseDiagnosisJSON(s string) (*Diagnosis, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var d Diagnosis
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// getLLMConfig 读 system_config.llm_config；找不到则用占位
func getLLMConfig(ctx context.Context) *llmConfig {
	row, err := dal.GetSystemConfig(ctx, "llm_config")
	if err == nil && row != nil && row.Value != "" {
		var c llmConfig
		if err := json.Unmarshal([]byte(row.Value), &c); err == nil && c.BaseURL != "" {
			if c.Model == "" {
				c.Model = "claude-sonnet-4-5"
			}
			return &c
		}
	}
	return &llmConfig{BaseURL: "https://api.upstream.cn/v1/chat/completions", Model: "claude-sonnet-4-5"}
}

type llmConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}
