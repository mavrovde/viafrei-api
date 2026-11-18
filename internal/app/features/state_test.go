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
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

type statesFake struct {
	*fake
	warnings []weather.StateWarning
	alerts   []transit.StateAlertRow
	err      error
	gotLand  string
	gotFams  []string
}

func (f *statesFake) StateWarnings(_ context.Context, land string, fams []string, _ time.Time) ([]weather.StateWarning, error) {
	f.gotLand, f.gotFams = land, fams
	return f.warnings, f.err
}

func (f *statesFake) StateAlerts(_ context.Context, land string, fams []string, _ time.Time) ([]transit.StateAlertRow, error) {
	f.gotLand, f.gotFams = land, fams
	return f.alerts, f.err
}

func statesService(f *statesFake, domain string) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf(domain)
	return features.New(features.Repositories{Register: f, States: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestStateWarnings(t *testing.T) {
	k := key("commercial", []access.Scope{"weather"}, "f1")
	f := &statesFake{warnings: []weather.StateWarning{{Level: 2, Families: []string{"f1"}}}}
	v, a, err := statesService(f, "weather").StateWarnings(context.Background(), k, "09")
	if err != nil || len(v) != 1 || f.gotLand != "09" || !slices.Equal(f.gotFams, []string{"f1"}) || !slices.Equal(names(a.Families), []string{"f1"}) {
		t.Errorf("answer %+v %+v %v", v, a, err)
	}
	for name, c := range map[string]struct {
		f    *statesFake
		want error
	}{
		"read":       {&statesFake{err: errStore}, errStore},
		"post-check": {&statesFake{warnings: []weather.StateWarning{{Families: []string{"f2"}}}}, features.ErrGateViolation},
	} {
		if _, _, err := statesService(c.f, "weather").StateWarnings(context.Background(), k, "09"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestStateAlerts(t *testing.T) {
	k := key("commercial", []access.Scope{"transit"}, "f1")
	row := func(id, header string, lines ...string) transit.StateAlertRow {
		return transit.StateAlertRow{ID: id, Header: header, Lines: lines, MessageKey: id, CarriedBy: []string{"f1"}, Lands: []string{"09"}}
	}
	f := &statesFake{alerts: []transit.StateAlertRow{row("a", "A", "S 1", "S 2"), row("b", "B", "S 1"), row("c", "C")}}
	a, err := statesService(f, "transit").StateAlerts(context.Background(), k, "09", 2)
	if err != nil || a.Total != 3 || len(a.Alerts) != 2 || a.Alerts[0].Header != "A" || f.gotLand != "09" || !slices.Equal(names(a.Meta.Families), []string{"f1"}) {
		t.Errorf("answer %+v %v", a, err)
	}
	// a family only a message beyond the limit carries is not credited
	f = &statesFake{alerts: []transit.StateAlertRow{row("a", "A", "S 1"), {ID: "b", Header: "B", MessageKey: "b", CarriedBy: []string{"f2"}, Lands: []string{"09"}}}}
	if a, err := statesService(f, "transit").StateAlerts(context.Background(), k, "09", 1); err != nil || a.Total != 2 || !slices.Equal(names(a.Meta.Families), []string{"f1"}) {
		t.Errorf("beyond the limit: %+v %v", a, err)
	}
	for name, c := range map[string]struct {
		f    *statesFake
		want error
	}{
		"read":       {&statesFake{err: errStore}, errStore},
		"post-check": {&statesFake{alerts: []transit.StateAlertRow{{ID: "x", MessageKey: "x", CarriedBy: []string{"f2"}, Lands: []string{"09"}}}}, features.ErrGateViolation},
	} {
		if _, err := statesService(c.f, "transit").StateAlerts(context.Background(), k, "09", 5); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
