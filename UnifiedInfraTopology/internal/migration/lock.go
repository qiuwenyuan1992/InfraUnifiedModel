package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

func applyMySQL(ctx context.Context, conn *sql.Conn, steps []migrationStep) (result error) {
	var database sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return fmt.Errorf("resolve migration database: %w", err)
	}
	if !database.Valid || database.String == "" {
		return fmt.Errorf("migration requires a selected target database")
	}
	// 锁名不暴露数据库名，且限制在 MySQL 的 64 字节上限内。
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(database.String)))
	lockName := "topology:" + digest[:48]
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", lockName, 30).Scan(&locked); err != nil {
		// 超时或断连时不能确认锁是否已获得，丢弃物理连接以免泄漏会话锁。
		discardConnection(conn)
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if !locked.Valid || locked.Int64 != 1 {
		return fmt.Errorf("migration lock unavailable; no schema changes applied")
	}
	defer func() {
		// 原请求取消后仍须在同一连接释放会话锁。
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released sql.NullInt64
		err := conn.QueryRowContext(cleanup, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
		if err != nil || !released.Valid || released.Int64 != 1 {
			discardConnection(conn)
			result = errors.Join(result, fmt.Errorf("release migration lock failed; connection discarded: %w", errors.Join(err, errors.New("lock ownership not confirmed"))))
		}
	}()
	// 失败标记必须立即持久化，不能依赖下一条 DDL 的隐式提交。
	var autocommit int
	if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.autocommit").Scan(&autocommit); err != nil {
		return fmt.Errorf("check migration autocommit: %w", err)
	}
	if autocommit != 1 {
		return fmt.Errorf("migration requires MySQL session autocommit=1; no schema changes applied")
	}
	return applyLocked(ctx, conn, "mysql", steps)
}

func applySQLite(ctx context.Context, conn *sql.Conn, steps []migrationStep) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	// IMMEDIATE 在读取账本前获取写锁，防止多个迁移进程同时判定版本缺失。
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("acquire SQLite migration lock: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
				discardConnection(conn)
			}
		}
	}()
	err := applyLocked(ctx, conn, "sqlite", steps)
	var rollbackErr *rollbackFailure
	if errors.As(err, &rollbackErr) {
		return fmt.Errorf("SQLite migration rollback failed; transaction will be discarded; manual recovery required: %w", err)
	}
	// 失败时 DDL 已回滚到保存点，但保留失败账本供人工检查。
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, commitErr := conn.ExecContext(cleanup, "COMMIT"); commitErr != nil {
		return fmt.Errorf("commit SQLite migration ledger failed: %w", errors.Join(err, commitErr))
	}
	committed = true
	return err
}

func discardConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
