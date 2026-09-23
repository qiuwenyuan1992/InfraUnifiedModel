package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

//go:embed mysql/*.sql sqlite/*.sql
var scripts embed.FS

type migrationStep struct {
	version  int
	name     string
	checksum string
	sql      string
}

// Apply 仅迁移调用者提供的目标数据库，不连接来源数据库，也不执行降级。
// MySQL DDL 不能事务回滚，失败后必须人工核对结构和账本，禁止自动重试。
func Apply(ctx context.Context, db *gorm.DB) error {
	dialect := db.Dialector.Name()
	if dialect != "mysql" && dialect != "sqlite" {
		return fmt.Errorf("unsupported dialect %q: migration supports only mysql and sqlite", dialect)
	}
	steps, err := loadSteps(dialect)
	if err != nil {
		return err
	}
	pool, err := db.DB()
	if err != nil {
		return fmt.Errorf("migration database: %w", err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin migration connection: %w", err)
	}
	defer conn.Close()
	if dialect == "sqlite" {
		return applySQLite(ctx, conn, steps)
	}
	return applyMySQL(ctx, conn, steps)
}

func loadSteps(dialect string) ([]migrationStep, error) {
	steps := make([]migrationStep, 0, 3)
	for version, name := range []string{"0000_ledger.sql", "0001_inventory.sql", "0002_sync_run_lease.sql"} {
		data, err := scripts.ReadFile(dialect + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration: %w", err)
		}
		steps = append(steps, migrationStep{version: version, name: name, checksum: fmt.Sprintf("%x", sha256.Sum256(data)), sql: string(data)})
	}
	return steps, nil
}

func applyLocked(ctx context.Context, conn *sql.Conn, dialect string, steps []migrationStep) error {
	if _, err := conn.ExecContext(ctx, steps[0].sql); err != nil {
		return fmt.Errorf("bootstrap migration ledger failed; manual recovery required: %w", err)
	}
	applied, err := verifyLedger(ctx, conn, steps)
	if err != nil {
		return err
	}
	if !applied[0] {
		if err := insertLedger(ctx, conn, steps[0], "applied"); err != nil {
			return fmt.Errorf("record bootstrap ledger; manual recovery required: %w", err)
		}
	}
	for _, step := range steps[1:] {
		if applied[step.version] {
			continue
		}
		if err := applyStep(ctx, conn, dialect, step); err != nil {
			return err
		}
	}
	return nil
}

func applyStep(ctx context.Context, conn *sql.Conn, dialect string, step migrationStep) error {
	if err := insertLedger(ctx, conn, step, "dirty"); err != nil {
		return fmt.Errorf("mark migration %d dirty; manual recovery may be required: %w", step.version, err)
	}
	if dialect == "sqlite" {
		if _, err := conn.ExecContext(ctx, "SAVEPOINT inventory_migration"); err != nil {
			return fmt.Errorf("create migration savepoint; manual recovery required: %w", err)
		}
	}
	// 内嵌脚本只有独立 DDL，不允许包含存储过程或分号字符串。
	statement := 0
	for _, query := range strings.Split(step.sql, ";") {
		if query = strings.TrimSpace(query); query == "" {
			continue
		}
		statement++
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return failStep(conn, dialect, step.version, fmt.Sprintf("statement %d failed", statement), err)
		}
	}
	if _, err := conn.ExecContext(ctx, "UPDATE topology_schema_migrations SET state = 'applied', applied_at = ?, error_detail = NULL WHERE version = ?", time.Now().UTC(), step.version); err != nil {
		return failStep(conn, dialect, step.version, "record applied state failed", err)
	}
	if dialect == "sqlite" {
		if _, err := conn.ExecContext(ctx, "RELEASE SAVEPOINT inventory_migration"); err != nil {
			return failStep(conn, dialect, step.version, "release migration savepoint failed", err)
		}
	}
	return nil
}

func failStep(conn *sql.Conn, dialect string, version int, detail string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if dialect == "sqlite" {
		if _, err := conn.ExecContext(ctx, "ROLLBACK TO SAVEPOINT inventory_migration"); err != nil {
			return &rollbackFailure{errors.Join(cause, err)}
		}
		if _, err := conn.ExecContext(ctx, "RELEASE SAVEPOINT inventory_migration"); err != nil {
			return &rollbackFailure{errors.Join(cause, err)}
		}
	}
	_, recordErr := conn.ExecContext(ctx, "UPDATE topology_schema_migrations SET state = 'failed', error_detail = ? WHERE version = ?", detail, version)
	return fmt.Errorf("migration %d failed (%s); manual recovery required: inspect schema and ledger before an operator-approved repair: %w", version, detail, errors.Join(cause, recordErr))
}

type rollbackFailure struct{ error }
