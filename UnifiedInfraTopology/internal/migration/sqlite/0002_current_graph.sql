ALTER TABLE topology_scopes ADD COLUMN projection_state TEXT NOT NULL DEFAULT 'uninitialized'
  CHECK (projection_state IN ('uninitialized', 'updating', 'ready', 'failed'));
ALTER TABLE topology_scopes ADD COLUMN projection_epoch INTEGER NOT NULL DEFAULT 0 CHECK (projection_epoch >= 0);
