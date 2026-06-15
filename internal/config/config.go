// Package config 集中加载所有配置；任何模块都从这里读，不允许散落 os.Getenv
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config 全局唯一配置
type Config struct {
	// 服务
	Port     string
	GinMode  string
	LogLevel string

	// 自有 DB
	OpsDSN string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// upstream newapi 只读 DB
	UpstreamRoDSN string

	// upstream Admin API
	UpstreamAdminBaseURL string
	UpstreamAdminToken   string
	UpstreamAdminUserID  string // New-Api-User header 的值（admin token 默认对应 root user id=1）

	// 对账业务参数
	QuotaPerUnit    float64
	USDCNYRate      float64
	DisplayCurrency string

	// 上游价目导入
	UpstreamImportMaxRows int

	// 调度
	DailyStmtCron string

	// LLM (P3)
	UpstreamLLMBaseURL string
	UpstreamLLMAPIKey  string
	UpstreamLLMModel   string

	// A 阶段: 账号系统
	JWTSecret           string // JWT 签名密钥 (>= 32 字节, 推荐 64 字节 hex)
	AdminUsername       string // bootstrap admin 用户名 (默认 "admin")
	AdminPassword       string // bootstrap admin 密码 (启动时若用户表空则创建)
	AdminPasswordChange bool   // 启动时若 admin 已存在, 是否强制更新密码 (默认 false)

	// 通知
	DingtalkWebhook string
	FeishuWebhook   string
	WecomWebhook    string
	AdminEmail      string
	SMTPHost        string
	SMTPPort        int
	SMTPUser        string
	SMTPPassword    string
	// 安全配置 (P0-1 + P0-3 修)
	OpsAPIToken        string // Bearer token, 必填
	CORSAllowedOrigins string // 逗号分隔的 CORS 白名单
}

// C 全局配置实例
var C *Config

// Load 从 .env (可选) + 环境变量加载配置
func Load() (*Config, error) {
	// .env 可选：找不到不报错
	_ = godotenv.Load(".env")

	cfg := &Config{
		Port:                  getEnv("PORT", "8088"),
		GinMode:               getEnv("GIN_MODE", "release"),
		LogLevel:              getEnv("LOG_LEVEL", "info"),
		OpsDSN:                os.Getenv("OPS_DB_DSN"),
		RedisAddr:             getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:         os.Getenv("REDIS_PASSWORD"),
		RedisDB:               getEnvInt("REDIS_DB", 0),
		UpstreamRoDSN:         os.Getenv("API_OPS_RO_DSN"),
		UpstreamAdminBaseURL:  os.Getenv("upstream_ADMIN_BASE_URL"),
		UpstreamAdminToken:    os.Getenv("API_OPS_ADMIN_TOKEN"),
		UpstreamAdminUserID:   getEnv("upstream_ADMIN_USER_ID", "1"),
		QuotaPerUnit:          getEnvFloat("QUOTA_PER_UNIT", 500000),
		USDCNYRate:            getEnvFloat("USD_CNY_RATE", 7.20),
		DisplayCurrency:       strings.ToUpper(getEnv("DISPLAY_CURRENCY", "CNY")),
		UpstreamImportMaxRows: getEnvInt("UPSTREAM_IMPORT_MAX_ROWS", 50000),
		DailyStmtCron:         getEnv("DAILY_STMT_CRON", "0 2 * * *"),
		UpstreamLLMBaseURL:    os.Getenv("upstream_LLM_BASE_URL"),
		UpstreamLLMAPIKey:     os.Getenv("API_OPS_LLM_API_KEY"),
		UpstreamLLMModel:      getEnv("upstream_LLM_MODEL", "claude-sonnet-4-5"),
		DingtalkWebhook:       os.Getenv("DINGTALK_WEBHOOK"),
		FeishuWebhook:         os.Getenv("FEISHU_WEBHOOK"),
		WecomWebhook:          os.Getenv("WECOM_WEBHOOK"),
		AdminEmail:            os.Getenv("ADMIN_EMAIL"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              getEnvInt("SMTP_PORT", 587),
		SMTPUser:              os.Getenv("SMTP_USER"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		// 安全配置 (P0-1 + P0-3 修)
		OpsAPIToken:        os.Getenv("OPS_API_TOKEN"),
		CORSAllowedOrigins: os.Getenv("CORS_ALLOWED_ORIGINS"),
		// A 阶段: 账号系统
		JWTSecret:           getEnv("JWT_SECRET", os.Getenv("OPS_API_TOKEN")), // 默认复用 OPS_API_TOKEN (>=32 字符)
		AdminUsername:       getEnv("ADMIN_USERNAME", "admin"),
		AdminPassword:       os.Getenv("ADMIN_PASSWORD"),
		AdminPasswordChange: getEnv("ADMIN_PASSWORD_CHANGE", "false") == "true",
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	C = cfg
	return cfg, nil
}

// Validate 必填项校验
// 注意：API_OPS_RO_DSN 在 Phase2 demo 模式下可空（仅靠自有 DB + seed 数据运行），
// 此时 RO 连接会降级为 nil，相关 API 返回"newapi 未连接"提示。
func (c *Config) Validate() error {
	if c.OpsDSN == "" {
		return fmt.Errorf("OPS_DB_DSN 必填")
	}
	if c.UpstreamRoDSN == "" {
		// 允许空（demo / 离线模式）—— 不 fatal
		// return fmt.Errorf("API_OPS_RO_DSN 必填（upstream 库只读账号）")
	}
	if c.QuotaPerUnit <= 0 {
		return fmt.Errorf("QUOTA_PER_UNIT 必须 > 0")
	}
	// P0-1 修: API Bearer Token 必填 (强 token, 至少 32 字符)
	// 缺 token = 启动拒绝 (防止误用空 token 上生产)
	if c.OpsAPIToken == "" {
		return fmt.Errorf("OPS_API_TOKEN 必填 (P0-1: 强 Bearer token, 至少 32 字符)")
	}
	if len(c.OpsAPIToken) < 32 {
		return fmt.Errorf("OPS_API_TOKEN 太短 (当前 %d 字符, 至少 32 字符, 推荐 64 字符 hex)", len(c.OpsAPIToken))
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// QuotaToUSD 内部 quota → USD
func (c *Config) QuotaToUSD(quota int64) float64 {
	return float64(quota) / c.QuotaPerUnit
}

// USDToQuota USD → 内部 quota
func (c *Config) USDToQuota(usd float64) int64 {
	return int64(usd * c.QuotaPerUnit)
}

// FormatMoney 按配置币种格式化金额
func (c *Config) FormatMoney(usd float64) string {
	if c.DisplayCurrency == "CNY" {
		return fmt.Sprintf("¥%.2f", usd*c.USDCNYRate)
	}
	return fmt.Sprintf("$%.4f", usd)
}
