//go:build integration

package store_test

import (
	"testing"
	"time"
)

// The timetable reaches to the Berlin midnight after the last service day
// its newest loaded run wrote, as today's board reads it; a run that failed
// does not count; with no run recorded, the calendar's last day, and no load
// time.
func TestTheTimetableReachesToTheLastDayItsLoadWrote(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	st, _ := private(t,
		`delete from public.ingest_runs where provider = 'delfi_static'`)
	r, err := st.TimetableReach(bounded(t))
	if err != nil || r.LoadedAt != nil || r.Until == nil || r.Until.In(berlin).Hour() != 0 {
		t.Fatalf("with no run: %+v %v", r, err)
	}
	st, _ = private(t,
		`insert into public.ingest_runs (provider, started_at, status, note) values
		 ('delfi_static', '2026-03-20T01:00:00Z', 'degraded', '{"w":"2026-03-19..2026-04-02"}'),
		 ('delfi_static', '2026-03-21T01:00:00Z', 'failed', '{"w":"2026-03-20..2026-05-02"}')`)
	r, err = st.TimetableReach(bounded(t))
	want := time.Date(2026, 4, 3, 0, 0, 0, 0, berlin)
	if err != nil || r.Until == nil || !r.Until.Equal(want) || r.LoadedAt == nil || !r.LoadedAt.Equal(time.Date(2026, 3, 20, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("the loaded run: %+v %v, want until %v", r, err, want)
	}
}
