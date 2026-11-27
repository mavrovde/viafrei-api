package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type boards struct {
	board     *transit.TrainBoard
	stored    *transit.StoredBoard
	ic        transit.InterchangeRead
	err       error
	storedErr error
	icErr     error
	gotEVA    string
	gotFams   []string
	gotEVAs   []string
	gotFrom   time.Time
	gotTo     time.Time
}

func (b *boards) TrainBoard(_ context.Context, eva string, fams []string) (*transit.TrainBoard, error) {
	b.gotEVA, b.gotFams = eva, fams
	return b.board, b.err
}

func (b *boards) StoredTrainBoard(_ context.Context, _ string, _ []string) (*transit.StoredBoard, error) {
	return b.stored, b.storedErr
}

func (b *boards) TrainInterchange(_ context.Context, evas []string, from, to, _ time.Time, _ []string) (transit.InterchangeRead, error) {
	b.gotEVAs, b.gotFrom, b.gotTo = evas, from, to
	return b.ic, b.icErr
}

func boardService(f *fake, b *boards, more ...access.Terms) *features.Service {
	f.reg = append(append([]access.Terms{}, register...),
		access.Terms{Family: transit.TrainBoardFamily, Domain: "transit", UseClasses: all, Version: "v1"},
		access.Terms{Family: transit.InterchangeFamily, Domain: "transit", UseClasses: all, Version: "v1"})
	f.reg = append(f.reg, more...)
	return features.New(features.Repositories{Register: f, TrainBoards: b},
		features.Settings{Language: "en", PartDeadline: 50 * time.Millisecond}, func() time.Time { return now })
}

var transitKey = key("commercial", []access.Scope{"transit"}, transit.TrainBoardFamily, transit.InterchangeFamily, "st")

var boardQ = features.TrainBoardQuery{EVA: "8000105", DurationMin: 60, Limit: 5}

func TestTrainBoardIsServedOnlyWhileFresh(t *testing.T) {
	f, b := &fake{}, &boards{}
	s := boardService(f, b)
	ctx := context.Background()
	a, err := s.TrainBoard(ctx, transitKey, boardQ)
	if err != nil || a.Board != nil || a.AsOf != nil || a.Interchange != nil || b.gotEVA != "8000105" || len(b.gotFams) != 1 || b.gotFams[0] != transit.TrainBoardFamily {
		t.Fatalf("none held: %+v %v %+v", a, err, b)
	}
	if !slices.Equal(b.gotEVAs, []string{"8000105"}) || !b.gotFrom.Equal(now.Add(-30*time.Minute)) || !b.gotTo.Equal(now.Add(time.Hour)) {
		t.Errorf("interchange asked %v %v %v", b.gotEVAs, b.gotFrom, b.gotTo)
	}
	b.board = &transit.TrainBoard{EVA: "8000105", FetchedAt: now.Add(-transit.TrainBoardFreshFor)}
	if a, err = s.TrainBoard(features.WithLanguage(ctx, "de"), transitKey, boardQ); err != nil || a.Board == nil || !a.AsOf.Equal(b.board.FetchedAt) || f.gotLang != "de" ||
		len(a.Meta.Families) != 1 || a.Meta.Families[0].Source.Family != transit.TrainBoardFamily {
		t.Fatalf("fresh: %+v %v", a, err)
	}
	b.board = &transit.TrainBoard{EVA: "8000105", FetchedAt: now.Add(-transit.TrainBoardFreshFor - time.Second)}
	if a, err = s.TrainBoard(ctx, transitKey, boardQ); err != nil || a.Board != nil || a.AsOf == nil || len(a.Meta.Families) != 0 {
		t.Fatalf("stale: %+v %v", a, err)
	}
	for _, e := range []*error{&b.err, &b.storedErr, &b.icErr} {
		*e = errStore
		if _, err = s.TrainBoard(ctx, transitKey, boardQ); !errors.Is(err, errStore) {
			t.Fatalf("store: %v", err)
		}
		*e = nil
	}
	// a key whose plan does not hold the family gets no board
	if _, err = s.TrainBoard(ctx, key("commercial", []access.Scope{"transit"}, "st"), boardQ); err == nil {
		t.Fatal("family not permitted, yet answered")
	}
}

func TestTrainBoardServesTheStoredBoardWithoutARefresh(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	at := func(m int) *string {
		s := now.Add(time.Duration(m) * time.Minute).UTC().Format(time.RFC3339Nano)
		return &s
	}
	line, five, yes := "ICE 1", 5, true
	rows := make([]transit.StoredDeparture, 8)
	for i := range rows {
		rows[i] = transit.StoredDeparture{Line: &line, PlannedAt: at(5 * (i + 1))}
	}
	rows[1].DelayMin, rows[2].Cancelled = &five, &yes
	b := &boards{
		board:  &transit.TrainBoard{EVA: "8000105", FetchedAt: now.Add(-time.Hour)},
		stored: &transit.StoredBoard{EVAs: []string{"8000105", "8098105"}, ReadAt: now.Add(-transit.StoredBoardFreshFor), CoversUntil: now.Add(time.Hour), Departures: rows},
	}
	s := boardService(&fake{}, b)
	a, err := s.TrainBoard(context.Background(), transitKey, boardQ)
	if err != nil || a.Board == nil || !a.Board.Stored || len(a.Board.Departures) != 5 || a.Cap == nil || a.Cap.More != 3 || !a.Cap.Until.Equal(now.Add(40*time.Minute)) {
		t.Fatalf("stored: %+v %v", a, err)
	}
	if d := a.Board.Departures[1]; d.DelayMin() != 5 || !a.Board.Departures[2].Cancelled || !a.AsOf.Equal(now.Add(-transit.StoredBoardFreshFor)) {
		t.Errorf("rows %+v", a.Board.Departures)
	}
	if !slices.Equal(b.gotEVAs, []string{"8000105", "8098105"}) {
		t.Errorf("interchange of %v", b.gotEVAs)
	}
	// A limit above the window's departures cuts nothing; a stale stored board is none.
	if a, _ = s.TrainBoard(context.Background(), transitKey, features.TrainBoardQuery{EVA: "8000105", DurationMin: 60, Limit: 50}); a.Cap != nil || len(a.Board.Departures) != 8 {
		t.Errorf("uncut %+v", a.Cap)
	}
	// The asked categories are kept before the limit (#298): of the eight,
	// the one ICE after five others is shown, the board's own rows untouched.
	ice, re := "ICE", "RE"
	for i := range b.stored.Departures {
		b.stored.Departures[i].Category = &re
	}
	b.stored.Departures[6].Category, b.stored.Departures[7].Category = &ice, nil
	if a, _ = s.TrainBoard(context.Background(), transitKey, features.TrainBoardQuery{EVA: "8000105", DurationMin: 60, Limit: 5, Categories: []string{"ICE"}}); a.Cap != nil || len(a.Board.Departures) != 1 || *a.Board.Departures[0].Category != "ICE" {
		t.Errorf("ICE only %+v %+v", a.Board, a.Cap)
	}
	if *b.stored.Departures[0].Category != "RE" || len(b.stored.Departures) != 8 {
		t.Errorf("the stored board was changed: %+v", b.stored.Departures)
	}
	b.stored.ReadAt = now.Add(-transit.StoredBoardFreshFor - time.Second)
	if a, _ = s.TrainBoard(context.Background(), transitKey, boardQ); a.Board != nil || a.AsOf == nil {
		t.Errorf("stale stored: %+v", a)
	}
}

func TestTrainBoardCarriesTheInterchange(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	u, one := "U2", 1
	b := &boards{ic: transit.InterchangeRead{Stops: true,
		Lines:           []transit.StopFamilyLines{{Modes: []string{"subway"}, Lines: []string{"U2"}}},
		Plan:            []transit.PlannedCall{{TripID: "t", StopSequence: 1, Line: &u, RouteType: 400, At: now.Add(time.Minute)}},
		TripUpdates:     []transit.TripUpdateRow{{TripID: "t", StopSequence: &one, DelayS: &one, Relationship: "SCHEDULED", ObservedAt: now}},
		TripUpdatesLive: true}}
	if _, err := boardService(&fake{}, b).TrainBoard(context.Background(), transitKey, boardQ); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a realtime row the key may not read: %v", err)
	}
	rt := access.Terms{Family: transit.TripUpdatesFamily, Domain: "transit", UseClasses: all, Version: "v1"}
	k := key("commercial", []access.Scope{"transit"}, transit.TrainBoardFamily, transit.InterchangeFamily, transit.TripUpdatesFamily, "st")
	a, err := boardService(&fake{}, b, rt).TrainBoard(context.Background(), k, boardQ)
	if err != nil || a.Interchange == nil || len(a.Interchange.Departures) != 1 || len(a.Interchange.Lines) != 1 || len(a.Meta.Families) != 2 {
		t.Fatalf("interchange %+v %v", a, err)
	}
}
