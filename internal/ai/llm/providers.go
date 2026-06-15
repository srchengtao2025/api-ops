package llm

import (
	"context"
	"fmt"
)

// GatewayProvider 通过 upstream 内部网关调用 LLM（默认；与 OpenAI Chat Completions 协议兼容）
type GatewayProvider struct{}

// Name 返回 vendor 名
func (g *GatewayProvider) Name() string { return "gateway" }

// Diagnose 通过 gateway 调 LLM
func (g *GatewayProvider) Diagnose(ctx context.Context, s ErrorSample) (*Diagnosis, error) {
	cfg := getLLMConfig(ctx)
	body := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "你是 SRE 助手，仅返回 JSON。"},
			{"role": "user", "content": BuildPrompt(s)},
		},
		"temperature": 0.2,
		"max_tokens":  800,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := PostJSON(ctx, cfg.BaseURL, cfg.APIKey, body, &resp); err != nil {
		return nil, fmt.Errorf("gateway call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("gateway: empty choices")
	}
	d, err := parseDiagnosisJSON(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	d.Source = "llm"
	d.Provider = "gateway"
	d.Tokens = resp.Usage.TotalTokens
	d.Pattern = s.Pattern
	return d, nil
}

// DirectOpenAI 直连 OpenAI（备选）
type DirectOpenAI struct{}

// Name 返回 vendor
func (d *DirectOpenAI) Name() string { return "openai" }

// Diagnose 调 OpenAI Chat Completions
func (d *DirectOpenAI) Diagnose(ctx context.Context, s ErrorSample) (*Diagnosis, error) {
	cfg := getLLMConfig(ctx)
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai api_key not configured")
	}
	body := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "你是 SRE 助手，仅返回 JSON。"},
			{"role": "user", "content": BuildPrompt(s)},
		},
		"temperature": 0.2,
		"max_tokens":  800,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := PostJSON(ctx, "https://api.openai.com/v1/chat/completions", cfg.APIKey, body, &resp); err != nil {
		return nil, fmt.Errorf("openai call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}
	diag, err := parseDiagnosisJSON(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	diag.Source = "llm"
	diag.Provider = "openai"
	diag.Tokens = resp.Usage.TotalTokens
	diag.Pattern = s.Pattern
	return diag, nil
}

// DirectAnthropic 直连 Anthropic（备选）
type DirectAnthropic struct{}

// Name 返回 vendor
func (d *DirectAnthropic) Name() string { return "anthropic" }

// Diagnose 调 Anthropic Messages API
func (d *DirectAnthropic) Diagnose(ctx context.Context, s ErrorSample) (*Diagnosis, error) {
	cfg := getLLMConfig(ctx)
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api_key not configured")
	}
	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 800,
		"messages": []map[string]string{
			{"role": "user", "content": BuildPrompt(s)},
		},
	}
	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := PostJSON(ctx, "https://api.anthropic.com/v1/messages", cfg.APIKey, body, &resp); err != nil {
		return nil, fmt.Errorf("anthropic call: %w", err)
	}
	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("anthropic: empty content")
	}
	diag, err := parseDiagnosisJSON(resp.Content[0].Text)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	diag.Source = "llm"
	diag.Provider = "anthropic"
	diag.Tokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	diag.Pattern = s.Pattern
	return diag, nil
}
