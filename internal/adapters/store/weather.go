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
// a position: a published, not cancelled warning of the given families that
// has not ended at now and whose area contains the position. $1 families, $2
// now, $3 latitude, $4 longitude. A warning stored with no area (a null
// region_code) cannot be placed, so it contains no position and answers no
// question: the inner join leaves it out, as a left join would (its null
// boundary intersects nothing).
const warningPlaced = `from contract.weather_warning w
join contract.admin_region r on r.region_code = w.region_code
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1)
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and r.display_class <> 'withhold' and (r.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2)
  and st_intersects(r.boundary, st_setsrid(st_makepoint($4, $3), 4326))`

// warningScope is the condition of the lamp's scope: a placed warning
// (warningPlaced) in force at now.
const warningScope = warningPlaced + `
  and w.valid_from <= $2`

// warningsPage reads the warnings of the scope the floor and the hazards
// keep, worst first. $5 the level floor, $6 the mapped hazards kept, $7
// whether the unmapped ones are kept, $8 every mapped hazard, $9 the limit.
const warningsPage = `
select w.warning_id, w.source_ref, w.hazard, w.warning_level, w.level_label, w.issuer_name, w.headline,
       w.text_lang, w.valid_from, w.valid_to, r.name
` + warningScope + `
  and w.warning_level >= $5
  and (w.hazard = any($6) or ($7 and not w.hazard = any($8)))
order by w.warning_level desc, w.valid_from, w.warning_id
limit $9`

// warningsHere reads every placed warning, whatever the floor and the
// hazards keep: its level, whether it is in force, and its next start or
// end after now.
const warningsHere = `select w.warning_id, w.warning_level, w.valid_from <= $2,
  case when w.valid_from > $2 then w.valid_from else w.valid_to end
` + warningPlaced

// levelOf refuses a stored level outside 1 to 4 rather than re-grading it
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
		&v.TextLanguage, &v.From, &until, &v.AreaName)
	v.Hazard, v.From = weather.MapHazard(hazard), v.From.UTC()
	if until != nil {
		u := until.UTC()
		v.Until = &u
	}
	lvl, lerr := levelOf(v.ID, level)
	v.Level = lvl
	return v, errors.Join(err, lerr)
}

// WarningsAt reads, in one snapshot, the warnings in the scope
// (warningScope) at or above the level floor, of the asked hazards; level
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
		rows, _ := tx.Query(ctx, warningsPage, families, now, q.At.Lat, q.At.Lon, int(q.MinLevel), hazards, other, every, q.Limit+1)
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
		rows, _ = tx.Query(ctx, warningsHere, families, now, q.At.Lat, q.At.Lon)
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

// Topic reads one rule topic and its rules in their order.
func (s *Store) Topic(ctx context.Context, code string) (rules.Topic, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.Topic, error) {
		rows, _ := tx.Query(ctx, `
select t.topic_code, t.title, t.reviewed_on,
       coalesce(array_agg(s.body order by s.position, s.statement_id) filter (where s.statement_id is not null), '{}'),
       coalesce(array_agg(s.legal_basis order by s.position, s.statement_id) filter (where s.statement_id is not null), '{}')
from contract.rule_topic t
left join contract.rule_statement s on s.topic_code = t.topic_code and `+permitted("s")+`
where t.topic_code = $1 and `+permitted("t")+`
group by t.topic_code, t.title, t.reviewed_on`, code)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (rules.Topic, error) {
			var (
				v      rules.Topic
				bodies []string
				bases  []*string
			)
			err := r.Scan(&v.Code, &v.Title, &v.ReviewedOn, &bodies, &bases)
			v.Rules = make([]rules.Rule, len(bodies))
			for i, b := range bodies {
				v.Rules[i] = rules.Rule{Text: b, LegalBasis: bases[i]}
			}
			return v, err
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

// zoneAlive is the platform's freshness gate of a zone source at $3: its last
// check within three of its own cadences (30 days when it states none), or
// its refresh paused after a check.
const zoneAlive = `(z.checked_at > $3::timestamptz - make_interval(secs => 3 * coalesce(z.cadence_s, 2592000))
    or (z.paused and z.checked_at is not null))`

// Zones reads the low-emission-zone rows at a point (within the platform's
// reach, inside first, then by distance) or of a city (by name), of the
// given families whose source is alive at now.
func (s *Store) Zones(ctx context.Context, q rules.ZoneQuery, families []string, now time.Time) ([]rules.Zone, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.Zone, error) {
		var res pgx.Rows
		if q.At != nil {
			res, _ = tx.Query(ctx, `
with p as (select st_transform(st_setsrid(st_makepoint($4::float8, $5::float8), 4326), 25832) m, st_setsrid(st_makepoint($4::float8, $5::float8), 4326) g),
r as (
  select z.*, (z.zone_role <> 'exempt_road' and st_covers(z.geometry, p.g)) inside,
         case when z.zone_role <> 'exempt_road' and st_covers(z.geometry, p.g) then 0 else round(st_distance(z.geometry_m, p.m))::int end dist
  from contract.low_emission_zone z, p
  where z.source_ref = any($1) and `+permitted("z")+` and `+zoneAlive+`
    and z.geometry_m && st_expand(p.m, $2::float8)
    and st_dwithin(z.geometry_m, p.m, case when z.zone_role = 'exempt_road' then $6::float8 else $2::float8 end))
select r.zone_key, r.zone_id, r.zone_role, r.name, r.city, r.publisher_name, r.inside, r.dist,
       array(select k from unnest(r.same_zone_keys) k where k in (select o.zone_key from r o where o.zone_role = 'zone') and r.zone_role = 'zone'),
       r.source_ref, r.changed_at, r.recorded_at, r.source_scope, r.source_place_code, r.source_city
from r
order by r.inside desc, r.dist, r.source_ref, r.zone_id`, families, float64(rules.ZoneReachM), now, q.At.Lon, q.At.Lat, float64(rules.ExemptRoadReachM))
		} else {
			res, _ = tx.Query(ctx, `
select z.zone_key, z.zone_id, z.zone_role, z.name, z.city, z.publisher_name, false, 0,
       array(select k from unnest(z.same_zone_keys) k
              where k in (select o.zone_key from contract.low_emission_zone o where lower(o.city) = lower($2) and o.source_ref = any($1))),
       z.source_ref, z.changed_at, z.recorded_at, z.source_scope, z.source_place_code, z.source_city
from contract.low_emission_zone z
where z.source_ref = any($1) and `+permitted("z")+` and `+zoneAlive+`
  and z.zone_role = 'zone' and lower(z.city) = lower($2)
order by z.source_ref, z.zone_id`, families, q.City, now)
		}
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.Zone, error) {
			var z rules.Zone
			err := r.Scan(&z.Key, &z.ZoneID, &z.Role, &z.Name, &z.City, &z.Publisher, &z.Inside, &z.DistanceM, &z.SameZone, &z.Family,
				&z.ChangedAt, &z.RecordedAt, &z.Scope, &z.PlaceCode, &z.SourceCity)
			return z, err
		})
	})
}

// ZoneEntries reads the entries of the curated zone list that speak for any
// of the Gemeinde keys codes.
func (s *Store) ZoneEntries(ctx context.Context, codes []string) ([]rules.ListEntry, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.ListEntry, error) {
		res, _ := tx.Query(ctx, `
select e.entry_key, e.name, e.place_codes, e.plaque, e.covered, e.lifted_on
from contract.low_emission_zone_entry e
where e.place_codes && $1::text[] and `+permitted("e")+`
order by e.entry_key`, codes)
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.ListEntry, error) {
			var e rules.ListEntry
			err := r.Scan(&e.Key, &e.Name, &e.PlaceCodes, &e.Plaque, &e.Covered, &e.LiftedOn)
			return e, err
		})
	})
}

// GemeindeAt reads the nearest municipality of the families within reachM
// metres of at: the Gemeinde whose key finds a zone list entry.
func (s *Store) GemeindeAt(ctx context.Context, at geo.Point, reachM int, families []string) (*places.Place, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (*places.Place, error) {
		rows, _ := tx.Query(ctx, nearestPlace, at.Lon, at.Lat, "municipality", reachM, families)
		ps, err := pgx.CollectRows(rows, scanPlace)
		return first(ps), err
	})
}
