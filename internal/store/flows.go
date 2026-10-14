package store

import "context"

// FlowRow is traffic of one (user, client IP, site, outbound), either for
// one hour (when added) or summed over a range (when read).
type FlowRow struct {
	Hour     string // 'YYYY-MM-DD HH'; empty in summed rows
	User     string
	IP       string
	Site     string
	Outbound string
	Up       int64
	Down     int64
	Conns    int64
}

// AddFlows adds rows to their hours in one transaction.
func (s *Store) AddFlows(ctx context.Context, rows []FlowRow) error {
	if len(rows) == 0 {
		return nil
	}
	return s.Transaction(ctx, func(ctx context.Context) error {
		tx := s.executor(ctx)
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_flows (hour, user, ip, site, outbound, up, down, conns) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (hour, user, ip, site, outbound) DO UPDATE SET
			  up = up + excluded.up, down = down + excluded.down, conns = conns + excluded.conns`,
				r.Hour, r.User, r.IP, r.Site, r.Outbound, r.Up, r.Down, r.Conns); err != nil {
				return err
			}
		}
		return nil
	})
}

// FlowsBetween sums rows over the hours from..to (inclusive).
func (s *Store) FlowsBetween(ctx context.Context, from, to string) ([]FlowRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user, ip, site, outbound, SUM(up), SUM(down), SUM(conns) FROM traffic_flows
		WHERE hour >= ? AND hour <= ? GROUP BY user, ip, site, outbound`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FlowRow{}
	for rows.Next() {
		var r FlowRow
		if err := rows.Scan(&r.User, &r.IP, &r.Site, &r.Outbound, &r.Up, &r.Down, &r.Conns); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FlowsSpan returns the first hour with flow data ("" if none).
func (s *Store) FlowsSpan(ctx context.Context) (string, error) {
	var first string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(hour), '') FROM traffic_flows`).Scan(&first)
	return first, err
}

// PruneFlows deletes hours before cutoff ('YYYY-MM-DD HH').
func (s *Store) PruneFlows(ctx context.Context, cutoff string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic_flows WHERE hour < ?`, cutoff)
	return err
}

// ClearFlows deletes all flow data.
func (s *Store) ClearFlows(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic_flows`)
	return err
}
