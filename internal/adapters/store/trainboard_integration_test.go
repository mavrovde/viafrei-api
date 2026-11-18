//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// A station's board is its newest recorded fetch, by planned time, with the
// times and platforms as the MCP server handed them over; a family the read
// does not name reads nothing.
func TestTrainBoardReadsTheNewestFetch(t *testing.T) {
	st := seeded(t)
	n := plant(t, `insert into contract.train_board (station_eva, fetched_at, planned_departure_at, changed_departure_at, platform, changed_platform, category, train_number, line, destination, cancelled)
values ('8099001', now() - interval '3 minutes', now() + interval '5 minutes', null, '1', null, 'RE', '1', 'RE 1', 'Alt', false),
       ('8099001', now() - interval '20 seconds', now() + interval '20 minutes', null, '3', null, 'RE', '3311', 'RE 1', 'Frankfurt (Oder)', true),
       ('8099001', now() - interval '20 seconds', now() + interval '12 minutes', now() + interval '17 minutes', '5', '7', 'ICE', '571', 'ICE 571', 'München Hbf', false)`)
	if n != 3 {
		t.Fatalf("planted %d of 3", n)
	}
	b, err := st.TrainBoard(bounded(t), "8099001", []string{transit.TrainBoardFamily})
	if err != nil || b == nil || len(b.Departures) != 2 || time.Since(b.FetchedAt) > time.Minute || b.FetchedAt.Location() != time.UTC {
		t.Fatalf("board %+v %v", b, err)
	}
	d0, d1 := b.Departures[0], b.Departures[1]
	if *d0.Number != "571" || d0.ChangedAt == nil || d0.DelayMin() != 5 || !d0.PlatformChanged() || *d1.Number != "3311" || !d1.Cancelled || d1.ChangedAt != nil {
		t.Errorf("departures %+v %+v", d0, d1)
	}
	if b, err := st.TrainBoard(bounded(t), "8099001", []string{"other"}); err != nil || b != nil {
		t.Errorf("another family: %+v %v", b, err)
	}
	if b, err := st.TrainBoard(bounded(t), "8099002", []string{transit.TrainBoardFamily}); err != nil || b != nil {
		t.Errorf("no board: %+v %v", b, err)
	}
}
