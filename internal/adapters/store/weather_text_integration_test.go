//go:build integration

package store_test

// The issuer's text of a warning (#222): this test plants it into the
// platform's warnings table, which the store reads directly.

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
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
	owner(t, `select st_y(st_pointonsurface(area::geometry)), st_x(st_pointonsurface(area::geometry)), valid_from, 'dwd:' || id from weather_warnings
where closed_at is null and deleted_at is null and valid_from is not null and closed_reason is distinct from 'withdrawn' and area is not null
order by id limit 1`, &p.Lat, &p.Lon, &from, &id)
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

// TestWarningsAtStateTheIssuersEnglishText: the DWD's English message of
// the same alert (its sent instant, onset, expiry, the warning's cell among
// its cells and the same event code) is the warning's English text, as
// today's MCP answer reads it (#222), credited to the English source and
// read only while that source is among the families; a warning with no
// English message has none, and so has every warning in the cell read.
func TestWarningsAtStateTheIssuersEnglishText(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	// The platform's English source, beside the fixtures' weather families.
	fams := append(families(t, st, "weather"), "dwd_warnings_en")
	var (
		p    geo.Point
		from time.Time
		id   string
		cell string
	)
	owner(t, `select st_y(st_pointonsurface(area::geometry)), st_x(st_pointonsurface(area::geometry)), valid_from, 'dwd:' || id, warncell_id from weather_warnings
where closed_at is null and deleted_at is null and valid_from is not null and closed_reason is distinct from 'withdrawn' and area is not null
  and published_at is not null and warncell_id is not null
order by id limit 1`, &p.Lat, &p.Lon, &from, &id, &cell)
	if n := change(t, `update weather_warnings set raw = jsonb_set(coalesce(raw, '{}'), '{properties}', coalesce(raw->'properties', '{}') || '{"EC_II": "84"}')
where 'dwd:' || id = $1`, id); n != 1 {
		t.Fatalf("updated %d rows", n)
	}
	if n := change(t, `insert into public.weather_warnings_en (source_id, source_feed, identifier, base_identifier, language, headline, description,
  content_sha256, sent_at, valid_from, valid_to, warncell_ids, raw)
select 'dwd_warnings_en', 'dwd_warnings_en', 'test-en-1', 'test-en-1', 'en-GB', 'Frost', 'Frost is expected.', repeat('a', 64), w.published_at, w.valid_from, w.valid_to,
  array[w.warncell_id], '{"eventCodes": {"II": "84"}}'
from weather_warnings w where 'dwd:' || w.id = $1`, id); n != 1 {
		t.Fatalf("inserted %d rows", n)
	}
	t.Cleanup(func() { change(t, `delete from public.weather_warnings_en where identifier = 'test-en-1'`) })
	for name, read := range map[string][]string{"read": fams, "not read": slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == "dwd_warnings_en" })} {
		sc, err := st.WarningsAt(bounded(t), weather.Query{LookAhead: lookAhead, Limit: maxWarnings, At: p}, read, from)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, w := range sc.Warnings {
			if w.ID != id || name == "not read" {
				if w.English != nil {
					t.Errorf("%s, %s: an English text %+v", name, w.ID, w.English)
				}
				seen = seen || w.ID == id
				continue
			}
			seen = true
			if e := w.English; e == nil || e.Family != "dwd_warnings_en" || e.Headline == nil || *e.Headline != "Frost" ||
				e.Description == nil || *e.Description != "Frost is expected." || e.Instruction != nil {
				t.Errorf("%s: %+v", id, e)
			}
		}
		if !seen {
			t.Fatalf("%s, %s not listed", name, id)
		}
	}
	c, err := st.CellWarnings(bounded(t), weather.CellQuery{Cell: cell}, fams, from)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, w := range c.Warnings {
		listed = listed || w.ID == id
		if (w.ID == id) != (w.English != nil) {
			t.Errorf("the cell read, %s: %+v", w.ID, w.English)
		}
	}
	if !listed {
		t.Errorf("the cell read does not list %s: %v", id, c.Warnings)
	}
}

// TestTheIssuersTextIsUnalteredWhileItsHashHolds: a warning's German text is
// unaltered only while its UTF-8 bytes hash to the hash taken when it was received, and
// its English text only while the headline, description and instruction,
// NUL-separated, hash to the English hash, as the platform judges both
// before it quotes the issuer (#222); a changed text, a changed hash and no
// text are not unaltered. The hashes here are taken in Go, not by the
// statement, so the rule is checked against its own statement of it.
func TestTheIssuersTextIsUnalteredWhileItsHashHolds(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	st := seeded(t)
	fams := append(families(t, st, "weather"), "dwd_warnings_en")
	var (
		p    geo.Point
		from time.Time
		id   string
		hash string
	)
	owner(t, `select st_y(st_pointonsurface(area::geometry)), st_x(st_pointonsurface(area::geometry)), valid_from, 'dwd:' || id, official_sha256 from weather_warnings
where closed_at is null and deleted_at is null and valid_from is not null and closed_reason is distinct from 'withdrawn' and area is not null
  and published_at is not null and warncell_id is not null
order by id limit 1`, &p.Lat, &p.Lon, &from, &id, &hash)
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	ptr := func(s string) *string { return &s }
	const de = "Es tritt leichter Frost auf. Glätte möglich."
	set := `update weather_warnings set description = $2, official_sha256 = $3 where 'dwd:' || id = $1`
	t.Cleanup(func() { change(t, set, id, nil, hash) })
	if n := change(t, `update weather_warnings set raw = jsonb_set(coalesce(raw, '{}'), '{properties}', coalesce(raw->'properties', '{}') || '{"EC_II": "84"}')
where 'dwd:' || id = $1`, id); n != 1 {
		t.Fatalf("updated %d rows", n)
	}
	en := "insert into public.weather_warnings_en (source_id, source_feed, identifier, base_identifier, language, headline, description,\n" +
		"  content_sha256, sent_at, valid_from, valid_to, warncell_ids, raw)\n" +
		"select 'dwd_warnings_en', 'dwd_warnings_en', 'test-en-2', 'test-en-2', 'en-GB', 'Frost', 'Frost is expected.', $2, w.published_at, w.valid_from, w.valid_to,\n" +
		"  array[w.warncell_id], '{\"eventCodes\": {\"II\": \"84\"}}'\nfrom weather_warnings w where 'dwd:' || w.id = $1"
	drop := `delete from public.weather_warnings_en where identifier = 'test-en-2'`
	t.Cleanup(func() { change(t, drop) })
	for _, c := range []struct {
		name               string
		text               *string
		hash, enHash       string
		unaltered, english bool
	}{
		{"as issued", ptr(de), sum(de), sum("Frost\x00Frost is expected.\x00"), true, true},
		{"changed since", ptr(de + " "), sum(de), sum("Frost\x00Frost is expected."), false, false},
		{"no text", nil, sum(""), sum("Frost\x00\x00Frost is expected."), false, false},
	} {
		if n := change(t, set, id, c.text, c.hash); n != 1 {
			t.Fatalf("%s: updated %d rows", c.name, n)
		}
		change(t, drop)
		if n := change(t, en, id, c.enHash); n != 1 {
			t.Fatalf("%s: inserted %d rows", c.name, n)
		}
		sc, err := st.WarningsAt(bounded(t), weather.Query{LookAhead: lookAhead, Limit: maxWarnings, At: p}, fams, from)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, w := range sc.Warnings {
			if w.ID != id {
				continue
			}
			seen = true
			if w.Unaltered != c.unaltered || w.English == nil || w.English.Unaltered != c.english {
				t.Errorf("%s: unaltered %v, English %+v", c.name, w.Unaltered, w.English)
			}
		}
		if !seen {
			t.Fatalf("%s: %s not listed", c.name, id)
		}
	}
}
