ALTER TABLE sync_runs ADD COLUMN lease_expires_at DATETIME NULL;

CREATE INDEX ix_run_lease ON sync_runs (status, lease_expires_at);
