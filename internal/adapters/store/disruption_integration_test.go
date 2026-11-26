//go:build integration

package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// plantLiveCalls plants, at the read contract's now, a station with a page
// (an EVA number gives it one) and five departures of its next hour: through
// a live estimated-timetable source one cancelled and two on time, and through the Trip
// Updates two ticks of the station's quay, 15 and 2 minutes late. Its key
// 09999 is no fixture area. The seed writes an estimated-timetable call with no delay,
// so a late call of that kind is the database's own test's.
func plantLiveCalls(t *testing.T) {
	t.Helper()
	plant(t, liveCalls[0])
	if n := plant(t, liveCalls[1]); n != 5 {
		t.Fatalf("planted %d of 5", n)
	}
}

// liveCalls are plantLiveCalls' statements: the station, then its five
// departures.
var liveCalls = []string{`insert into vf_seed.stop_place (stop_place_id, name, location, national_stop_code, source_ref, eva_no)
values ('1:de:09999:1', 'Testbahnhof Live', st_setsrid(st_makepoint(11.0, 49.0), 4326), 'de:09999:1', 'delfi_static', '8099999')`,
	`insert into vf_seed.departure (stop_place_id, stop_point_id, journey_id, call_order, line_name, transport_mode, planned_departure_at,
  boarding, realtime_source, cancelled, delay_s, source_ref)
select '1:de:09999:1', v.point, '1:' || v.trip || ':' || to_char(now() at time zone 'Europe/Berlin', 'YYYYMMDD'), 1, v.line, 'rail',
       date_trunc('minute', now()) + v.at, true, v.src, v.cancelled, v.delay, 'delfi_static'
from (values ('1:de:09999:1', 'live-1', 'RE 9', interval '10 minutes', 'delfi_siri_et_zz', true, null::int),
             ('1:de:09999:1', 'live-2', 'RE 9', interval '12 minutes', 'delfi_siri_et_zz', false, null),
             ('1:de:09999:1', 'live-3', 'S 1', interval '20 minutes', 'delfi_siri_et_zz', false, null),
             ('1:de:09999:1:1', 'live-4', 'RB 2', interval '15 minutes', 'delfi_rt_tu', false, 900),
             ('1:de:09999:1:1', 'live-5', 'RB 2', interval '25 minutes', 'delfi_rt_tu', false, 120)) v(point, trip, line, at, src, cancelled, delay)`}

// plant runs one seed statement as the owner, with a setup bound of its own:
// the seed reads each departure back through its view, slower than a read
// under test may take. It returns the rows planted.
func plant(t *testing.T, sql string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tag, err := conn.Exec(ctx, sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return tag.RowsAffected()
}

func TestLiveCallsCountAsThePlatformCountsThem(t *testing.T) {
	st := seeded(t)
	plantLiveCalls(t)
	var fams []string
	owner(t, `select array_agg(distinct source_ref order by source_ref) from contract.source`, &fams)
	if !slices.Contains(fams, "delfi_siri_et_zz") {
		t.Fatalf("the planted source is no family: %v", fams)
	}
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)

	t.Run("a station with a cancellation and nothing else is listed", func(t *testing.T) {
		got, err := st.AffectedStations(ctx, fams, time.Now(), 500)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(got, func(a transit.Affected) bool { return a.Station.ID == "1:de:09999:1" })
		if i < 0 {
			t.Fatalf("not listed: %+v", got)
		}
		if a := got[i]; a.Cancelled != 1 || a.Late != 0 || a.Alerts != 0 || a.LiftsOut != 0 || !slices.Equal(a.Families, []string{"delfi_static", "delfi_siri_et_zz"}) {
			t.Errorf("counted %+v", a)
		}
		// without the source's family nothing names it
		none, err := st.AffectedStations(ctx, slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == "delfi_siri_et_zz" }), time.Now(), 500)
		if err != nil || slices.ContainsFunc(none, func(a transit.Affected) bool { return a.Station.ID == "1:de:09999:1" }) {
			t.Errorf("listed without its source: %v", err)
		}
	})

	t.Run("the region watch's trips over any threshold", func(t *testing.T) {
		// The trips are the Trip Updates' figures, under that source's key.
		for _, c := range []struct {
			area       string
			over, late int
		}{{"09", 5, 1}, {"09999", 1, 2}, {"09999", 15, 0}} {
			d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: c.area, Limit: 6, Now: time.Now(), LateOverMin: c.over}, fams)
			if err != nil || d.Trips == nil || d.Trips.Observed != 2 || d.Trips.Late != c.late || d.Trips.Family != "tripupdates" || d.Trips.ObservedAt.IsZero() {
				t.Errorf("%s over %d: %+v %v", c.area, c.over, d.Trips, err)
			}
		}
		for _, q := range []transit.DisruptionQuery{{Area: "08", LateOverMin: 5}, {Area: "09999"}} {
			q.Limit, q.Now = 6, time.Now()
			if d, err := st.Disruptions(bounded(t), q, fams); err != nil || d.Trips != nil {
				t.Errorf("%+v: %+v %v", q, d.Trips, err)
			}
		}
		if d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: "09", Limit: 6, Now: time.Now(), LateOverMin: 5}, []string{"delfi_static"}); err != nil || d.Trips != nil {
			t.Errorf("without the Trip Updates' family: %+v %v", d.Trips, err)
		}
	})
}
