-- When per-user traffic was last seen (unix; 0 = never). Written by the
-- traffic collector, so it survives restarts.
ALTER TABLE users ADD COLUMN last_active_at INTEGER NOT NULL DEFAULT 0;
