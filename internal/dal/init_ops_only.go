// 仅初始化 OPS DB（用于 cmd/seed 等不需要 upstream RO 的命令）
// 现有 dal.Init 需要 UpstreamRoDSN，cmd/seed 是独立进程，写完 OPS 后即退出，
// 不需要 RO 连接。把 Init 拆出来可以避免在 seed 场景伪造一个空 RO_DSN。
package dal

import (
	"fmt"
	"log"
	"time"

	"github.com/api-ops/api-ops/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitOpsOnly 只初始化 OPS DB（跳过 RO）
func InitOpsOnly(cfg *config.Config) error {
	if cfg.OpsDSN == "" {
		return fmt.Errorf("OPS_DB_DSN 必填")
	}

	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: cfg.OpsDSN,
	}), gormCfg)
	if err != nil {
		return fmt.Errorf("open ops db: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetConnMaxLifetime(10 * time.Minute)

	OPS = db
	log.Println("[dal] ops db connected (seed mode)")
	return nil
}
