CREATE TABLE inventory_state (
    id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    active_generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    projection_state VARCHAR(16) NOT NULL DEFAULT 'uninitialized',
    projection_epoch BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT ck_inventory_state_singleton CHECK (id = 1),
    CONSTRAINT ck_inventory_projection_state CHECK (projection_state IN ('uninitialized', 'updating', 'ready', 'failed')),
    CONSTRAINT ck_inventory_projection_epoch CHECK (projection_epoch >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
INSERT INTO inventory_state (id) VALUES (1);

CREATE TABLE sources (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    name VARCHAR(191) NOT NULL,
    adapter_kind VARCHAR(64) NOT NULL,
    config_ref VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL,
    UNIQUE KEY uq_source_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE sync_runs (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    status VARCHAR(32) NOT NULL,
    mode VARCHAR(32) NOT NULL,
    base_generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    request_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    requested_by VARCHAR(191) NOT NULL,
    cancel_requested_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    UNIQUE KEY uq_run_idempotency (idempotency_key),
    KEY ix_run_status (status, created_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE generations (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(32) NOT NULL,
    inventory_ready BOOLEAN NOT NULL,
    graph_ready BOOLEAN NOT NULL,
    routing_ready BOOLEAN NOT NULL,
    created_at DATETIME(6) NOT NULL,
    published_at DATETIME(6) NULL,
    UNIQUE KEY uq_generation_run (run_id),
    KEY ix_generation_published (published_at, id),
    CONSTRAINT fk_generation_run FOREIGN KEY (run_id) REFERENCES sync_runs (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE inventory_state ADD CONSTRAINT fk_inventory_active_generation
    FOREIGN KEY (active_generation_id) REFERENCES generations (id);
ALTER TABLE sync_runs ADD CONSTRAINT fk_run_base_generation
    FOREIGN KEY (base_generation_id) REFERENCES generations (id);
ALTER TABLE sync_runs ADD CONSTRAINT fk_run_generation
    FOREIGN KEY (generation_id) REFERENCES generations (id);

CREATE TABLE sync_run_sources (
    run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(32) NOT NULL,
    PRIMARY KEY (run_id, source_id),
    CONSTRAINT fk_run_source_run FOREIGN KEY (run_id) REFERENCES sync_runs (id),
    CONSTRAINT fk_run_source_source FOREIGN KEY (source_id) REFERENCES sources (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE entities (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    kind VARCHAR(32) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    KEY ix_entity_kind (kind, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE source_keys (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    key_hash BINARY(32) NOT NULL,
    object_type VARCHAR(64) NOT NULL,
    namespace VARCHAR(191) NOT NULL,
    native_id TEXT NOT NULL,
    UNIQUE KEY uq_source_key_hash (source_id, key_hash),
    CONSTRAINT fk_source_key_source FOREIGN KEY (source_id) REFERENCES sources (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE identity_bindings (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    source_key_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    incarnation BIGINT NOT NULL,
    entity_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    first_generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    retired_generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    UNIQUE KEY uq_binding_incarnation (source_key_id, incarnation),
    CONSTRAINT fk_binding_source_key FOREIGN KEY (source_key_id) REFERENCES source_keys (id),
    CONSTRAINT fk_binding_entity FOREIGN KEY (entity_id) REFERENCES entities (id),
    CONSTRAINT fk_binding_first_generation FOREIGN KEY (first_generation_id) REFERENCES generations (id),
    CONSTRAINT fk_binding_retired_generation FOREIGN KEY (retired_generation_id) REFERENCES generations (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE device_versions (
    generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    entity_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    name VARCHAR(255) NOT NULL,
    device_kind VARCHAR(64) NOT NULL,
    role VARCHAR(64) NOT NULL,
    serial_number VARCHAR(255) NULL,
    lifecycle VARCHAR(32) NOT NULL,
    resolution_status VARCHAR(32) NOT NULL,
    PRIMARY KEY (generation_id, entity_id),
    KEY ix_device_kind (generation_id, device_kind, entity_id),
    KEY ix_device_name (generation_id, name, entity_id),
    CONSTRAINT fk_device_generation FOREIGN KEY (generation_id) REFERENCES generations (id),
    CONSTRAINT fk_device_entity FOREIGN KEY (entity_id) REFERENCES entities (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE interface_versions (
    generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    entity_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    device_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    source_name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    interface_kind VARCHAR(32) NOT NULL,
    admin_state VARCHAR(32) NOT NULL,
    oper_state VARCHAR(32) NOT NULL,
    speed_bps BIGINT NULL,
    lifecycle VARCHAR(32) NOT NULL,
    resolution_status VARCHAR(32) NOT NULL,
    PRIMARY KEY (generation_id, entity_id),
    UNIQUE KEY uq_interface_device_entity (generation_id, device_id, entity_id),
    KEY ix_interface_device_name (generation_id, device_id, namespace, normalized_name),
    CONSTRAINT fk_interface_generation FOREIGN KEY (generation_id) REFERENCES generations (id),
    CONSTRAINT fk_interface_entity FOREIGN KEY (entity_id) REFERENCES entities (id),
    CONSTRAINT fk_interface_device FOREIGN KEY (generation_id, device_id) REFERENCES device_versions (generation_id, entity_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE address_versions (
    generation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    entity_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    device_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interface_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    address_family INTEGER NOT NULL,
    address BINARY(16) NOT NULL,
    prefix_length INTEGER NULL,
    address_scope_key VARCHAR(255) NOT NULL,
    scope_status VARCHAR(32) NOT NULL,
    purpose VARCHAR(64) NOT NULL,
    lifecycle VARCHAR(32) NOT NULL,
    resolution_status VARCHAR(32) NOT NULL,
    PRIMARY KEY (generation_id, entity_id),
    KEY ix_address_lookup (generation_id, address_scope_key, address_family, address),
    CONSTRAINT ck_address_family CHECK (address_family IN (4, 6)),
    CONSTRAINT ck_address_prefix CHECK (prefix_length IS NULL OR (prefix_length >= 0 AND prefix_length <= CASE address_family WHEN 4 THEN 32 ELSE 128 END)),
    CONSTRAINT fk_address_generation FOREIGN KEY (generation_id) REFERENCES generations (id),
    CONSTRAINT fk_address_entity FOREIGN KEY (entity_id) REFERENCES entities (id),
    CONSTRAINT fk_address_device FOREIGN KEY (generation_id, device_id) REFERENCES device_versions (generation_id, entity_id),
    CONSTRAINT fk_address_interface FOREIGN KEY (generation_id, device_id, interface_id) REFERENCES interface_versions (generation_id, device_id, entity_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
