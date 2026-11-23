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
// The text, the town, the point and the category decide the plan (a
// trigram match, a town's names, a radius), so they are sent as written, not
// opaque: a generic plan cannot see which of them is set.
var poiMatch = named("api_poi_match", `select 'osm:' || p.osm_type || p.osm_id poi_id, p.name, p.category, p.category_value, p.brand, p.operator,
       p.street, p.house_number, p.postcode, p.place, st_y(p.geom::geometry), st_x(p.geom::geometry),
       case when $3::float8 is null then null
            else round(st_distance(p.geom, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography))::int end as distance_m,
       p.extract_date, p.source_id
from osm_pois p
where p.closed_at is null and p.geom is not null
  and (p.name_norm like '%' || $1::text || '%' or lower(p.brand) like '%' || $1::text || '%')
  and ($2::text is null or p.place_norm = $2::text)
  and ($3::float8 is null or st_dwithin(p.geom, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography, $5::float8))
  and ($6::text is null or p.category = $6::text)
  and p.source_id = any((`+ruledFamilies("$7::text[]")+`)::text[])
order by case when p.name_norm = $1::text then 0 when p.name_norm like $1::text || '%' then 1 else 2 end,
         distance_m nulls last, similarity(p.name_norm, $1::text) desc, p.name, poi_id
limit $8::int`, "text", "text", "float8", "float8", "float8", "text", "text[]", "int")

// Pois reads the places of interest q asks for, from the first, at most
// places.PoiReach+1 (features.Pois).
func (s *Store) Pois(ctx context.Context, q places.PoiQuery, families []string) ([]places.Poi, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Poi, error) {
		var lon, lat *float64
		if q.At != nil {
			lon, lat = &q.At.Lon, &q.At.Lat
		}
		rows, _ := run(ctx, tx, poiMatch, places.NameFold(q.Name), nullText(places.NameFold(q.In)), lon, lat, float64(q.RadiusM),
			nullText(q.Category), families, places.PoiReach+1)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Poi, error) {
			var v places.Poi
			err := r.Scan(&v.ID, &v.Name, &v.Category, &v.CategoryValue, &v.Brand, &v.Operator, &v.Street, &v.HouseNumber,
				&v.Postcode, &v.Place, &v.Location.Lat, &v.Location.Lon, &v.DistanceM, &v.DataAsOf, &v.Family)
			return v, err
		})
	})
}
