//go:build integration

package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// plantCells plants, beside the fixture's warnings, warnings of three warn
// cells far from every fixture area: a Gemeinde cell (8…) and a coast cell
// (7…) that overlap, the coast cell loaded later, and a Kreis cell (1…) over
// both, which a position never keys.
func plantCells(t *testing.T) {
	t.Helper()
	var n int
	owner(t, `with ins as (
  insert into vf_seed.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, source_ref, recorded_at, withdrawn_at, warn_cell_id, area_name, area, event, identifier, seen_at)
  select v.id, 'wind', v.level, 'Stufe', 'actual', 'Deutscher Wetterdienst', 'Wind', 'de',
    v.since::timestamptz, '2103-01-02T00:00:00Z', null, w.source_ref, '2103-01-01T00:00:00Z', null, v.cell, v.name,
    st_multi(st_makeenvelope(v.x0, 70.0, v.x1, 70.2, 4326)), 'STURMBÖEN', 'msg-' || v.id, v.seen::timestamptz
  from contract.weather_warning w, (values
    ('dwd:990001', 2, '2103-01-01T00:00:00Z', '809990001', 'Gemeinde Testort', 0.0, 0.2, '2103-01-01T00:00:00Z'),
    ('dwd:990002', 3, '2103-01-01T00:30:00Z', '809990001', 'Gemeinde Testort', 0.0, 0.2, '2103-01-01T00:00:00Z'),
    ('dwd:990003', 1, '2103-01-01T00:00:00Z', '809990001', 'Gemeinde Testort', 0.0, 0.2, '2103-01-01T00:00:00Z'),
    ('dwd:990004', 2, '2103-01-01T12:00:00Z', '809990001', 'Gemeinde Testort', 0.0, 0.2, '2103-01-01T00:00:00Z'),
    ('dwd:990005', 2, '2103-01-01T00:00:00Z', '709990002', 'Küste Testmeer', 0.1, 0.3, '2103-01-01T00:05:00Z'),
    ('dwd:990006', 2, '2103-01-01T00:00:00Z', '109990000', 'Kreis Testkreis', -1.0, 1.0, '2103-01-01T00:10:00Z')
  ) v(id, level, since, cell, name, x0, x1, seen)
  where w.warning_id = 'dwd:900001'
  returning 1)
select count(*) from ins`, &n)
	if n != 6 {
		t.Fatalf("planted %d of 6", n)
	}
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id between 'dwd:990001' and 'dwd:990006' returning 1) select count(*) from del`, &gone)
	})
}

func TestCellWarningsKeyAsThePlatform(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	plantCells(t)
	at := time.Date(2103, 1, 1, 1, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		q        weather.CellQuery
		key      string
		cellName string
		ids      []string
	}{
		// in both the Gemeinde and the coast cell: the newer load keys it
		"overlap": {weather.CellQuery{At: &geo.Point{Lat: 70.1, Lon: 0.15}, MinLevel: 2}, "709990002", "Küste Testmeer", []string{"dwd:990005"}},
		// only in the Gemeinde cell: its warnings in force at the floor, worst first
		"gemeinde": {weather.CellQuery{At: &geo.Point{Lat: 70.1, Lon: 0.05}, MinLevel: 2}, "809990001", "Gemeinde Testort", []string{"dwd:990002", "dwd:990001"}},
		"floor 0":  {weather.CellQuery{At: &geo.Point{Lat: 70.1, Lon: 0.05}}, "809990001", "Gemeinde Testort", []string{"dwd:990002", "dwd:990001", "dwd:990003"}},
		// a cell id as given, and a cell's name
		"id":   {weather.CellQuery{Cell: "109990000", MinLevel: 2}, "109990000", "", []string{"dwd:990006"}},
		"name": {weather.CellQuery{Cell: "Kreis Testkreis", MinLevel: 2}, "Kreis Testkreis", "Kreis Testkreis", []string{"dwd:990006"}},
	} {
		got, err := st.CellWarnings(bounded(t), c.q, fams, at)
		if err != nil || got.Key != c.key || !slices.Equal(warningIDs(got.Warnings), c.ids) || (c.cellName != "") != (got.Name != nil) || got.Name != nil && *got.Name != c.cellName {
			t.Errorf("%s: %+v %v %v", name, got, warningIDs(got.Warnings), err)
		}
	}
	// a position only the Kreis cell holds, and a name no cell carries
	for name, q := range map[string]weather.CellQuery{
		"kreis only": {At: &geo.Point{Lat: 70.1, Lon: -0.5}},
		"nowhere":    {At: &geo.Point{Lat: -80, Lon: 0}},
		"no name":    {Cell: "Nirgendwo am Meer"},
	} {
		if _, err := st.CellWarnings(bounded(t), q, fams, at); !errors.Is(err, weather.ErrNoCell) {
			t.Errorf("%s: %v, want no cell", name, err)
		}
	}
	// a family the key may not read keys no cell
	if _, err := st.CellWarnings(bounded(t), weather.CellQuery{At: &geo.Point{Lat: 70.1, Lon: 0.05}}, []string{"none"}, at); !errors.Is(err, weather.ErrNoCell) {
		t.Errorf("no family: %v", err)
	}
}

// plantHistory plants, far from every other area, an area A held twice (a
// live warning of cell 809990011 and a withdrawn one of 809990012) and two
// overlapping areas, a Gemeinde cell B (809990013, live) and a coast cell C
// (709990014, withdrawn), each live warning stamped at live: the history the
// loader wrote before it stamped an unchanged live warning once an hour
// (live after every withdrawal) and the one it writes now (live before it).
func plantHistory(t *testing.T, live string) {
	t.Helper()
	var n int
	owner(t, `with ins as (
  insert into vf_seed.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, source_ref, recorded_at, withdrawn_at, warn_cell_id, area_name, area, event, identifier, seen_at)
  select v.id, 'wind', 2, 'Stufe', 'actual', 'Deutscher Wetterdienst', 'Wind', 'de',
    '2103-01-01T00:00:00Z', '2103-01-02T00:00:00Z', null, w.source_ref, '2103-01-01T00:00:00Z', v.gone::timestamptz, v.cell, v.name,
    st_multi(st_makeenvelope(v.x0, 72.0, v.x1, 72.2, 4326)), 'STURMBÖEN', 'msg-' || v.id, coalesce(v.seen, '`+live+`')::timestamptz
  from contract.weather_warning w, (values
    ('dwd:990011', '809990011', 'Gemeinde Lebendort', 0.0, 0.2, null, null),
    ('dwd:990012', '809990012', 'Gemeinde Altort', 0.0, 0.2, '2103-01-01T00:40:00Z', '2103-01-01T00:30:00Z'),
    ('dwd:990013', '809990013', 'Gemeinde Neuort', 1.0, 1.2, null, null),
    ('dwd:990014', '709990014', 'Küste Altmeer', 1.1, 1.3, '2103-01-01T00:40:00Z', '2103-01-01T00:30:00Z')
  ) v(id, cell, name, x0, x1, gone, seen)
  where w.warning_id = 'dwd:900001'
  returning 1)
select count(*) from ins`, &n)
	if n != 4 {
		t.Fatalf("planted %d of 4", n)
	}
}

// A position is keyed alike whether the loader stamped a live warning every
// cycle or once an hour: a live cell before a withdrawn one, in an area and
// across overlapping areas.
func TestCellKeyIsTheSameUnderEitherStampHistory(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	at := time.Date(2103, 1, 1, 1, 0, 0, 0, time.UTC)
	points := map[string]geo.Point{"one area held twice": {Lat: 72.1, Lon: 0.1}, "two areas overlapping": {Lat: 72.1, Lon: 1.15}}
	want := map[string]string{"one area held twice": "809990011", "two areas overlapping": "809990013"}
	answers := map[string]map[string]weather.Cell{}
	for history, live := range map[string]string{"stamped every cycle": "2103-01-01T01:00:00Z", "stamped once an hour": "2103-01-01T00:00:00Z"} {
		plantHistory(t, live)
		answers[history] = map[string]weather.Cell{}
		for name, p := range points {
			got, err := st.CellWarnings(bounded(t), weather.CellQuery{At: &p}, fams, at)
			if err != nil || got.Key != want[name] {
				t.Errorf("%s, %s: keyed %q, want %s: %v", history, name, got.Key, want[name], err)
			}
			answers[history][name] = got
		}
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id between 'dwd:990011' and 'dwd:990014' returning 1) select count(*) from del`, &gone)
	}
	for name := range points {
		a, b := answers["stamped every cycle"][name], answers["stamped once an hour"][name]
		if a.Key != b.Key || !slices.Equal(warningIDs(a.Warnings), warningIDs(b.Warnings)) || (a.Name == nil) != (b.Name == nil) || a.Name != nil && *a.Name != *b.Name {
			t.Errorf("%s: the histories answer %+v and %+v", name, a, b)
		}
	}
}
