//go:build integration

package store_test

import "testing"

// TestAffectedCallsWithinTheViewsBound: the station disruption relation read
// at most 500 calls a ref (LIMIT 500, in no order, so which 500 it read past
// that was not defined); api_affected_calls reads the hour's calls as one
// range, without the limit. The two answer the same rows wherever no ref
// holds more than 500 calls in the relation's window, which this asserts of
// the fixture, so the parity hook's comparisons of api_affected_calls cover
// every case the relation answered deterministically. On production the most
// any ref held in an hour was 88 (read only, 2026-11-23).
//
// The fixture's own estimated-timetable calls are the live calls a test
// plants (plantLiveCalls), so they are planted here, on a copy of the
// fixture of this test's own: the shared fixture holds them only once that
// test has run, which a shard's order does not promise.
func TestAffectedCallsWithinTheViewsBound(t *testing.T) {
	_, pdb := private(t, liveCalls...)
	ctx := bounded(t)
	conn, err := pdb.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var most, refs int
	if err := conn.QueryRow(ctx, `select coalesce(max(n), 0)::int, count(*)::int from (
		select x.stop_ref, count(*) n from public.delfi_siri_et_calls_latest x group by 1) c`).Scan(&most, &refs); err != nil {
		t.Fatal(err)
	}
	if refs == 0 {
		t.Fatal("the fixture holds no call: the parity of api_affected_calls proves nothing")
	}
	if most > 500 {
		t.Fatalf("a ref holds %d calls: past the relation's 500 its answer is not defined, so the parity does not hold there", most)
	}
}
