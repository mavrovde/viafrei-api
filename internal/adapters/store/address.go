package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// addressExact reads the addresses a typed address names, as the platform's
// address search does: the street and the house number in their search
// forms ($1, $2), in the postcode $3 or the town $4 (a town's addresses
// whose postcode is another are not it), those in the postcode first; $5
// the families (those their source rule gives the key, ruledFamilies), $6
// the limit.
var addressExact = named("api_address_exact", `
with fam as (`+ruledFamilies("(select $5::text[])")+`)
select 'osm:' || a.osm_type || a.osm_id address_id, a.street, a.house_number, a.postcode, a.place, a.land,
       st_y(a.geom::geometry), st_x(a.geom::geometry), a.extract_date, a.source_id
from osm_addresses a, fam
where a.street_norm = (select $1::text) and a.house_number_norm = (select $2::text)
  and a.closed_at is null and a.geom is not null
  and (((select $3::text) is not null and a.postcode = (select $3::text))
       or ((select $4::text) is not null and a.place_norm = (select $4::text)
           and ((select $3::text) is null or a.postcode is null or a.postcode = (select $3::text))))
  and a.source_id = any(fam.f)
order by a.postcode is not distinct from (select $3::text) desc, address_id
limit (select $6::int)`, "text", "text", "text", "text", "text[]", "int")

// Addresses reads the addresses q names (features.Addresses).
func (s *Store) Addresses(ctx context.Context, q places.AddressQuery, families []string) ([]places.Address, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Address, error) {
		rows, _ := run(ctx, tx, addressExact, places.StreetFold(q.Street), places.HouseNumberFold(q.HouseNumber),
			nullText(q.Postcode), nullText(places.NameFold(q.Place)), families, q.Limit)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Address, error) {
			var v places.Address
			err := r.Scan(&v.ID, &v.Street, &v.HouseNumber, &v.Postcode, &v.Place, &v.Land, &v.Location.Lat, &v.Location.Lon, &v.DataAsOf, &v.Family)
			return v, err
		})
	})
}
