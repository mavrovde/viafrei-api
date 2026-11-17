package transit_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestTrainBoardValidateNamesTheField(t *testing.T) {
	now := time.Date(2026, 11, 17, 15, 30, 0, 0, time.UTC)
	s := func(v string) *string { return &v }
	tp := func(v time.Time) *time.Time { return &v }
	ok := func() transit.TrainBoard {
		return transit.TrainBoard{EVA: "8011160", FetchedAt: now.Add(-time.Second), Departures: []transit.TrainDeparture{
			{PlannedAt: now.Add(10 * time.Minute), ChangedAt: tp(now.Add(15 * time.Minute)), Platform: s("5"), ChangedPlatform: s("7"),
				Category: s("ICE"), Number: s("571"), Line: s("ICE 571"), Destination: s("München Hbf")},
		}}
	}
	if err := ok().Validate(now); err != nil {
		t.Fatalf("valid board refused: %v", err)
	}
	cases := map[string]func(*transit.TrainBoard){
		"eva":       func(b *transit.TrainBoard) { b.EVA = "801116" },
		"fetchedAt": func(b *transit.TrainBoard) { b.FetchedAt = now.Add(2 * time.Minute) },
		"departures": func(b *transit.TrainBoard) {
			b.Departures = make([]transit.TrainDeparture, transit.MaxBoardDepartures+1)
		},
		"departures[0].plannedAt":       func(b *transit.TrainBoard) { b.Departures[0].PlannedAt = now.Add(-25 * time.Hour) },
		"departures[0].changedAt":       func(b *transit.TrainBoard) { b.Departures[0].ChangedAt = tp(now.Add(26 * time.Hour)) },
		"departures[0].platform":        func(b *transit.TrainBoard) { b.Departures[0].Platform = s(" ") },
		"departures[0].changedPlatform": func(b *transit.TrainBoard) { b.Departures[0].ChangedPlatform = s(strings.Repeat("7", 17)) },
		"departures[0].destination":     func(b *transit.TrainBoard) { b.Departures[0].Destination = s(strings.Repeat("x", 121)) },
	}
	for field, mutate := range cases {
		b := ok()
		mutate(&b)
		var fe *transit.BoardFieldError
		err := b.Validate(now)
		if !errors.As(err, &fe) || fe.Field != field || !errors.Is(err, transit.ErrBoardInvalid) || !strings.HasPrefix(err.Error(), field+": ") {
			t.Errorf("%s: %v", field, err)
		}
	}
	old := ok()
	old.FetchedAt = now.Add(-25 * time.Hour)
	if err := old.Validate(now); err == nil {
		t.Error("a fetch older than a day accepted")
	}
}

func TestTrainDepartureReadsAsTheBoardStatesIt(t *testing.T) {
	at := time.Date(2026, 11, 17, 15, 42, 0, 0, time.UTC)
	late := at.Add(5*time.Minute + 31*time.Second)
	s := func(v string) *string { return &v }
	d := transit.TrainDeparture{PlannedAt: at, ChangedAt: &late, Platform: s("5"), ChangedPlatform: s("7")}
	if !d.ExpectedAt().Equal(late) || d.DelayMin() != 6 || !d.PlatformChanged() {
		t.Errorf("changed: %v %d %v", d.ExpectedAt(), d.DelayMin(), d.PlatformChanged())
	}
	same := transit.TrainDeparture{PlannedAt: at, Platform: s("5"), ChangedPlatform: s("5")}
	if !same.ExpectedAt().Equal(at) || same.DelayMin() != 0 || same.PlatformChanged() {
		t.Errorf("same: %+v", same)
	}
	if !(transit.TrainDeparture{ChangedPlatform: s("2")}).PlatformChanged() || (transit.TrainDeparture{Platform: s("2")}).PlatformChanged() {
		t.Error("platform changed without a planned one")
	}
}
