-- Daily traffic totals. kind: user (key = user id), inbound or outbound (key = tag).
CREATE TABLE traffic_daily (
  day   TEXT    NOT NULL, -- YYYY-MM-DD in the panel's time zone
  kind  TEXT    NOT NULL,
  key   TEXT    NOT NULL,
  up    INTEGER NOT NULL DEFAULT 0,
  down  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, kind, key)
);

CREATE TABLE sub_access (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL,
  at         INTEGER NOT NULL,
  ip         TEXT    NOT NULL,
  user_agent TEXT    NOT NULL,
  format     TEXT    NOT NULL
);
CREATE INDEX sub_access_user ON sub_access (user_id, id);

ALTER TABLE admin ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE admin ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
