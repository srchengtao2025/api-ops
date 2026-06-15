// api-ops 自有 DB 的 model 定义
// 与 newapi 完全隔离；只读账号无法访问这些表
package dal

import (
	"time"
)

// ===== 上游价目 =====

// UpstreamVendor 上游供应商（一家上游供应商 = 一份定期账单）
type UpstreamVendor struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Code         string    `gorm:"size:64;uniqueIndex" json:"code"` // 内部简称，如 "openai-cn"、"azure-eastus"
	Name         string    `gorm:"size:128" json:"name"`
	ContactName  string    `gorm:"size:64" json:"contact_name"`
	ContactPhone string    `gorm:"size:32" json:"contact_phone"`
	ContactEmail string    `gorm:"size:128" json:"contact_email"`
	BillingCycle string    `gorm:"size:16;default:'monthly'" json:"billing_cycle"` // monthly | weekly | custom
	Remark       string    `gorm:"type:text" json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (UpstreamVendor) TableName() string { return "upstream_vendors" }

// UpstreamPricing 已下线 (2026-06-14)
// v3 PR #2 之后, cost 反推改用 channel_vendor_map.discount, 价目表 0 引用
// 表移到 archive schema (migrations/2026-06-14-upstream-pricing-archive.sql)
// struct 删除, 9 行数据保留在 archive.upstream_pricing

// ChannelVendorMap 渠道 ↔ 上游供应商的映射关系 (1:1 关系, 一渠道对一供应商)
//
// 业务定义 (用户确认 2026-06-14):
//   - 一个供应商可以提供多个渠道配置 (1:N)
//   - 一个渠道归属于唯一一个供应商 (N:1)
//   - 表结构上 channel_id 加 UNIQUE 约束 (避免一渠道挂多供应商)
//
// 字段:
//   - discount: 该渠道的实际成本折扣 (0-1), 用于上游对账
//   - auto_discount: 自动从渠道名解析出的折扣 (不可改, 留 audit)
//   - auto_matched: 解析匹配到的字符串 (如 "42折" / "0.06折")
//   - auto_recognized: 解析是否成功 (false = 需人工矫正)
//   - discount_override: 人工矫正过的标记 (true 时 final_discount 优先用 discount 字段)
type ChannelVendorMap struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	ChannelID  int    `gorm:"uniqueIndex:idx_channel_id;not null" json:"channel_id"`
	VendorCode string `gorm:"size:64;not null" json:"vendor_code"`

	// ===== discount 字段 =====
	// final discount (实际用的折扣, 0-1)
	Discount float64 `gorm:"type:numeric(5,4);default:1.0" json:"discount"`
	// 自动从渠道名解析的折扣 (只读, 留 audit 用)
	AutoDiscount float64 `gorm:"type:numeric(5,4);default:1.0" json:"auto_discount"`
	// 解析匹配到的字符串 (如 "42折" / "0.06 折")
	AutoMatched string `gorm:"size:64" json:"auto_matched"`
	// 解析是否成功 (false = 需人工矫正, UI 显示 ⚠️)
	AutoRecognized bool `gorm:"default:false" json:"auto_recognized"`
	// 人工矫正过 (true → discount 是手填的, 不能被 auto 覆盖)
	DiscountOverride bool `gorm:"default:false" json:"discount_override"`

	// 人工备注 (矫正原因 / 特殊说明)
	Remark    string    `gorm:"type:text" json:"remark"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ChannelVendorMap) TableName() string { return "channel_vendor_map" }

// ===== 对账单 =====

// BillingStatement 对账单主表（下游客户账单 & 上游供应商账单共用）
type BillingStatement struct {
	ID            uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	StatementType string `gorm:"size:20;index" json:"statement_type"` // customer | upstream
	// 下游 / 上游主体
	SubjectType string `gorm:"size:20;index" json:"subject_type"` // user | channel | vendor
	SubjectID   string `gorm:"size:64;index" json:"subject_id"`   // username / channel id / vendor code
	SubjectName string `gorm:"size:128" json:"subject_name"`
	// 账单周期
	PeriodStart int64 `gorm:"index" json:"period_start"`
	PeriodEnd   int64 `gorm:"index" json:"period_end"`
	// 金额（USD，原始货币）
	Revenue    float64 `gorm:"type:numeric(20,8);default:0" json:"revenue"`    // 客户视角：客户实付
	Cost       float64 `gorm:"type:numeric(20,8);default:0" json:"cost"`       // 上游视角：上游成本
	Profit     float64 `gorm:"type:numeric(20,8);default:0" json:"profit"`     // 利润 (revenue - cost)
	ProfitRate float64 `gorm:"type:numeric(8,6);default:0" json:"profit_rate"` // 利润率
	// 统计
	RequestCount     int64 `gorm:"default:0" json:"request_count"`
	ErrorCount       int64 `gorm:"default:0" json:"error_count"`
	RefundCount      int64 `gorm:"default:0" json:"refund_count"`
	PromptTokens     int64 `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens int64 `gorm:"default:0" json:"completion_tokens"`
	CacheTokens      int64 `gorm:"default:0" json:"cache_tokens"`
	// 元数据
	Status      string     `gorm:"size:16;default:'draft'" json:"status"` // draft | confirmed | exported
	GeneratedAt time.Time  `gorm:"autoCreateTime" json:"generated_at"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	ConfirmedBy string     `gorm:"size:64" json:"confirmed_by"`
	ExportedAt  *time.Time `json:"exported_at"`
	Remark      string     `gorm:"type:text" json:"remark"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (BillingStatement) TableName() string { return "billing_statements" }

// BillingStatementLine 对账单明细行（按模型维度展开）
type BillingStatementLine struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	StatementID uint64 `gorm:"index;not null" json:"statement_id"`
	ModelName   string `gorm:"size:128;index" json:"model_name"`
	Group       string `gorm:"size:64" json:"group"`
	ChannelID   int    `gorm:"index" json:"channel_id"`
	ChannelName string `gorm:"size:128" json:"channel_name"`
	VendorCode  string `gorm:"size:64" json:"vendor_code"`
	// 量
	RequestCount     int64 `gorm:"default:0" json:"request_count"`
	ErrorCount       int64 `gorm:"default:0" json:"error_count"`
	PromptTokens     int64 `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens int64 `gorm:"default:0" json:"completion_tokens"`
	CacheTokens      int64 `gorm:"default:0" json:"cache_tokens"`
	// 客户视角
	RevenueUSD float64 `gorm:"type:numeric(20,8);default:0" json:"revenue_usd"`
	// 上游视角
	CostUSD float64 `gorm:"type:numeric(20,8);default:0" json:"cost_usd"`
	// 利润
	ProfitUSD float64 `gorm:"type:numeric(20,8);default:0" json:"profit_usd"`
	// 利润率
	ProfitRate float64   `gorm:"type:numeric(8,6);default:0" json:"profit_rate"`
	CreatedAt  time.Time `json:"created_at"`
}

func (BillingStatementLine) TableName() string { return "billing_statement_lines" }

// ===== 导入记录 (已下线 2026-06-14) =====
// UpstreamPricingImport 表移到 archive schema (migrations/2026-06-14-upstream-pricing-archive.sql)
// struct 删除, 0 行数据保留在 archive.upstream_pricing_imports

// ===== 渠道健康度（P1 监控引擎） =====

// ChannelHealth5min 渠道 5min 滑窗聚合（每 1min roll 一条）
// 命中策略：UNIQUE(channel_id, bucket_ts) → ON CONFLICT DO UPDATE
type ChannelHealth5min struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ChannelID        int       `gorm:"uniqueIndex:idx_ch5_ch_bucket,priority:1;not null" json:"channel_id"`
	BucketTS         int64     `gorm:"uniqueIndex:idx_ch5_ch_bucket,priority:2;not null" json:"bucket_ts"`
	RequestCount     int64     `gorm:"default:0" json:"request_count"`
	ErrorCount       int64     `gorm:"default:0" json:"error_count"`
	SuccessCount     int64     `gorm:"default:0" json:"success_count"`
	PromptTokens     int64     `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens int64     `gorm:"default:0" json:"completion_tokens"`
	P50LatencyMs     int       `gorm:"default:0" json:"p50_latency_ms"`
	P95LatencyMs     int       `gorm:"default:0" json:"p95_latency_ms"`
	P99LatencyMs     int       `gorm:"default:0" json:"p99_latency_ms"`
	TTFTP95Ms        int       `gorm:"default:0" json:"ttft_p95_ms"`
	ErrorRate        float64   `gorm:"type:numeric(6,4);default:0" json:"error_rate"`
	Balance          float64   `gorm:"type:numeric(20,8);default:0" json:"balance"`
	Status           string    `gorm:"size:16;default:'enabled'" json:"status"` // enabled / manual_disabled / auto_disabled
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (ChannelHealth5min) TableName() string { return "channel_health_5min" }

// ChannelHealth1h 渠道 1h 聚合（每 5min roll 一条，存 1 年）
type ChannelHealth1h struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ChannelID        int       `gorm:"uniqueIndex:idx_ch1h_ch_bucket,priority:1;not null" json:"channel_id"`
	BucketTS         int64     `gorm:"uniqueIndex:idx_ch1h_ch_bucket,priority:2;not null" json:"bucket_ts"`
	RequestCount     int64     `gorm:"default:0" json:"request_count"`
	ErrorCount       int64     `gorm:"default:0" json:"error_count"`
	SuccessCount     int64     `gorm:"default:0" json:"success_count"`
	PromptTokens     int64     `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens int64     `gorm:"default:0" json:"completion_tokens"`
	P50LatencyMs     int       `gorm:"default:0" json:"p50_latency_ms"`
	P95LatencyMs     int       `gorm:"default:0" json:"p95_latency_ms"`
	P99LatencyMs     int       `gorm:"default:0" json:"p99_latency_ms"`
	TTFTP95Ms        int       `gorm:"default:0" json:"ttft_p95_ms"`
	ErrorRate        float64   `gorm:"type:numeric(6,4);default:0" json:"error_rate"`
	Balance          float64   `gorm:"type:numeric(20,8);default:0" json:"balance"`
	Status           string    `gorm:"size:16;default:'enabled'" json:"status"`
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (ChannelHealth1h) TableName() string { return "channel_health_1h" }

// ===== 告警 / 通知（P1 启用） =====

// AlertRule 告警规则（YAML 驱动，DB 存查询 + 完整 yaml 字符串）
type AlertRule struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name           string    `gorm:"size:128;not null" json:"name"`
	Type           string    `gorm:"size:32;index" json:"type"`                 // channel_error_rate | user_consecutive_error | balance_low | p95_degraded | ...
	Target         string    `gorm:"size:128" json:"target"`                    // 匹配规则（channel_id=10 / group=vip / all）
	Condition      string    `gorm:"size:255" json:"condition"`                 // 表达式 + 参数，如 ">0.20 window=5m duration=10m"
	Severity       string    `gorm:"size:16;default:'warning'" json:"severity"` // info | warning | high | critical
	NotifyChannels string    `gorm:"type:text" json:"notify_channels"`          // JSON 数组
	Actions        string    `gorm:"type:text" json:"actions"`                  // JSON 数组：notify_feishu / auto_disable_channel / ai_diagnose
	YAMLFull       string    `gorm:"type:text" json:"yaml_full"`                // 完整 YAML 字符串（前端可读）
	Enabled        bool      `gorm:"default:true" json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (AlertRule) TableName() string { return "alert_rules" }

// AlertHistory 告警历史（状态机：firing → acknowledged / resolved / suppressed / escalated）
type AlertHistory struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	RuleID        uint64     `gorm:"index" json:"rule_id"`
	RuleName      string     `gorm:"size:128" json:"rule_name"`
	Severity      string     `gorm:"size:16;index" json:"severity"`
	SubjectType   string     `gorm:"size:20;index" json:"subject_type"` // channel | user
	SubjectID     string     `gorm:"size:64;index" json:"subject_id"`
	SubjectName   string     `gorm:"size:128" json:"subject_name"`
	Message       string     `gorm:"type:text" json:"message"`
	Status        string     `gorm:"size:16;default:'firing';index" json:"status"` // firing | acknowledged | resolved | suppressed | escalated
	NotifiedAt    *time.Time `json:"notified_at"`
	AckedAt       *time.Time `json:"acked_at"`
	AckedBy       string     `gorm:"size:64" json:"acked_by"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	AIDiagnosisID *uint64    `json:"ai_diagnosis_id"`
	CreatedAt     time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
}

func (AlertHistory) TableName() string { return "alert_histories" }

// AlertAction 单条告警的通知动作（飞书 / 钉钉 / 邮件 / 内部日志）
type AlertAction struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	AlertHistoryID uint64     `gorm:"index;not null" json:"alert_history_id"`
	Channel        string     `gorm:"size:32;not null" json:"channel"` // feishu / dingtalk / email / log
	Target         string     `gorm:"size:255" json:"target"`
	Status         string     `gorm:"size:16;default:'pending'" json:"status"` // pending / sent / failed
	SentAt         *time.Time `json:"sent_at"`
	AckedAt        *time.Time `json:"acked_at"`
	AckedBy        string     `gorm:"size:64" json:"acked_by"`
	Response       string     `gorm:"type:text" json:"response"`
	Error          string     `gorm:"type:text" json:"error"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (AlertAction) TableName() string { return "alert_actions" }

// ===== AI 报告（P3 启用） =====

// AIReport 报告存档
type AIReport struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ReportType  string    `gorm:"size:32;index" json:"report_type"` // error_analysis | weekly_summary | customer_health
	PeriodStart int64     `gorm:"index" json:"period_start"`
	PeriodEnd   int64     `gorm:"index" json:"period_end"`
	SubjectType string    `gorm:"size:20" json:"subject_type"`
	SubjectID   string    `gorm:"size:64" json:"subject_id"`
	Title       string    `gorm:"size:255" json:"title"`
	Content     string    `gorm:"type:text" json:"content"`  // Markdown
	Metadata    string    `gorm:"type:text" json:"metadata"` // JSON
	GeneratedAt time.Time `gorm:"autoCreateTime" json:"generated_at"`
}

func (AIReport) TableName() string { return "ai_reports" }

// AIErrorCluster 错误聚类（每小时 roll 一次；pattern 已归一化 UUID/timestamp/digit）
type AIErrorCluster struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Pattern       string    `gorm:"size:512;uniqueIndex:idx_aiec_pat_ch_model,priority:1" json:"pattern"`
	ChannelID     int       `gorm:"uniqueIndex:idx_aiec_pat_ch_model,priority:2" json:"channel_id"`
	ModelName     string    `gorm:"size:128;uniqueIndex:idx_aiec_pat_ch_model,priority:3" json:"model_name"`
	WindowStart   int64     `gorm:"index" json:"window_start"`
	WindowEnd     int64     `gorm:"index" json:"window_end"`
	Count         int64     `gorm:"default:0" json:"count"`
	SampleContent string    `gorm:"type:text" json:"sample_content"` // 取首条原始 content（不归一化）
	AffectedUsers string    `gorm:"type:text" json:"affected_users"` // JSON 数组
	DiagnosisID   *uint64   `json:"diagnosis_id"`                    // 关联 ai_diagnoses
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (AIErrorCluster) TableName() string { return "ai_error_clusters" }

// AIDiagnosis 错误诊断结果（KB 命中 or LLM 生成；confidence ∈ [0, 1]）
type AIDiagnosis struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Pattern        string    `gorm:"size:512;index" json:"pattern"`
	ChannelID      int       `gorm:"index" json:"channel_id"`
	ModelName      string    `gorm:"size:128" json:"model_name"`
	Source         string    `gorm:"size:16;index" json:"source"` // kb | llm | kb_fallback
	Confidence     float64   `gorm:"type:numeric(4,3);default:0" json:"confidence"`
	Category       string    `gorm:"size:32" json:"category"`
	Severity       string    `gorm:"size:16" json:"severity"`
	RootCause      string    `gorm:"type:text" json:"root_cause"`
	Action         string    `gorm:"type:text" json:"action"`
	DocURL         string    `gorm:"type:text" json:"doc_url"`
	KBEntryID      *uint64   `json:"kb_entry_id"`                 // 命中 KB 时填
	LLMProvider    string    `gorm:"size:32" json:"llm_provider"` // openai / anthropic / empty
	LLMTokens      int       `gorm:"default:0" json:"llm_tokens"`
	RawLLMResponse string    `gorm:"type:text" json:"raw_llm_response"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (AIDiagnosis) TableName() string { return "ai_diagnoses" }

// ===== 客户分级（PRD §9.1.9） =====

// UserTier 客户分级映射（关联 newapi.users.id）
type UserTier struct {
	UserID       uint64    `gorm:"primaryKey" json:"user_id"`
	UserName     string    `gorm:"size:128;not null;default:''" json:"user_name"`
	Tier         string    `gorm:"size:16;not null;default:'normal';index" json:"tier"` // normal / vip-1 / vip-2 / vip-3 / svip
	TierReason   string    `gorm:"size:64" json:"tier_reason"`
	Spend30d     float64   `gorm:"type:numeric(18,6);default:0" json:"spend_30d"`
	CallCount30d int64     `gorm:"default:0" json:"call_count_30d"`
	HealthScore  int       `gorm:"default:100" json:"health_score"`
	AssignedBy   string    `gorm:"size:64;default:'auto'" json:"assigned_by"`
	AssignedAt   time.Time `gorm:"autoCreateTime" json:"assigned_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (UserTier) TableName() string { return "user_tier" }

// TierThreshold 分级阈值配置
type TierThreshold struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Tier        string    `gorm:"size:16;not null;uniqueIndex" json:"tier"`
	MinSpend30d float64   `gorm:"type:numeric(18,6);default:0" json:"min_spend_30d"`
	MinCalls30d int64     `gorm:"default:0" json:"min_calls_30d"`
	Description string    `gorm:"type:text" json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (TierThreshold) TableName() string { return "tier_threshold" }

// ===== 错误知识库（PRD §9.3.5） =====

// ErrorKBEntry 上游错误码知识库
type ErrorKBEntry struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Vendor    string    `gorm:"size:32;not null;index" json:"vendor"` // aws_bedrock / provider_gamma / openai / anthropic / gemini
	ErrorCode string    `gorm:"size:64;not null" json:"error_code"`
	Patterns  string    `gorm:"type:text" json:"patterns"` // JSON 数组字符串：关键词列表
	Category  string    `gorm:"size:32;not null" json:"category"`
	Severity  string    `gorm:"size:16;not null" json:"severity"`
	RootCause string    `gorm:"type:text;not null" json:"root_cause"`
	Action    string    `gorm:"type:text;not null" json:"action"`
	DocURL    string    `gorm:"type:text" json:"doc_url"`
	Source    string    `gorm:"size:64;default:'manual'" json:"source"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ErrorKBEntry) TableName() string { return "error_kb_entries" }

// ===== 运行时配置（PRD §9.4.2 / Q-C6） =====

// SystemConfig 运行时可修改配置（飞书 webhook / LLM 阈值等）
type SystemConfig struct {
	Key         string    `gorm:"primaryKey;size:128" json:"key"`
	Value       string    `gorm:"type:text;not null" json:"value"` // JSON 序列化
	Description string    `gorm:"type:text" json:"description"`
	UpdatedBy   string    `gorm:"size:64;default:'system'" json:"updated_by"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SystemConfig) TableName() string { return "system_config" }

// ===== 审计日志（PRD §11.2 / Q5） =====

// AuditLog 写操作审计日志（覆盖账单确认 / 价目删除 / 告警 ACK / AI 报告生成 / 客户封禁 / 配置变更等）
type AuditLog struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID         uint64    `gorm:"index" json:"user_id"`
	Username       string    `gorm:"size:64" json:"username"`
	Action         string    `gorm:"size:128;index" json:"action"`       // "post billing.customer.statements.:id.confirm"
	ResourceType   string    `gorm:"size:64;index" json:"resource_type"` // "billing" / "vendors" / "monitor" / ...
	ResourceID     string    `gorm:"size:64;index" json:"resource_id"`   // 路径里的 ID
	Method         string    `gorm:"size:10" json:"method"`              // POST / PUT / DELETE / PATCH
	Path           string    `gorm:"size:255;index" json:"path"`
	IP             string    `gorm:"size:64" json:"ip"`
	UserAgent      string    `gorm:"size:255" json:"user_agent"`
	RequestBody    string    `gorm:"type:text" json:"request_body"` // 已截断到 1KB
	ResponseStatus int       `gorm:"default:0" json:"response_status"`
	DurationMs     int       `gorm:"default:0" json:"duration_ms"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_logs" }

// ===== Ops 内部用户（账号系统） =====
//
// 10 人内部系统，独立于 newapi users。3 角色：
//   - admin   : 全部权限 (含改 vendor 价目 / user 管理)
//   - finance : 月对账 + 报表 (只读 vendor 价目)
//   - viewer  : 只读 dashboard + 监控
//
// 不做资源级权限 (单租户，所有人看全量数据)。
// 密码 bcrypt cost=10, JWT 24h, 撤销靠 password_changed_at + DB 短查。
type OpsUserRole string

const (
	OpsUserRoleAdmin   OpsUserRole = "admin"
	OpsUserRoleFinance OpsUserRole = "finance"
	OpsUserRoleViewer  OpsUserRole = "viewer"
)

type OpsUserStatus int

const (
	OpsUserStatusActive  OpsUserStatus = 1
	OpsUserStatusLocked  OpsUserStatus = 0 // 0 也表示 active (历史迁移)
	OpsUserStatusDeleted OpsUserStatus = -1
)

type OpsUser struct {
	ID                uint64        `gorm:"primaryKey;autoIncrement" json:"id"`
	Username          string        `gorm:"size:64;uniqueIndex" json:"username"`
	PasswordHash      string        `gorm:"type:text" json:"-"` // 不暴露
	DisplayName       string        `gorm:"size:128" json:"display_name"`
	Email             string        `gorm:"size:128" json:"email"`
	Role              OpsUserRole   `gorm:"size:16;default:'viewer'" json:"role"`
	Status            OpsUserStatus `gorm:"default:1" json:"status"`
	PasswordChangedAt int64         `gorm:"default:0" json:"password_changed_at"` // unix 秒, JWT 签发时间 < 此值则 token 失效
	LastLoginAt       int64         `gorm:"default:0" json:"last_login_at"`
	Remark            string        `gorm:"type:text" json:"remark"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

func (OpsUser) TableName() string { return "ops_users" }

// OpsUserSession 短查询表，记录有效 JWT jti（可选，MVP 不上）
// (暂时用 password_changed_at 单字段撤销，省一张表)

// AllOpsTables 返回所有需要迁移的 ops 表（用于 initOPS 时 AutoMigrate）
func AllOpsTables() []interface{} {
	return []interface{}{
		&OpsUser{},
		&UpstreamVendor{},
		// &UpstreamPricing{}, // 2026-06-14 下线, 表移到 archive
		&ChannelVendorMap{},
		&BillingStatement{},
		&BillingStatementLine{},
		// &UpstreamPricingImport{}, // 2026-06-14 下线, 表移到 archive
		&AlertRule{},
		&AlertHistory{},
		&AlertAction{},
		&ChannelHealth5min{},
		&ChannelHealth1h{},
		&AIReport{},
		&AIErrorCluster{},
		&AIDiagnosis{},
		&UserTier{},
		&TierThreshold{},
		&ErrorKBEntry{},
		&SystemConfig{},
		&AuditLog{},
		// newapi 表 cache（admin API 同步数据）
		&UpstreamChannelCache{},
		&UpstreamUserCache{},
		&UpstreamTokenCache{},
		// logs 摘要 cache（1min tick 从 RoDB 同步）
		&LogsSummary5min{},
		// logs 摘要 cache by-model（billing 对账用，1min tick 从 RoDB 同步）
		&LogsSummaryByModel5min{},
		// BILLING v3 上游对账 5min cache (scheduler 5min tick 写, handler/worker 优先读)
		&OpsUpstreamSummary5min{},
	}
}

// MigrateOps 在 OPS DB 上执行迁移
func MigrateOps() error {
	if OPS == nil {
		return ErrDBNotInitialized
	}
	return OPS.AutoMigrate(AllOpsTables()...)
}

// AllSeedTables 返回 demo 需要的 newapi 镜像表（仅 seed 场景使用，避免污染生产 schema）
// 这些表在生产中由 newapi DB 提供，只读账号访问；demo 阶段在 api_ops DB 内创建等价表
// 以便独立验证。
func AllSeedTables() []interface{} {
	return []interface{}{
		&UserMirror{},
		&ChannelMirror{},
		&LogMirror{},
	}
}

// ===== BILLING v2 异步导出任务 (2026-06-14 RFC) =====

// BillingExportTask 异步账单导出任务
// 状态机: pending → running → success/failed, 或 pending → cancelled
// 配合 internal/billing/export_worker.go 工作
//
// BILLING v3 (PR #4, 2026-06-14) 加 2 字段:
//   - Kind: 'customer' (v2) / 'upstream' (v3)
//   - VendorCode: v3 上游对账任务用, 客户对账任务为空
type BillingExportTask struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID     string     `gorm:"size:64;uniqueIndex;not null" json:"task_id"` // uuid, 暴露给前端
	UserID     int        `gorm:"not null" json:"user_id"`
	Username   string     `gorm:"size:64;not null" json:"username"` // 冗余, 列表展示
	Period     string     `gorm:"size:7;not null" json:"period"`    // '2026-05'
	Formats    string     `gorm:"size:32;not null" json:"formats"`  // 'html' / 'xlsx' / 'html,xlsx'
	Kind       string     `gorm:"size:16;not null;default:'customer';check:kind IN ('customer','upstream')" json:"kind"`
	VendorCode string     `gorm:"size:64" json:"vendor_code"`
	Status     string     `gorm:"size:16;not null;default:pending" json:"status"`
	Progress   int        `gorm:"default:0" json:"progress"`  // 0-100
	FilePath   string     `gorm:"type:text" json:"file_path"` // /data/billing-exports/{task_id}.zip
	FileSize   int64      `json:"file_size"`
	ErrorMsg   string     `gorm:"type:text" json:"error_msg"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `gorm:"not null;default:now()" json:"created_at"`
	Operator   string     `gorm:"size:64;not null" json:"operator"`
}

func (BillingExportTask) TableName() string { return "billing_export_tasks" }

// 任务进度日志 (可选, 调试用)
type BillingExportTaskLog struct {
	ID     uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID string    `gorm:"size:64;not null;index" json:"task_id"`
	TS     time.Time `gorm:"default:now()" json:"ts"`
	Level  string    `gorm:"size:8" json:"level"`
	Msg    string    `gorm:"type:text" json:"msg"`
}

func (BillingExportTaskLog) TableName() string { return "billing_export_task_logs" }

// MigrateSeed 在 OPS DB 上创建 seed 用的 mirror 表
// 生产环境不应调用（会与 newapi 镜像产生重复）
func MigrateSeed() error {
	if OPS == nil {
		return ErrDBNotInitialized
	}
	return OPS.AutoMigrate(AllSeedTables()...)
}

// ===== BILLING v3 上游对账 5min cache (2026-06-15) =====
//
// 用途: 月对账场景, 把 "vendor × period × 5min bucket" 的聚合成本/收入/利润预算到 cache,
// handler / worker 优先读 cache (5min 延迟可接受), cache miss 时 fallback 到 CalcUpstreamStatement 实时算.
//
// 数据流:
//
//	newapi logs (RoDB) ──(5min tick scheduler)──► ops_upstream_summary_5min (OPS)
//
// PRIMARY KEY: (vendor_code, period_label, ts_bucket) → UNIQUE 幂等 UPSERT
//   - vendor_code: 跟 upstream_vendors.code 关联
//   - period_label: 'current-month' (本月至今) | 'last-month' (上月完整)
//   - ts_bucket: 5min 对齐的 tick 时刻 (UTC)
//
// 保留期: 30 天 (跟 billing_export_tasks 一致), prune 由调用方在 tick 完成后调度
type OpsUpstreamSummary5min struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	VendorCode   string    `gorm:"size:64;uniqueIndex:idx_ops_upstream_vp_bucket,priority:1;not null" json:"vendor_code"`
	PeriodLabel  string    `gorm:"size:16;uniqueIndex:idx_ops_upstream_vp_bucket,priority:2;not null" json:"period_label"`
	PeriodStart  int64     `json:"period_start"` // unix 秒, 含
	PeriodEnd    int64     `json:"period_end"`   // unix 秒, 含
	RequestCount int64     `gorm:"default:0" json:"request_count"`
	Revenue      float64   `gorm:"type:numeric(20,8);default:0" json:"revenue"` // 客户消耗 USD
	Cost         float64   `gorm:"type:numeric(20,8);default:0" json:"cost"`    // 上游成本 USD (反推)
	Profit       float64   `gorm:"type:numeric(20,8);default:0" json:"profit"`  // revenue - cost
	TSBucket     time.Time `gorm:"uniqueIndex:idx_ops_upstream_vp_bucket,priority:3;not null" json:"ts_bucket"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (OpsUpstreamSummary5min) TableName() string { return "ops_upstream_summary_5min" }
