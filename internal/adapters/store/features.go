package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
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
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]provenance.Family, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select s.source_ref, s.domain, p.display_name, s.attribution_text, l.licence_name, l.licence_url,
       l.share_alike, s.share_alike_offer_url, s.terms_url, p.website, s.stale_after_s, f.data_as_of, f.confirmed_at,
       `+familyVersion+`,
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
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (provenance.Family, error) {
			var (
				f            provenance.Family
				v            access.Source
				staleAfter   *int64
				kinds, texts []string
			)
			err := r.Scan(&v.Family, &v.Domain, &v.Publisher, &v.AttributionText, &v.Licence, &v.LicenceURL,
				&v.ShareAlike, &v.ShareAlikeOffer, &v.TermsURL, &v.SourceURL, &staleAfter, &f.DataAsOf, &f.ReadAt, &f.Version, &kinds, &texts)
			f.Source = v
			f.StaleAfter, f.NeverStale = threshold(staleAfter)
			for i := range min(len(kinds), len(texts)) {
				f.Notes = append(f.Notes, provenance.Note{Kind: provenance.MapNoteKind(kinds[i]), Text: texts[i]})
			}
			return f, err
		})
	})
}

// pointFilter is the platform's charging point filter over contract
// charge point p: at least $6 kW, the plug $9, at most $10 kW with a stated
// power, and every term of $13 held ($8 is now, for the free-price age).
var pointFilter = `($6::float8 <= 0 or p.power_max_kw >= $6)
    and ($9::text is null or $9 = any(p.connectors))
    and ($10::float8 <= 0 or nullif(p.power_max_kw, 0) <= $10)
    and ($13::text[] is null or ` + termsHeld + ` @> $13::text[])`

// termsHeld is the array of the charging terms point p meets, the
// platform's rule per term.
var termsHeld = `array_remove(array[
      case when (case p.opening_hours_kind when 'always' then true when 'restricted' then false end) then 'open247' end,
      case when p.payment_card then 'card' end,
      case when (case when p.free_of_charge is not null then p.free_of_charge when p.price_per_kwh > 0 then false
                      when p.price_adhoc_zero = false then false
                      when p.price_adhoc_zero and p.price_updated_at >= $8::timestamptz - interval '` + strconv.Itoa(energy.FreePriceMaxAgeDays) + ` days' then true end) then 'free' end,
      case when (case p.parking_access when 'public' then true when 'customers' then false end) then 'public' end]::text[], null)`

// ChargingNear reads the charging sites of the given families within the
// radius, nearest first, then by id, each with its charge points (under a
// minimum power only those of at least that power) and their last
// availability report from the same families. next is the earliest report
// of those points dated after now, when one is: the answer changes then.
func (s *Store) ChargingNear(ctx context.Context, q energy.Query, families []string, now time.Time) (sites []energy.Site, more bool, next *time.Time, err error) {
	var accessCodes []string
	for _, a := range q.Access.Codes() {
		accessCodes = append(accessCodes, string(a))
	}
	f := q.Filter
	var connector, opKey *string
	if f.Connector != "" {
		connector = &f.Connector
	}
	if f.OperatorKey != "" {
		opKey = &f.OperatorKey
	}
	var terms []string
	if len(f.Terms) > 0 {
		terms = f.Terms
	}
	pointFiltered := q.MinKw > 0 || connector != nil || f.MaxKw > 0 || terms != nil
	type row struct {
		site  energy.Site
		point *energy.ChargePoint
		next  *time.Time
	}
	rows, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]row, error) {
		// One statement: the sites (one more than the limit, to tell whether
		// there are more), then their points. A failed query surfaces through
		// CollectRows.
		read := chargingNear
		if q.SiteID != "" {
			read = chargingByID
		}
		res, _ := run(ctx, tx, read, q.At.Lat, q.At.Lon, families, float64(q.RadiusM), accessCodes, q.MinKw, q.Limit+1, now,
			connector, f.MaxKw, opKey, pointFiltered, terms, q.SiteID)
		return pgx.CollectRows(res, func(r pgx.CollectableRow) (row, error) {
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
				&pointID, &current, &power, &state, &observed, &family, &v.next)
			v.site.Access = energy.MapAccess(access)
			if pointID != nil {
				v.point = &energy.ChargePoint{ID: *pointID, Current: energy.MapCurrent(current), MaxPowerKw: power}
				if state != nil {
					v.point.Availability = &energy.Availability{State: energy.MapState(*state), ObservedAt: observed.UTC(), Family: *family}
				}
			}
			return v, err
		})
	})
	for _, r := range rows {
		if len(sites) == 0 || sites[len(sites)-1].ID != r.site.ID {
			if len(sites) == q.Limit {
				// The one site past the limit only tells that there are more.
				more = true
				break
			}
			sites = append(sites, r.site)
		}
		if r.point != nil {
			last := &sites[len(sites)-1]
			last.Points = append(last.Points, *r.point)
		}
		next = earliest(next, r.next)
	}
	if next != nil {
		t := next.UTC()
		next = &t
	}
	return sites, more, next, err
}

// ChargingInRadius counts the sites of families within q's radius of q's
// position, every filter of q dropped: the list's own sites, the rules every
// read keeps (withdrawn, permitted) included.
func (s *Store) ChargingInRadius(ctx context.Context, q energy.Query, families []string) (int, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (int, error) {
		rows, _ := run(ctx, tx, chargingInRadius, q.At.Lat, q.At.Lon, families, float64(q.RadiusM))
		n, err := pgx.CollectOneRow(rows, pgx.RowTo[int])
		return n, err
	})
}

// earliest returns the earlier of a and b, either of which may be nil.
func earliest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.Before(*a)) {
		return b
	}
	return a
}

// parkingHere is the asked position, $1 lat and $2 lon.
const parkingHere = `with here as (select st_setsrid(st_makepoint($2, $1), 4326)::geography g)`

// parkingWhere is the where of a parking read past its gates, and its order:
// $5 the radius, $7 the stored kind or null, $8 the amenities or null, $9
// the road or empty; a road read's $10 is the road's line (hex EWKB, null
// without one), from the road snapshot: the read contract's road view derives
// it per read, and the corridor test asked it once per facility. A road read
// keeps the platform's corridor (CorridorM of
// one of the road's location-code points, which only the platform's own
// parking family is matched by, or filed under the road by its source) and
// is in name order, unnamed last; a position read keeps the radius and is
// nearest first. Both then go by id.
func parkingWhere(q parking.Query) (where, order string) {
	filters := `($7::text is null or f.stored_kind = $7) and ($8::text[] is null or f.amenities @> $8)`
	if q.Road == "" {
		return `$9 = '' and st_dwithin(f.location::geography, here.g, $5) and ` + filters, `8, f.facility_id`
	}
	return `$5::int >= 0 and f.facility_id like 'p:%' and (f.road_ref = $9 or exists (
    select 1 from st_dumppoints($10::text::geometry) d
    where st_dwithin(f.location::geography, d.geom::geography, ` + strconv.Itoa(parking.CorridorM) + `))) and ` + filters, `f.name nulls last, f.facility_id`
}

// shiftParams moves the where's $7..$10 to $8..$10 and $12, the positions
// the list read gives them after its own $7 and $11.
func shiftParams(where string) string {
	return strings.NewReplacer("$10", "$12", "$7", "$8", "$8", "$9", "$9", "$10").Replace(where)
}

// parkingArgs appends a road read's line ($10 of parkingWhere) to args; a
// position read's where names no $10.
func parkingArgs(q parking.Query, line *string, args ...any) []any {
	if q.Road == "" {
		return args
	}
	return append(args, line)
}

// parkingDistance is the distance column: the metres from the asked
// position, or 0 along a road, where no point was asked.
func parkingDistance(q parking.Query) string {
	if q.Road != "" {
		return `0`
	}
	return `round(st_distance(f.location::geography, here.g))::int`
}

// parkingKind is the stored kind a query keeps, or nil for every kind.
func parkingKind(q parking.Query) *string {
	if k := parking.FilterKind(q.Kind); k != "" {
		return &k
	}
	return nil
}

// parkingAmenities is the amenities a query asks for, or nil for none.
func parkingAmenities(q parking.Query) []string {
	if len(q.Amenities) == 0 {
		return nil
	}
	return q.Amenities
}

// ParkingNear reads the parking sites of the listed families within the
// radius, nearest first, then by id, each with its last occupancy report
// from the occupancy families. next is the earliest occupancy report dated
// after now of any site within the radius, whether the free_min filter keeps
// it or not, when one is: the answer may change then.
func (s *Store) ParkingNear(ctx context.Context, q parking.Query, listed, occupancy []string, now time.Time) (sites []parking.Site, more bool, next *time.Time, err error) {
	type answer struct {
		sites []parking.Site
		next  *time.Time
	}
	// The road's line is read before the read's own transaction: a cold
	// snapshot is a read of its own.
	var line *string
	if q.Road != "" {
		shapes, err := s.roadShapes(ctx)
		if err != nil {
			return nil, false, nil, err
		}
		line = shapes[q.Road].line
	}
	a, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (answer, error) {
		var a answer
		// The earliest future report, before the free_min filter: a site it
		// leaves out may qualify once a later report takes effect. A failure
		// aborts the transaction, so the next statement fails too and both
		// surface together.
		where, order := parkingWhere(q)
		nextErr := tx.QueryRow(ctx, parkingHere+`
select min(nx.next_at)
from contract.parking_facility f
cross join here
cross join lateral (
  select min(o.observed_at) next_at
  from contract.occupancy_observation o
  where o.facility_id = f.facility_id and o.source_ref = any($4) and o.observed_at > $6 and `+held("o")+`) nx
where f.withdrawn_at is null
  and f.source_ref = any($3)
  and `+held("f")+`
  and `+where, parkingArgs(q, line, q.At.Lat, q.At.Lon, listed, occupancy, q.RadiusM, now, parkingKind(q), parkingAmenities(q), q.Road)...).Scan(&a.next)
		rows, _ := tx.Query(ctx, parkingHere+`
select f.facility_id, f.source_ref, f.name, f.operator_name, f.parking_kind,
       st_y(f.location), st_x(f.location),
       `+parkingDistance(q)+`,
       o.free_spaces, o.total_spaces, o.occupancy_status, o.observed_at, o.source_ref,
       f.amenities, f.amenities_absent
from contract.parking_facility f
cross join here
left join lateral (
  select free_spaces, total_spaces, occupancy_status, observed_at, source_ref
  from contract.occupancy_observation o
  where o.facility_id = f.facility_id and o.source_ref = any($4) and o.observed_at <= $7 and `+held("o")+`
  order by o.observed_at desc
  limit 1) o on true
where f.withdrawn_at is null
  and f.source_ref = any($3)
  and `+held("f")+`
  and `+shiftParams(where)+`
  and ($6::int <= 0 or (o.free_spaces >= $6 and (
        select s.stale_after_s is null or o.observed_at >= $7::timestamptz - make_interval(secs => s.stale_after_s)
        from contract.source s where s.source_ref = o.source_ref)))
order by `+order+`
limit $11`, parkingArgs(q, line, q.At.Lat, q.At.Lon, listed, occupancy, q.RadiusM, q.FreeMin, now, parkingKind(q), parkingAmenities(q), q.Road, q.Limit+1)...)
		var err error
		a.sites, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.Site, error) {
			var (
				v           parking.Site
				kind        string
				free, total *int
				state       *string
				observed    *time.Time
				family      *string
				has, lacks  []string
			)
			err := r.Scan(&v.ID, &v.Family, &v.Name, &v.Operator, &kind, &v.Location.Lat, &v.Location.Lon, &v.DistanceM,
				&free, &total, &state, &observed, &family, &has, &lacks)
			v.Kind = parking.MapKind(kind)
			v.Amenities = parking.StatedOf(has, lacks)
			if state != nil {
				v.Occupancy = &parking.Occupancy{Free: free, Total: total, State: parking.MapState(*state), ObservedAt: observed.UTC(), Family: *family}
			}
			return v, err
		})
		return a, errors.Join(nextErr, err)
	})
	sites, next = a.sites, a.next
	if len(sites) > q.Limit {
		sites, more = sites[:q.Limit], true
	}
	if next != nil {
		t := next.UTC()
		next = &t
	}
	return sites, more, next, err
}

// placeByID is the open place $1 (a gazetteer id, read on the key) of the
// families $2, with the platform's kind and its point on the surface.
var placeByID = named("api_place_by_id", `
select g.id::text, g.name, `+placeKind+`, st_y(st_pointonsurface(g.geom::geometry)), st_x(st_pointonsurface(g.geom::geometry)), g.source_id
from gazetteer_places g
where g.id = (select case when $1::text ~ '^[0-9]{1,18}$' then $1::text::bigint end) and g.id::text = (select $1::text)
  and g.closed_at is null and g.geom is not null and g.source_id = any((select $2::text[])::text[])`, "text", "text[]")

// Place reads one place of the given families.
func (s *Store) Place(ctx context.Context, id string, families []string) (places.Place, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (places.Place, error) {
		rows, _ := run(ctx, tx, placeByID, id, families)
		ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Place, error) {
			var (
				v    places.Place
				kind string
			)
			err := r.Scan(&v.ID, &v.Name, &kind, &v.Location.Lat, &v.Location.Lon, &v.Family)
			v.Kind = places.MapKind(kind)
			return v, err
		})
		var p places.Place
		if len(ps) > 0 {
			p = ps[0]
		}
		return p, err
	})
	if err == nil && out.ID == "" {
		err = places.ErrNotFound
	}
	return out, err
}

// threshold reads a family's stale_after_s: NULL is a family with no fixed
// interval, never stale.
func threshold(staleAfterS *int64) (time.Duration, bool) {
	if staleAfterS == nil {
		return 0, true
	}
	return time.Duration(*staleAfterS) * time.Second, false
}

// chargingSites is the statement of ChargingNear: the sites within the
// radius ($4) of the position ($1, $2), or, by id, the one site $14.
//
// The sites kept are those the site's nearest walk keeps (site 2.6.39):
// nearest first by the walk's own measure, the geography distance operator
// (on the sphere), and sites at one distance by family, then origin key. A
// rounded distance or the joined id would let two sites at one metre, or two
// copies at one coordinate, fall either side of the limit in another order
// than the site's. As the site's read (2.6.39), the index walks nearest first
// and keeps the limit's rows with every tie of the last distance; only those
// are ranked, never every site of the ring. The family test is written "is
// true", as the site's (2.6.35): a generic plan of the cached statement may
// not take it as an index condition and read every site of the families
// before the ring. Kept, they are listed by the distance the answer reports,
// then id, as before; the one past the limit ($7, to tell that there are
// more) stays last.
func chargingSites(byID bool) stmt {
	q := `
with here as (select st_setsrid(st_makepoint($2, $1), 4326)::geography g),
near as (
  select c.site_id, c.source_feed source_ref, c.name, c.operator_name, c.access, c.origin_key,
         st_y(c.geom::geometry) lat, st_x(c.geom::geometry) lon,
         round(st_distance(c.geom, here.g))::int dist,
         c.geom <-> here.g d
  from ` + chargingSiteRows + ` c, here
  where (c.source_feed = any((` + ruledFamilies("$3") + `)::text[])) is true
    and st_dwithin(c.geom, here.g, $4) and $14::text = ''
    and ($5::text[] is null or c.access = any($5))
    and ($11::text is null or coalesce(` + energy.OperatorKeySQL("c.operator_name") + `, '` + energy.NoOperatorKey + `') = $11)
    and (not $12::boolean or exists (
          select 1 from ` + chargePointRows + ` p
          where p.source_feed = c.source_feed and p.raw_site = c.origin_key and ` + pointFilter + `))
  order by c.geom <-> here.g
  fetch first $7 rows with ties),
sites as (
  select n.site_id, n.source_ref, n.name, n.operator_name, n.access, n.lat, n.lon, n.dist, n.origin_key,
         row_number() over (order by n.d, n.source_ref, n.origin_key) rn
  from near n
  order by rn
  limit $7)
select s.site_id, s.source_ref, s.name, s.operator_name, s.access, s.lat, s.lon, s.dist,
       p.point_id, p.current_kind, p.power_max_kw::float8, o.availability, o.observed_at, o.source_ref,
       nx.next_at
from sites s
left join ` + chargePointRows + ` p on p.source_feed = s.source_ref and p.raw_site = s.origin_key and ` + pointFilter + `
left join lateral (
  select ` + chargeAvailability + ` availability, o.observed_at, o.source_feed source_ref
  from charging_point_status o
  where o.evse_id = p.point_id and o.source_feed = any((` + ruledFamilies("$3") + `)::text[]) and o.observed_at <= $8
    and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction'
  order by o.observed_at desc
  limit 1) o on true
left join lateral (
  select min(o.observed_at) next_at
  from charging_point_status o
  where o.evse_id = p.point_id and o.source_feed = any((` + ruledFamilies("$3") + `)::text[]) and o.observed_at > $8
    and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction') nx on true
order by s.rn = $7, s.dist, s.site_id, p.point_id`
	name := "api_charging_near"
	if byID {
		// The site asked by its id, on the sites' key: the family before the
		// first colon, the provider's id after it.
		q = strings.Replace(q, "st_dwithin(c.geom, here.g, $4) and $14::text = ''",
			"c.source_feed = split_part($14::text, ':', 1) and c.origin_key = substr($14, length(split_part($14, ':', 1)) + 2)\n    and c.site_id = $14 and $4 >= 0", 1)
		name = "api_charging_by_id"
	}
	return named(name, opaqued(q, "float8", "float8", "text[]", "float8", "text[]", "float8", "bigint",
		"timestamptz", "text", "float8", "text", "bool", "text[]", "text"),
		"float8", "float8", "text[]", "float8", "text[]", "float8", "bigint",
		"timestamptz", "text", "float8", "text", "bool", "text[]", "text")
}

// chargingSiteRows are the charging sites a read may list: not deleted nor
// closed, with a position, and not withheld (a confidentiality other than
// noRestriction, or a private access). The site's id is its family and the
// provider's id, joined by a colon.
const chargingSiteRows = `(
  select s.source_feed || ':' || s.site_id site_id, s.source_feed, s.site_id origin_key, s.name, s.operator operator_name,
         case when s.access_private then 'restricted' else 'public' end access, s.geom
  from charging_sites s
  where s.deleted_at is null and s.closed_at is null and s.geom is not null
    and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction' and s.access_private is not true)`

// chargePointRows are the charge points a read may list, with the
// register's terms: not deleted, of a site, not confidential. The point's id
// is its EVSE id, else its family and key. Its family is its site's, so its
// source rule is the one the site was read under.
const chargePointRows = `(
  select coalesce(p.evse_id, p.source_feed || ':' || p.point_key) point_id, p.source_feed, p.site_id raw_site,
         case when p.current_type ~* '^ac' then 'ac' when p.current_type ~* '^dc' then 'dc' when p.current_type is not null then 'other' end current_kind,
         p.max_power_kw power_max_kw,
         array_remove(array[
           case when p.connector_types && array['iec62196T2COMBO', 'iec62196T2Combo', 'iec62196t2combo', 'combo', 'ccs'] then 'ccs2' end,
           case when p.connector_types && array['iec62196T2', 'iec62196t2', 'type2'] then 'type2' end,
           case when p.connector_types && array['chademo', 'chaDemo', 'CHAdeMO'] then 'chademo' end,
           case when p.connector_types && array['mcs'] then 'mcs' end], null) connectors,
         p.price_per_kwh, p.price_adhoc_zero, p.price_last_updated price_updated_at,
         charging_register_opening_hours(p.extras) opening_hours_kind, charging_register_payment_card(p.extras) payment_card,
         charging_register_free_of_charge(p.extras) free_of_charge, charging_register_parking(p.extras) parking_access
  from charging_points p
  where p.deleted_at is null and p.site_id is not null and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction')`

// chargeAvailability is a status report's state over charging_point_status
// o, the platform's mapping.
const chargeAvailability = `case when o.status ~* '^available$' then 'available'
              when o.status ~* '^(charging|occupied|blocked)$' then 'occupied'
              when o.status ~* '^reserved$' then 'reserved'
              when o.status ~* '^(out_?of_?order|inoperative|faulted)$' then 'out_of_order'
              when o.status is null or o.status ~* '^unknown$' then 'unknown'
              else 'other' end`

// opaqued rewrites every parameter $n of sql as opaque does, for a named
// statement: each a one-row subquery cast to its type.
func opaqued(sql string, types ...string) string {
	return parameter.ReplaceAllStringFunc(sql, func(p string) string {
		n, _ := strconv.Atoi(p[1:])
		return "((select " + p + "::" + types[n-1] + ")::" + types[n-1] + ")"
	})
}

// chargingNear and chargingByID are the two charging reads, built once:
// the sites in the ring and the one site asked by its id.
var chargingNear, chargingByID = chargingSites(false), chargingSites(true)

// chargingInRadius counts the sites of the families $3 within $4 metres
// of ($2, $1).
var chargingInRadius = named("api_charging_in_radius", `select count(*)::int
from `+chargingSiteRows+` c
where (c.source_feed = any((`+ruledFamilies("(select $3::text[])")+`)::text[])) is true
  and st_dwithin(c.geom, st_setsrid(st_makepoint((select $2::float8), (select $1::float8)), 4326)::geography, (select $4::float8))`,
	"float8", "float8", "text[]", "float8")
