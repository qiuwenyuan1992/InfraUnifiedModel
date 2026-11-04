CREATE TABLE sync_rounds (
 id TEXT NOT NULL PRIMARY KEY, source_id TEXT NOT NULL REFERENCES sources(id),
 status TEXT NOT NULL, mode TEXT NOT NULL, request_hash TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, requested_by TEXT NOT NULL,
 created_at DATETIME NOT NULL, started_at DATETIME NULL, finished_at DATETIME NULL,
 snapshot_at DATETIME NULL, cancel_requested_at DATETIME NULL, lease_expires_at DATETIME NULL,
 error_code TEXT NOT NULL DEFAULT '', UNIQUE(source_id, idempotency_key), UNIQUE(id, source_id)
);
CREATE INDEX ix_round_status ON sync_rounds(status, created_at, id);
CREATE INDEX ix_round_source_status ON sync_rounds(source_id, status, created_at, id);
CREATE TABLE sync_round_phases (
 round_id TEXT NOT NULL REFERENCES sync_rounds(id), name TEXT NOT NULL,
 position INTEGER NOT NULL, status TEXT NOT NULL, run_id TEXT NULL REFERENCES sync_runs(id),
 started_at DATETIME NULL, finished_at DATETIME NULL,
 nodes BIGINT NOT NULL DEFAULT 0, edges BIGINT NOT NULL DEFAULT 0,
 objects BIGINT NOT NULL DEFAULT 0, skipped BIGINT NOT NULL DEFAULT 0,
 missing_endpoints BIGINT NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(round_id, name), UNIQUE(round_id, position), UNIQUE(run_id), UNIQUE(round_id, run_id)
);
CREATE TABLE sync_diagnostics_round_upgrade (
 id TEXT NOT NULL PRIMARY KEY, run_id TEXT NULL, source_id TEXT NOT NULL,
 resource TEXT NOT NULL, severity TEXT NOT NULL, code TEXT NOT NULL,
 object_ref TEXT NOT NULL DEFAULT '', field_path TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL, created_at DATETIME NOT NULL, round_id TEXT NULL REFERENCES sync_rounds(id),
 FOREIGN KEY(source_id) REFERENCES sources(id),
 FOREIGN KEY(run_id, source_id) REFERENCES sync_runs(id, source_id),
 FOREIGN KEY(round_id, source_id) REFERENCES sync_rounds(id, source_id),
 FOREIGN KEY(round_id, run_id) REFERENCES sync_round_phases(round_id, run_id)
);
INSERT INTO sync_diagnostics_round_upgrade(id,run_id,source_id,resource,severity,code,object_ref,field_path,detail,created_at)
 SELECT id,run_id,source_id,resource,severity,code,object_ref,field_path,detail,created_at FROM sync_diagnostics;
DROP TABLE sync_diagnostics;
ALTER TABLE sync_diagnostics_round_upgrade RENAME TO sync_diagnostics;
CREATE INDEX ix_diagnostic_run ON sync_diagnostics(run_id,created_at,id);
CREATE INDEX ix_diagnostic_source ON sync_diagnostics(source_id,resource,severity,created_at,id);
CREATE INDEX ix_diagnostic_round ON sync_diagnostics(round_id,created_at,id);
