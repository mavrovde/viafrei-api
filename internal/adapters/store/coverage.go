package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// coverageKinds counts the places of the families ($1) per family and kind:
// rows, when the newest was last loaded, and those with an English name.
var coverageKinds = named("api_coverage_kinds", `
select g.source_id, case g.kind when 'city' then 'municipality' when 'district' then 'district' when 'admin' then 'state'
                                when 'station' then 'station_area' when 'stop' then 'station_area' when 'island' then 'locality'
                                when 'motorway' then 'motorway' when 'junction' then 'junction' else 'other' end kind,
       count(*)::int, max(g.last_seen_at), count(*) filter (where g.name_en is not null)::int
from gazetteer_places g
where g.geom is not null and g.closed_at is null and g.source_id = any((select $1::text[])::text[])
group by 1, 2
order by 1, 2`, "text[]")

// coverageLands are the Länder the gazetteer families ($1) hold, by key.
var coverageLands = named("api_coverage_lands", `
select g.admin_code, g.name, g.source_id
from gazetteer_places g
where g.kind = 'admin' and g.admin_code is not null and g.geom is not null and g.closed_at is null
  and g.source_id = any((select $1::text[])::text[])
order by 1, 3`, "text[]")

// coverageAddresses are the Länder the address families ($1, those their
// source rule gives the key) hold addresses of, with the newest extract
// date: a skip scan over the Land (osm_addresses_open_land), one probe per
// Land, never a count of the table; then the addresses placed in no Land,
// when there are any. The newest extract of a Land is read in the order of
// no index: on prod each probe reads every address of its Land (up to 15 s
// in all), until viafrei-database holds the open addresses by Land and
// extract date.
var coverageAddresses = named("api_coverage_addresses", `
with recursive fam as (`+ruledFamilies("(select $1::text[])")+`),
lands(land) as (
  (select a.land from osm_addresses a, fam
    where a.land is not null and a.closed_at is null and a.geom is not null and a.source_id = any(fam.f) order by 1 limit 1)
  union all
  select (select a.land from osm_addresses a, fam
           where a.land > l.land and a.closed_at is null and a.geom is not null and a.source_id = any(fam.f) order by 1 limit 1)
    from lands l where l.land is not null)
select l.land, x.extract_date, x.source_id
from lands l
cross join lateral (select a.extract_date, a.source_id from osm_addresses a, fam
                     where a.land = l.land and a.closed_at is null and a.geom is not null and a.source_id = any(fam.f)
                     order by a.extract_date desc nulls last limit 1) x
where l.land is not null
union all
select null, x.extract_date, x.source_id
from (select a.extract_date, a.source_id from osm_addresses a, fam
       where a.land is null and a.closed_at is null and a.geom is not null and a.source_id = any(fam.f)
       order by a.extract_date desc nulls last limit 1) x
order by 1 nulls last`, "text[]")

// PlacesCoverage reads what the gazetteer and the addresses of the families
// hold (features.PlacesCoverage).
func (s *Store) PlacesCoverage(ctx context.Context, families []string) (places.Coverage, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (places.Coverage, error) {
		var c places.Coverage
		rows, _ := run(ctx, tx, coverageKinds, families)
		kinds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.KindCount, error) {
			var v places.KindCount
			return v, r.Scan(&v.Family, &v.Kind, &v.Rows, &v.NewestLoad, &v.EnglishNames)
		})
		if err != nil {
			return c, err
		}
		rows, _ = run(ctx, tx, coverageLands, families)
		lands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Land, error) {
			var v places.Land
			return v, r.Scan(&v.Code, &v.Name, &v.Family)
		})
		if err != nil {
			return c, err
		}
		rows, _ = run(ctx, tx, coverageAddresses, families)
		addrs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.AddressLand, error) {
			var v places.AddressLand
			return v, r.Scan(&v.Land, &v.DataAsOf, &v.Family)
		})
		return places.Coverage{Kinds: kinds, Lands: lands, Addresses: addrs}, err
	})
}
