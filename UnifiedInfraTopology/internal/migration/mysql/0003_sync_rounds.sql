CREATE TABLE sync_rounds (
 id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 source_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 status VARCHAR(32) NOT NULL, mode VARCHAR(32) NOT NULL,
 request_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 idempotency_key VARCHAR(128) NOT NULL, requested_by VARCHAR(191) NOT NULL,
 created_at DATETIME(6) NOT NULL, started_at DATETIME(6) NULL,
 finished_at DATETIME(6) NULL, snapshot_at DATETIME(6) NULL,
 cancel_requested_at DATETIME(6) NULL, lease_expires_at DATETIME(6) NULL,
 error_code VARCHAR(64) NOT NULL DEFAULT '',
 UNIQUE KEY uq_round_idempotency (source_id, idempotency_key),
 UNIQUE KEY uq_round_source (id, source_id),
 KEY ix_round_status (status, created_at, id),
 KEY ix_round_source_status (source_id, status, created_at, id),
 CONSTRAINT fk_round_source FOREIGN KEY (source_id) REFERENCES sources(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE sync_round_phases (
 round_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 name VARCHAR(64) NOT NULL, position INTEGER NOT NULL, status VARCHAR(32) NOT NULL,
 run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
 started_at DATETIME(6) NULL, finished_at DATETIME(6) NULL,
 nodes BIGINT NOT NULL DEFAULT 0, edges BIGINT NOT NULL DEFAULT 0,
 objects BIGINT NOT NULL DEFAULT 0, skipped BIGINT NOT NULL DEFAULT 0,
 missing_endpoints BIGINT NOT NULL DEFAULT 0, error_code VARCHAR(64) NOT NULL DEFAULT '',
 PRIMARY KEY (round_id, name), UNIQUE KEY uq_round_position (round_id, position),
 UNIQUE KEY uq_round_phase_run (run_id),
 UNIQUE KEY uq_round_phase_binding (round_id, run_id),
 CONSTRAINT fk_phase_round FOREIGN KEY (round_id) REFERENCES sync_rounds(id),
 CONSTRAINT fk_phase_run FOREIGN KEY (run_id) REFERENCES sync_runs(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
ALTER TABLE sync_diagnostics DROP FOREIGN KEY fk_diagnostic_run;
ALTER TABLE sync_diagnostics MODIFY run_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL;
ALTER TABLE sync_diagnostics ADD CONSTRAINT fk_diagnostic_run FOREIGN KEY (run_id, source_id) REFERENCES sync_runs (id, source_id);
ALTER TABLE sync_diagnostics ADD round_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL;
ALTER TABLE sync_diagnostics ADD KEY ix_diagnostic_round (round_id, created_at, id);
ALTER TABLE sync_diagnostics ADD CONSTRAINT fk_diagnostic_round FOREIGN KEY (round_id, source_id) REFERENCES sync_rounds(id, source_id);
ALTER TABLE sync_diagnostics ADD CONSTRAINT fk_diagnostic_round_run FOREIGN KEY (round_id, run_id) REFERENCES sync_round_phases(round_id, run_id);
