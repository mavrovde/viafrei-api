package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// baysNear is the platform's on-street bay read (STREET_BAYS_NEAR): the
// open bays of kind $4 within $3 m of ($1, $2), nearest first, at most $7;
// a state counts only while the newest message is younger than $6 s by the
// request's clock $5, and never at a sensor-less site; the counts cover the
// whole radius; $9 keeps only the bays reported free. $8 the families.
var baysNear = `with p as (select st_setsrid(st_makepoint($1, $2), 4326)::geography as g),
m as (
  select b.bay_id, b.bay_kind, b.publisher_kind, b.street, b.house_number, b.postcode, b.city,
         st_y(b.location) as lat, st_x(b.location) as lon,
         round(st_distance(b.location::geography, p.g))::int as distance_m,
         case when b.spaces is null and b.state_at is not null and b.state_at > $5::timestamptz - make_interval(secs => $6::int)
              then b.state end as state,
         b.state_at, b.spaces, b.reserved_when, b.source_ref
    from contract.street_bay b, p
   where b.bay_kind = $4 and b.source_ref = any($8) and ` + permitted("b") + `
     and st_dwithin(b.location::geography, p.g, $3)
)
select m.bay_id, m.bay_kind, m.publisher_kind, m.street, m.house_number, m.postcode, m.city, m.lat, m.lon, m.distance_m,
       m.state, m.state_at, m.spaces, m.reserved_when, m.source_ref,
       count(*) over ()::int, (count(*) filter (where m.spaces is null) over ())::int,
       (count(*) filter (where m.spaces is not null) over ())::int, coalesce(sum(m.spaces) over (), 0)::int,
       (count(*) filter (where m.state = 'free') over ())::int, (count(*) filter (where m.state = 'occupied') over ())::int
  from m
 where not $9::boolean or m.state = 'free'
 order by m.distance_m, m.bay_id
 limit $7`

// Bays reads the bays q asks for (features.Bays).
func (s *Store) Bays(ctx context.Context, q parking.BayQuery, families []string) (parking.Bays, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (parking.Bays, error) {
		var out parking.Bays
		rows, _ := tx.Query(ctx, baysNear, q.At.Lon, q.At.Lat, q.RadiusM, q.Kind, q.Now, parking.BayStateFreshS, q.Limit, families, q.OnlyFree)
		items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.Bay, error) {
			var b parking.Bay
			c := &out.Counts
			err := r.Scan(&b.ID, &b.Kind, &b.PublisherKind, &b.Street, &b.HouseNumber, &b.Postcode, &b.City, &b.Location.Lat, &b.Location.Lon,
				&b.DistanceM, &b.State, &b.MessageAt, &b.Spaces, &b.ReservedWhen, &b.Family,
				&c.Total, &c.Sensors, &c.StaticSites, &c.StaticSpaces, &c.Free, &c.Occupied)
			if b.MessageAt != nil {
				at := b.MessageAt.UTC()
				b.MessageAt = &at
			}
			return b, err
		})
		out.Items = items
		return out, err
	})
}
