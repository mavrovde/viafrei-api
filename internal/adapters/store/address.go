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
// the families, $6 the limit.
var addressExact = `select a.address_id, a.street, a.house_number, a.postcode, a.place_name, a.land_name,
       st_y(a.location), st_x(a.location), a.data_as_of, a.source_ref
from contract.address a
where a.street_fold = $1 and a.house_number_fold = $2
  and (($3::text is not null and a.postcode = $3)
       or ($4::text is not null and a.place_fold = $4 and ($3::text is null or a.postcode is null or a.postcode = $3)))
  and a.source_ref = any($5) and ` + permitted("a") + `
order by a.postcode is not distinct from $3 desc, a.address_id
limit $6`

// Addresses reads the addresses q names (features.Addresses).
func (s *Store) Addresses(ctx context.Context, q places.AddressQuery, families []string) ([]places.Address, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Address, error) {
		rows, _ := tx.Query(ctx, addressExact, places.StreetFold(q.Street), places.HouseNumberFold(q.HouseNumber),
			nullText(q.Postcode), nullText(places.NameFold(q.Place)), families, q.Limit)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Address, error) {
			var v places.Address
			err := r.Scan(&v.ID, &v.Street, &v.HouseNumber, &v.Postcode, &v.Place, &v.Land, &v.Location.Lat, &v.Location.Lon, &v.DataAsOf, &v.Family)
			return v, err
		})
	})
}
