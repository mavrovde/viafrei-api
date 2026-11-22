//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// maxWarnings is the configured default of weather.max_warnings; capLimit is
// a smaller limit the truncation test plants one warning past.
const (
	maxWarnings = 100
	capLimit    = 5
)

// knownHazards is the SQL list of the hazards the service names; a stored
// hazard outside it is answered as other.
func knownHazards() string {
	known := make([]string, 0, len(weather.Hazards)-1)
	for _, h := range weather.Hazards[:len(weather.Hazards)-1] {
		known = append(known, "'"+string(h)+"'")
	}
	return strings.Join(known, ",")
}

// unmappedWarning is the first published, not cancelled warning whose stored
// hazard the service does not name, with the centre of its area, its area
// code, its level and its validity.
func unmappedWarning(t *testing.T) (id, region string, at geo.Point, level int, from, until time.Time) {
	t.Helper()
	owner(t, `select w.warning_id, w.region_code, st_y(st_centroid(r.boundary)), st_x(st_centroid(r.boundary)), w.warning_level, w.valid_from, w.valid_to
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.valid_to is not null and w.hazard not in (`+knownHazards()+`)
order by w.warning_id limit 1`, &id, &region, &at.Lat, &at.Lon, &level, &from, &until)
	return id, region, at, level, from, until
}

// noBoundary is the ask that blocks every read of warnings by position.
const noBoundary = "contract.admin_region.boundary is NULL (0312 maps no area geometry): no warning is found by position, so warnings_at answers none and the road lamp reads no warning"

func warningIDs(ws []weather.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}

func TestWarningsAtReadTheAreaInForce(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "FR-WEATHER-002", "SEC-031")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	id, region, at, level, from, until := unmappedWarning(t)

	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from)
	ws, levels, more := sc.Warnings, sc.Levels, sc.HasMore
	if err != nil || more || len(levels) != len(ws) {
		t.Fatalf("%d warnings, %d levels, more %v: %v", len(ws), len(levels), more, err)
	}
	var want string
	owner(t, fmt.Sprintf(`select coalesce(string_agg(warning_id, ',' order by warning_level desc, valid_from, warning_id), '')
from contract.weather_warning where region_code = '%s' and withdrawn_at is null and message_status <> 'cancelled'
  and source_ref in (%s) and valid_from <= '%s' and (valid_to is null or valid_to > '%s')`,
		region, quoted(fams), from.Format(time.RFC3339Nano), from.Format(time.RFC3339Nano)), &want)
	if got := strings.Join(warningIDs(ws), ","); got != want || !slices.Contains(warningIDs(ws), id) {
		t.Fatalf("warnings at the centre of %s: %s, want %s (with %s)", region, got, want, id)
	}
	for _, w := range ws {
		var stored int
		owner(t, `select warning_level from contract.weather_warning where warning_id = '`+w.ID+`'`, &stored)
		if int(w.Level) != stored || w.AreaName == "" || w.Issuer == "" || w.Headline == "" || w.TextLanguage == "" || w.From.Location() != time.UTC {
			t.Errorf("%s: level %d (stored %d), %+v", w.ID, w.Level, stored, w)
		}
		if w.ID == id && (w.Hazard != weather.Other || w.Until == nil || !w.Until.Equal(until) || !w.From.Equal(from)) {
			t.Errorf("the unmapped warning %+v", w)
		}
	}

	only := func(q weather.Query, now time.Time) []string {
		t.Helper()
		q.At = at
		sc, err := st.WarningsAt(bounded(t), q, fams, now)
		if err != nil {
			t.Fatal(err)
		}
		ws := sc.Warnings
		for _, w := range ws {
			if w.Level < q.MinLevel {
				t.Errorf("%s at level %d under the floor %d", w.ID, w.Level, q.MinLevel)
			}
		}
		return warningIDs(ws)
	}
	if got := only(weather.Query{Limit: maxWarnings, MinLevel: 1, Hazards: []weather.Hazard{weather.Other}}, from); !slices.Contains(got, id) {
		t.Errorf("hazards=other: %v lacks %s", got, id)
	}
	if got := only(weather.Query{Limit: maxWarnings, MinLevel: 1, Hazards: []weather.Hazard{"rain", "snow"}}, from); slices.Contains(got, id) {
		t.Errorf("hazards=rain,snow: %v holds the unmapped %s", got, id)
	}
	if level < 4 {
		if got := only(weather.Query{Limit: maxWarnings, MinLevel: weather.Level(level + 1)}, from); slices.Contains(got, id) {
			t.Errorf("min_level %d: %v holds %s at level %d", level+1, got, id, level)
		}
	}
	if got := only(weather.Query{Limit: maxWarnings, MinLevel: 1}, until); slices.Contains(got, id) {
		t.Errorf("at its end: %v still holds %s", got, id)
	}
	if got := only(weather.Query{Limit: maxWarnings, MinLevel: 1}, from.Add(-time.Nanosecond)); slices.Contains(got, id) {
		t.Errorf("before its start: %v already holds %s", got, id)
	}
	if sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, []string{"src-none"}, from); err != nil || len(sc.Warnings) != 0 || len(sc.Levels) != 0 || sc.NextChange != nil {
		t.Errorf("a family not asked for answered: %v %v %v %v", warningIDs(sc.Warnings), sc.Levels, sc.NextChange, err)
	}
}

func TestWarningsAtLeaveOutTheCancelledAndTheWithdrawn(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	// The fixture plants three warnings in one area over the same window:
	// one in force, the same cancelled, and one withdrawn from publication.
	var (
		at   geo.Point
		from time.Time
	)
	owner(t, `select st_y(st_centroid(r.boundary)), st_x(st_centroid(r.boundary)), w.valid_from
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.warning_id = 'dwd:900001'`, &at.Lat, &at.Lon, &from)
	// Without the status and withdrawal filter, the area holds all three at
	// that instant: the read below has something to leave out.
	var unfiltered string
	owner(t, `select string_agg(w.warning_id, ',' order by w.warning_id)
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.warning_id like 'dwd:90000_' and w.source_ref in (`+quoted(fams)+`)
  and w.valid_from <= '`+from.Format(time.RFC3339Nano)+`' and (w.valid_to is null or w.valid_to > '`+from.Format(time.RFC3339Nano)+`')
  and st_intersects(r.boundary, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326))`, &unfiltered)
	if unfiltered != "dwd:900002,dwd:900001,dwd:900003" {
		t.Fatalf("the planted warnings without the filter: %s", unfiltered)
	}
	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from)
	ws, levels := sc.Warnings, sc.Levels
	got := warningIDs(ws)
	if err != nil || !slices.Contains(got, "dwd:900001") || slices.Contains(got, "dwd:900002") || slices.Contains(got, "dwd:900003") || len(levels) != len(ws) {
		t.Errorf("answered %v (%d levels), %v", got, len(levels), err)
	}
}

// plantedAt is the centre of the planted warning's area, the area's code
// and the warning's start.
func plantedAt(t *testing.T) (at geo.Point, region string, from time.Time) {
	t.Helper()
	owner(t, `select st_y(st_centroid(r.boundary)), st_x(st_centroid(r.boundary)), r.region_code, w.valid_from
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.warning_id = 'dwd:900001'`, &at.Lat, &at.Lon, &region, &from)
	return at, region, from
}

// TestWarningsAtSayWhenTheScopeChanges: the next change is the earliest
// start or end after now of any warning placed at the position, of any level
// and hazard, whatever the floor and the hazards keep; before the unmapped
// warning starts its start is one, and while it is in force its end bounds
// the next change.
func TestWarningsAtSayWhenTheScopeChanges(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	_, _, at, _, from, until := unmappedWarning(t)
	want := func(now time.Time) *time.Time {
		t.Helper()
		var next *time.Time
		ts := "'" + now.Format(time.RFC3339Nano) + "'"
		owner(t, `select min(case when w.valid_from > `+ts+` then w.valid_from else w.valid_to end)
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.source_ref in (`+quoted(fams)+`)
  and (w.valid_to is null or w.valid_to > `+ts+`)
  and st_intersects(r.boundary, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326))`, &next)
		return next
	}
	for name, now := range map[string]time.Time{"before it starts": from.Add(-time.Microsecond), "at its start": from, "before its end": until.Add(-time.Second)} {
		for _, q := range []weather.Query{{Limit: maxWarnings, At: at, MinLevel: 1}, {Limit: maxWarnings, At: at, MinLevel: 4, Hazards: []weather.Hazard{"uv"}}} {
			sc, err := st.WarningsAt(bounded(t), q, fams, now)
			w := want(now)
			if err != nil || sc.NextChange == nil || w == nil || !sc.NextChange.Equal(*w) || sc.NextChange.Location() != time.UTC {
				t.Errorf("%s, %+v: next change %v, want %v: %v", name, q, sc.NextChange, w, err)
			}
		}
	}
	if sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from.Add(-time.Microsecond)); err != nil || sc.NextChange.After(from) {
		t.Errorf("before the unmapped warning starts: next change %v after its start %v: %v", sc.NextChange, from, err)
	}
	if sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from); err != nil || sc.NextChange.After(until) {
		t.Errorf("while the unmapped warning is in force: next change %v after its end %v: %v", sc.NextChange, until, err)
	}
}

// TestTheLampLevelsIgnoreTheFilters: the planted level-2 ice warning is
// left out of the list by min_level=3 and by hazards=rain, and its level is
// still read for the lamp, with every other warning in force there.
func TestTheLampLevelsIgnoreTheFilters(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	at, region, from := plantedAt(t)
	var want int
	owner(t, `select count(*) from contract.weather_warning
where region_code = '`+region+`' and withdrawn_at is null and message_status <> 'cancelled' and source_ref in (`+quoted(fams)+`)
  and valid_from <= '`+from.Format(time.RFC3339Nano)+`' and (valid_to is null or valid_to > '`+from.Format(time.RFC3339Nano)+`')`, &want)
	for name, q := range map[string]weather.Query{
		"min_level=3":  {Limit: maxWarnings, At: at, MinLevel: 3},
		"hazards=rain": {Limit: maxWarnings, At: at, MinLevel: 1, Hazards: []weather.Hazard{"rain"}},
	} {
		sc, err := st.WarningsAt(bounded(t), q, fams, from)
		ws, levels, more := sc.Warnings, sc.Levels, sc.HasMore
		if err != nil || more || slices.Contains(warningIDs(ws), "dwd:900001") || len(levels) != want || !slices.Contains(levels, 2) {
			t.Errorf("%s: listed %v, levels %v (want %d with a 2), more %v: %v", name, warningIDs(ws), levels, want, more, err)
		}
		if weather.LampOf(levels, false, false) == weather.LampGreen {
			t.Errorf("%s: the lamp is green over %v", name, levels)
		}
	}
}

// TestAWarningWithNoAreaAnswersNoPosition plants, far after the fixture's
// window, two level-3 warnings: one in the planted area and one stored with
// no area (a null region_code). Only the first is listed or read for the
// lamp: a warning with no area cannot be placed.
func TestAWarningWithNoAreaAnswersNoPosition(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	at, region, _ := plantedAt(t)
	var n int
	owner(t, `with ins as (
  insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at)
  select v.id, 'ice', 3, 'Stufe 3', 'issued', 'Synthetischer Wetterdienst', 'Glätte', 'de',
    '2103-01-01T00:00:00Z', '2103-01-02T00:00:00Z', null, v.region, w.source_ref, '2103-01-01T00:00:00Z', null
  from contract.weather_warning w, (values ('ww-area-here', '`+region+`'), ('ww-area-none', null)) v(id, region)
  where w.warning_id = 'dwd:900001'
  returning 1)
select count(*) from ins`, &n)
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id like 'ww-area-%' returning 1) select count(*) from del`, &gone)
	})
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, time.Date(2103, 1, 1, 1, 0, 0, 0, time.UTC))
	ws, levels := sc.Warnings, sc.Levels
	got := warningIDs(ws)
	if err != nil || !slices.Contains(got, "ww-area-here") || slices.Contains(got, "ww-area-none") || len(levels) != len(ws) {
		t.Errorf("answered %v, levels %v: %v", got, levels, err)
	}
}

func TestWarningsAtRefuseALevelOutsideTheScaleByID(t *testing.T) {
	req.Covers(t, "FR-WEATHER-002")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	// A row far in the future, so no other test's instant sees it.
	var (
		id     string
		at     geo.Point
		region string
	)
	from := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	owner(t, `select region_code, st_y(st_centroid(boundary)), st_x(st_centroid(boundary)) from contract.admin_region
where region_code = (select region_code from contract.weather_warning where warning_id = 'dwd:900001')`, &region, &at.Lat, &at.Lon)
	owner(t, `insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
  valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at)
select 'ww-level-7', 'ice', 7, 'Stufe 7', 'issued', 'Synthetischer Wetterdienst', 'Glätte', 'de',
  '2100-01-01T00:00:00Z', null, null, '`+region+`', source_ref, '2100-01-01T00:00:00Z', null
from contract.weather_warning where warning_id = 'dwd:900001'
returning warning_id`, &id)
	t.Cleanup(func() {
		var gone string
		owner(t, `delete from contract.weather_warning where warning_id = 'ww-level-7' returning warning_id`, &gone)
	})
	_, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from)
	if !errors.Is(err, weather.ErrLevel) || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "level 7") {
		t.Errorf("a level of 7: %v", err)
	}
	// Left out of the list by the hazards, it is still read for the lamp,
	// and still refused.
	_, err = st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1, Hazards: []weather.Hazard{"rain"}}, fams, from)
	if !errors.Is(err, weather.ErrLevel) || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "level 7") {
		t.Errorf("a level of 7 outside the list: %v", err)
	}
	// Before it starts it is not in force: its level is not judged, and its
	// start is when the scope changes.
	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: at, MinLevel: 1}, fams, from.Add(-time.Hour))
	if err != nil || sc.NextChange == nil || !sc.NextChange.Equal(from) {
		t.Errorf("an hour before a level of 7 starts: next change %v, %v", sc.NextChange, err)
	}
}

// TestWarningsAtStopAtTheCap: the query's limit (weather.max_warnings) caps
// the list, and truncation says more answer; the lamp still reads them all.
func TestWarningsAtStopAtTheCap(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	dbAsk(t, noBoundary)
	st := seeded(t)
	fams := families(t, st, "weather")
	var at geo.Point
	var region string
	owner(t, `select region_code, st_y(st_centroid(boundary)), st_x(st_centroid(boundary)) from contract.admin_region
where region_code = (select region_code from contract.weather_warning where warning_id = 'dwd:900001')`, &region, &at.Lat, &at.Lon)
	var n int
	owner(t, `with ins as (
  insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at)
  select 'ww-cap-' || g, 'wind', 1, 'Stufe 1', 'issued', 'Synthetischer Wetterdienst', 'Wind', 'de',
    '2101-01-01T00:00:00Z', null, null, '`+region+`', w.source_ref, '2101-01-01T00:00:00Z', null
  from contract.weather_warning w, generate_series(1, `+fmt.Sprint(capLimit+1)+`) g
  where w.warning_id = 'dwd:900001'
  returning 1)
select count(*) from ins`, &n)
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id like 'ww-cap-%' returning 1) select count(*) from del`, &gone)
	})
	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: capLimit, At: at, MinLevel: 1}, fams, time.Date(2101, 1, 1, 0, 0, 0, 0, time.UTC))
	ws, levels, more := sc.Warnings, sc.Levels, sc.HasMore
	if err != nil || n != capLimit+1 || len(ws) != capLimit || !more || len(levels) < n {
		t.Errorf("%d of %d planted (%d levels), more %v: %v", len(ws), n, len(levels), more, err)
	}
}

func TestTopicReadsTheRulesInOrder(t *testing.T) {
	req.Covers(t, "FR-RULES-001")
	st := seeded(t)
	var (
		code, bodies string
		n, based     int
		reviewed     time.Time
	)
	owner(t, `select t.topic_code, min(s.reviewed_on), count(*), count(s.legal_basis), string_agg(s.body, '|' order by s.position, s.statement_id)
from contract.rule_topic t join contract.rule_statement s on s.topic_code = t.topic_code
group by t.topic_code, t.reviewed_on order by t.topic_code limit 1`, &code, &reviewed, &n, &based, &bodies)
	topic, err := st.Topic(bounded(t), code, "de")
	if err != nil {
		t.Fatal(err)
	}
	got, withBasis := make([]string, len(topic.Rules)), 0
	for i, r := range topic.Rules {
		got[i] = r.Text
		if r.LegalBasis != nil {
			withBasis++
		}
	}
	if topic.Code != code || topic.Title == "" || !topic.ReviewedOn.Equal(reviewed) || len(got) != n || strings.Join(got, "|") != bodies || withBasis != based {
		t.Errorf("topic %s: %+v, want %d rules (%d with a basis): %s", code, topic, n, based, bodies)
	}
	if _, err := st.Topic(bounded(t), "no_such_topic", "de"); !errors.Is(err, rules.ErrUnknownTopic) {
		t.Errorf("an unknown topic: %v", err)
	}
}

func TestWeatherAndRulesReadsReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.WarningsAt(ctx, weather.Query{Limit: maxWarnings, MinLevel: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("WarningsAt on an ended context succeeded")
	}
	if _, err := st.Topic(ctx, "winter_tyres", "de"); err == nil || errors.Is(err, rules.ErrUnknownTopic) {
		t.Errorf("Topic on an ended context: %v", err)
	}
}

// TestWarningsAtPlaceEveryWarningByItsArea reads, for each published, not
// cancelled warning of the fixture in force at its start, the warnings at a
// point inside its own area at that instant: the warning is listed whether
// or not the DWD filed it under a region code (a copy of one is planted
// under none), as the platform's warnings at a point test the warning's area
// alone.
func TestWarningsAtPlaceEveryWarningByItsArea(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	type placed struct {
		id       string
		at       geo.Point
		from     time.Time
		noRegion bool
	}
	// A copy of the first warning in a cell of no known Kreis: the read
	// contract derives its region code from the cell, so it has none.
	if n := change(t, `insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at, area, warn_cell_id, area_name)
  select 'dwd:990021', hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, null, source_ref, recorded_at, null, area, '809999999', area_name
  from contract.weather_warning
  where withdrawn_at is null and message_status <> 'cancelled' and area is not null and region_code is not null
  order by warning_id limit 1`); n != 1 {
		t.Fatalf("%d warnings planted", n)
	}
	t.Cleanup(func() { change(t, `delete from contract.weather_warning where warning_id = 'dwd:990021'`) })
	var ids []string
	var lats, lons []float64
	var froms []time.Time
	var unfiledIDs []bool
	owner(t, `select coalesce(array_agg(w.warning_id order by w.warning_id), '{}'),
       coalesce(array_agg(st_y(st_pointonsurface(w.area)) order by w.warning_id), '{}'),
       coalesce(array_agg(st_x(st_pointonsurface(w.area)) order by w.warning_id), '{}'),
       coalesce(array_agg(w.valid_from order by w.warning_id), '{}'),
       coalesce(array_agg(not exists (select 1 from contract.admin_region r where r.region_code = w.region_code) order by w.warning_id), '{}')
from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.area is not null
  and w.source_ref in ('`+strings.Join(fams, "','")+`')
  and w.display_class <> 'withhold' and w.audience = 'public' and (w.valid_to is null or w.valid_to > w.valid_from)`,
		&ids, &lats, &lons, &froms, &unfiledIDs)
	all := make([]placed, len(ids))
	for i := range ids {
		all[i] = placed{id: ids[i], at: geo.Point{Lat: lats[i], Lon: lons[i]}, from: froms[i], noRegion: unfiledIDs[i]}
	}
	unfiled := 0
	for _, w := range all {
		if w.noRegion {
			unfiled++
		}
		sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: w.at, MinLevel: 1}, fams, w.from)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(warningIDs(sc.Warnings), w.id) {
			t.Errorf("%s (no region code: %v): not read at a point of its own area, got %v", w.id, w.noRegion, warningIDs(sc.Warnings))
		}
	}
	if len(all) == 0 || unfiled == 0 {
		t.Fatalf("%d warnings, %d under no region code: the fixture no longer plants the case", len(all), unfiled)
	}
}

// TestWarningsAtListTheNextDayAhead plants, at a point inside the area of a
// fixture warning, two copies of it far after the fixture's window: one
// starting 23 hours after the asked instant and one starting 25 hours after.
// The answer lists the first beside those in force and not the second, as
// the platform's weather check lists the warnings of now and the next 24
// hours; the lamp reads neither, as neither is in force.
func TestWarningsAtListTheNextDayAhead(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	at := time.Date(2104, 6, 1, 12, 0, 0, 0, time.UTC)
	var p geo.Point
	owner(t, `select st_y(st_pointonsurface(area)), st_x(st_pointonsurface(area)) from contract.weather_warning
where withdrawn_at is null and message_status <> 'cancelled' and area is not null and display_class <> 'withhold' and audience = 'public'
order by warning_id limit 1`, &p.Lat, &p.Lon)
	if n := change(t, `insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at, area, warn_cell_id, area_name)
  select v.id, w.hazard, 4, w.level_label, w.message_status, w.issuer_name, w.headline, w.text_lang,
    $1::timestamptz + v.after, $1::timestamptz + v.after + interval '6 hours', null, w.region_code, w.source_ref, $1::timestamptz, null, w.area, w.warn_cell_id, w.area_name
  from (select * from contract.weather_warning
        where withdrawn_at is null and message_status <> 'cancelled' and area is not null and display_class <> 'withhold' and audience = 'public'
        order by warning_id limit 1) w,
       (values ('dwd:990041', interval '23 hours'), ('dwd:990042', interval '25 hours')) v(id, after)`, at); n != 2 {
		t.Fatalf("%d warnings planted", n)
	}
	t.Cleanup(func() {
		change(t, `delete from contract.weather_warning where warning_id in ('dwd:990041', 'dwd:990042')`)
	})
	sc, err := st.WarningsAt(bounded(t), weather.Query{Limit: maxWarnings, At: p, MinLevel: 1}, fams, at)
	if err != nil {
		t.Fatal(err)
	}
	ids := warningIDs(sc.Warnings)
	if !slices.Contains(ids, "dwd:990041") || slices.Contains(ids, "dwd:990042") {
		t.Errorf("listed %v, want dwd:990041 (starts within a day) and not dwd:990042", ids)
	}
	if len(sc.Levels) != 0 {
		t.Errorf("lamp levels %v, want none in force", sc.Levels)
	}
	if want := at.Add(23 * time.Hour); sc.NextChange == nil || !sc.NextChange.Equal(want) {
		t.Errorf("next change %v, want %v", sc.NextChange, want)
	}
}
