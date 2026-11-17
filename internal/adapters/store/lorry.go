package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// lorryColumns are a lorry row's columns; $4 lon, $5 lat (null without a
// position) give its distance.
const lorryColumns = `r.rule_id, r.rule_layer, r.restriction_kind, r.sign, r.unit, r.published_text, r.road_ref, r.street,
       r.municipality, r.direction, r.times, r.days, r.category, r.network_state, r.value::float8, r.exceptions, r.version_date,
       case when $4::float8 is null then null else round(st_distance(r.geometry::geography, st_setsrid(st_makepoint($4, $5), 4326)::geography))::int end,
       r.source_ref
from contract.lorry_rule r`

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
		res, _ := tx.Query(ctx, `select `+lorryColumns+`
where r.source_ref = any($1) and `+permitted("r")+`
  and (case when $3::text = 'long_lorry' then r.rule_layer = 'long_lorry' else r.rule_layer <> 'long_lorry' end)
  and ($2::text = '' or upper(replace(r.road_ref, ' ', '')) = $2::text)
  and ($4::float8 is null or st_dwithin(r.geometry::geography, st_setsrid(st_makepoint($4, $5), 4326)::geography, $6))
order by 18 nulls last, r.rule_id
limit $7`, families, q.Road, layer, lon, lat, q.RadiusM, q.Limit)
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.LorryRule, error) {
			var v roads.LorryRule
			err := r.Scan(&v.ID, &v.Layer, &v.Kind, &v.Sign, &v.Unit, &v.PublishedText, &v.Road, &v.Street, &v.Municipality,
				&v.Direction, &v.Times, &v.Days, &v.Category, &v.NetworkState, &v.Value, &v.Exceptions, &v.VersionDate, &v.DistanceM, &v.Family)
			return v, err
		})
	})
}
