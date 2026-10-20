package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDeleteUserPurgesStats(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	gone := &User{Name: "gone", Enabled: true, UUID: "u-gone", SubToken: "t-gone", Password: "p1"}
	kept := &User{Name: "kept", Enabled: true, UUID: "u-kept", SubToken: "t-kept", Password: "p2"}
	if err := st.SaveUsers(ctx, []*User{gone, kept}, nil); err != nil {
		t.Fatal(err)
	}
	var deltas []TrafficDelta
	for _, u := range []*User{gone, kept} {
		key := itoa(u.ID)
		deltas = append(deltas, TrafficDelta{Day: "2026-10-20", Hour: "2026-10-20 10", Kind: "user", Key: key, Up: 1, Down: 2})
		if err := st.LogSubAccess(ctx, u.ID, SubAccess{IP: "192.0.2.1", Format: "mihomo"}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddFlows(ctx, []FlowRow{{Hour: "2026-10-20 10", User: u.Name, IP: "192.0.2.1", Site: "example.com", Outbound: "direct", Up: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	// An inbound sharing the deleted user's id as its key must survive.
	deltas = append(deltas, TrafficDelta{Day: "2026-10-20", Hour: "2026-10-20 10", Kind: "inbound", Key: itoa(gone.ID), Up: 5})
	if err := st.AddTraffic(ctx, deltas); err != nil {
		t.Fatal(err)
	}

	if err := st.SaveUsers(ctx, nil, []int64{gone.ID}); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := st.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, c := range []struct {
		name      string
		q         string
		arg       any
		gone, all int
	}{
		{"daily", `SELECT count(*) FROM traffic_daily WHERE kind = 'user' AND key = ?`, itoa(gone.ID), 0, 0},
		{"hourly", `SELECT count(*) FROM traffic_hourly WHERE kind = 'user' AND key = ?`, itoa(gone.ID), 0, 0},
		{"flows", `SELECT count(*) FROM traffic_flows WHERE user = ?`, "gone", 0, 0},
		{"sub_access", `SELECT count(*) FROM sub_access WHERE user_id = ?`, gone.ID, 0, 0},
	} {
		if n := count(c.q, c.arg); n != c.gone {
			t.Errorf("%s rows of the deleted user: %d", c.name, n)
		}
	}
	if count(`SELECT count(*) FROM traffic_daily WHERE kind = 'user' AND key = ?`, itoa(kept.ID)) != 1 ||
		count(`SELECT count(*) FROM traffic_flows WHERE user = 'kept'`) != 1 ||
		count(`SELECT count(*) FROM sub_access WHERE user_id = ?`, kept.ID) != 1 ||
		count(`SELECT count(*) FROM traffic_daily WHERE kind = 'inbound'`) != 1 {
		t.Error("other users' or inbounds' rows were removed")
	}
	// Deleting an id that no longer exists is not an error.
	if err := st.SaveUsers(ctx, nil, []int64{gone.ID}); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
