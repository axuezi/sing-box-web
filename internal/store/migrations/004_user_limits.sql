-- Expiry and monthly traffic quota per user.
ALTER TABLE users ADD COLUMN expires_at  INTEGER NOT NULL DEFAULT 0; -- unix; 0 = never
ALTER TABLE users ADD COLUMN quota_bytes INTEGER NOT NULL DEFAULT 0; -- up + down per cycle; 0 = unlimited
ALTER TABLE users ADD COLUMN reset_day   INTEGER NOT NULL DEFAULT 1; -- day of month (1-28) the cycle restarts
-- Usage in the current cycle, kept beside traffic_daily so a manual reset
-- can take effect mid-day.
ALTER TABLE users ADD COLUMN cycle_start INTEGER NOT NULL DEFAULT 0; -- unix; 0 = not started yet
ALTER TABLE users ADD COLUMN cycle_up    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN cycle_down  INTEGER NOT NULL DEFAULT 0;
-- Why the panel took the user out of the config: '' | expired | quota.
-- Kept apart from enabled, which is the administrator's own switch.
ALTER TABLE users ADD COLUMN limited     TEXT    NOT NULL DEFAULT '';

-- Seed the first cycle with this month's recorded traffic.
UPDATE users SET
  cycle_up   = (SELECT COALESCE(SUM(up), 0)   FROM traffic_daily
                 WHERE kind = 'user' AND key = CAST(users.id AS TEXT) AND day >= date('now', 'localtime', 'start of month')),
  cycle_down = (SELECT COALESCE(SUM(down), 0) FROM traffic_daily
                 WHERE kind = 'user' AND key = CAST(users.id AS TEXT) AND day >= date('now', 'localtime', 'start of month'));
