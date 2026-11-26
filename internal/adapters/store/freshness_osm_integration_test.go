//go:build integration

package store_test

import (
	"testing"
	"time"
)

// The OpenStreetMap family keeps no status row of its own: its freshness is
// the POI extract's row; a row of its own, once there, is read instead.
func TestTheOSMFamilyIsDatedByThePOIExtract(t *testing.T) {
	st := seeded(t)
	var sources int
	owner(t, `select count(*) from contract_sources where source_ref = 'osm'`, &sources)
	if sources != 1 {
		t.Fatalf("the seed holds %d OpenStreetMap families, want 1", sources)
	}
	extract := time.Date(2103, 1, 1, 5, 50, 0, 0, time.UTC)
	own := extract.Add(24 * time.Hour)
	plantStatus := func(key string, at time.Time) {
		t.Helper()
		if n := change(t, `insert into control.feed_state (ledger, key, last_success_at, last_changed_at, updated_at) values ('ingest', $1, $2, $2, $2)
on conflict (ledger, key) do update set last_success_at = excluded.last_success_at, last_changed_at = excluded.last_changed_at`, key, at); n != 1 {
			t.Fatalf("planted %d status rows for %s", n, key)
		}
	}
	var had int
	owner(t, `select count(*) from control.feed_state where key in ('osm', 'osm_pois')`, &had)
	if had != 0 {
		t.Fatalf("the seed holds %d OpenStreetMap status rows, want none", had)
	}
	t.Cleanup(func() {
		change(t, `delete from control.feed_state where ledger = 'ingest' and key in ('osm', 'osm_pois')`)
	})
	dated := func() *time.Time {
		t.Helper()
		got, err := st.Provenance(bounded(t), []string{"osm"}, "en")
		if err != nil || len(got) != 1 {
			t.Fatalf("read %+v %v", got, err)
		}
		return got[0].DataAsOf
	}
	if at := dated(); at != nil {
		t.Errorf("no status row: dated %v", at)
	}
	plantStatus("osm_pois", extract)
	if at := dated(); at == nil || !at.Equal(extract) {
		t.Errorf("the extract's row: dated %v, want %v", at, extract)
	}
	plantStatus("osm", own)
	if at := dated(); at == nil || !at.Equal(own) {
		t.Errorf("a row of its own: dated %v, want %v", at, own)
	}
}
