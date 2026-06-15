// Package dal: 数据访问层
// 设计原则：
//  1. roDB 只连接 upstream/newapi DB，账号在 PG 层是 SELECT only，本包只用 GORM Read 方法
//  2. opsDB 连接自有 api_ops 库，承载上游价目、对账单、告警、报告等
//  3. 任何写 upstream/newapi DB 的行为都会被 Linter/Review 拦截
package dal

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrDBNotInitialized 数据库未初始化错误
var ErrDBNotInitialized = errors.New("dal: db not initialized")

// ErrNoRoDB 真实 newapi DB 未配置 (API_OPS_RO_DSN 空)
// 业务上: 需要直连 newapi RDS 的 handler 应检查这个错误, 提示用户配 API_OPS_RO_DSN
// 不能再 fallback 到 OPS (影子表已清, 违反 3 数据源原则)
var ErrNoRoDB = errors.New("dal: upstream RO DSN not configured (set API_OPS_RO_DSN env to enable newapi direct queries)")

var (
	// RO 指向 upstream/newapi DB 的只读连接
	RO *gorm.DB
	// OPS 指向自有 api_ops DB 的读写连接
	OPS *gorm.DB
)

// SetOpsDBForTest 注入 OPS 测试用 DB (sqlite 内存库)
//
// 用于 internal/billing/*_test.go 集成测 (PR #6, 2026-06-14).
// 生产代码不应调用.
func SetOpsDBForTest(db *gorm.DB) { OPS = db }

// SetRoDBForTest 注入 RO 测试用 DB (sqlite 内存库)
//
// 用于 internal/billing/*_test.go 集成测 (PR #6, 2026-06-14).
// 生产代码不应调用.
func SetRoDBForTest(db *gorm.DB) { RO = db }

// Init 初始化两个 DB 连接
// 注意：RO 连接在 demo 模式（API_OPS_RO_DSN 空）下允许失败，仅记 warning。
func Init(cfg *config.Config) error {
	if err := initRO(cfg); err != nil {
		// RO 失败不 fatal（demo 模式），但记 warning
		log.Printf("[dal] WARN: init upstream ro failed (will run in demo / no-newapi mode): %v", err)
	}
	if err := initOPS(cfg); err != nil {
		return fmt.Errorf("init ops: %w", err)
	}
	return nil
}

func initRO(cfg *config.Config) error {
	// 空 DSN 直接跳过（demo 模式）
	if cfg.UpstreamRoDSN == "" {
		log.Println("[dal] upstream ro db skipped (API_OPS_RO_DSN empty = demo mode)")
		return nil
	}
	gormCfg := &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
	}
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  cfg.UpstreamRoDSN,
		PreferSimpleProtocol: true,
	}), gormCfg)
	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	RO = db
	log.Println("[dal] upstream ro db connected")
	return nil
}

func initOPS(cfg *config.Config) error {
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: cfg.OpsDSN,
	}), gormCfg)
	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	OPS = db
	log.Println("[dal] ops db connected")
	return nil
}

// Close 关闭所有连接
func Close() {
	if RO != nil {
		if sqlDB, err := RO.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	if OPS != nil {
		if sqlDB, err := OPS.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// roDB 返回只读连接；demo 模式下 RO 为 nil 时 fallback 到 OPS（自有 DB）
// RoDB 返回 upstream/newapi DB 的只读连接
//
// A 阶段 (2026-06-14) 行为修正:
//   - 之前: 没真 RoDB 时 fallback 到 OPS (api_ops 库, 影子表)
//   - 现在: 没真 RoDB 时 return nil
//   - 原因: 影子表 (logs / users / channels) 违反 3 数据源原则, 已 truncate
//   - 调用方应检查 nil, 提示用户配置 API_OPS_RO_DSN
//
// OPS (api_ops 本地库) 仍由 OPS() 访问, 专用于自有表 (ops_users / cache_logs_summary_* 等)
func RoDB() *gorm.DB {
	return RO
}

// HasRoDB 报告是否已配置真实 newapi 直连
func HasRoDB() bool {
	return RO != nil
}
