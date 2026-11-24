package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// The weather-risk and driving-rules repositories (anti-corruption layer).

// warningPlaced is the condition of a warning the lamp reads or will read at
// a position, as the platform's warnings at a point read them: a published,
// not withdrawn or cancelled warning of the given families that has not
// ended at now and whose warn cell's area contains the position. $1
// families, $2 now, $3 latitude, $4 longitude. A warning stored with no area
// cannot be placed, so it answers no position. The box test is on the
// area's geometry, the expression of the open rows' area index
// (weather_warnings_live_area_geom): the read contract's view cast the area
// to geometry(MultiPolygon,4326), an expression no index holds, and read
// every open warning's area on prod (1.4 s a call).
const warningPlaced = `from weather_warnings w
where w.closed_at is null and w.deleted_at is null and w.valid_from is not null
  and w.closed_reason is distinct from 'withdrawn'
  and w.source_feed = any((select $1::text[])::text[])
  and (w.valid_to is null or w.valid_to > (select $2::timestamptz))
  and w.area is not null and w.area::geometry && (select st_setsrid(st_makepoint($4::float8, $3::float8), 4326))
  and st_intersects(w.area, (select st_setsrid(st_makepoint($4::float8, $3::float8), 4326)::geography))`

// warningHazard is the hazard of the warning aliased w, by its DWD event, as
// the platform maps it.
const warningHazard = `case when w.event ~* 'gewitter' then 'thunderstorm'
  when w.event ~* '(sturm|wind|böen|orkan)' then 'wind' when w.event ~* 'hochwasser|flut' then 'flood'
  when w.event ~* 'schnee' then 'snow' when w.event ~* '(glätte|glatteis|eis)' then 'ice'
  when w.event ~* 'regen' then 'rain' when w.event ~* 'nebel' then 'fog' when w.event ~* 'hitze' then 'heat'
  when w.event ~* 'frost' then 'frost' when w.event ~* 'uv' then 'uv' else 'other' end`

// warningColumns are a warning's columns, as scanWarning reads them.
const warningColumns = `select 'dwd:' || w.id warning_id, w.source_feed, ` + warningHazard + ` hazard, w.level::int warning_level,
       coalesce(w.severity, w.level::text), 'Deutscher Wetterdienst', coalesce(w.headline, w.event),
       'de', w.valid_from, w.valid_to, coalesce(w.area_desc, ''), w.warncell_id, w.event, w.identifier, w.published_at`

// warningsPage reads the placed warnings in force at now or starting before
// $10 (now and the look-ahead) that the floor and the hazards keep, worst
// first, then the earliest start. $5 the level floor, $6 the mapped hazards
// kept, $7 whether the unmapped ones are kept, $8 every mapped hazard, $9
// the limit. Every parameter is read through a subquery, so the plan cache
// keeps the generic plan.
var warningsPage = named("api_weather_warnings_page", warningColumns+`
`+warningPlaced+`
  and w.valid_from < (select $10::timestamptz)
  and w.level >= (select $5::int)
  and (`+warningHazard+` = any((select $6::text[])::text[])
       or ((select $7::bool) and not `+warningHazard+` = any((select $8::text[])::text[])))
order by warning_level desc, w.valid_from, warning_id
limit (select $9::bigint)`, "text[]", "timestamptz", "float8", "float8", "int", "text[]", "bool", "text[]", "bigint", "timestamptz")

// warningsHere reads every placed warning, whatever the floor and the
// hazards keep: its level, whether it is in force, and its next start or
// end after now.
var warningsHere = named("api_weather_warnings_here", `select 'dwd:' || w.id, w.level::int, w.valid_from <= (select $2::timestamptz),
  case when w.valid_from > (select $2::timestamptz) then w.valid_from else w.valid_to end
`+warningPlaced, "text[]", "timestamptz", "float8", "float8")

// levelOf refuses a stored level outside 0 to 4 rather than re-grading it
// (FR-WEATHER-002); the error names the warning.
func levelOf(id string, raw int) (weather.Level, error) {
	lvl, err := weather.CheckLevel(raw)
	if err != nil {
		return 0, fmt.Errorf("weather warning %s, level %d: %w", id, raw, err)
	}
	return lvl, nil
}

func scanWarning(r pgx.CollectableRow) (weather.Warning, error) {
	var (
		v      weather.Warning
		hazard string
		level  int
		until  *time.Time
	)
	err := r.Scan(&v.ID, &v.Family, &hazard, &level, &v.LevelName, &v.Issuer, &v.Headline,
		&v.TextLanguage, &v.From, &until, &v.AreaName, &v.CellID, &v.Event, &v.Identifier, &v.Published)
	v.Hazard, v.From = weather.MapHazard(hazard), v.From.UTC()
	if v.Published != nil {
		p := v.Published.UTC()
		v.Published = &p
	}
	if until != nil {
		u := until.UTC()
		v.Until = &u
	}
	lvl, lerr := levelOf(v.ID, level)
	v.Level = lvl
	return v, errors.Join(err, lerr)
}

// WarningsAt reads, in one snapshot, the placed warnings in force or
// starting within q.LookAhead, at or above the level floor, of the asked hazards; level
// descending, then start, then id; at most q.Limit, and whether
// more answer. It also reads every placed warning (warningPlaced), whatever
// the floor and the hazards keep: the level of each one in force, for the
// lamp, and the earliest start or end after now of any, when the scope
// changes with the clock alone. A stored level outside 1 to 4 of a warning in
// force is refused rather than re-graded.
func (s *Store) WarningsAt(ctx context.Context, q weather.Query, families []string, now time.Time) (weather.Scope, error) {
	known, other := q.KnownHazards()
	hazards := make([]string, len(known))
	for i, h := range known {
		hazards[i] = string(h)
	}
	every := make([]string, len(weather.Hazards)-1)
	for i, h := range weather.Hazards[:len(weather.Hazards)-1] {
		every[i] = string(h)
	}
	// The scope is built inside the read and handed back by ReadValue, so
	// nothing the caller holds is written after a read past its deadline.
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (weather.Scope, error) {
		var out weather.Scope
		rows, _ := run(ctx, tx, warningsPage, families, now, q.At.Lat, q.At.Lon, int(q.MinLevel), hazards, other, every, q.Limit+1, now.Add(q.LookAhead))
		var err error
		if out.Warnings, err = pgx.CollectRows(rows, scanWarning); err != nil {
			return out, err
		}
		var (
			id      string
			level   int
			inForce bool
			next    *time.Time
		)
		rows, _ = run(ctx, tx, warningsHere, families, now, q.At.Lat, q.At.Lon)
		_, err = pgx.ForEachRow(rows, []any{&id, &level, &inForce, &next}, func() error {
			if next != nil && (out.NextChange == nil || next.Before(*out.NextChange)) {
				t := next.UTC()
				out.NextChange = &t
			}
			if !inForce {
				return nil
			}
			lvl, err := levelOf(id, level)
			out.Levels = append(out.Levels, lvl)
			return err
		})
		return out, err
	})
	if err != nil {
		return weather.Scope{}, err
	}
	if len(out.Warnings) > q.Limit {
		out.Warnings, out.HasMore = out.Warnings[:q.Limit], true
	}
	return out, nil
}

// ruleTopic reads the rule topic $1 and its rules in their order, in the
// language $2 (en: the English text where one is held).
var ruleTopic = named("api_rule_topic", `
select t.topic_code, case when (select $2::text) = 'en' then coalesce(t.title_en, t.title_de) else t.title_de end,
       coalesce(min(s.reviewed_on), t.reviewed_on),
       coalesce(jsonb_agg(jsonb_build_object(
         'id', s.fact_key,
         'text', case when (select $2::text) = 'en' then coalesce(s.body_en, s.body_de) else s.body_de end,
         'basis', s.source_name, 'url', s.source_url, 'reviewed', s.reviewed_on,
         'note', nullif(btrim(case when (select $2::text) = 'en' then coalesce(s.source_note_en, s.source_note_de) else s.source_note_de end), ''),
         'verify', nullif(btrim(case when (select $2::text) = 'en' then coalesce(s.to_verify_en, s.to_verify_de) else s.to_verify_de end), ''))
         order by s.position, s.fact_key) filter (where s.fact_key is not null), '[]')
from driving_rule_topics t
left join driving_rule_facts s on s.topic_code = t.topic_code
where t.topic_code = (select $1::text)
group by t.topic_code, t.title_de, t.title_en, t.reviewed_on`, "text", "text")

// Topic reads one rule topic and its rules in their order.
func (s *Store) Topic(ctx context.Context, code, lang string) (rules.Topic, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.Topic, error) {
		rows, _ := run(ctx, tx, ruleTopic, code, lang)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (rules.Topic, error) {
			var (
				v   rules.Topic
				raw []struct {
					ID       string  `json:"id"`
					Text     string  `json:"text"`
					Basis    *string `json:"basis"`
					URL      *string `json:"url"`
					Reviewed string  `json:"reviewed"`
					Verify   *string `json:"verify"`
					Note     *string `json:"note"`
				}
			)
			if err := r.Scan(&v.Code, &v.Title, &v.ReviewedOn, &raw); err != nil {
				return v, err
			}
			v.Rules = make([]rules.Rule, len(raw))
			for i, x := range raw {
				on, err := time.Parse(time.DateOnly, x.Reviewed)
				if err != nil {
					return v, err
				}
				v.Rules[i] = rules.Rule{ID: x.ID, Text: x.Text, LegalBasis: x.Basis, LegalBasisURL: x.URL, LegalBasisNote: x.Note, ReviewedOn: on, ToVerify: x.Verify}
			}
			return v, nil
		})
	})
	if err == nil && len(out) == 0 {
		err = rules.ErrUnknownTopic
	}
	if err != nil {
		return rules.Topic{}, err
	}
	return out[0], nil
}

// sourceRuled is whether the source rule of the family expr (the first of
// contract_source_rules by priority whose pattern it matches) gives its rows
// to the key: not excluded, not withheld, and public or the key one of
// ViaFrei's own; a family no rule matches is open and public.
func sourceRuled(expr string) string {
	return `coalesce((select not coalesce(k.excluded, false) and coalesce(k.display_class, 'open') <> 'withhold'
      and (coalesce(k.audience, 'public') = 'public' or ` + ownKeyOn + `)
    from contract_source_rules k where ` + expr + ` ~ k.pattern order by k.priority limit 1), true)`
}

// ruledFamilies is the families of the array expr whose source rule gives
// their rows to the key (sourceRuled), as one array f: a row of a family is
// read when its family is one of them, the rule decided once a family, not
// once a row.
func ruledFamilies(expr string) string {
	return `select coalesce(array_agg(x.f), '{}') f from unnest(` + expr + `) x(f) where ` + sourceRuled("x.f")
}

// zoneAlive is the platform's freshness gate of a zone source at $3: its last
// check within three of its own cadences (30 days when it states none), or
// its refresh paused after a check.
const zoneAlive = `(c.checked_at > (select $3::timestamptz) - make_interval(secs => 3 * coalesce(c.cadence_s, 2592000))
    or (coalesce(c.paused, false) and c.checked_at is not null))`

// zonesAt reads the low-emission-zone rows of the live zones within $2
// metres of the point $4, $5 (an exempt road within $6), of the families $1
// whose source rule (contract_source_rules, the first by priority) neither
// excludes nor withholds them and gives them to the key, and whose source is
// alive at $3: inside first, then by distance.
var zonesAt = named("api_zones_at", `
with p as (select st_transform(st_setsrid(st_makepoint((select $4::float8), (select $5::float8)), 4326), 25832) m,
                  st_setsrid(st_makepoint((select $4::float8), (select $5::float8)), 4326) g),
r as (
  select z.source_feed || '/' || z.zone_id zone_key, z.zone_id, z.role zone_role, z.name, z.place_name city, z.source_publisher publisher_name,
         z.source_feed, z.provider_updated_at changed_at, z.last_seen_at recorded_at,
         f.scope source_scope, f.place_code source_place_code, f.city source_city,
         coalesce((select array_agg(x.other_feed || '/' || x.other_zone_id order by x.other_feed, x.other_zone_id) from lez_zone_overlaps x
                    where x.source_feed = z.source_feed and x.zone_id = z.zone_id and x.shared_area >= 0.5 * x.smaller_area), '{}') same_zone_keys,
         (z.role <> 'exempt_road' and st_covers(z.geom, p.g)) inside,
         case when z.role <> 'exempt_road' and st_covers(z.geom, p.g) then 0 else round(st_distance(z.geom_m, p.m))::int end dist
  from lez_zones_live z
  cross join p
  left join feed_checks c on c.feed_key = z.source_feed
  left join lez_feed_places f on f.feed_key = z.source_feed
  where z.source_feed = any((select $1::text[])::text[]) and `+zoneAlive+`
    and `+sourceRuled("z.source_feed")+`
    and z.geom_m && st_expand(p.m, (select $2::float8))
    and st_dwithin(z.geom_m, p.m, case when z.role = 'exempt_road' then (select $6::float8) else (select $2::float8) end))
select r.zone_key, r.zone_id, r.zone_role, r.name, r.city, r.publisher_name, r.inside, r.dist,
       array(select k from unnest(r.same_zone_keys) k where k in (select o.zone_key from r o where o.zone_role = 'zone') and r.zone_role = 'zone'),
       r.source_feed, r.changed_at, r.recorded_at, r.source_scope, r.source_place_code, r.source_city
from r
order by r.inside desc, r.dist, r.source_feed, r.zone_id`, "text[]", "float8", "timestamptz", "float8", "float8", "float8")

// Zones reads the low-emission-zone rows at q's point (within the
// platform's reach, inside first, then by distance), of the given families
// whose source is alive at now; none without a point (a city is answered
// from the curated list, ZoneEntryNamed).
func (s *Store) Zones(ctx context.Context, q rules.ZoneQuery, families []string, now time.Time) ([]rules.Zone, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.Zone, error) {
		if q.At == nil {
			return nil, nil
		}
		res, _ := run(ctx, tx, zonesAt, families, float64(rules.ZoneReachM), now, q.At.Lon, q.At.Lat, float64(rules.ExemptRoadReachM))
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.Zone, error) {
			var z rules.Zone
			err := r.Scan(&z.Key, &z.ZoneID, &z.Role, &z.Name, &z.City, &z.Publisher, &z.Inside, &z.DistanceM, &z.SameZone, &z.Family,
				&z.ChangedAt, &z.RecordedAt, &z.Scope, &z.PlaceCode, &z.SourceCity)
			return z, err
		})
	})
}

// zoneEntries are the entries of the curated zone list that speak for any of
// the Gemeinde keys $1.
var zoneEntries = named("api_zone_entries", `
select e.entry_key, e.name, e.place_codes, e.plaque, e.covered, e.lifted_on
from lez_list_entries e
where e.place_codes && (select $1::text[])
order by e.entry_key`, "text[]")

// zoneEntryNamed is the entry of the curated zone list whose key or an alias
// is $1.
var zoneEntryNamed = named("api_zone_entry_named", `
select e.entry_key, e.name, e.place_codes, e.plaque, e.covered, e.lifted_on
from lez_list_entries e
where (e.entry_key = (select $1::text) or (select $1::text) = any(e.aliases))
order by e.entry_key limit 1`, "text")

// ZoneEntries reads the entries of the curated zone list that speak for any
// of the Gemeinde keys codes.
func (s *Store) ZoneEntries(ctx context.Context, codes []string) ([]rules.ListEntry, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.ListEntry, error) {
		res, _ := run(ctx, tx, zoneEntries, codes)
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.ListEntry, error) {
			var e rules.ListEntry
			err := r.Scan(&e.Key, &e.Name, &e.PlaceCodes, &e.Plaque, &e.Covered, &e.LiftedOn)
			return e, err
		})
	})
}

// ZoneEntryNamed reads the entry of the curated zone list whose key or an
// alias is name, a city name normalised as the list keys them; nil when the
// list holds none.
func (s *Store) ZoneEntryNamed(ctx context.Context, name string) (*rules.ListEntry, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (*rules.ListEntry, error) {
		res, _ := run(ctx, tx, zoneEntryNamed, name)
		es, err := pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.ListEntry, error) {
			var e rules.ListEntry
			err := r.Scan(&e.Key, &e.Name, &e.PlaceCodes, &e.Plaque, &e.Covered, &e.LiftedOn)
			return e, err
		})
		return first(es), err
	})
}

// GemeindeAt reads the nearest municipality of the families within reachM
// metres of at: the Gemeinde whose key finds a zone list entry.
func (s *Store) GemeindeAt(ctx context.Context, at geo.Point, reachM int, families []string) (*places.Place, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (*places.Place, error) {
		rows, _ := run(ctx, tx, nearestPlace, at.Lon, at.Lat, "municipality", reachM, families)
		ps, err := pgx.CollectRows(rows, scanPlace)
		return first(ps), err
	})
}
