package rules_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

func ptr[T any](v T) *T { return &v }

func zoneRow(key, role, fam, scope string, inside bool, d int, same ...string) rules.Zone {
	z := rules.Zone{Key: key, Role: role, Family: fam, Inside: inside, DistanceM: d, SameZone: same, Name: key}
	if scope != "" {
		z.Scope = ptr(scope)
	}
	return z
}

func TestVerdictOfFoldsTheRegisterAsThePlatform(t *testing.T) {
	keys := func(zs []*rules.Zone) []string {
		var out []string
		for _, z := range zs {
			out = append(out, z.Key)
		}
		return out
	}
	for name, c := range map[string]struct {
		rows       []rules.Zone
		kind, zone string
		also       []string
	}{
		"no city zone: the register alone": {
			rows: []rules.Zone{zoneRow("t", "transfer", "c", "city", false, 50), zoneRow("n", "zone", "u", "national", true, 0)},
			kind: "inside", zone: "n",
		},
		"no register row: whatever answered": {
			rows: []rules.Zone{zoneRow("t", "transfer", "c", "", true, 0)},
			kind: "transfer",
		},
		"inside a city's zone, its register copy credited, another register zone not": {
			rows: []rules.Zone{
				zoneRow("c", "zone", "c", "city", true, 0), zoneRow("n", "zone", "u", "national", true, 0, "c"),
				zoneRow("n-out", "zone", "u", "national", false, 10, "c"), zoneRow("n2", "zone", "u", "national", true, 0),
				zoneRow("n-road", "exempt_road", "u", "national", true, 0, "c"),
			},
			kind: "inside", zone: "c", also: []string{"n"},
		},
		"outside the city's zone, inside a register zone that is no city's": {
			rows: []rules.Zone{zoneRow("n2", "zone", "u", "national", true, 0), zoneRow("c", "zone", "c", "city", false, 900), zoneRow("n", "zone", "u", "national", false, 950, "c")},
			kind: "inside", zone: "n2",
		},
		"outside both, the register's own zone nearer": {
			rows: []rules.Zone{zoneRow("n2", "zone", "u", "national", false, 300), zoneRow("c", "zone", "c", "city", false, 900)},
			kind: "outside", zone: "n2",
		},
		"outside both, the city's nearer, its register copy within the boundary tolerance credited": {
			rows: []rules.Zone{
				zoneRow("c", "zone", "c", "city", false, 900), zoneRow("n-far", "zone", "u", "national", false, 1500, "c"),
				zoneRow("n-in", "zone", "u", "national", true, 0, "c"), zoneRow("n-road", "exempt_road", "u", "national", false, 950, "c"),
				zoneRow("n-other", "zone", "u", "national", false, 950, "x"), zoneRow("n", "zone", "u", "national", false, 1200, "c"),
				zoneRow("n-also", "zone", "u", "national", false, 600, "c"), zoneRow("n2", "zone", "u", "national", false, 1000),
			},
			kind: "outside", zone: "c", also: []string{"n"},
		},
		"in a city's released area: the city's verdict stands": {
			rows: []rules.Zone{zoneRow("c", "zone", "c", "city", false, 40), zoneRow("t", "transfer", "c", "city", true, 0), zoneRow("n", "zone", "u", "national", false, 30, "c")},
			kind: "transfer",
		},
	} {
		v := rules.VerdictOf(c.rows)
		zone := ""
		if v.Zone != nil {
			zone = v.Zone.Key
		}
		if v.Kind != c.kind || zone != c.zone || len(keys(v.Also)) != len(c.also) || (len(c.also) > 0 && keys(v.Also)[0] != c.also[0]) {
			t.Errorf("%s: %s %q also %v", name, v.Kind, zone, keys(v.Also))
		}
	}
	// The outside fold credits only the first agreeing register row, at most
	// SameBoundaryM from the city's boundary on either side.
	v := rules.VerdictOf([]rules.Zone{zoneRow("c", "zone", "c", "city", false, 900), zoneRow("n", "zone", "u", "national", false, 500, "c"), zoneRow("m", "zone", "u", "national", false, 1400, "c")})
	if len(v.Also) != 1 || v.Also[0].Key != "n" {
		t.Errorf("tolerance: %v", keys(v.Also))
	}
}

func TestRowsAndDeciderNameWhatTheVerdictUsed(t *testing.T) {
	z, n, tr, e := zoneRow("z", "zone", "a", "", true, 0), zoneRow("n", "zone", "u", "national", true, 0), zoneRow("t", "transfer", "a", "", true, 0), zoneRow("e", "exempt_road", "a", "", false, 5)
	for name, c := range map[string]struct {
		v       rules.Verdict
		rows    int
		decider string
	}{
		"inside with all":   {rules.Verdict{Kind: "inside", Zone: &z, Also: []*rules.Zone{&n}, Transfer: &tr, ExemptRoad: &e}, 4, "z"},
		"inside alone":      {rules.Verdict{Kind: "inside", Zone: &z}, 1, "z"},
		"transfer":          {rules.Verdict{Kind: "transfer", Transfer: &tr}, 1, "t"},
		"outside with also": {rules.Verdict{Kind: "outside", Zone: &z, Also: []*rules.Zone{&n}}, 2, "z"},
		"unknown":           {rules.Verdict{Kind: "unknown"}, 0, ""},
	} {
		d := ""
		if x := c.v.Decider(); x != nil {
			d = x.Key
		}
		if len(c.v.Rows()) != c.rows || d != c.decider {
			t.Errorf("%s: %d rows, decider %q", name, len(c.v.Rows()), d)
		}
	}
}

func TestZoneNameAsThePlatformPrintsIt(t *testing.T) {
	for want, z := range map[string]rules.Zone{
		"Umweltzone Bottrop":       {Name: "DE_NW_1", Scope: ptr("national"), City: ptr("Bottrop")},
		"Umweltzone (UBA)":         {Name: "DE_NW_1", Publisher: ptr("UBA")},
		"Umweltzone ()":            {Name: "DE_NW_2"},
		"Umweltzone Berlin":        {Name: "Umweltzone", Scope: ptr("city"), SourceCity: ptr("Berlin")},
		"Umweltzone Duisburg":      {Name: "Umweltzone Duisburg", SourceCity: ptr("Duisburg")},
		"Umweltzone Grüne Zone":    {Name: "Grüne Zone", SourceCity: ptr("grüne")},
		"Umweltzone Innenstadt":    {Name: "Innenstadt"},
		"umweltzone München-Mitte": {Name: "umweltzone München-Mitte"},
	} {
		if got := rules.ZoneName(&z); got != want {
			t.Errorf("%+v: %q, want %q", z, got, want)
		}
	}
	if got := rules.ZoneName(&rules.Zone{Name: "Mitte", SourceCity: ptr("Ruhr")}); got != "Umweltzone Ruhr" {
		t.Errorf("city: %q", got)
	}
}

func TestAnswerOfStatesThePlaqueAsThePlatform(t *testing.T) {
	read := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	changed := time.Date(2025, 6, 24, 0, 0, 0, 0, time.UTC)
	lifted := time.Date(2024, 2, 22, 0, 0, 0, 0, time.UTC)
	entries := []rules.ListEntry{
		{Key: "muenchen", PlaceCodes: []string{"09162000"}, Plaque: ptr("green"), Covered: true},
		{Key: "hannover", PlaceCodes: []string{"03241001"}, Covered: true, LiftedOn: &lifted},
	}
	city := rules.Zone{Key: "m/1", Role: "zone", Name: "Umweltzone", Scope: ptr("city"), PlaceCode: ptr("09162000"), SourceCity: ptr("München"), RecordedAt: read}
	nat := rules.Zone{Key: "u/1", Role: "zone", Name: "DE_NI_1", Scope: ptr("national"), PlaceCode: ptr("09162000"), City: ptr("Hannover"), ChangedAt: &changed, RecordedAt: read}
	other := rules.Zone{Key: "o/1", Role: "zone", Name: "Zone X", RecordedAt: read}
	tr := rules.Zone{Key: "m/t", Role: "transfer", Name: "Freigabe", RecordedAt: read}

	a := rules.AnswerOf(rules.Verdict{Kind: "inside", Zone: &city}, ptr("03241001"), entries)
	if !a.Plaque || a.Entry == nil || a.Entry.Key != "muenchen" || a.FoundByPoint || a.LiftedOn != nil || *a.Name != "Umweltzone München" || a.DataDate != nil || !a.BoundaryReadAt.Equal(read) {
		t.Errorf("city source's own entry: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "inside", Zone: &nat}, ptr("03241001"), entries)
	if !a.Plaque || a.Entry == nil || a.Entry.Key != "hannover" || !a.FoundByPoint || !a.LiftedOn.Equal(lifted) || *a.Name != "Umweltzone Hannover" || !a.DataDate.Equal(changed) {
		t.Errorf("register zone, the point's entry: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "inside", Zone: &other}, nil, entries)
	if !a.Plaque || a.Entry != nil || a.FoundByPoint {
		t.Errorf("no entry: the national fact: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "inside", Zone: &city, Transfer: &tr}, nil, entries)
	if a.Plaque || a.Entry != nil {
		t.Errorf("released at the edge: no plaque: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "transfer", Transfer: &tr}, nil, entries)
	if a.Plaque || *a.Name != "Freigabe" {
		t.Errorf("transfer: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "outside", Zone: &nat}, ptr("03241001"), entries)
	if a.Plaque || a.FoundByPoint || !a.LiftedOn.Equal(lifted) {
		t.Errorf("outside a lifted zone: %+v", a)
	}
	a = rules.AnswerOf(rules.Verdict{Kind: "outside", Zone: &other}, ptr("99999999"), entries)
	if a.LiftedOn != nil || a.Plaque {
		t.Errorf("outside, no entry: %+v", a)
	}
	if a := rules.AnswerOf(rules.Verdict{Kind: "unknown"}, ptr("03241001"), entries); a.Name != nil || a.BoundaryReadAt != nil || a.LiftedOn != nil {
		t.Errorf("unknown: %+v", a)
	}
}
