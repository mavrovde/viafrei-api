//go:build integration

package store_test

import (
	"context"
	"errors"
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
	if got := families(t, st, "energy"); !slices.Equal(got, []string{"src-alpha-energy", "src-alpha-energy-stale", "src-beta-energy", "src-gamma-energy"}) {
		t.Errorf("energy families %v", got)
	}
	if got := families(t, st, "parking"); !slices.Contains(got, "src-delta-parking") || slices.Contains(got, "src-alpha-energy") {
		t.Errorf("parking families %v", got)
	}
}

func TestProvenanceReadsTheGivenFamiliesInOneLanguage(t *testing.T) {
	req.Covers(t, "ENV-003", "SEC-031")
	st := seeded(t)
	got, err := st.Provenance(bounded(t), []string{"src-gamma-energy", "src-delta-parking", "src-alpha-energy-stale", "no-such-family"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Source.Family != "src-alpha-energy-stale" || got[1].Source.Family != "src-delta-parking" || got[2].Source.Family != "src-gamma-energy" {
		t.Fatalf("read %+v", got)
	}
	end := fixtures.ReferenceInstant.AddDate(0, 0, 2)
	stale, delta, gamma := got[0], got[1], got[2]
	if stale.StaleAfter != time.Hour || stale.DataAsOf == nil || !stale.DataAsOf.Equal(end.Add(-10*time.Hour)) || !stale.Assess(end).Stale {
		t.Errorf("stale family %+v", stale)
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
	de, err := st.Provenance(bounded(t), []string{"src-gamma-energy"}, "de")
	if err != nil || len(de) != 1 || len(de[0].Notes) != 1 || de[0].Notes[0].Text == gamma.Notes[0].Text {
		t.Errorf("German notes %+v, %v", de, err)
	}
	none, err := st.Provenance(bounded(t), []string{"src-gamma-energy"}, "xx")
	if err != nil || len(none) != 1 || len(none[0].Notes) != 0 {
		t.Errorf("a language without notes %+v, %v", none, err)
	}
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
where o.availability = 'x_unmapped' order by o.observed_at limit 1`, &id, &fam, &at.Lat, &at.Lon, &unknown)
	fams := families(t, st, "energy")
	q := energy.Query{At: at, RadiusM: everywhere, Access: energy.FilterAll, Limit: 100}

	sites, more, _, err := st.ChargingNear(bounded(t), q, fams, unknown)
	if err != nil || len(sites) == 0 {
		t.Fatalf("read %d sites: %v", len(sites), err)
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
	if err != nil || len(one) != 1 || !more || one[0].ID != id {
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
	if dropped == 0 {
		t.Error("min_kw dropped no slower point of a kept site; the fixture no longer proves the point filter")
	}
	other, _, _, err := st.ChargingNear(bounded(t), q, []string{"src-alpha-parking"}, unknown)
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
where o.occupancy_status = 'x_unmapped' order by o.observed_at limit 1`, &id, &at.Lat, &at.Lon, &unknown)
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
		if s.Kind == parking.KindOther || !slices.Contains(listed, s.Family) || i > 0 && s.DistanceM < sites[i-1].DistanceM {
			t.Errorf("site %+v", s)
		}
	}

	var delta string
	var dAt geo.Point
	owner(t, `select facility_id, st_y(location), st_x(location) from contract.parking_facility
where source_ref = 'src-delta-parking' order by facility_id limit 1`, &delta, &dAt.Lat, &dAt.Lon)
	end := fixtures.ReferenceInstant.AddDate(0, 0, 2)
	occupancy := slices.DeleteFunc(slices.Clone(listed), func(f string) bool { return f == "src-delta-parking" })
	got, _, _, err := st.ParkingNear(bounded(t), parking.Query{At: dAt, RadiusM: 1, Limit: 5}, listed, occupancy, end)
	if err != nil || len(got) != 1 || got[0].ID != delta || got[0].Occupancy != nil || got[0].Kind != parking.KindTruckParking {
		t.Errorf("a pending family's site carries occupancy: %+v, %v", got, err)
	}
	got, _, _, err = st.ParkingNear(bounded(t), parking.Query{At: dAt, RadiusM: 1, Limit: 5}, occupancy, occupancy, end)
	if err != nil || len(got) != 0 {
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
where o.free_spaces > 0 and f.source_ref <> 'src-delta-parking' order by o.observed_at desc limit 1`, &fid, &fAt.Lat, &fAt.Lon, &free, &report)
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
	if got := near(free, report.Add(30*time.Minute)); len(got) != 1 {
		t.Errorf("a report at the stale threshold is live: %+v", got)
	}
	if got := near(free, report.Add(30*time.Minute+time.Second)); len(got) != 0 {
		t.Errorf("a stale report satisfies free_min: %+v", got)
	}
	if got := near(0, report.Add(30*time.Minute+time.Second)); len(got) != 1 {
		t.Errorf("without free_min a stale report still lists the site: %+v", got)
	}
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
	if _, err := st.Place(bounded(t), id, []string{"src-alpha-energy"}); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("a place of another family: %v", err)
	}
	if _, err := st.Place(bounded(t), "pl-00-9999", []string{fam}); !errors.Is(err, places.ErrNotFound) {
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
