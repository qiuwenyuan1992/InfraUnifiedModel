CREATE TABLE inventory_state (
    id INTEGER PRIMARY KEY NOT NULL CHECK (id = 1),
    active_generation_id TEXT NULL,
    projection_state TEXT NOT NULL DEFAULT 'uninitialized'
      CHECK (projection_state IN ('uninitialized', 'updating', 'ready', 'failed')),
    projection_epoch INTEGER NOT NULL DEFAULT 0 CHECK (projection_epoch >= 0),
    FOREIGN KEY (active_generation_id) REFERENCES generations (id)
);

CREATE TABLE sources (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    adapter_kind TEXT NOT NULL,
    config_ref TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    UNIQUE (name)
);

CREATE TABLE sync_runs (
    id TEXT PRIMARY KEY NOT NULL,
    status TEXT NOT NULL,
    mode TEXT NOT NULL,
    base_generation_id TEXT NULL,
    generation_id TEXT NULL,
    request_hash TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    cancel_requested_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    error_code TEXT NOT NULL DEFAULT '',
    UNIQUE (idempotency_key),
    FOREIGN KEY (base_generation_id) REFERENCES generations (id),
    FOREIGN KEY (generation_id) REFERENCES generations (id)
);
CREATE INDEX ix_run_status ON sync_runs (status, created_at, id);

CREATE TABLE generations (
    id TEXT PRIMARY KEY NOT NULL,
    run_id TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL,
    inventory_ready BOOLEAN NOT NULL,
    graph_ready BOOLEAN NOT NULL,
    routing_ready BOOLEAN NOT NULL,
    created_at DATETIME NOT NULL,
    published_at DATETIME NULL,
    FOREIGN KEY (run_id) REFERENCES sync_runs (id)
);
CREATE INDEX ix_generation_published ON generations (published_at, id);
INSERT INTO inventory_state (id) VALUES (1);

CREATE TABLE sync_run_sources (
    run_id TEXT NOT NULL REFERENCES sync_runs (id),
    source_id TEXT NOT NULL REFERENCES sources (id),
    status TEXT NOT NULL,
    PRIMARY KEY (run_id, source_id)
);

CREATE TABLE entities (
    id TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX ix_entity_kind ON entities (kind, id);

CREATE TABLE source_keys (
    id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources (id),
    key_hash BLOB NOT NULL CHECK (length(key_hash) = 32),
    object_type TEXT NOT NULL,
    namespace TEXT NOT NULL,
    native_id TEXT NOT NULL,
    UNIQUE (source_id, key_hash)
);

CREATE TABLE identity_bindings (
    id TEXT PRIMARY KEY NOT NULL,
    source_key_id TEXT NOT NULL REFERENCES source_keys (id),
    incarnation BIGINT NOT NULL,
    entity_id TEXT NOT NULL REFERENCES entities (id),
    first_generation_id TEXT NOT NULL REFERENCES generations (id),
    retired_generation_id TEXT NULL REFERENCES generations (id),
    UNIQUE (source_key_id, incarnation)
);

CREATE TABLE device_versions (
    generation_id TEXT NOT NULL REFERENCES generations (id),
    entity_id TEXT NOT NULL REFERENCES entities (id),
    name TEXT NOT NULL,
    device_kind TEXT NOT NULL,
    role TEXT NOT NULL,
    serial_number TEXT NULL,
    lifecycle TEXT NOT NULL,
    resolution_status TEXT NOT NULL,
    PRIMARY KEY (generation_id, entity_id)
);
CREATE INDEX ix_device_kind ON device_versions (generation_id, device_kind, entity_id);
CREATE INDEX ix_device_name ON device_versions (generation_id, name, entity_id);

CREATE TABLE interface_versions (
    generation_id TEXT NOT NULL REFERENCES generations (id),
    entity_id TEXT NOT NULL REFERENCES entities (id),
    device_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    source_name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    interface_kind TEXT NOT NULL,
    admin_state TEXT NOT NULL,
    oper_state TEXT NOT NULL,
    speed_bps BIGINT NULL,
    lifecycle TEXT NOT NULL,
    resolution_status TEXT NOT NULL,
    PRIMARY KEY (generation_id, entity_id),
    UNIQUE (generation_id, device_id, entity_id),
    FOREIGN KEY (generation_id, device_id) REFERENCES device_versions (generation_id, entity_id)
);
CREATE INDEX ix_interface_device_name ON interface_versions (generation_id, device_id, namespace, normalized_name);

CREATE TABLE address_versions (
    generation_id TEXT NOT NULL REFERENCES generations (id),
    entity_id TEXT NOT NULL REFERENCES entities (id),
    device_id TEXT NOT NULL,
    interface_id TEXT NULL,
    address_family INTEGER NOT NULL CHECK (address_family IN (4, 6)),
    address BLOB NOT NULL CHECK (length(address) = 16),
    prefix_length INTEGER NULL,
    address_scope_key TEXT NOT NULL,
    scope_status TEXT NOT NULL,
    purpose TEXT NOT NULL,
    lifecycle TEXT NOT NULL,
    resolution_status TEXT NOT NULL,
    PRIMARY KEY (generation_id, entity_id),
    CHECK (prefix_length IS NULL OR (prefix_length >= 0 AND prefix_length <= CASE address_family WHEN 4 THEN 32 ELSE 128 END)),
    FOREIGN KEY (generation_id, device_id) REFERENCES device_versions (generation_id, entity_id),
    FOREIGN KEY (generation_id, device_id, interface_id) REFERENCES interface_versions (generation_id, device_id, entity_id)
);
CREATE INDEX ix_address_lookup ON address_versions (generation_id, address_scope_key, address_family, address);
