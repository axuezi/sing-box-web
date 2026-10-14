-- Hourly traffic totals, kept for a few weeks so single days can be
-- compared hour by hour. Same kinds and keys as traffic_daily.
CREATE TABLE traffic_hourly (
  hour  TEXT    NOT NULL, -- 'YYYY-MM-DD HH' in the panel's time zone
  kind  TEXT    NOT NULL,
  key   TEXT    NOT NULL,
  up    INTEGER NOT NULL DEFAULT 0,
  down  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, kind, key)
);
