// cmd/seed 种子灌库入口：把因果边清单（draft 种子数据）经摄取接口灌入 OKB 库。
// 与 server 同一连接约定：OKB_DB_DRIVER=sqlite|mysql + OKB_MYSQL_DSN / OKB_SQLITE_PATH。
// 幂等：摄取内容寻址去重，重复执行返回 duplicate 不虚增。
package main

import (
	"fmt"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"UnifiedInfraOKB/internal/repository"
	"UnifiedInfraOKB/internal/seeder"
	"UnifiedInfraOKB/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "okb-seed:", err)
		os.Exit(1)
	}
}

func run() error {
	driver := envOr("OKB_DB_DRIVER", "sqlite")
	var db *gorm.DB
	var err error
	switch driver {
	case "mysql":
		dsn := os.Getenv("OKB_MYSQL_DSN")
		if dsn == "" {
			dsn = "root:123456@tcp(127.0.0.1:3306)/okb?charset=utf8mb4&parseTime=True&loc=Local"
		}
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	case "sqlite":
		db, err = gorm.Open(sqlite.Open(envOr("OKB_SQLITE_PATH", "file::memory:?cache=shared")), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	default:
		return fmt.Errorf("unknown driver %q (expect sqlite/mysql)", driver)
	}
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	store, err := repository.New(db, true)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	svc := service.New(store, nil, service.DefaultConfig())
	rep, err := seeder.Seed(svc)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	fmt.Printf("seed done (driver=%s): sources=%d new_anomalies=%d new_rules=%d new_mappings=%d\n",
		driver, rep.Sources, rep.Anomalies, rep.Rules, rep.Mappings)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
