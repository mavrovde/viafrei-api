//go:build integration

package accessstore_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// TestTheWriterAppendsABoardAndCanDoNothingElse: the API's writer role
// appends one row per departure of a board, with the source, attribution and
// licence the table fixes, and can neither read, change nor remove a tick.
func TestTheWriterAppendsABoardAndCanDoNothingElse(t *testing.T) {
	ctx := bounded(t)
	o := owner(t)
	if _, err := o.Exec(ctx, `select public.ensure_daily_partitions()`); err != nil {
		t.Fatal(err)
	}
	s := open(t)
	at := time.Now().UTC().Truncate(time.Second).Add(-5 * time.Second)
	late := at.Add(17 * time.Minute)
	str := func(v string) *string { return &v }
	b := transit.TrainBoard{EVA: "8011160", FetchedAt: at, Departures: []transit.TrainDeparture{
		{PlannedAt: at.Add(12 * time.Minute), ChangedAt: &late, Platform: str("5"), ChangedPlatform: str("7"), Category: str("ICE"), Number: str("571"),
			Line: str("ICE 571"), Destination: str("München Hbf")},
		{PlannedAt: at.Add(21 * time.Minute), Platform: str("13"), Cancelled: true},
	}}
	if err := s.AppendTrainBoard(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrainBoard(ctx, transit.TrainBoard{EVA: "8011160", FetchedAt: at}); err != nil {
		t.Fatalf("an empty board: %v", err)
	}
	var n, changed int
	var source, licence string
	if err := o.QueryRow(ctx, `select count(*), count(changed_platform), min(source_id), min(licence)
from domains.train_board_departures where eva = '8011160' and observed_at = $1`, at).Scan(&n, &changed, &source, &licence); err != nil {
		t.Fatal(err)
	}
	if n != 2 || changed != 1 || source != transit.TrainBoardFamily || licence != "CC BY 4.0" {
		t.Errorf("appended %d (%d changed) of %s under %s", n, changed, source, licence)
	}
	// append-only: the writer role may not read, change or remove a tick
	conn, err := pgx.Connect(ctx, db.AccessURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range []string{
		`select count(*) from domains.train_board_departures`,
		`update domains.train_board_departures set cancelled = true`,
		`delete from domains.train_board_departures`,
		`insert into domains.train_board_departures (eva, observed_at, planned_at, source_id) values ('8011160', now(), now(), 'other_source')`,
	} {
		if _, err := conn.Exec(ctx, q); err == nil {
			t.Errorf("the writer role may: %s", q)
		}
	}
}
