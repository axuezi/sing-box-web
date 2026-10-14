-- Hourly traffic by who connected and where to: one row per user, client
-- IP, destination site and outbound. Recorded from the Clash API connection
-- list only while the "flows" setting is on; kept for a few weeks.
CREATE TABLE traffic_flows (
  hour     TEXT    NOT NULL, -- 'YYYY-MM-DD HH' in the panel's time zone
  user     TEXT    NOT NULL, -- user name; '' = could not be attributed
  ip       TEXT    NOT NULL, -- client address
  site     TEXT    NOT NULL, -- registrable domain, or the IP when no domain was seen
  outbound TEXT    NOT NULL,
  up       INTEGER NOT NULL DEFAULT 0,
  down     INTEGER NOT NULL DEFAULT 0,
  conns    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, user, ip, site, outbound)
) WITHOUT ROWID;
