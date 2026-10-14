-- IP rules for the proxy inbounds: list = deny (blocklist) or allow
-- (allowlist, enforced only while the allowlist is switched on).
CREATE TABLE access_rules (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  list       TEXT    NOT NULL,
  cidr       TEXT    NOT NULL, -- normalised prefix, e.g. 203.0.113.7/32
  note       TEXT    NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL DEFAULT 0, -- unix; 0 = permanent
  created_at INTEGER NOT NULL,
  created_by TEXT    NOT NULL DEFAULT '',
  UNIQUE (list, cidr)
);
