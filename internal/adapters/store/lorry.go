package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// lorryRows are the open lorry rows of the five lorry tables, in one shape:
// the long-lorry network, the SEVAS, IVM and Bremen restrictions and the
// city rules, each id prefixed with its table.
const lorryRows = `(
  select 'long:' || l.id rule_id, l.layer, null::text kind, null::text sign, null::numeric value, null::text unit, null::text published_text,
         l.road, null::text street, null::text municipality, l.direction, '{}'::text[] exceptions, null::text times, null::text days,
         null::text category, l.network_state, l.version_date, l.geom, l.source_feed
  from lorry_long_network_live l
  union all
  select 'sevas:' || v.id, v.layer, v.kind, v.sign, v.value, v.unit, null, null, v.street, v.municipality, null, v.exceptions,
         v.times, v.days, null, v.network_state, v.version_date, v.geom, v.source_feed
  from lorry_sevas_live v
  union all
  select 'ivm:' || v.id, v.layer, v.kind, v.sign, v.value, v.unit, null, null, null, null, null, v.exceptions,
         v.times, v.days, v.category, null, v.version_date, v.geom, v.source_feed
  from lorry_ivm_live v
  union all
  select 'hb:' || v.id, v.layer, v.kind, v.sign, v.value, v.unit, v.published_text, null, null, null, null, v.exceptions,
         v.times, v.days, null, null, v.version_date, v.geom, v.source_feed
  from lorry_hb_live v
  union all
  select 'city:' || v.id, v.layer, v.kind, v.sign, v.value, v.unit, v.published_text, v.road, v.street, null, v.direction, v.exceptions,
         v.times, v.days, v.category, null, v.version_date, v.geom, v.source_feed
  from lorry_city_live v) r`

// lorryRules reads the lorry rows of the families $1 (those their source
// rule gives the key): a long lorry's network rows on the road $2, or
// around ($4, $5) the network rows ($3 long_lorry) or every other layer
// within $6 metres, by distance; at most $7. The distance is on the rows'
// own geography, the area index of each lorry table.
var lorryRules = named("api_lorry_rules", `select r.rule_id, r.layer, r.kind, r.sign, r.unit, r.published_text, r.road, r.street,
       r.municipality, r.direction, r.times, r.days, r.category, r.network_state, r.value::float8, r.exceptions, r.version_date,
       case when $4::float8 is null then null
            else round(st_distance(r.geom, st_setsrid(st_makepoint($4::float8, $5::float8), 4326)::geography))::int end,
       r.source_feed
from `+lorryRows+`
where r.source_feed = any((`+ruledFamilies("$1::text[]")+`)::text[])
  and (case when $3::text = 'long_lorry' then r.layer = 'long_lorry' else r.layer <> 'long_lorry' end)
  and ($2::text = '' or upper(replace(r.road, ' ', '')) = $2::text)
  and ($4::float8 is null or st_dwithin(r.geom, st_setsrid(st_makepoint($4::float8, $5::float8), 4326)::geography, $6::float8))
order by 18 nulls last, r.rule_id
limit $7::int`, "text[]", "text", "text", "float8", "float8", "float8", "int")

// LorryRules reads the lorry rows of the given families: a long lorry's
// network rows on a road (by id), or around a position the network rows
// (long_lorry) or every other layer (lorry) within the radius, by distance.
// $1 families, $2 road, $3 the layer kept (long_lorry, or other), $6 the
// radius, $7 the limit.
func (s *Store) LorryRules(ctx context.Context, q roads.LorryQuery, families []string) ([]roads.LorryRule, error) {
	layer := "other"
	if q.Vehicle == "long_lorry" {
		layer = "long_lorry"
	}
	var lon, lat *float64
	if q.At != nil {
		lon, lat = &q.At.Lon, &q.At.Lat
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.LorryRule, error) {
		res, _ := run(ctx, tx, lorryRules, families, q.Road, layer, lon, lat, float64(q.RadiusM), q.Limit)
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.LorryRule, error) {
			var v roads.LorryRule
			err := r.Scan(&v.ID, &v.Layer, &v.Kind, &v.Sign, &v.Unit, &v.PublishedText, &v.Road, &v.Street, &v.Municipality,
				&v.Direction, &v.Times, &v.Days, &v.Category, &v.NetworkState, &v.Value, &v.Exceptions, &v.VersionDate, &v.DistanceM, &v.Family)
			return v, err
		})
	})
}
