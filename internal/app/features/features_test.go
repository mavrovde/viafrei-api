package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var now = time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)

var all = []access.UseClass{"consumer", "non_commercial", "commercial"}

// register: two energy families (one consumer only), two parking families
// (one behind acceptance), one places family.
var register = []access.Terms{
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "e2", Domain: "energy", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
	{Family: "p1", Domain: "parking", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "p2", Domain: "parking", UseClasses: all, AcceptanceRequired: true, Version: "v1", Reviewed: true},
	{Family: "pl", Domain: "places", UseClasses: all, Version: "v1", Reviewed: true},
}

var errStore = errors.New("store down")

type fake struct {
	regErr, provErr, chErr, pkErr, plErr error
	provCalls                            int
	provErrOnCall                        int
	sites                                []energy.Site
	parks                                []parking.Site
	place                                places.Place
	gotFamilies, gotListed, gotOccupancy []string
	gotNow                               time.Time
	gotLang                              string
	gotProv                              [][]string
	chDelay                              bool
}

func (f *fake) Register(context.Context) ([]access.Terms, error) { return register, f.regErr }

func (f *fake) Provenance(_ context.Context, fams []string, lang string) ([]provenance.Family, error) {
	f.provCalls++
	f.gotLang = lang
	f.gotProv = append(f.gotProv, slices.Clone(fams))
	if f.provErr != nil && (f.provErrOnCall == 0 || f.provErrOnCall == f.provCalls) {
		return nil, f.provErr
	}
	out := make([]provenance.Family, len(fams))
	for i, s := range fams {
		out[i] = provenance.Family{Source: access.Source{Family: s}}
	}
	return out, nil
}

func (f *fake) ChargingNear(ctx context.Context, _ energy.Query, fams []string, at time.Time) ([]energy.Site, bool, error) {
	f.gotFamilies, f.gotNow = fams, at
	if f.chDelay {
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	return slices.Clone(f.sites), true, f.chErr
}

func (f *fake) ParkingNear(_ context.Context, _ parking.Query, listed, occ []string, at time.Time) ([]parking.Site, bool, error) {
	f.gotListed, f.gotOccupancy, f.gotNow = listed, occ, at
	return slices.Clone(f.parks), false, f.pkErr
}

func (f *fake) Place(context.Context, string, []string) (places.Place, error) {
	return f.place, f.plErr
}

func service(f *fake, isTimeout func(error) bool) *features.Service {
	return features.New(features.Repositories{Register: f, Charging: f, Parking: f, Places: f},
		features.Settings{Language: "en", PartDeadline: 50 * time.Millisecond, IsTimeout: isTimeout},
		func() time.Time { return now })
}

func key(use access.UseClass, scopes []access.Scope, sources ...string) access.Record {
	return access.Record{Plan: access.Plan{UseClass: use, Scopes: scopes, Sources: sources}}
}

var everything = []string{"e1", "e2", "p1", "p2", "pl"}

var allScopes = []access.Scope{"places", "energy", "parking"}

func names(fams []provenance.Family) []string {
	var out []string
	for _, f := range fams {
		out = append(out, f.Source.Family)
	}
	return out
}

func site(fam string, points ...string) energy.Site {
	s := energy.Site{ID: "s-" + fam, Family: fam}
	for _, p := range points {
		s.Points = append(s.Points, energy.ChargePoint{Availability: &energy.Availability{Family: p}})
	}
	return s
}

func TestChargingIsGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "SEC-030", "SEC-032", "ENV-003")
	f := &fake{sites: []energy.Site{site("e1", "e1"), site("e1")}}
	a, err := service(f, nil).Charging(context.Background(), key("commercial", allScopes, everything...), energy.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFamilies, []string{"e1"}) || !a.Partial || !a.HasMore || !a.At.Equal(now) || !f.gotNow.Equal(now) {
		t.Errorf("read %v, partial %v, more %v, at %v", f.gotFamilies, a.Partial, a.HasMore, a.At)
	}
	if !slices.Equal(names(a.Families), []string{"e1"}) || f.gotLang != "en" || len(a.Items) != 2 {
		t.Errorf("attributed %v in %q, %d items", names(a.Families), f.gotLang, len(a.Items))
	}

	f = &fake{}
	a, err = service(f, nil).Charging(context.Background(), key("consumer", allScopes, everything...), energy.Query{})
	if err != nil || a.Partial || !slices.Equal(names(a.Families), []string{"e1", "e2"}) {
		t.Errorf("an empty answer names the consulted families: %v, %v, %v", names(a.Families), a.Partial, err)
	}
}

func TestChargingRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	cases := []struct {
		name string
		f    *fake
		key  access.Record
		want error
	}{
		{"register", &fake{regErr: errStore}, key("consumer", allScopes, everything...), errStore},
		{"none permitted", &fake{}, key("consumer", allScopes, "p1"), access.ErrNonePermitted},
		{"store", &fake{chErr: errStore}, key("consumer", allScopes, everything...), errStore},
		{"a site of a withheld family", &fake{sites: []energy.Site{site("e2")}}, key("commercial", allScopes, everything...), features.ErrGateViolation},
		{"a report of a withheld family", &fake{sites: []energy.Site{site("e1", "e2")}}, key("commercial", allScopes, everything...), features.ErrGateViolation},
		{"provenance", &fake{provErr: errStore}, key("consumer", allScopes, everything...), errStore},
	}
	for _, c := range cases {
		if _, err := service(c.f, nil).Charging(context.Background(), c.key, energy.Query{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func free(n int, fam string) *parking.Occupancy {
	return &parking.Occupancy{Free: &n, State: parking.StateFree, Family: fam}
}

func TestParkingListsPendingFamiliesWithoutOccupancy(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "FR-PARK-003", "SEC-030")
	f := &fake{parks: []parking.Site{
		{ID: "a", Family: "p1", Occupancy: free(3, "p1")},
		{ID: "b", Family: "p2", Occupancy: free(5, "p2")},
		{ID: "c", Family: "p1"},
	}}
	a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, everything...), parking.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotListed, []string{"p1", "p2"}) || !slices.Equal(f.gotOccupancy, []string{"p1"}) {
		t.Errorf("listed %v, occupancy from %v", f.gotListed, f.gotOccupancy)
	}
	if !a.Partial || a.Items[0].Occupancy == nil || a.Items[1].Occupancy != nil || !a.Items[1].TermsRequired || a.Items[2].TermsRequired {
		t.Errorf("items %+v, partial %v", a.Items, a.Partial)
	}
	if !slices.Equal(names(a.Families), []string{"p1", "p2"}) || a.HasMore || !a.At.Equal(now) {
		t.Errorf("attributed %v", names(a.Families))
	}

	accepted := key("commercial", allScopes, everything...)
	accepted.Accepted = []access.Acceptance{{Source: "p2", Version: "v1"}}
	f = &fake{parks: []parking.Site{{ID: "b", Family: "p2", Occupancy: free(5, "p2")}}}
	if a, err := service(f, nil).Parking(context.Background(), accepted, parking.Query{}); err != nil || a.Partial || a.Items[0].Occupancy == nil {
		t.Errorf("after acceptance: %+v, %v", a, err)
	}
	f = &fake{}
	if a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p1"), parking.Query{}); err != nil || !slices.Equal(names(a.Families), []string{"p1"}) {
		t.Errorf("an empty answer names the listed families: %v, %v", names(a.Families), err)
	}
}

func TestParkingRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	k := key("commercial", allScopes, everything...)
	cases := []struct {
		name string
		f    *fake
		key  access.Record
		want error
	}{
		{"register", &fake{regErr: errStore}, k, errStore},
		{"nothing permitted", &fake{}, key("commercial", allScopes, "p2"), access.ErrNonePermitted},
		{"store", &fake{pkErr: errStore}, k, errStore},
		{"a site of an unlisted family", &fake{parks: []parking.Site{{Family: "e1"}}}, k, features.ErrGateViolation},
		{"occupancy of a pending family", &fake{parks: []parking.Site{{Family: "p1", Occupancy: free(1, "p2")}}}, k, features.ErrGateViolation},
		{"provenance", &fake{provErr: errStore}, k, errStore},
	}
	for _, c := range cases {
		if _, err := service(c.f, nil).Parking(context.Background(), c.key, parking.Query{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

var berlin = places.Place{ID: "pl-1", Name: "B", Kind: "locality", Location: geo.Point{Lat: 52.5, Lon: 13.4}, Family: "pl"}

func statuses(o features.Overview) []features.Status {
	var out []features.Status
	for _, p := range o.Parts {
		out = append(out, p.Status)
	}
	return out
}

func TestOverviewComposesTheParts(t *testing.T) {
	req.Covers(t, "FR-BRIEF-008", "FR-BRIEF-002", "ENV-004")
	f := &fake{place: berlin, sites: []energy.Site{site("e1")}, parks: []parking.Site{{Family: "p2"}}}
	o, err := service(f, nil).Overview(context.Background(), key("commercial", allScopes, everything...), features.OverviewQuery{PlaceID: "pl-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(statuses(o), []features.Status{features.StatusOK, features.StatusOK}) || o.Charging == nil || o.Parking == nil {
		t.Fatalf("parts %v", statuses(o))
	}
	if !o.Partial || !slices.Equal(names(o.Families), []string{"e1", "p2", "pl"}) || o.Place.ID != "pl-1" {
		t.Errorf("partial %v, families %v", o.Partial, names(o.Families))
	}

	accepted := key("consumer", allScopes, everything...)
	accepted.Accepted = []access.Acceptance{{Source: "p2", Version: "v1"}}
	f = &fake{place: berlin}
	o, err = service(f, nil).Overview(context.Background(), accepted, features.OverviewQuery{Needs: []features.Part{features.PartParking}})
	if err != nil || !slices.Equal(statuses(o), []features.Status{features.StatusNotRequested, features.StatusOK}) || o.Charging != nil || o.Partial {
		t.Errorf("needs parking: %v, %v, partial %v", statuses(o), err, o.Partial)
	}
}

func TestOverviewPartOutcomes(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "FR-BRIEF-003")
	k := key("commercial", allScopes, everything...)
	isStore := func(err error) bool { return errors.Is(err, errStore) }
	cases := []struct {
		name      string
		f         *fake
		key       access.Record
		isTimeout func(error) bool
		want      []features.Status
	}{
		{"no energy scope", &fake{place: berlin}, key("commercial", []access.Scope{"places", "parking"}, everything...), nil,
			[]features.Status{features.StatusNotPermitted, features.StatusOK}},
		{"no energy family", &fake{place: berlin}, key("commercial", allScopes, "pl", "p1"), nil,
			[]features.Status{features.StatusNotPermitted, features.StatusOK}},
		{"charging past its deadline", &fake{place: berlin, chDelay: true}, k, nil,
			[]features.Status{features.StatusTimeout, features.StatusOK}},
		{"a store timeout", &fake{place: berlin, chErr: errStore}, k, isStore,
			[]features.Status{features.StatusTimeout, features.StatusOK}},
		{"a store failure", &fake{place: berlin, pkErr: errStore}, k, func(error) bool { return false },
			[]features.Status{features.StatusOK, features.StatusUnavailable}},
	}
	for _, c := range cases {
		o, err := service(c.f, c.isTimeout).Overview(context.Background(), c.key, features.OverviewQuery{})
		if err != nil || !slices.Equal(statuses(o), c.want) {
			t.Errorf("%s: %v, %v", c.name, statuses(o), err)
		}
		for _, p := range o.Parts {
			if (p.Status == features.StatusOK) != (p.Err == nil) {
				t.Errorf("%s: %s carries %v", c.name, p.Part, p.Err)
			}
		}
	}
}

func TestOverviewRefusals(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "SEC-033")
	k := key("commercial", allScopes, everything...)
	cases := []struct {
		name string
		f    *fake
		key  access.Record
		want error
	}{
		{"register", &fake{regErr: errStore}, k, errStore},
		{"no places family", &fake{}, key("commercial", allScopes, "e1"), access.ErrNonePermitted},
		{"not found", &fake{plErr: places.ErrNotFound}, k, places.ErrNotFound},
		{"a place of another family", &fake{place: places.Place{Family: "x"}}, k, features.ErrGateViolation},
		{"a part's gate violation", &fake{place: berlin, sites: []energy.Site{site("x")}}, k, features.ErrGateViolation},
		{"no part ok", &fake{place: berlin, chErr: errStore, pkErr: errStore}, k, features.ErrNoPart},
		{"nothing permitted", &fake{place: berlin}, key("commercial", []access.Scope{"places"}, everything...), features.ErrNoPart},
		{"provenance of the whole", &fake{place: berlin, provErr: errStore, provErrOnCall: 3}, k, errStore},
	}
	for _, c := range cases {
		if _, err := service(c.f, nil).Overview(context.Background(), c.key, features.OverviewQuery{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	o, err := service(&fake{place: berlin}, nil).Overview(context.Background(), key("commercial", []access.Scope{"places"}, everything...), features.OverviewQuery{})
	if !errors.Is(err, features.ErrNoPart) || len(o.Parts) != 2 || !errors.Is(o.Parts[0].Err, features.ErrScopeDenied) {
		t.Errorf("a failed overview keeps its parts: %+v", o.Parts)
	}
}
