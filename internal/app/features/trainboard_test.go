package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

type boards struct {
	board    *transit.TrainBoard
	err      error
	gotEVA   string
	gotFams  []string
	appended []transit.TrainBoard
}

func (b *boards) TrainBoard(_ context.Context, eva string, fams []string) (*transit.TrainBoard, error) {
	b.gotEVA, b.gotFams = eva, fams
	return b.board, b.err
}

func (b *boards) AppendTrainBoard(_ context.Context, tb transit.TrainBoard) error {
	b.appended = append(b.appended, tb)
	return b.err
}

func boardService(f *fake, b *boards) *features.Service {
	f.reg = append(append([]access.Terms{}, register...), access.Terms{Family: transit.TrainBoardFamily, Domain: "transit", UseClasses: all, Version: "v1"})
	return features.New(features.Repositories{Register: f, TrainBoards: b},
		features.Settings{Language: "en", PartDeadline: 50 * time.Millisecond}, func() time.Time { return now })
}

var transitKey = key("commercial", []access.Scope{"transit"}, transit.TrainBoardFamily, "st")

func TestTrainBoardIsServedOnlyWhileFresh(t *testing.T) {
	f, b := &fake{}, &boards{}
	s := boardService(f, b)
	ctx := context.Background()
	a, err := s.TrainBoard(ctx, transitKey, "8000105")
	if err != nil || a.Board != nil || a.AsOf != nil || b.gotEVA != "8000105" || len(b.gotFams) != 1 || b.gotFams[0] != transit.TrainBoardFamily {
		t.Fatalf("none held: %+v %v %+v", a, err, b)
	}
	b.board = &transit.TrainBoard{EVA: "8000105", FetchedAt: now.Add(-transit.TrainBoardFreshFor)}
	if a, err = s.TrainBoard(features.WithLanguage(ctx, "de"), transitKey, "8000105"); err != nil || a.Board == nil || !a.AsOf.Equal(b.board.FetchedAt) || f.gotLang != "de" ||
		len(a.Meta.Families) != 1 || a.Meta.Families[0].Source.Family != transit.TrainBoardFamily {
		t.Fatalf("fresh: %+v %v", a, err)
	}
	b.board = &transit.TrainBoard{EVA: "8000105", FetchedAt: now.Add(-transit.TrainBoardFreshFor - time.Second)}
	if a, err = s.TrainBoard(ctx, transitKey, "8000105"); err != nil || a.Board != nil || a.AsOf == nil || len(a.Meta.Families) != 0 {
		t.Fatalf("stale: %+v %v", a, err)
	}
	b.err = errStore
	if _, err = s.TrainBoard(ctx, transitKey, "8000105"); !errors.Is(err, errStore) {
		t.Fatalf("store: %v", err)
	}
	// a key whose plan does not hold the family gets no board
	if _, err = s.TrainBoard(ctx, key("commercial", []access.Scope{"transit"}, "st"), "8000105"); err == nil {
		t.Fatal("family not permitted, yet answered")
	}
}

func TestRecordTrainBoardIsForOwnKeysAndValidBoards(t *testing.T) {
	f, b := &fake{}, &boards{}
	s := boardService(f, b)
	ctx := context.Background()
	at := now.Add(-time.Minute).In(time.FixedZone("CET", 3600))
	board := transit.TrainBoard{EVA: "8000105", FetchedAt: at, Departures: []transit.TrainDeparture{{PlannedAt: now.Add(10 * time.Minute)}}}
	own := transitKey
	own.Audience = access.AudienceOwn
	if err := s.RecordTrainBoard(ctx, own, board); !errors.Is(err, features.ErrNoWriter) {
		t.Fatalf("no writer: %v", err)
	}
	s.WithTrainBoardWriter(b)
	if err := s.RecordTrainBoard(ctx, transitKey, board); !errors.Is(err, features.ErrOwnKeyOnly) || len(b.appended) != 0 {
		t.Fatalf("public key: %v", err)
	}
	if err := s.RecordTrainBoard(ctx, own, transit.TrainBoard{EVA: "1"}); !errors.Is(err, transit.ErrBoardInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	if err := s.RecordTrainBoard(ctx, own, board); err != nil || len(b.appended) != 1 || b.appended[0].FetchedAt.Location() != time.UTC {
		t.Fatalf("own: %v %+v", err, b.appended)
	}
}
