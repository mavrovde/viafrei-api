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

func ids(ws []weather.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}

func quoted(xs []string) string {
	return "'" + strings.Join(xs, "','") + "'"
}

func TestWarningsAtReadTheAreaInForce(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "FR-WEATHER-002", "SEC-031")
	st := seeded(t)
	fams := families(t, st, "weather")
	id, region, at, level, from, until := unmappedWarning(t)

	ws, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, fams, from)
	if err != nil {
		t.Fatal(err)
	}
	var want string
	owner(t, fmt.Sprintf(`select coalesce(string_agg(warning_id, ',' order by warning_level desc, valid_from, warning_id), '')
from contract.weather_warning where region_code = '%s' and withdrawn_at is null and message_status <> 'cancelled'
  and source_ref in (%s) and valid_from <= '%s' and (valid_to is null or valid_to > '%s')`,
		region, quoted(fams), from.Format(time.RFC3339Nano), from.Format(time.RFC3339Nano)), &want)
	if got := strings.Join(ids(ws), ","); got != want || !slices.Contains(ids(ws), id) {
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
		ws, err := st.WarningsAt(bounded(t), q, fams, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws {
			if w.Level < q.MinLevel {
				t.Errorf("%s at level %d under the floor %d", w.ID, w.Level, q.MinLevel)
			}
		}
		return ids(ws)
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
	if ws, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, []string{"src-none"}, from); err != nil || len(ws) != 0 {
		t.Errorf("a family not asked for answered: %v %v", ids(ws), err)
	}
}

func TestWarningsAtLeaveOutTheCancelled(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	var (
		id   string
		at   geo.Point
		from time.Time
	)
	owner(t, `select w.warning_id, st_y(st_centroid(r.boundary)), st_x(st_centroid(r.boundary)), w.valid_from
from contract.weather_warning w join contract.admin_region r on r.region_code = w.region_code
where w.message_status = 'cancelled' order by w.warning_id limit 1`, &id, &at.Lat, &at.Lon, &from)
	ws, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1}, families(t, st, "weather"), from)
	if err != nil || slices.Contains(ids(ws), id) {
		t.Errorf("the cancelled %s answered: %v %v", id, ids(ws), err)
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
	if _, err := st.WarningsAt(ctx, weather.Query{MinLevel: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("WarningsAt on an ended context succeeded")
	}
	if _, err := st.Topic(ctx, "winter_tyres"); err == nil || errors.Is(err, rules.ErrUnknownTopic) {
		t.Errorf("Topic on an ended context: %v", err)
	}
}
