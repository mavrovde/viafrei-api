//go:build integration

package store_test

// The issuer's text of a warning (#222): this test plants it into the
// platform's warnings table, which the store reads directly.

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestWarningsAtStateTheIssuersText: a warning listed at a point of its area
// states the issuer's full text, its advice and its CAP urgency and
// certainty as stored, as today's MCP answer states them (#222); a warning
// stored without them states none.
func TestWarningsAtStateTheIssuersText(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := families(t, st, "weather")
	var (
		p    geo.Point
		from time.Time
		id   string
	)
	owner(t, `select st_y(st_pointonsurface(area)), st_x(st_pointonsurface(area)), valid_from, warning_id from contract.weather_warning
where withdrawn_at is null and message_status <> 'cancelled' and area is not null and display_class <> 'withhold' and audience = 'public'
order by warning_id limit 1`, &p.Lat, &p.Lon, &from, &id)
	set := `update weather_warnings set description = $2, instruction = $3, urgency = $4, certainty = $5 where 'dwd:' || id = $1`
	if n := change(t, set, id, "Es tritt Frost auf.", "Vorsicht auf Brücken.", "Immediate", "Likely"); n != 1 {
		t.Fatalf("updated %d rows", n)
	}
	t.Cleanup(func() { change(t, set, id, nil, nil, nil, nil) })
	sc, err := st.WarningsAt(bounded(t), weather.Query{LookAhead: lookAhead, Limit: maxWarnings, At: p}, fams, from)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, w := range sc.Warnings {
		text := []*string{w.Description, w.Instruction, w.Urgency, w.Certainty}
		if w.ID != id {
			if text[0] != nil || text[1] != nil || text[2] != nil || text[3] != nil {
				t.Errorf("%s states a text it was not given: %v", w.ID, text)
			}
			continue
		}
		seen = true
		if w.Description == nil || *w.Description != "Es tritt Frost auf." || w.Instruction == nil || *w.Instruction != "Vorsicht auf Brücken." ||
			w.Urgency == nil || *w.Urgency != "Immediate" || w.Certainty == nil || *w.Certainty != "Likely" {
			t.Errorf("%s: %v", id, text)
		}
	}
	if !seen {
		t.Fatalf("%s not listed: %v", id, sc.Warnings)
	}
}
