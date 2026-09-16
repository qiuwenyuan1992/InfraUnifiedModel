CREATE TABLE IF NOT EXISTS topology_schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('dirty', 'failed', 'applied')),
    started_at DATETIME NOT NULL,
    applied_at DATETIME NULL,
    error_detail TEXT NULL
);
