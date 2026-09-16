ALTER TABLE topology_scopes
  ADD COLUMN projection_state VARCHAR(16) NOT NULL DEFAULT 'uninitialized',
  ADD COLUMN projection_epoch BIGINT NOT NULL DEFAULT 0,
  ADD CONSTRAINT ck_scope_projection_state CHECK (projection_state IN ('uninitialized', 'updating', 'ready', 'failed')),
  ADD CONSTRAINT ck_scope_projection_epoch CHECK (projection_epoch >= 0);
