package rules_test

import (
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

func TestCityOf(t *testing.T) {
	green := "green"
	e := &rules.ListEntry{Key: "muenchen", Name: "München", Plaque: &green, Covered: true}
	if c := rules.CityOf("Munich", e, "en"); c.Name != "München" || !c.Covered || c.Plakette != &green || c.Zone != nil || c.Caveat != "" {
		t.Fatalf("an entry: %+v", c)
	}
	for _, lang := range []string{"en", "de"} {
		u := rules.CityOf("Matfienland", nil, lang)
		if u.Name != "Matfienland" || u.Covered || u.Plakette != nil || !strings.Contains(u.Caveat, "Matfienland") {
			t.Fatalf("%s: no entry: %+v", lang, u)
		}
	}
	if rules.NoBoundaryCaveat("X", "en") == rules.NoBoundaryCaveat("X", "de") {
		t.Fatal("one caveat for both languages")
	}
	for in, want := range map[string]string{"Köln-Süd ß": "koelnsuedss", "Monaco di Baviera": "monacodibaviera", "Château": "chateau", "!!": ""} {
		if got := rules.NormaliseCity(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
