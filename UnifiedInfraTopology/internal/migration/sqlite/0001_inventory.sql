CREATE TABLE sources (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    adapter_kind TEXT NOT NULL,
    config_ref TEXT NOT NULL,
    enabled BOOLEAN NOT NULL CHECK (enabled IN (0, 1)),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (name)
);

CREATE TABLE sync_runs (
    id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL,
    status TEXT NOT NULL,
    mode TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    cancel_requested_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    error_code TEXT NOT NULL DEFAULT '',
    UNIQUE (source_id, idempotency_key),
    UNIQUE (id, source_id),
    FOREIGN KEY (source_id) REFERENCES sources (id)
);
CREATE INDEX ix_run_status ON sync_runs (status, created_at, id);
CREATE INDEX ix_run_source_status ON sync_runs (source_id, status, created_at, id);

CREATE TABLE sync_checkpoints (
    source_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    `cursor` TEXT NOT NULL DEFAULT '',
    complete BOOLEAN NOT NULL DEFAULT FALSE CHECK (complete IN (0, 1)),
    completed_at DATETIME NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (source_id, resource),
    FOREIGN KEY (source_id) REFERENCES sources (id)
);

CREATE TABLE sync_diagnostics (
    id TEXT PRIMARY KEY NOT NULL,
    run_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    severity TEXT NOT NULL,
    code TEXT NOT NULL,
    object_ref TEXT NOT NULL DEFAULT '',
    field_path TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    FOREIGN KEY (source_id) REFERENCES sources (id),
    FOREIGN KEY (run_id, source_id) REFERENCES sync_runs (id, source_id)
);
CREATE INDEX ix_diagnostic_run ON sync_diagnostics (run_id, created_at, id);
CREATE INDEX ix_diagnostic_source ON sync_diagnostics (source_id, resource, severity, created_at, id);

CREATE TABLE publications (
    id TEXT PRIMARY KEY NOT NULL,
    run_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    status TEXT NOT NULL,
    published_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (source_id, version),
    UNIQUE (run_id),
    FOREIGN KEY (source_id) REFERENCES sources (id),
    FOREIGN KEY (run_id, source_id) REFERENCES sync_runs (id, source_id)
);
CREATE INDEX ix_publication_source_status ON publications (source_id, status, version);
