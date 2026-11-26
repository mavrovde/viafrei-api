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
	warnings     []weather.StateWarning
	alerts       []transit.StateAlertRow
	err          error
	gotLand      string
	gotFams      []string
	english      map[string]string
	enErr        error
	askedEnglish int
}

func (f *statesFake) StateEventsEnglish(_ context.Context, _ string, _ []string, _ time.Time) (map[string]string, string, error) {
	f.askedEnglish++
	return f.english, "f-en", f.enErr
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

// enService is the weather service whose register also holds the English
// family the fake names.
func enService(f *statesFake) *features.Service {
	svc := statesService(f, "weather")
	f.reg = append(f.reg, access.Terms{Family: "f-en", Domain: "weather", UseClasses: all, Version: "v1"})
	return svc
}

// An answer in English names an event by the issuer's English name,
// credited to its family; one in German, or an event with no English name,
// keeps the German name.
func TestStateWarningsNameTheEventsInTheAskedLanguage(t *testing.T) {
	k := key("commercial", []access.Scope{"weather"}, "f1", "f-en")
	snow, frost := "SCHNEEFALL", "FROST"
	warnings := func() []weather.StateWarning {
		return []weather.StateWarning{{Event: &snow, Level: 2, Families: []string{"f1"}}, {Event: &frost, Level: 1, Families: []string{"f1"}}, {Level: 1, Families: []string{"f1"}}}
	}
	en := map[string]string{"SCHNEEFALL": "snowfall"}
	f := &statesFake{warnings: warnings(), english: en}
	v, a, err := enService(f).StateWarnings(features.WithLanguage(context.Background(), "en"), k, "09")
	if err != nil || *v[0].Event != "snowfall" || *v[1].Event != "FROST" || v[2].Event != nil ||
		!slices.Equal(v[0].Families, []string{"f1", "f-en"}) || !slices.Equal(v[1].Families, []string{"f1"}) ||
		!slices.Contains(names(a.Families), "f-en") {
		t.Fatalf("en: %+v %v %v", v, names(a.Families), err)
	}
	f = &statesFake{warnings: warnings(), english: en}
	if v, _, err := enService(f).StateWarnings(features.WithLanguage(context.Background(), "de"), k, "09"); err != nil || *v[0].Event != "SCHNEEFALL" || f.askedEnglish != 0 {
		t.Errorf("de: %+v asked %d %v", v, f.askedEnglish, err)
	}
	f = &statesFake{warnings: warnings(), english: en, enErr: errStore}
	if _, _, err := enService(f).StateWarnings(features.WithLanguage(context.Background(), "en"), k, "09"); !errors.Is(err, errStore) {
		t.Errorf("english read: %v, want %v", err, errStore)
	}
	f = &statesFake{warnings: []weather.StateWarning{{Level: 1, Families: []string{"f1"}}}}
	if _, _, err := enService(f).StateWarnings(features.WithLanguage(context.Background(), "en"), k, "09"); err != nil || f.askedEnglish != 0 {
		t.Errorf("no event named: asked %d %v", f.askedEnglish, err)
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
