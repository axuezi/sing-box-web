CREATE TABLE users (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL UNIQUE,
  note         TEXT    NOT NULL DEFAULT '',
  enabled      INTEGER NOT NULL DEFAULT 1,
  inbounds     TEXT    NOT NULL DEFAULT '[]', -- JSON array of inbound tags
  password     TEXT    NOT NULL,              -- hysteria2, trojan, ...
  uuid         TEXT    NOT NULL,              -- vless, vmess, tuic, ...
  sub_token    TEXT    NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  last_sub_at  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE revisions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at   INTEGER NOT NULL,
  actor        TEXT    NOT NULL,
  source       TEXT    NOT NULL, -- panel | external | rollback | import
  summary      TEXT    NOT NULL DEFAULT '',
  sha256       TEXT    NOT NULL,
  content      BLOB    NOT NULL,
  status       TEXT    NOT NULL, -- pending | applied | written | rolled_back | observed
  apply_log    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX revisions_created ON revisions (created_at);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
