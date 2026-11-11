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

func warningIDs(ws []weather.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}

func TestWarningsAtReadTheAreaInForce(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "FR-WEATHER-002", "SEC-031")
	st := seeded(t)
	fams := families(t, st, "weather")
	id, region, at, level, from, until := unmappedWarning(t)

	ws, levels, more, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, from)
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
		ws, _, _, err := st.WarningsAt(bounded(t), q, fams, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws {
			if w.Level < q.MinLevel {
				t.Errorf("%s at level %d under the floor %d", w.ID, w.Level, q.MinLevel)
			}
		}
		return warningIDs(ws)
	}
	if got := only(weather.Query{MinLevel: 1, Hazards: []weather.Hazard{weather.Other}}, from); !slices.Contains(got, id) {
		t.Errorf("hazards=other: %v lacks %s", got, id)
	}
	if got := only(weather.Query{MinLevel: 1, Hazards: []weather.Hazard{"rain", "snow"}}, from); slices.Contains(got, id) {
		t.Errorf("hazards=rain,snow: %v holds the unmapped %s", got, id)
	}
	if level < 4 {
		if got := only(weather.Query{MinLevel: weather.Level(level + 1)}, from); slices.Contains(got, id) {
			t.Errorf("min_level %d: %v holds %s at level %d", level+1, got, id, level)
		}
	}
	if got := only(weather.Query{MinLevel: 1}, until); slices.Contains(got, id) {
		t.Errorf("at its end: %v still holds %s", got, id)
	}
	if got := only(weather.Query{MinLevel: 1}, from.Add(-time.Nanosecond)); slices.Contains(got, id) {
		t.Errorf("before its start: %v already holds %s", got, id)
	}
	if ws, levels, _, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, []string{"src-none"}, from); err != nil || len(ws) != 0 || len(levels) != 0 {
		t.Errorf("a family not asked for answered: %v %v %v", warningIDs(ws), levels, err)
	}
}

func TestWarningsAtLeaveOutTheCancelledAndTheWithdrawn(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
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
where w.warning_id = 'ww-planted-issued'`, &at.Lat, &at.Lon, &from)
	// Without the status and withdrawal filter, the area holds all three at
	// that instant: the read below has something to leave out.
	var unfiltered string
	owner(t, `select string_agg(w.warning_id, ',' order by w.warning_id)
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.warning_id like 'ww-planted-%' and w.source_ref in (`+quoted(fams)+`)
  and w.valid_from <= '`+from.Format(time.RFC3339Nano)+`' and (w.valid_to is null or w.valid_to > '`+from.Format(time.RFC3339Nano)+`')
  and st_intersects(r.boundary, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326))`, &unfiltered)
	if unfiltered != "ww-planted-cancelled,ww-planted-issued,ww-planted-withdrawn" {
		t.Fatalf("the planted warnings without the filter: %s", unfiltered)
	}
	ws, levels, _, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, from)
	got := warningIDs(ws)
	if err != nil || !slices.Contains(got, "ww-planted-issued") || slices.Contains(got, "ww-planted-cancelled") || slices.Contains(got, "ww-planted-withdrawn") || len(levels) != len(ws) {
		t.Errorf("answered %v (%d levels), %v", got, len(levels), err)
	}
}

// plantedAt is the centre of the planted warning's area, the area's code
// and the warning's start.
func plantedAt(t *testing.T) (at geo.Point, region string, from time.Time) {
	t.Helper()
	owner(t, `select st_y(st_centroid(r.boundary)), st_x(st_centroid(r.boundary)), r.region_code, w.valid_from
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.warning_id = 'ww-planted-issued'`, &at.Lat, &at.Lon, &region, &from)
	return at, region, from
}

// TestTheLampLevelsIgnoreTheFilters: the planted level-2 ice warning is
// left out of the list by min_level=3 and by hazards=rain, and its level is
// still read for the lamp, with every other warning in force there.
func TestTheLampLevelsIgnoreTheFilters(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	at, region, from := plantedAt(t)
	var want int
	owner(t, `select count(*) from contract.weather_warning
where region_code = '`+region+`' and withdrawn_at is null and message_status <> 'cancelled' and source_ref in (`+quoted(fams)+`)
  and valid_from <= '`+from.Format(time.RFC3339Nano)+`' and (valid_to is null or valid_to > '`+from.Format(time.RFC3339Nano)+`')`, &want)
	for name, q := range map[string]weather.Query{
		"min_level=3":  {At: at, MinLevel: 3},
		"hazards=rain": {At: at, MinLevel: 1, Hazards: []weather.Hazard{"rain"}},
	} {
		ws, levels, more, err := st.WarningsAt(bounded(t), q, fams, from)
		if err != nil || more || slices.Contains(warningIDs(ws), "ww-planted-issued") || len(levels) != want || !slices.Contains(levels, 2) {
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
  where w.warning_id = 'ww-planted-issued'
  returning 1)
select count(*) from ins`, &n)
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id like 'ww-area-%' returning 1) select count(*) from del`, &gone)
	})
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	ws, levels, _, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, time.Date(2103, 1, 1, 1, 0, 0, 0, time.UTC))
	got := warningIDs(ws)
	if err != nil || !slices.Contains(got, "ww-area-here") || slices.Contains(got, "ww-area-none") || len(levels) != len(ws) {
		t.Errorf("answered %v, levels %v: %v", got, levels, err)
	}
}

func TestWarningsAtRefuseALevelOutsideTheScaleByID(t *testing.T) {
	req.Covers(t, "FR-WEATHER-002")
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
where region_code = (select region_code from contract.weather_warning where warning_id = 'ww-planted-issued')`, &region, &at.Lat, &at.Lon)
	owner(t, `insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
  valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at)
select 'ww-level-7', 'ice', 7, 'Stufe 7', 'issued', 'Synthetischer Wetterdienst', 'Glätte', 'de',
  '2100-01-01T00:00:00Z', null, null, '`+region+`', source_ref, '2100-01-01T00:00:00Z', null
from contract.weather_warning where warning_id = 'ww-planted-issued'
returning warning_id`, &id)
	t.Cleanup(func() {
		var gone string
		owner(t, `delete from contract.weather_warning where warning_id = 'ww-level-7' returning warning_id`, &gone)
	})
	_, _, _, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, from)
	if !errors.Is(err, weather.ErrLevel) || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "level 7") {
		t.Errorf("a level of 7: %v", err)
	}
	// Left out of the list by the hazards, it is still read for the lamp,
	// and still refused.
	_, _, _, err = st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1, Hazards: []weather.Hazard{"rain"}}, fams, from)
	if !errors.Is(err, weather.ErrLevel) || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "level 7") {
		t.Errorf("a level of 7 outside the list: %v", err)
	}
}

func TestWarningsAtStopAtTheCap(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	var at geo.Point
	var region string
	owner(t, `select region_code, st_y(st_centroid(boundary)), st_x(st_centroid(boundary)) from contract.admin_region
where region_code = (select region_code from contract.weather_warning where warning_id = 'ww-planted-issued')`, &region, &at.Lat, &at.Lon)
	var n int
	owner(t, `with ins as (
  insert into contract.weather_warning (warning_id, hazard, warning_level, level_label, message_status, issuer_name, headline, text_lang,
    valid_from, valid_to, published_at, region_code, source_ref, recorded_at, withdrawn_at)
  select 'ww-cap-' || g, 'wind', 1, 'Stufe 1', 'issued', 'Synthetischer Wetterdienst', 'Wind', 'de',
    '2101-01-01T00:00:00Z', null, null, '`+region+`', w.source_ref, '2101-01-01T00:00:00Z', null
  from contract.weather_warning w, generate_series(1, `+fmt.Sprint(weather.MaxWarnings+1)+`) g
  where w.warning_id = 'ww-planted-issued'
  returning 1)
select count(*) from ins`, &n)
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.weather_warning where warning_id like 'ww-cap-%' returning 1) select count(*) from del`, &gone)
	})
	ws, levels, more, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, time.Date(2101, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || n != weather.MaxWarnings+1 || len(ws) != weather.MaxWarnings || !more || len(levels) < n {
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
	owner(t, `select t.topic_code, t.reviewed_on, count(*), count(s.legal_basis), string_agg(s.body, '|' order by s.position, s.statement_id)
from contract.rule_topic t join contract.rule_statement s on s.topic_code = t.topic_code
group by t.topic_code, t.reviewed_on order by t.topic_code limit 1`, &code, &reviewed, &n, &based, &bodies)
	topic, err := st.Topic(bounded(t), code)
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
	if _, err := st.Topic(bounded(t), "no_such_topic"); !errors.Is(err, rules.ErrUnknownTopic) {
		t.Errorf("an unknown topic: %v", err)
	}
}

func TestWeatherAndRulesReadsReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := st.WarningsAt(ctx, weather.Query{MinLevel: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("WarningsAt on an ended context succeeded")
	}
	if _, err := st.Topic(ctx, "winter_tyres"); err == nil || errors.Is(err, rules.ErrUnknownTopic) {
		t.Errorf("Topic on an ended context: %v", err)
	}
}
