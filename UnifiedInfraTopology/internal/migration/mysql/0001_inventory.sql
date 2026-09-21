CREATE TABLE sources (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    name VARCHAR(191) NOT NULL,
    adapter_kind VARCHAR(64) NOT NULL,
    config_ref VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY uq_source_name (name),
    CONSTRAINT ck_source_enabled CHECK (enabled IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE sync_runs (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(32) NOT NULL,
    mode VARCHAR(32) NOT NULL,
    request_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    requested_by VARCHAR(191) NOT NULL,
    cancel_requested_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    UNIQUE KEY uq_run_idempotency (source_id, idempotency_key),
    UNIQUE KEY uq_run_source (id, source_id),
    KEY ix_run_status (status, created_at, id),
    KEY ix_run_source_status (source_id, status, created_at, id),
    CONSTRAINT fk_run_source FOREIGN KEY (source_id) REFERENCES sources (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE sync_checkpoints (
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    resource VARCHAR(64) NOT NULL,
    `cursor` TEXT NOT NULL,
    complete BOOLEAN NOT NULL DEFAULT FALSE,
    completed_at DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (source_id, resource),
    CONSTRAINT ck_checkpoint_complete CHECK (complete IN (0, 1)),
    CONSTRAINT fk_checkpoint_source FOREIGN KEY (source_id) REFERENCES sources (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE sync_diagnostics (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    resource VARCHAR(64) NOT NULL,
    severity VARCHAR(32) NOT NULL,
    code VARCHAR(64) NOT NULL,
    object_ref VARCHAR(255) NOT NULL DEFAULT '',
    field_path VARCHAR(255) NOT NULL DEFAULT '',
    detail TEXT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    KEY ix_diagnostic_run (run_id, created_at, id),
    KEY ix_diagnostic_source (source_id, resource, severity, created_at, id),
    CONSTRAINT fk_diagnostic_source FOREIGN KEY (source_id) REFERENCES sources (id),
    CONSTRAINT fk_diagnostic_run FOREIGN KEY (run_id, source_id) REFERENCES sync_runs (id, source_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE publications (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    version BIGINT NOT NULL,
    schema_version INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL,
    published_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    UNIQUE KEY uq_publication_source_version (source_id, version),
    UNIQUE KEY uq_publication_run (run_id),
    KEY ix_publication_source_status (source_id, status, version),
    CONSTRAINT ck_publication_version CHECK (version > 0),
    CONSTRAINT ck_publication_schema_version CHECK (schema_version > 0),
    CONSTRAINT fk_publication_source FOREIGN KEY (source_id) REFERENCES sources (id),
    CONSTRAINT fk_publication_run FOREIGN KEY (run_id, source_id) REFERENCES sync_runs (id, source_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
