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

// warningScope is the condition of the lamp's scope: a published, not
// cancelled warning of the given families in force at now whose area
// contains the position. $1 families, $2 now, $3 latitude, $4 longitude. A
// warning stored with no area (a null region_code) cannot be placed, so it
// contains no position and answers no question: the inner join leaves it
// out, as a left join would (its null boundary intersects nothing).
const warningScope = `from contract.weather_warning w
join contract.admin_region r on r.region_code = w.region_code
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1)
  and w.valid_from <= $2 and (w.valid_to is null or w.valid_to > $2)
  and st_intersects(r.boundary, st_setsrid(st_makepoint($4, $3), 4326))`

// WarningsAt reads the warnings in the scope (warningScope) at or above the
// level floor, of the asked hazards; level descending, then start, then id;
// at most weather.MaxWarnings, and whether more answer. In the same snapshot
// (Read is one repeatable-read transaction)
// it reads the level of every warning in the scope, whatever the floor and
// the hazards keep, for the lamp. A stored level outside 1 to 4 is refused
// rather than re-graded.
func (s *Store) WarningsAt(ctx context.Context, q weather.Query, families []string, now time.Time) ([]weather.Warning, []weather.Level, bool, error) {
	var (
		out    []weather.Warning
		levels []weather.Level
	)
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		known, other := q.KnownHazards()
		hazards := make([]string, len(known))
		for i, h := range known {
			hazards[i] = string(h)
		}
		every := make([]string, len(weather.Hazards)-1)
		for i, h := range weather.Hazards[:len(weather.Hazards)-1] {
			every[i] = string(h)
		}
		rows, _ := tx.Query(ctx, `
select w.warning_id, w.source_ref, w.hazard, w.warning_level, w.level_label, w.issuer_name, w.headline,
       w.text_lang, w.valid_from, w.valid_to, r.name
`+warningScope+`
  and w.warning_level >= $5
  and (w.hazard = any($6) or ($7 and not w.hazard = any($8)))
order by w.warning_level desc, w.valid_from, w.warning_id
limit $9`, families, now, q.At.Lat, q.At.Lon, int(q.MinLevel), hazards, other, every, weather.MaxWarnings+1)
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (weather.Warning, error) {
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
			// A level outside 1 to 4 is refused, never re-graded
			// (FR-WEATHER-002); the error names the warning.
			lvl, lerr := weather.CheckLevel(level)
			if lerr != nil {
				lerr = fmt.Errorf("weather warning %s, level %d: %w", v.ID, level, lerr)
			}
			v.Level = lvl
			return v, errors.Join(err, lerr)
		})
		if err != nil {
			return err
		}
		rows, _ = tx.Query(ctx, `select w.warning_id, w.warning_level `+warningScope, families, now, q.At.Lat, q.At.Lon)
		levels, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (weather.Level, error) {
			var (
				id    string
				level int
			)
			err := r.Scan(&id, &level)
			lvl, lerr := weather.CheckLevel(level)
			if lerr != nil {
				lerr = fmt.Errorf("weather warning %s, level %d: %w", id, level, lerr)
			}
			return lvl, errors.Join(err, lerr)
		})
		return err
	})
	if err != nil {
		return nil, nil, false, err
	}
	if len(out) > weather.MaxWarnings {
		return out[:weather.MaxWarnings], levels, true, nil
	}
	return out, levels, false, nil
}

// Topic reads one rule topic and its rules in their order.
func (s *Store) Topic(ctx context.Context, code string) (rules.Topic, error) {
	var out []rules.Topic
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
select t.topic_code, t.title, t.reviewed_on,
       coalesce(array_agg(s.body order by s.position, s.statement_id) filter (where s.statement_id is not null), '{}'),
       coalesce(array_agg(s.legal_basis order by s.position, s.statement_id) filter (where s.statement_id is not null), '{}')
from contract.rule_topic t
left join contract.rule_statement s on s.topic_code = t.topic_code
where t.topic_code = $1
group by t.topic_code, t.title, t.reviewed_on`, code)
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (rules.Topic, error) {
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
		return err
	})
	if err == nil && len(out) == 0 {
		err = rules.ErrUnknownTopic
	}
	if err != nil {
		return rules.Topic{}, err
	}
	return out[0], nil
}
