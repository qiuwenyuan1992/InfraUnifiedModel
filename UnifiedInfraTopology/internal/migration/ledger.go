package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func verifyLedger(ctx context.Context, conn *sql.Conn, steps []migrationStep) (map[int]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version, name, checksum, state FROM topology_schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make(map[int]bool, len(steps))
	for rows.Next() {
		var version int
		var name, checksum, state string
		if err := rows.Scan(&version, &name, &checksum, &state); err != nil {
			return nil, fmt.Errorf("decode migration ledger: %w", err)
		}
		if version < 0 || version >= len(steps) {
			return nil, fmt.Errorf("unknown migration version %d; use the matching application release", version)
		}
		if checksum != steps[version].checksum || name != steps[version].name {
			return nil, fmt.Errorf("migration %d checksum/name mismatch; never edit previously applied SQL", version)
		}
		if state != "applied" {
			return nil, fmt.Errorf("migration %d is %s; manual recovery required: inspect schema and ledger before an operator-approved repair", version, state)
		}
		if version > 0 && !applied[version-1] {
			return nil, fmt.Errorf("migration ledger has a missing predecessor for version %d; manual recovery required", version)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read migration ledger rows: %w", err)
	}
	return applied, nil
}

func insertLedger(ctx context.Context, conn *sql.Conn, step migrationStep, state string) error {
	now := time.Now().UTC()
	var appliedAt any
	if state == "applied" {
		appliedAt = now
	}
	_, err := conn.ExecContext(ctx, "INSERT INTO topology_schema_migrations (version, name, checksum, state, started_at, applied_at) VALUES (?, ?, ?, ?, ?, ?)", step.version, step.name, step.checksum, state, now, appliedAt)
	return err
}
