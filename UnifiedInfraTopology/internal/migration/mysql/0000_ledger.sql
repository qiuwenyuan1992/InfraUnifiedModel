CREATE TABLE IF NOT EXISTS topology_schema_migrations (
    version BIGINT NOT NULL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(16) NOT NULL,
    started_at DATETIME(6) NOT NULL,
    applied_at DATETIME(6) NULL,
    error_detail VARCHAR(255) NULL,
    CONSTRAINT ck_migration_state CHECK (state IN ('dirty', 'failed', 'applied'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
