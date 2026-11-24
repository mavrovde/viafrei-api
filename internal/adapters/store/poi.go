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
// Every parameter is opaque (opaqued), as in every other read, so the plan
// cache settles on one generic plan and does not plan each call. That one
// plan holds a branch for each way the match is asked (poiCandidates), and
// only the branch the parameters name runs (a one-time filter on them).
var poiMatch = namedOpaque("api_poi_match", `select 'osm:' || p.osm_type || p.osm_id poi_id, p.name, p.category, p.category_value, p.brand, p.operator,
       p.street, p.house_number, p.postcode, p.place, st_y(p.geom::geometry), st_x(p.geom::geometry),
       case when $3::float8 is null then null
            else round(st_distance(p.geom, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography))::int end as distance_m,
       p.extract_date, p.source_id
from `+poiCandidates+` p
where p.closed_at is null and p.geom is not null
  and ($2::text is null or p.place_norm = $2::text)
  and ($3::float8 is null or st_dwithin(p.geom, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography, $5::float8))
  and ($6::text is null or p.category = $6::text)
  and p.source_id = any((`+ruledFamilies("$7::text[]")+`)::text[])
order by case when p.name_norm = $1::text then 0 when p.name_norm like $1::text || '%' then 1 else 2 end,
         distance_m nulls last, similarity(p.name_norm, $1::text) desc, p.name, poi_id
limit $8::int`, poiTypes...)

// poiCandidates are the places the match then filters, from the one branch
// its parameters name: the town's places through its index; else those
// within the radius of the point through the GiST; else, for a text of three
// characters or more, those whose name or brand holds it, a BitmapOr of the
// two trigram indexes; else (a shorter text, of no trigram) a read of the
// table, the text found by strpos, which no trigram index serves (a pattern
// of no trigram matches every entry of one) and which holds the same rows as
// the like test, as the search form holds no wildcard (places.NameFold keeps
// letters, digits and spaces). Each branch holds the
// text test itself (the match's own filters hold none, as the planner pushes
// them into every branch, and the short text's branch would take the trigram
// index again). The branches never overlap and together hold every row the
// match keeps, so the answer is the match's own.
const poiCandidates = `(
  select ` + poiColumns + ` from osm_pois p
  where $2::text is not null and p.closed_at is null and p.place_norm = $2::text
    and (p.name_norm like '%' || $1::text || '%' or lower(p.brand) like '%' || $1::text || '%')
  union all
  select ` + poiColumns + ` from osm_pois p
  where $2::text is null and $3::float8 is not null and p.closed_at is null
    and st_dwithin(p.geom, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography, $5::float8)
    and (p.name_norm like '%' || $1::text || '%' or lower(p.brand) like '%' || $1::text || '%')
  union all
  select ` + poiColumns + ` from osm_pois p
  where $2::text is null and $3::float8 is null and length($1::text) >= 3 and p.closed_at is null
    and (p.name_norm like '%' || $1::text || '%' or lower(p.brand) like '%' || $1::text || '%')
  union all
  select ` + poiColumns + ` from osm_pois p
  where $2::text is null and $3::float8 is null and length($1::text) < 3 and p.closed_at is null
    and (strpos(p.name_norm, $1::text) > 0 or strpos(lower(p.brand), $1::text) > 0))`

// poiColumns are the columns of a place the match reads.
const poiColumns = `p.osm_type, p.osm_id, p.name, p.name_norm, p.category, p.category_value, p.brand, p.operator, p.street,
         p.house_number, p.postcode, p.place, p.place_norm, p.geom, p.extract_date, p.source_id, p.closed_at`

// poiTypes are the match's parameter types.
var poiTypes = []string{"text", "text", "double precision", "double precision", "double precision", "text", "text[]", "integer"}

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
