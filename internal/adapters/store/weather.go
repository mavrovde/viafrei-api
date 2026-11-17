package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
func (s *Store) Topic(ctx context.Context, code, lang string) (rules.Topic, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]rules.Topic, error) {
		rows, _ := tx.Query(ctx, `
select t.topic_code, case when $2 = 'en' then coalesce(t.title_en, t.title) else t.title end,
       coalesce(min(s.reviewed_on), t.reviewed_on),
       coalesce(jsonb_agg(jsonb_build_object(
         'id', s.statement_id,
         'text', case when $2 = 'en' then coalesce(s.body_en, s.body) else s.body end,
         'basis', s.legal_basis, 'url', s.legal_basis_url, 'reviewed', s.reviewed_on,
         'verify', nullif(btrim(case when $2 = 'en' then coalesce(s.to_verify_en, s.to_verify) else s.to_verify end), ''))
         order by s.position, s.statement_id) filter (where s.statement_id is not null), '[]')
from contract.rule_topic t
left join contract.rule_statement s on s.topic_code = t.topic_code and `+permitted("s")+`
where t.topic_code = $1 and `+permitted("t")+`
group by t.topic_code, t.title, t.title_en, t.reviewed_on`, code, lang)
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
				v.Rules[i] = rules.Rule{ID: x.ID, Text: x.Text, LegalBasis: x.Basis, LegalBasisURL: x.URL, ReviewedOn: on, ToVerify: x.Verify}
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
       r.source_ref
from r
order by r.inside desc, r.dist, r.source_ref, r.zone_id`, families, float64(rules.ZoneReachM), now, q.At.Lon, q.At.Lat, float64(rules.ExemptRoadReachM))
		} else {
			res, _ = tx.Query(ctx, `
select z.zone_key, z.zone_id, z.zone_role, z.name, z.city, z.publisher_name, false, 0,
       array(select k from unnest(z.same_zone_keys) k
              where k in (select o.zone_key from contract.low_emission_zone o where lower(o.city) = lower($2) and o.source_ref = any($1))),
       z.source_ref
from contract.low_emission_zone z
where z.source_ref = any($1) and `+permitted("z")+` and `+zoneAlive+`
  and z.zone_role = 'zone' and lower(z.city) = lower($2)
order by z.source_ref, z.zone_id`, families, q.City, now)
		}
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (rules.Zone, error) {
			var z rules.Zone
			err := r.Scan(&z.Key, &z.ZoneID, &z.Role, &z.Name, &z.City, &z.Publisher, &z.Inside, &z.DistanceM, &z.SameZone, &z.Family)
			return z, err
		})
	})
}
