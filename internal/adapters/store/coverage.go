package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// coverageKinds counts the places of the families ($1) per family and kind:
// rows, when the newest was last loaded, and those with an English name.
const coverageKinds = `
select p.source_ref, p.place_kind, count(*)::int, max(p.changed_at),
       count(*) filter (where exists (select 1 from contract.place_alias a
                                       where a.place_id = p.place_id and a.lang = 'en' and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `))::int
from contract.place p
where p.withdrawn_at is null and p.source_ref = any($1) and ` + "p.display_class <> 'withhold' and (p.audience = 'public' or " + ownKeyOn + ")" + `
group by 1, 2
order by 1, 2`

// coverageLands are the Länder the gazetteer families ($1) hold, by key.
const coverageLands = `
select p.official_key, p.name, p.source_ref
from contract.place p
where p.place_kind = 'state' and p.official_key is not null and p.withdrawn_at is null and p.source_ref = any($1)
  and ` + "p.display_class <> 'withhold' and (p.audience = 'public' or " + ownKeyOn + ")" + `
order by 1, 3`

// coverageAddresses are the Länder the address families ($1) hold
// addresses of, with the newest extract date: a skip scan over the Land, one
// probe per Land, never a count of the table; then the addresses placed in
// no Land, when there are any.
const coverageAddresses = `
with recursive lands(land) as (
  (select a.land_name from contract.address a
    where a.land_name is not null and a.source_ref = any($1) order by 1 limit 1)
  union all
  select (select a.land_name from contract.address a
           where a.land_name > l.land and a.source_ref = any($1) order by 1 limit 1)
    from lands l where l.land is not null)
select l.land, x.data_as_of, x.source_ref
from lands l
cross join lateral (select a.data_as_of, a.source_ref from contract.address a
                     where a.land_name = l.land and a.source_ref = any($1) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
                     order by a.data_as_of desc nulls last limit 1) x
where l.land is not null
union all
select null, x.data_as_of, x.source_ref
from (select a.data_as_of, a.source_ref from contract.address a
       where a.land_name is null and a.source_ref = any($1) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
       order by a.data_as_of desc nulls last limit 1) x
order by 1 nulls last`

// PlacesCoverage reads what the gazetteer and the addresses of the families
// hold (features.PlacesCoverage).
func (s *Store) PlacesCoverage(ctx context.Context, families []string) (places.Coverage, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (places.Coverage, error) {
		var c places.Coverage
		rows, _ := tx.Query(ctx, coverageKinds, families)
		kinds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.KindCount, error) {
			var v places.KindCount
			return v, r.Scan(&v.Family, &v.Kind, &v.Rows, &v.NewestLoad, &v.EnglishNames)
		})
		if err != nil {
			return c, err
		}
		rows, _ = tx.Query(ctx, coverageLands, families)
		lands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Land, error) {
			var v places.Land
			return v, r.Scan(&v.Code, &v.Name, &v.Family)
		})
		if err != nil {
			return c, err
		}
		rows, _ = tx.Query(ctx, coverageAddresses, families)
		addrs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.AddressLand, error) {
			var v places.AddressLand
			return v, r.Scan(&v.Land, &v.DataAsOf, &v.Family)
		})
		return places.Coverage{Kinds: kinds, Lands: lands, Addresses: addrs}, err
	})
}
