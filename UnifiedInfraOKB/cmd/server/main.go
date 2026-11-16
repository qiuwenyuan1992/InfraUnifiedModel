// cmd/server OKB HTTP API 入口。
// 驱动选择：OKB_DB_DRIVER=sqlite（默认，零依赖开发/测试）或 mysql（生产）。
// 配置全部经环境变量，凭据不进 Git；权限表 OKB_AUTHZ 格式：id:level,id:level。
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"UnifiedInfraOKB/internal/handler"
	"UnifiedInfraOKB/internal/repository"
	"UnifiedInfraOKB/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "okb-server:", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. 数据库连接（独立知识库，不与拓扑库共享）
	db, err := openDB(envOr("OKB_DB_DRIVER", "sqlite"))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	// 2. 建表（开发/测试；生产 MySQL 可先执行 migrations 脚本再以 OKB_AUTO_MIGRATE=false 启动）
	autoMigrate := envOr("OKB_AUTO_MIGRATE", "true") == "true"
	store, err := repository.New(db, autoMigrate)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	// 3. 权限表（服务端识别主体 → 动作权限等级；0=仅摘要 1=读原文 2=审核）
	authz := parseAuthz(os.Getenv("OKB_AUTHZ"))

	// 4. 装配 service（模型提取器：未配置真实模型时使用 FakeExtract，仅限开发）
	svc := service.New(store, nil, service.DefaultConfig())

	// 5. HTTP
	mux := http.NewServeMux()
	handler.New(svc, authz).Routes(mux)
	addr := envOr("OKB_LISTEN", ":18090")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		fmt.Printf("okb-server listening on %s (driver=%s, fake-extractor=on)\n", addr, envOr("OKB_DB_DRIVER", "sqlite"))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "listen:", err)
			os.Exit(1)
		}
	}()

	// 6. 优雅退出
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// openDB 独立知识库连接。
func openDB(driver string) (*gorm.DB, error) {
	switch driver {
	case "mysql":
		dsn := os.Getenv("OKB_MYSQL_DSN")
		if dsn == "" {
			// 开发默认：本机独立 okb 库（与拓扑库隔离）
			dsn = "root:123456@tcp(127.0.0.1:3306)/okb?charset=utf8mb4&parseTime=True&loc=Local"
		}
		return gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	case "sqlite":
		path := envOr("OKB_SQLITE_PATH", "file::memory:?cache=shared")
		return gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	default:
		return nil, fmt.Errorf("unknown driver %q (expect sqlite/mysql)", driver)
	}
}

// parseAuthz 解析 "id:level,id:level" 权限表；空串返回 nil（开发模式 level 1）。
func parseAuthz(raw string) map[string]int {
	if raw == "" {
		return nil
	}
	authz := map[string]int{}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(kv) != 2 {
			continue
		}
		if lv, err := strconv.Atoi(kv[1]); err == nil {
			authz[kv[0]] = lv
		}
	}
	return authz
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
