CREATE TABLE admin (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  username      TEXT    NOT NULL,
  password_hash TEXT    NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT    PRIMARY KEY,
  csrf_token TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip         TEXT    NOT NULL,
  user_agent TEXT    NOT NULL
);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE audit_log (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  at     INTEGER NOT NULL,
  actor  TEXT    NOT NULL,
  ip     TEXT    NOT NULL,
  action TEXT    NOT NULL,
  target TEXT    NOT NULL DEFAULT '',
  detail TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log (at);
