package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// The repositories below are the anti-corruption layer: they read the read
// contract and hand the domain its own model, mapping every stored code
// through the domain's mapping (ENV-051). No storage name leaves this package.

// Provenance reads the attribution, the notes in lang and the freshness of
// the given families only.
func (s *Store) Provenance(ctx context.Context, families []string, lang string) ([]provenance.Family, error) {
	var out []provenance.Family
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select s.source_ref, s.domain, p.display_name, s.attribution_text, l.licence_name, l.licence_url,
       l.share_alike, s.share_alike_offer_url, s.terms_url, s.stale_after_s, f.data_as_of,
       array(select n.note_kind from contract.source_note n
             where n.source_ref = s.source_ref and n.lang = $2 order by n.note_kind, n.body),
       array(select n.body from contract.source_note n
             where n.source_ref = s.source_ref and n.lang = $2 order by n.note_kind, n.body)
from contract.source s
join contract.publisher p using (publisher_id)
join contract.licence l using (licence_code)
left join contract.source_freshness f using (source_ref)
where s.source_ref = any($1)
order by s.source_ref`, families, lang)
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (provenance.Family, error) {
			var (
				f            provenance.Family
				v            access.Source
				staleAfter   int64
				kinds, texts []string
			)
			err := r.Scan(&v.Family, &v.Domain, &v.Publisher, &v.AttributionText, &v.Licence, &v.LicenceURL,
				&v.ShareAlike, &v.ShareAlikeOffer, &v.TermsURL, &staleAfter, &f.DataAsOf, &kinds, &texts)
			f.Source, f.StaleAfter = v, time.Duration(staleAfter)*time.Second
			for i := range min(len(kinds), len(texts)) {
				f.Notes = append(f.Notes, provenance.Note{Kind: provenance.MapNoteKind(kinds[i]), Text: texts[i]})
			}
			return f, err
		})
		return err
	})
	return out, err
}

// ChargingNear reads the charging sites of the given families within the
// radius, nearest first, then by id, each with its charge points and their
// last availability report from the same families.
func (s *Store) ChargingNear(ctx context.Context, q energy.Query, families []string, now time.Time) ([]energy.Site, bool, error) {
	var accessCodes []string
	for _, a := range q.Access.Codes() {
		accessCodes = append(accessCodes, string(a))
	}
	type row struct {
		site  energy.Site
		point *energy.ChargePoint
	}
	var rows []row
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// One statement: the sites (one more than the limit, to tell whether
		// there are more), then their points. A failed query surfaces through
		// CollectRows.
		res, _ := tx.Query(ctx, `
with here as (select st_setsrid(st_makepoint($2, $1), 4326)::geography g),
sites as (
  select c.site_id, c.source_ref, c.name, c.operator_name, c.access,
         st_y(c.location) lat, st_x(c.location) lon,
         round(st_distance(c.location::geography, here.g))::int dist
  from contract.charging_site c, here
  where c.withdrawn_at is null
    and c.source_ref = any($3)
    and st_dwithin(c.location::geography, here.g, $4)
    and ($5::text[] is null or c.access = any($5))
    and ($6::float8 <= 0 or exists (
          select 1 from contract.charge_point p
          where p.site_id = c.site_id and p.power_max_kw >= $6))
  order by dist, c.site_id
  limit $7)
select s.site_id, s.source_ref, s.name, s.operator_name, s.access, s.lat, s.lon, s.dist,
       p.point_id, p.current_kind, p.power_max_kw::float8, o.availability, o.observed_at, o.source_ref
from sites s
left join contract.charge_point p using (site_id)
left join lateral (
  select availability, observed_at, source_ref
  from contract.charge_point_state_observation o
  where o.point_id = p.point_id and o.source_ref = any($3) and o.observed_at <= $8
  order by o.observed_at desc
  limit 1) o on true
order by s.dist, s.site_id, p.point_id`, q.At.Lat, q.At.Lon, families, q.RadiusM, accessCodes, q.MinKw, q.Limit+1, now)
		var err error
		rows, err = pgx.CollectRows(res, func(r pgx.CollectableRow) (row, error) {
			var (
				v                       row
				access                  string
				pointID, current, state *string
				power                   *float64
				observed                *time.Time
				family                  *string
			)
			err := r.Scan(&v.site.ID, &v.site.Family, &v.site.Name, &v.site.Operator, &access,
				&v.site.Location.Lat, &v.site.Location.Lon, &v.site.DistanceM,
				&pointID, &current, &power, &state, &observed, &family)
			v.site.Access = energy.MapAccess(access)
			if pointID != nil {
				v.point = &energy.ChargePoint{ID: *pointID, Current: energy.MapCurrent(current), MaxPowerKw: power}
				if state != nil {
					v.point.Availability = &energy.Availability{State: energy.MapState(*state), ObservedAt: observed.UTC(), Family: *family}
				}
			}
			return v, err
		})
		return err
	})
	var sites []energy.Site
	for _, r := range rows {
		if len(sites) == 0 || sites[len(sites)-1].ID != r.site.ID {
			sites = append(sites, r.site)
		}
		if r.point != nil {
			last := &sites[len(sites)-1]
			last.Points = append(last.Points, *r.point)
		}
	}
	more := len(sites) > q.Limit
	if more {
		sites = sites[:q.Limit]
	}
	return sites, more, err
}

// ParkingNear reads the parking sites of the listed families within the
// radius, nearest first, then by id, each with its last occupancy report
// from the occupancy families.
func (s *Store) ParkingNear(ctx context.Context, q parking.Query, listed, occupancy []string, now time.Time) ([]parking.Site, bool, error) {
	var (
		sites []parking.Site
		more  bool
	)
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
with here as (select st_setsrid(st_makepoint($2, $1), 4326)::geography g)
select f.facility_id, f.source_ref, f.name, f.operator_name, f.parking_kind,
       st_y(f.location), st_x(f.location),
       round(st_distance(f.location::geography, here.g))::int,
       o.free_spaces, o.total_spaces, o.occupancy_status, o.observed_at, o.source_ref
from contract.parking_facility f
cross join here
left join lateral (
  select free_spaces, total_spaces, occupancy_status, observed_at, source_ref
  from contract.occupancy_observation o
  where o.facility_id = f.facility_id and o.source_ref = any($4) and o.observed_at <= $7
  order by o.observed_at desc
  limit 1) o on true
where f.withdrawn_at is null
  and f.source_ref = any($3)
  and st_dwithin(f.location::geography, here.g, $5)
  and ($6::int <= 0 or (o.free_spaces >= $6 and o.observed_at >= $7::timestamptz - (
        select make_interval(secs => s.stale_after_s) from contract.source s where s.source_ref = o.source_ref)))
order by 8, f.facility_id
limit $8`, q.At.Lat, q.At.Lon, listed, occupancy, q.RadiusM, q.FreeMin, now, q.Limit+1)
		var err error
		sites, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.Site, error) {
			var (
				v           parking.Site
				kind        string
				free, total *int
				state       *string
				observed    *time.Time
				family      *string
			)
			err := r.Scan(&v.ID, &v.Family, &v.Name, &v.Operator, &kind, &v.Location.Lat, &v.Location.Lon, &v.DistanceM,
				&free, &total, &state, &observed, &family)
			v.Kind = parking.MapKind(kind)
			if state != nil {
				v.Occupancy = &parking.Occupancy{Free: free, Total: total, State: parking.MapState(*state), ObservedAt: observed.UTC(), Family: *family}
			}
			return v, err
		})
		return err
	})
	if len(sites) > q.Limit {
		sites, more = sites[:q.Limit], true
	}
	return sites, more, err
}

// Place reads one place of the given families.
func (s *Store) Place(ctx context.Context, id string, families []string) (places.Place, error) {
	var out places.Place
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
select place_id, name, place_kind, st_y(location), st_x(location), source_ref
from contract.place
where place_id = $1 and withdrawn_at is null and source_ref = any($2)`, id, families)
		ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Place, error) {
			var (
				v    places.Place
				kind string
			)
			err := r.Scan(&v.ID, &v.Name, &kind, &v.Location.Lat, &v.Location.Lon, &v.Family)
			v.Kind = places.MapKind(kind)
			return v, err
		})
		if len(ps) > 0 {
			out = ps[0]
		}
		return err
	})
	if err == nil && out.ID == "" {
		err = places.ErrNotFound
	}
	return out, err
}
