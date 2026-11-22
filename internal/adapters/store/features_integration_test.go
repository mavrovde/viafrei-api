//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The expectations below are read from the fixture rows with plain owner
// queries, never through the repositories under test.

// owner runs one row query as the database owner.
func owner(t *testing.T, sql string, dest ...any) {
	t.Helper()
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, sql).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// change runs one statement as the database owner on the read contract. It
// returns the rows changed.
func change(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tag, err := conn.Exec(ctx, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return tag.RowsAffected()
}

// seedChanges stops a test that changes the given columns ("view.column")
// or removes rows of their views, before it plants anything: a test names no table under the
// read contract, and the test seed takes rows through the views (INSERT) and
// nothing else yet. The skip is the DB-lane ask for the seed's INSTEAD OF
// UPDATE and DELETE.
func seedChanges(t *testing.T, columns ...string) {
	t.Helper()
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, vc := range columns {
		v, c, _ := strings.Cut(vc, ".")
		for _, probe := range []string{"update contract." + v + " set " + c + " = " + c + " where false", "delete from contract." + v + " where false"} {
			if _, err := conn.Exec(ctx, probe); err != nil {
				dbAsk(t, "the test seed changes no contract."+vc+" or removes no row of its view (INSERT only): a test that changes or removes a seeded row cannot run")
			}
		}
	}
}

// dbAsk stops a test at an expectation the read contract cannot hold yet:
// its view gives a constant or NULL where the API reads a value. The skip
// names the ask to the database lane; the count of these is reported with
// every integration run.
func dbAsk(t *testing.T, ask string) {
	t.Helper()
	t.Skip("DB-lane ask: " + ask)
}

// fixtureEnergy are the fixture's energy families a public key reads.
var fixtureEnergy = []string{"afir_alpha", "afir_alpha_stale", "afir_beta", "afir_zeta", "chargepool_gamma"}

// families returns the register's families of one domain.
func families(t *testing.T, st *store.Store, domain string) []string {
	t.Helper()
	reg, err := st.Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	return access.Answering(reg, domain)
}

var everywhere = 50000

func TestRegisterCarriesTheDomain(t *testing.T) {
	req.Covers(t, "SEC-032")
	st := seeded(t)
	energy := families(t, st, "energy")
	for _, f := range fixtureEnergy {
		if !slices.Contains(energy, f) {
			t.Errorf("energy families %v lack %s", energy, f)
		}
	}
	if got := families(t, st, "parking"); !slices.Contains(got, "truckpark_sid_static") || slices.Contains(got, "afir_alpha") || slices.Contains(energy, "park_alpha") {
		t.Errorf("parking families %v", got)
	}
}

func TestProvenanceReadsTheGivenFamiliesInOneLanguage(t *testing.T) {
	req.Covers(t, "ENV-003", "SEC-031")
	st := seeded(t)
	got, err := st.Provenance(bounded(t), []string{"chargepool_gamma", "truckpark_sid_static", "afir_alpha_stale", "geonames", "no-such-family"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Source.Family != "afir_alpha_stale" || got[1].Source.Family != "chargepool_gamma" || got[2].Source.Family != "geonames" || got[3].Source.Family != "truckpark_sid_static" {
		t.Fatalf("read %+v", got)
	}
	end := fixtures.ReferenceInstant.AddDate(0, 0, 2)
	stale, gamma, never, delta := got[0], got[1], got[2], got[3]
	var staleAfterS int64
	var asOf time.Time
	var readAt *time.Time
	owner(t, `select s.stale_after_s, f.data_as_of, f.confirmed_at from contract.source s join contract.source_freshness f using (source_ref)
where source_ref = 'afir_alpha_stale'`, &staleAfterS, &asOf, &readAt)
	// The read is the family's newest confirmation, which a dated note states.
	if readAt == nil || stale.ReadAt == nil || !stale.ReadAt.Equal(*readAt) {
		t.Errorf("read at %v, want %v", stale.ReadAt, readAt)
	}
	if stale.StaleAfter != time.Duration(staleAfterS)*time.Second || stale.NeverStale || stale.DataAsOf == nil || !stale.DataAsOf.Equal(asOf) || !stale.Assess(end).Stale {
		t.Errorf("stale family %+v", stale)
	}
	// A family with no fixed interval (stale_after_s NULL) is never stale.
	// The source page is the one the read contract names for the family.
	for _, f := range got {
		var website *string
		owner(t, `select p.website from contract.source s join contract.publisher p using (publisher_id) where s.source_ref = '`+f.Source.Family+`'`, &website)
		if (website == nil) != (f.Source.SourceURL == nil) || website != nil && *website != *f.Source.SourceURL {
			t.Errorf("%s: source page %v, want %v", f.Source.Family, f.Source.SourceURL, website)
		}
	}
	if !never.NeverStale || never.Assess(end).Stale || never.Assess(end).StaleAfterS != nil {
		t.Errorf("a family with no interval %+v", never)
	}
	if gamma.DataAsOf == nil || gamma.Assess(end).Stale || gamma.Source.Domain != "energy" || gamma.Source.Publisher == "" || gamma.Source.Licence == "" {
		t.Errorf("fresh family %+v", gamma)
	}
	if !slices.Equal(gamma.Notes, []provenance.Note{{Kind: provenance.Purpose, Text: "For consumer information only (synthetic)."}}) {
		t.Errorf("purpose notes %+v", gamma.Notes)
	}
	if !slices.Equal(delta.Notes, []provenance.Note{{Kind: provenance.Condition, Text: "Use only after accepting the terms (synthetic)."}}) || delta.Source.TermsURL == nil {
		t.Errorf("condition notes %+v", delta)
	}
	de, err := st.Provenance(bounded(t), []string{"chargepool_gamma"}, "de")
	if err != nil || len(de) != 1 || len(de[0].Notes) != 1 || de[0].Notes[0].Text == gamma.Notes[0].Text {
		t.Errorf("German notes %+v, %v", de, err)
	}
	none, err := st.Provenance(bounded(t), []string{"chargepool_gamma"}, "xx")
	if err != nil || len(none) != 1 || len(none[0].Notes) != 0 {
		t.Errorf("a language without notes %+v, %v", none, err)
	}
}

func TestChargingNearReadsAReservedPointAsOccupied(t *testing.T) {
	// #18: a point whose newest report is reserved is answered occupied, as
	// the MCP capability answers it.
	st := seeded(t)
	var (
		site, point string
		at          geo.Point
		observed    time.Time
	)
	owner(t, `select c.site_id, p.point_id, st_y(c.location), st_x(c.location), o.observed_at
from contract.charging_site c join contract.charge_point p using (site_id)
join contract.charge_point_state_observation o using (point_id)
where o.availability = 'reserved'
  and not exists (select 1 from contract.charge_point_state_observation n
                  where n.point_id = o.point_id and n.observed_at = o.observed_at and n.availability <> 'reserved')
order by o.observed_at, p.point_id limit 1`, &site, &point, &at.Lat, &at.Lon, &observed)
	sites, _, _, err := st.ChargingNear(bounded(t), energy.Query{At: at, RadiusM: 1, Access: energy.FilterAll, Limit: 100}, families(t, st, "energy"), observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		for _, p := range s.Points {
			if s.ID == site && p.ID == point {
				if a := p.Availability; a == nil || a.State != energy.StateOccupied || !a.ObservedAt.Equal(observed) {
					t.Errorf("a reserved report reads as %+v, want occupied", a)
				}
				return
			}
		}
	}
	t.Fatalf("point %s of site %s was not read", point, site)
}

func TestChargingNearReadsTheNearestSitesOfTheGivenFamilies(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "FR-ENERGY-005", "FR-ENERGY-006", "SEC-031")
	st := seeded(t)
	var (
		id, fam string
		at      geo.Point
		unknown time.Time
	)
	owner(t, `select c.site_id, c.source_ref, st_y(c.location), st_x(c.location), o.observed_at
from contract.charging_site c join contract.charge_point p using (site_id)
join contract.charge_point_state_observation o using (point_id)
where o.availability = 'other' order by o.observed_at limit 1`, &id, &fam, &at.Lat, &at.Lon, &unknown)
	fams := families(t, st, "energy")
	q := energy.Query{At: at, RadiusM: everywhere, Access: energy.FilterAll, Limit: 100}

	sites, more, _, err := st.ChargingNear(bounded(t), q, fams, unknown)
	if err != nil || len(sites) == 0 {
		t.Fatalf("read %d sites: %v", len(sites), err)
	}
	// The circle's count is the list's sites, every filter dropped: a filter
	// no site passes leaves the count as it is; no family counts none, and a
	// circle of 0 m on a site counts it.
	var inCircle int
	owner(t, `select count(*) from contract.charging_site c where c.withdrawn_at is null and c.source_ref in (`+quoted(fams)+`)
  and c.display_class <> 'withhold' and c.audience = 'public'
  and st_dwithin(c.location::geography, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326)::geography, `+fmt.Sprint(everywhere)+`)`, &inCircle)
	strict := q
	strict.MinKw, strict.Filter.Connector = 100_000, "none"
	if n, err := st.ChargingInRadius(bounded(t), strict, fams); err != nil || n != inCircle || n < len(sites) {
		t.Errorf("in radius %d, want %d (listed %d): %v", n, inCircle, len(sites), err)
	}
	if n, err := st.ChargingInRadius(bounded(t), q, []string{fam}); err != nil || n == 0 || n > inCircle {
		t.Errorf("one family: %d of %d, %v", n, inCircle, err)
	}
	if n, err := st.ChargingInRadius(bounded(t), q, []string{}); err != nil || n != 0 {
		t.Errorf("no family: %d, %v", n, err)
	}
	small := q
	small.RadiusM = 0
	if n, err := st.ChargingInRadius(bounded(t), small, fams); err != nil || n == 0 || n > inCircle {
		t.Errorf("a circle of 0 m on a site: %d of %d, %v", n, inCircle, err)
	}
	if sites[0].ID != id || sites[0].DistanceM != 0 || sites[0].Family != fam || more {
		t.Errorf("nearest %+v, more %v", sites[0], more)
	}
	for i, s := range sites {
		if s.DistanceM > everywhere || !slices.Contains(fams, s.Family) || len(s.Points) == 0 {
			t.Errorf("site %+v", s)
		}
		if i > 0 && (s.DistanceM < sites[i-1].DistanceM || s.DistanceM == sites[i-1].DistanceM && s.ID < sites[i-1].ID) {
			t.Errorf("site %d out of order", i)
		}
		for _, p := range s.Points {
			if p.Current == nil || p.MaxPowerKw == nil {
				t.Errorf("point %+v", p)
			}
			if a := p.Availability; a != nil && (a.ObservedAt.After(unknown) || a.ObservedAt.Location() != time.UTC || !slices.Contains(fams, a.Family)) {
				t.Errorf("report %+v", a)
			}
		}
	}
	if a := sites[0].Points[0].Availability; a == nil || a.State != energy.StateOther || !a.ObservedAt.Equal(unknown) {
		t.Errorf("an unmapped state reads as other: %+v", a)
	}

	before, _, _, err := st.ChargingNear(bounded(t), q, fams, fixtures.ReferenceInstant.Add(-time.Second))
	if err != nil || before[0].Points[0].Availability != nil {
		t.Errorf("a report from after now was read: %v", err)
	}
	one, more, _, err := st.ChargingNear(bounded(t), energy.Query{At: at, RadiusM: everywhere, Access: energy.FilterAll, Limit: 1}, fams, unknown)
	if err != nil || len(one) != 1 || more != (len(sites) > 1) || one[0].ID != id {
		t.Errorf("limit 1: %d sites, more %v, %v", len(one), more, err)
	}
	for _, f := range []energy.AccessFilter{energy.FilterPublic, energy.FilterRestricted} {
		got, _, _, err := st.ChargingNear(bounded(t), energy.Query{At: at, RadiusM: everywhere, Access: f, Limit: 100}, fams, unknown)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if s.Access != f.Codes()[0] {
				t.Errorf("filter %s read %+v", f, s)
			}
		}
	}
	fast, _, _, err := st.ChargingNear(bounded(t), energy.Query{At: at, RadiusM: everywhere, Access: energy.FilterAll, MinKw: 150, Limit: 100}, fams, unknown)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]int{}
	for _, s := range sites {
		all[s.ID] = len(s.Points)
	}
	dropped := 0
	for _, s := range fast {
		// Of a site the filter keeps, only the points it asks for are read
		// (the MCP capability's rule).
		if len(s.Points) == 0 || slices.ContainsFunc(s.Points, func(p energy.ChargePoint) bool { return p.MaxPowerKw == nil || *p.MaxPowerKw < 150 }) {
			t.Errorf("min_kw 150 read %+v", s)
		}
		dropped += all[s.ID] - len(s.Points)
	}
	if len(fast) >= len(sites) {
		t.Errorf("min_kw filtered nothing: %d of %d", len(fast), len(sites))
	}
	// A site with points on both sides of the floor keeps its fast ones only.
	var mixed string
	var mAt geo.Point
	owner(t, `select c.site_id, st_y(c.location), st_x(c.location) from contract.charging_site c join contract.charge_point p using (site_id)
where c.source_ref = any('{`+strings.Join(fams, ",")+`}') group by 1, 2, 3
having min(p.power_max_kw) < 150 and max(p.power_max_kw) >= 150 order by 1 limit 1`, &mixed, &mAt.Lat, &mAt.Lon)
	kept, _, _, err := st.ChargingNear(bounded(t), energy.Query{At: mAt, RadiusM: 1, Access: energy.FilterAll, MinKw: 150, Limit: 100}, fams, unknown)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range kept {
		if s.ID == mixed {
			dropped++
			if slices.ContainsFunc(s.Points, func(p energy.ChargePoint) bool { return p.MaxPowerKw == nil || *p.MaxPowerKw < 150 }) {
				t.Errorf("min_kw 150 kept a slower point of %s: %+v", mixed, s.Points)
			}
		}
	}
	if dropped == 0 {
		t.Error("min_kw dropped no slower point of a kept site; the fixture no longer proves the point filter")
	}
	other, _, _, err := st.ChargingNear(bounded(t), q, []string{"park_alpha"}, unknown)
	if err != nil || len(other) != 0 {
		t.Errorf("another domain's family read %d sites: %v", len(other), err)
	}
}

func TestParkingNearReadsOccupancyFromTheOccupancyFamiliesOnly(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "FR-PARK-003", "SEC-030", "ENV-051")
	st := seeded(t)
	var (
		id      string
		at      geo.Point
		unknown time.Time
	)
	owner(t, `select f.facility_id, st_y(f.location), st_x(f.location), o.observed_at
from contract.parking_facility f join contract.occupancy_observation o using (facility_id)
where o.occupancy_status = 'other' order by o.observed_at limit 1`, &id, &at.Lat, &at.Lon, &unknown)
	listed := families(t, st, "parking")
	q := parking.Query{At: at, RadiusM: everywhere, Limit: 100}
	sites, more, _, err := st.ParkingNear(bounded(t), q, listed, listed, unknown)
	if err != nil || len(sites) == 0 || more {
		t.Fatalf("read %d sites, more %v: %v", len(sites), more, err)
	}
	if o := sites[0].Occupancy; sites[0].ID != id || o == nil || o.State != parking.StateOther || o.Free != nil || !o.ObservedAt.Equal(unknown) {
		t.Errorf("an unmapped status reads as other: %+v", sites[0])
	}
	for i, s := range sites {
		if !slices.Contains(listed, s.Family) || i > 0 && s.DistanceM < sites[i-1].DistanceM {
			t.Errorf("site %+v", s)
		}
	}

	var delta string
	var dAt geo.Point
	owner(t, `select facility_id, st_y(location), st_x(location) from contract.parking_facility
where source_ref = 'truckpark_sid_static' order by facility_id limit 1`, &delta, &dAt.Lat, &dAt.Lon)
	end := fixtures.ReferenceInstant.AddDate(0, 0, 2)
	// The SID's occupancy is its dynamic family's; a request that has not
	// accepted its terms reads the facility without it.
	occupancy := slices.DeleteFunc(slices.Clone(listed), func(f string) bool { return f == "truckpark_sid_static" || f == "truckpark_sid_dyn" })
	got, _, _, err := st.ParkingNear(bounded(t), parking.Query{At: dAt, RadiusM: 1, Limit: 5}, listed, occupancy, end)
	if err != nil || len(got) != 1 || got[0].ID != delta || got[0].Occupancy != nil || got[0].Kind != parking.KindTruckParking {
		t.Errorf("a pending family's site carries occupancy: %+v, %v", got, err)
	}
	got, _, _, err = st.ParkingNear(bounded(t), parking.Query{At: dAt, RadiusM: 1, Limit: 5}, occupancy, occupancy, end)
	if err != nil || slices.ContainsFunc(got, func(s parking.Site) bool { return s.ID == delta }) {
		t.Errorf("an unlisted family's site read: %+v, %v", got, err)
	}

	var (
		fid    string
		fAt    geo.Point
		free   int
		report time.Time
	)
	owner(t, `select f.facility_id, st_y(f.location), st_x(f.location), o.free_spaces, o.observed_at
from contract.parking_facility f join contract.occupancy_observation o using (facility_id)
where o.free_spaces > 0 and f.source_ref <> 'truckpark_sid_static' order by o.observed_at desc limit 1`, &fid, &fAt.Lat, &fAt.Lon, &free, &report)
	var threshold time.Duration
	owner(t, `select make_interval(secs => s.stale_after_s) from contract.occupancy_observation o join contract.source s using (source_ref)
where o.facility_id = '`+fid+`' and o.observed_at = '`+report.Format(time.RFC3339Nano)+`'`, &threshold)
	near := func(min int, now time.Time) []parking.Site {
		got, _, _, err := st.ParkingNear(bounded(t), parking.Query{At: fAt, RadiusM: 1, FreeMin: min, Limit: 5}, listed, listed, now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := near(free, report); len(got) != 1 || got[0].ID != fid || *got[0].Occupancy.Free != free {
		t.Errorf("free_min at the report: %+v", got)
	}
	if got := near(free+1, report); len(got) != 0 {
		t.Errorf("free_min above the report: %+v", got)
	}
	if got := near(free, report.Add(threshold)); len(got) != 1 {
		t.Errorf("a report at the stale threshold is live: %+v", got)
	}
	if got := near(free, report.Add(threshold+time.Second)); len(got) != 0 {
		t.Errorf("a stale report satisfies free_min: %+v", got)
	}
	if got := near(0, report.Add(threshold+time.Second)); len(got) != 1 {
		t.Errorf("without free_min a stale report still lists the site: %+v", got)
	}
	// A family with no fixed interval (stale_after_s NULL) never goes
	// stale: its report satisfies free_min however old it is.
	t.Run("a family with no interval", func(t *testing.T) {
		seedChanges(t, "source.stale_after_s", "source.freshness_target_s")
		var fam string
		var target, after *int64
		owner(t, `select s.source_ref, s.freshness_target_s::bigint, s.stale_after_s::bigint from contract.occupancy_observation o join contract.source s using (source_ref)
where o.facility_id = '`+fid+`' and o.observed_at = '`+report.Format(time.RFC3339Nano)+`'`, &fam, &target, &after)
		t.Cleanup(func() {
			change(t, `update contract.source set freshness_target_s = $2, stale_after_s = $3 where source_ref = $1`, fam, target, after)
		})
		change(t, `update contract.source set freshness_target_s = null, stale_after_s = null where source_ref = $1`, fam)
		if got := near(free, report.AddDate(1, 0, 0)); len(got) != 1 {
			t.Errorf("a report of a family with no interval is stale: %+v", got)
		}
	})

	var crowded geo.Point
	owner(t, `select st_y(f.location), st_x(f.location) from contract.parking_facility f
where (select count(*) from contract.parking_facility g
       where st_dwithin(f.location::geography, g.location::geography, 50000)) >= 2
order by f.facility_id limit 1`, &crowded.Lat, &crowded.Lon)
	one, more, _, err := st.ParkingNear(bounded(t), parking.Query{At: crowded, RadiusM: everywhere, Limit: 1}, listed, listed, unknown)
	if err != nil || len(one) != 1 || !more {
		t.Errorf("limit 1: %d sites, more %v, %v", len(one), more, err)
	}
}

// TestTheReadsTellTheEarliestReportAfterNow checks the instant a cached
// answer must end at: the earliest report dated after now of the points
// shown, and of every parking site within the radius whether free_min keeps
// it or not.
func TestTheReadsTellTheEarliestReportAfterNow(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	st := seeded(t)
	fams := families(t, st, "energy")
	in := "'{" + strings.Join(fams, ",") + "}'::text[]"
	var (
		id   string
		at   geo.Point
		now  time.Time
		want time.Time
	)
	// A site with at least two reports: now is its first, the next is its
	// second.
	owner(t, `select c.site_id, st_y(c.location), st_x(c.location), min(o.observed_at)
from contract.charging_site c join contract.charge_point p using (site_id)
join contract.charge_point_state_observation o using (point_id)
where o.source_ref = any(`+in+`) and c.withdrawn_at is null and c.source_ref = any(`+in+`)
group by 1, 2, 3 having count(*) >= 2 order by 1 limit 1`, &id, &at.Lat, &at.Lon, &now)
	q := energy.Query{At: at, RadiusM: 1, Access: energy.FilterAll, Limit: 1}
	sites, _, next, err := st.ChargingNear(bounded(t), q, fams, now)
	if err != nil || len(sites) != 1 || sites[0].ID != id {
		t.Fatalf("read %+v: %v", sites, err)
	}
	owner(t, `select min(o.observed_at) from contract.charge_point p
join contract.charge_point_state_observation o using (point_id)
where p.site_id = '`+id+`' and o.source_ref = any(`+in+`) and o.observed_at > '`+now.Format(time.RFC3339Nano)+`'`, &want)
	if next == nil || !next.Equal(want) || next.Location() != time.UTC {
		t.Errorf("charging: next report %v, want %v", next, want)
	}
	if _, _, next, err := st.ChargingNear(bounded(t), q, fams, now.AddDate(10, 0, 0)); err != nil || next != nil {
		t.Errorf("charging with no later report: %v, %v", next, err)
	}

	// The site past the limit only tells that there are more: its later
	// report does not end the answer. Two sites, the nearer with no report
	// after now, the next one with one.
	var (
		near, far string
		radius    int
	)
	owner(t, `with s as (
  select site_id, location from contract.charging_site where withdrawn_at is null and source_ref = any(`+in+`)),
ranked as (
  select a.site_id a, b.site_id b, ceil(st_distance(a.location::geography, b.location::geography))::int + 1 r,
         row_number() over (partition by a.site_id
           order by round(st_distance(a.location::geography, b.location::geography))::int, b.site_id) rk
  from s a cross join s b),
last as (
  select p.site_id, max(o.observed_at) at from contract.charge_point p
  join contract.charge_point_state_observation o using (point_id)
  where o.source_ref = any(`+in+`) group by 1)
select r2.a, st_y(c.location), st_x(c.location), r2.r, l.at, r2.b
from ranked r1 join ranked r2 on r2.a = r1.a and r1.rk = 1 and r2.rk = 2 and r1.b = r1.a
join contract.charging_site c on c.site_id = r2.a
join last l on l.site_id = r2.a
where exists (select 1 from contract.charge_point p join contract.charge_point_state_observation o using (point_id)
              where p.site_id = r2.b and o.source_ref = any(`+in+`) and o.observed_at > l.at)
order by r2.a limit 1`, &near, &at.Lat, &at.Lon, &radius, &now, &far)
	two := energy.Query{At: at, RadiusM: radius, Access: energy.FilterAll, Limit: 2}
	if sites, _, next, err := st.ChargingNear(bounded(t), two, fams, now); err != nil || len(sites) != 2 || sites[0].ID != near || sites[1].ID != far || next == nil {
		t.Fatalf("limit 2 (%s, %s): %+v, next %v, %v", near, far, sites, next, err)
	}
	two.Limit = 1
	if sites, more, next, err := st.ChargingNear(bounded(t), two, fams, now); err != nil || len(sites) != 1 || sites[0].ID != near || !more || next != nil {
		t.Errorf("limit 1: %+v, more %v, next %v (the site past the limit), %v", sites, more, next, err)
	}

	listed := families(t, st, "parking")
	pin := "'{" + strings.Join(listed, ",") + "}'::text[]"
	var fid string
	owner(t, `select f.facility_id, st_y(f.location), st_x(f.location), min(o.observed_at)
from contract.parking_facility f join contract.occupancy_observation o using (facility_id)
where o.source_ref = any(`+pin+`) and f.source_ref = any(`+pin+`) and f.withdrawn_at is null
group by 1, 2, 3 having count(*) >= 2 order by 1 limit 1`, &fid, &at.Lat, &at.Lon, &now)
	owner(t, `select min(o.observed_at) from contract.occupancy_observation o
join contract.parking_facility f using (facility_id)
where o.source_ref = any(`+pin+`) and f.source_ref = any(`+pin+`) and f.withdrawn_at is null and o.observed_at > '`+now.Format(time.RFC3339Nano)+`'
  and st_dwithin(f.location::geography, (select location::geography from contract.parking_facility where facility_id = '`+fid+`'), 1)`, &want)
	for _, freeMin := range []int{0, 1 << 20} {
		got, _, next, err := st.ParkingNear(bounded(t), parking.Query{At: at, RadiusM: 1, FreeMin: freeMin, Limit: 5}, listed, listed, now)
		if err != nil || next == nil || !next.Equal(want) || next.Location() != time.UTC {
			t.Errorf("parking, free_min %d (%d sites): next report %v, want %v, %v", freeMin, len(got), next, want, err)
		}
	}
	if _, _, next, err := st.ParkingNear(bounded(t), parking.Query{At: at, RadiusM: 1, Limit: 5}, listed, listed, now.AddDate(10, 0, 0)); err != nil || next != nil {
		t.Errorf("parking with no later report: %v, %v", next, err)
	}
}

func TestPlaceReadsOnePlaceOfTheGivenFamilies(t *testing.T) {
	req.Covers(t, "FR-BRIEF-008", "SEC-031")
	st := seeded(t)
	var (
		id, fam, name string
		at            geo.Point
	)
	owner(t, `select place_id, source_ref, name, st_y(location), st_x(location) from contract.place order by place_id limit 1`, &id, &fam, &name, &at.Lat, &at.Lon)
	p, err := st.Place(bounded(t), id, []string{fam})
	if err != nil || p.ID != id || p.Family != fam || p.Name != name || p.Location != at || p.Kind == places.Other {
		t.Errorf("place %+v, %v", p, err)
	}
	if _, err := st.Place(bounded(t), id, []string{"afir_alpha"}); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("a place of another family: %v", err)
	}
	if _, err := st.Place(bounded(t), "999999", []string{fam}); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("an unknown place: %v", err)
	}
}

func TestFeatureReadsReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.Provenance(ctx, []string{"x"}, "en"); err == nil {
		t.Error("Provenance on an ended context succeeded")
	}
	if _, _, _, err := st.ChargingNear(ctx, energy.Query{Limit: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("ChargingNear on an ended context succeeded")
	}
	if _, _, _, err := st.ParkingNear(ctx, parking.Query{Limit: 1}, []string{"x"}, nil, time.Now()); err == nil {
		t.Error("ParkingNear on an ended context succeeded")
	}
	if _, err := st.Place(ctx, "x", []string{"x"}); err == nil || errors.Is(err, places.ErrNotFound) {
		t.Errorf("Place on an ended context: %v", err)
	}
	if !store.IsDeadline(store.ErrDeadlineExhausted) || store.IsDeadline(errors.New("other")) {
		t.Error("IsDeadline")
	}
}

func TestParkingFiltersAndRoadReadTheStoredKindAmenitiesAndCorridor(t *testing.T) {
	req.Covers(t, "FR-PARK-002")
	st := seeded(t)
	listed := families(t, st, "parking")
	var at geo.Point
	owner(t, `select st_y(location), st_x(location) from contract.parking_facility order by facility_id limit 1`, &at.Lat, &at.Lon)
	ids := func(q parking.Query) []string {
		t.Helper()
		sites, _, _, err := st.ParkingNear(bounded(t), q, listed, listed, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range sites {
			out = append(out, s.ID)
		}
		return out
	}
	in := "'" + strings.Join(listed, "','") + "'"
	want := func(where string) []string {
		t.Helper()
		var out []string
		owner(t, `select coalesce(array_agg(facility_id order by facility_id), '{}') from contract.parking_facility
where withdrawn_at is null and source_ref in (`+in+`) and `+where, &out)
		return out
	}
	near := parking.Query{At: at, RadiusM: 2_000_000, Limit: 100}
	kind := near
	kind.Kind = "truck"
	if got, w := ids(kind), want(`stored_kind = 'truck'`); len(got) != len(w) {
		t.Errorf("kind truck: %v, want %v", got, w)
	}
	rest := near
	rest.Kind = "rest_area"
	if got, all := ids(rest), ids(near); !slices.Equal(got, all) {
		t.Errorf("rest_area is not answered unfiltered: %v, %v", got, all)
	}
	amen := near
	amen.Amenities = []string{"toilet"}
	if got, w := ids(amen), want(`amenities @> '{toilet}'`); len(got) != len(w) {
		t.Errorf("amenity toilet: %v, want %v", got, w)
	}
	var road string
	owner(t, `select coalesce(min(road_ref), 'A1') from contract.parking_facility where road_ref is not null and facility_id like 'p:%'`, &road)
	sites, _, _, err := st.ParkingNear(bounded(t), parking.Query{Road: road, Limit: 100}, listed, listed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range sites {
		if !strings.HasPrefix(s.ID, "p:") || s.DistanceM != 0 || i > 0 && s.Name != nil && sites[i-1].Name != nil && *s.Name < *sites[i-1].Name {
			t.Errorf("along %s: %+v", road, s)
		}
	}
}
