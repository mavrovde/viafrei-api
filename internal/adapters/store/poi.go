package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// poiMatch reads the places of interest whose name or brand holds $1 (both
// in their search form): in the town $2, or within $5 metres of ($3, $4),
// or anywhere; in the family $6 when set. The name spelt as asked first,
// then one that begins with it, then the nearest, then the closest in
// trigrams and by name, as the platform ranks equal matches; at most
// places.PoiReach+1 from the start, so the total says whether more matched;
// $7 the families.
var poiMatch = `select p.poi_id, p.name, p.category, p.category_value, p.brand, p.operator_name, p.street, p.house_number,
       p.postcode, p.place_name, st_y(p.location), st_x(p.location),
       case when $3::float8 is null then null
            else round(st_distance(p.location::geography, st_setsrid(st_makepoint($3, $4), 4326)::geography))::int end as distance_m,
       p.data_as_of, p.source_ref
from contract.poi p
where (p.name_fold like '%' || $1 || '%' or lower(p.brand) like '%' || $1 || '%')
  and ($2::text is null or p.place_fold = $2)
  and ($3::float8 is null or st_dwithin(p.location::geography, st_setsrid(st_makepoint($3, $4), 4326)::geography, $5))
  and ($6::text is null or p.category = $6)
  and p.source_ref = any($7) and ` + permitted("p") + `
order by case when p.name_fold = $1 then 0 when p.name_fold like $1 || '%' then 1 else 2 end,
         distance_m nulls last, similarity(p.name_fold, $1) desc, p.name, p.poi_id
limit $8`

// Pois reads the places of interest q asks for, from the first, at most
// places.PoiReach+1 (features.Pois).
func (s *Store) Pois(ctx context.Context, q places.PoiQuery, families []string) ([]places.Poi, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Poi, error) {
		var lon, lat *float64
		if q.At != nil {
			lon, lat = &q.At.Lon, &q.At.Lat
		}
		rows, _ := tx.Query(ctx, poiMatch, places.NameFold(q.Name), nullText(places.NameFold(q.In)), lon, lat, q.RadiusM,
			nullText(q.Category), families, places.PoiReach+1)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Poi, error) {
			var v places.Poi
			err := r.Scan(&v.ID, &v.Name, &v.Category, &v.CategoryValue, &v.Brand, &v.Operator, &v.Street, &v.HouseNumber,
				&v.Postcode, &v.Place, &v.Location.Lat, &v.Location.Lon, &v.DistanceM, &v.DataAsOf, &v.Family)
			return v, err
		})
	})
}
