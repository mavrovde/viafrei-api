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
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "e2", Domain: "energy", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
	{Family: "p1", Domain: "parking", UseClasses: all, Version: "v1"},
	{Family: "p2", Domain: "parking", UseClasses: all, AcceptanceRequired: true, Version: "p2-terms-1"},
	{Family: "pl", Domain: "places", UseClasses: all, Version: "v1"},
	{Family: "st", Domain: "transit", UseClasses: all, Version: "v1"},
	{Family: "rd", Domain: "roads", UseClasses: all, Version: "v1"},
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
	gotStops                             []string
	gotPlaceQ                            places.Query
	gotPlaceRoads                        []string
	gotNow                               time.Time
	gotLang                              string
	gotProv                              [][]string
	chDelay                              bool
	reg                                  []access.Terms // replaces register when set
	regCalls                             int
	termsErr                             error
	gotTerms                             []string
	notes                                map[string][]provenance.Note
	next                                 *time.Time                      // the earliest future report both reads return
	feeding                              map[provenance.Dataset][]string // the families of each dataset; nil holds every family
	feedErr                              error
	feedErrOn                            provenance.Dataset // the dataset feedErr fails; empty fails every one
	gotFeeding                           []provenance.Dataset
}

func (f *fake) Feeding(_ context.Context, d provenance.Dataset, fams []string) ([]string, error) {
	f.gotFeeding = append(f.gotFeeding, d)
	if f.feedErr != nil && (f.feedErrOn == "" || f.feedErrOn == d) {
		return nil, f.feedErr
	}
	if f.feeding == nil {
		return slices.Clone(fams), nil
	}
	out := []string{}
	for _, x := range fams {
		if slices.Contains(f.feeding[d], x) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (f *fake) Register(context.Context) ([]access.Terms, error) {
	f.regCalls++
	if f.reg != nil {
		return f.reg, f.regErr
	}
	return register, f.regErr
}

func (f *fake) TermsTexts(_ context.Context, gates []string) ([]access.TermsText, error) {
	f.gotTerms = slices.Clone(gates)
	out := make([]access.TermsText, len(gates))
	for i, g := range gates {
		out[i] = access.TermsText{Gate: g, Lang: "de", Binding: true, Heading: g + " heading", Body: g + " body"}
	}
	return out, f.termsErr
}

func (f *fake) Provenance(_ context.Context, fams []string, lang string) ([]provenance.Family, error) {
	f.provCalls++
	f.gotLang = lang
	f.gotProv = append(f.gotProv, slices.Clone(fams))
	if f.provErr != nil && (f.provErrOnCall == 0 || f.provErrOnCall == f.provCalls) {
		return nil, f.provErr
	}
	out := make([]provenance.Family, len(fams))
	for i, s := range fams {
		out[i] = provenance.Family{Source: access.Source{Family: s}, Notes: f.notes[s]}
	}
	return out, nil
}

func (f *fake) ChargingNear(ctx context.Context, _ energy.Query, fams []string, at time.Time) ([]energy.Site, bool, *time.Time, error) {
	f.gotFamilies, f.gotNow = fams, at
	if f.chDelay {
		<-ctx.Done()
		return nil, false, nil, ctx.Err()
	}
	return slices.Clone(f.sites), true, f.next, f.chErr
}

func (f *fake) ParkingNear(_ context.Context, _ parking.Query, listed, occ []string, at time.Time) ([]parking.Site, bool, *time.Time, error) {
	f.gotListed, f.gotOccupancy, f.gotNow = listed, occ, at
	return slices.Clone(f.parks), false, f.next, f.pkErr
}

func (f *fake) Place(context.Context, string, []string) (places.Place, error) {
	return f.place, f.plErr
}

func (f *fake) Places(_ context.Context, q places.Query, fams, stops, roads []string) ([]places.Place, bool, error) {
	f.gotPlaceQ, f.gotPlaceRoads = q, roads
	f.gotFamilies, f.gotStops = fams, stops
	if f.place.ID == "" {
		return nil, false, f.plErr
	}
	return []places.Place{f.place}, true, f.plErr
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
	if err != nil || a.Partial || len(a.Families) != 0 {
		t.Errorf("an empty answer credits none, as the platform's charging search: %v, %v, %v", names(a.Families), a.Partial, err)
	}
}

func TestTheAnswersCarryTheEarliestReportAfterNow(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	next := now.Add(7 * time.Minute)
	f := &fake{place: berlin, sites: []energy.Site{site("e1")}, parks: []parking.Site{{Family: "p2"}}, next: &next}
	k := key("commercial", allScopes, everything...)
	c, err := service(f, nil).Charging(context.Background(), k, energy.Query{})
	if err != nil || c.NextReport != &next {
		t.Errorf("charging: next report %v, %v", c.NextReport, err)
	}
	p, err := service(f, nil).Parking(context.Background(), k, parking.Query{})
	if err != nil || p.NextReport != &next {
		t.Errorf("parking: next report %v, %v", p.NextReport, err)
	}
	o, err := service(f, nil).Overview(context.Background(), k, features.OverviewQuery{PlaceID: "pl-1"})
	if err != nil || o.Charging == nil || o.Charging.NextReport != &next || o.Parking == nil || o.Parking.NextReport != &next {
		t.Errorf("overview: %+v, %v", o, err)
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
		{"provenance", &fake{provErr: errStore, sites: []energy.Site{site("e1")}}, key("consumer", allScopes, everything...), errStore},
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

func TestParkingDoesNotListPendingFamilies(t *testing.T) {
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
	// p2's terms are not accepted: its site is not listed, as the platform
	// lists no Toll Collect site before its terms are accepted.
	if !a.Partial || len(a.Items) != 2 || a.Items[0].ID != "a" || a.Items[0].Occupancy == nil || a.Items[1].ID != "c" || a.Items[1].TermsRequired {
		t.Errorf("items %+v, partial %v", a.Items, a.Partial)
	}
	if !slices.Equal(names(a.Families), []string{"p1"}) || a.HasMore || !a.At.Equal(now) {
		t.Errorf("attributed %v", names(a.Families))
	}

	if len(a.Terms) != 1 || a.Terms[0].Gate != "p2-terms-1" || a.Terms[0].Body != "p2-terms-1 body" {
		t.Errorf("the pending family's terms: %+v", a.Terms)
	}

	accepted := access.WithAccepted(context.Background(), []string{"p2-terms-1"})
	// Accepted, p2's site is listed with its reading beside another site,
	// named without a distance as the platform names a Toll Collect site.
	f = &fake{parks: []parking.Site{{ID: "a", Family: "p1"}, {ID: "b", Family: "p2", Occupancy: free(5, "p2")}}}
	if a, err := service(f, nil).Parking(accepted, key("commercial", allScopes, everything...), parking.Query{}); err != nil || a.Partial || a.Items[1].Occupancy == nil || !a.Items[1].Unranged || a.Items[0].Unranged || a.Terms != nil || f.gotTerms != nil {
		t.Errorf("after acceptance: %+v, %v", a, err)
	}
	// With no other site, the platform's answer is empty: the gated site
	// is not named alone.
	f = &fake{parks: []parking.Site{{ID: "b", Family: "p2", Occupancy: free(5, "p2")}}}
	if a, err := service(f, nil).Parking(accepted, key("commercial", allScopes, everything...), parking.Query{}); err != nil || len(a.Items) != 0 {
		t.Errorf("a gated site alone: %+v, %v", a, err)
	}
	f = &fake{}
	if a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p1"), parking.Query{}); err != nil || len(a.Families) != 0 {
		t.Errorf("an empty answer over an empty scope credits none: %v, %v", names(a.Families), err)
	}
}

func TestParkingStatesAPendingFamilysConditionOnlyOnceAccepted(t *testing.T) {
	req.Covers(t, "FR-PARK-003", "SEC-030")
	cond := provenance.Note{Kind: provenance.Condition, Text: "p2 condition"}
	purpose := provenance.Note{Kind: provenance.Purpose, Text: "p2 purpose"}
	other := provenance.Note{Kind: provenance.Condition, Text: "p1 condition"}
	notes := map[string][]provenance.Note{"p1": {other}, "p2": {cond, purpose}}
	parks := []parking.Site{{ID: "a", Family: "p1", Occupancy: free(3, "p1")}, {ID: "b", Family: "p2", Occupancy: free(5, "p2")}}
	ask := func(ctx context.Context) features.Answer[parking.Site] {
		t.Helper()
		a, err := service(&fake{parks: parks, notes: notes}, nil).Parking(ctx, key("commercial", allScopes, everything...), parking.Query{})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	// Pending: p2 is neither listed nor credited, so none of its notes is
	// stated; another family's condition is kept.
	a := ask(context.Background())
	if got := provenance.Notes(a.Families, provenance.Condition); !slices.Equal(got, []string{"p1 condition"}) {
		t.Errorf("pending: conditions %v", got)
	}
	if got := provenance.Notes(a.Families, provenance.Purpose); len(got) != 0 {
		t.Errorf("pending: purposes %v", got)
	}
	if !slices.Equal(names(a.Families), []string{"p1"}) {
		t.Errorf("pending: credited %v", names(a.Families))
	}
	// Accepted: the reading is shown and its condition with it.
	a = ask(access.WithAccepted(context.Background(), []string{"p2-terms-1"}))
	if got := provenance.Notes(a.Families, provenance.Condition); !slices.Equal(got, []string{"p1 condition", "p2 condition"}) {
		t.Errorf("accepted: conditions %v", got)
	}
	if notes["p2"][0] != cond || len(notes["p2"]) != 2 {
		t.Errorf("the register's notes were changed: %v", notes["p2"])
	}
}

func TestParkingAttributesOccupancyOfAnotherFamily(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "SEC-032", "ENV-003")
	// A site of p1 whose occupancy is reported by p3, a third permitted
	// family: the report is gate-checked and attributed as p3's.
	reg := append(slices.Clone(register), access.Terms{Family: "p3", Domain: "parking", UseClasses: all, Version: "v1"})
	f := &fake{reg: reg, parks: []parking.Site{{ID: "a", Family: "p1", Occupancy: free(2, "p3")}}}
	a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p1", "p3"), parking.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names(a.Families), []string{"p1", "p3"}) || !slices.Equal(f.gotProv[0], []string{"p1", "p3"}) || a.Items[0].Occupancy == nil {
		t.Errorf("attributed %v, provenance asked for %v", names(a.Families), f.gotProv)
	}
	// The same report from a family outside the key is still refused.
	f = &fake{reg: reg, parks: []parking.Site{{ID: "a", Family: "p1", Occupancy: free(2, "p3")}}}
	if _, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p1"), parking.Query{}); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a report of a family outside the key: %v", err)
	}
}

func TestParkingListsAKeyWhoseFamiliesAreAllPending(t *testing.T) {
	req.Covers(t, "FR-PARK-003", "SEC-030")
	// Every parking family that could answer waits for its terms: the answer
	// is partial and offers the terms rather than refusing, and lists no site.
	reg := []access.Terms{
		{Family: "p2", Domain: "parking", UseClasses: all, AcceptanceRequired: true, Version: "p2-terms-1"},
		{Family: "p4", Domain: "parking", UseClasses: all, AcceptanceRequired: true, Version: "v1"},
	}
	f := &fake{reg: reg, parks: []parking.Site{{ID: "b", Family: "p2", Occupancy: free(5, "p2")}, {ID: "d", Family: "p4"}}}
	a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p2", "p4"), parking.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotListed, []string{"p2", "p4"}) || len(f.gotOccupancy) != 0 {
		t.Errorf("listed %v, occupancy from %v", f.gotListed, f.gotOccupancy)
	}
	if !a.Partial || len(a.Items) != 0 || len(a.Terms) != 2 || len(a.Families) != 0 {
		t.Errorf("items %+v, partial %v, attributed %v", a.Items, a.Partial, names(a.Families))
	}
	// Pending and otherwise refused together: the pending one is read, not listed.
	f = &fake{parks: []parking.Site{{ID: "b", Family: "p2"}}}
	if a, err := service(f, nil).Parking(context.Background(), key("commercial", allScopes, "p2"), parking.Query{}); err != nil || !a.Partial || !slices.Equal(f.gotListed, []string{"p2"}) {
		t.Errorf("a pending family beside a refused one: %+v, %v", a, err)
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
		{"nothing permitted", &fake{}, key("commercial", allScopes, "e1"), access.ErrNonePermitted},
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
	// The pending p2 lists no site, and its gated site is no coverage: the
	// parking part credits none.
	if !o.Partial || !slices.Equal(names(o.Families), []string{"e1", "pl"}) || o.Place.ID != "pl-1" {
		t.Errorf("partial %v, families %v", o.Partial, names(o.Families))
	}

	accepted := access.WithAccepted(context.Background(), []string{"p2-terms-1"})
	f = &fake{place: berlin}
	o, err = service(f, nil).Overview(accepted, key("consumer", allScopes, everything...), features.OverviewQuery{Needs: []features.Part{features.PartParking}})
	if err != nil || !slices.Equal(statuses(o), []features.Status{features.StatusNotRequested, features.StatusOK}) || o.Charging != nil || o.Partial {
		t.Errorf("needs parking: %v, %v, partial %v", statuses(o), err, o.Partial)
	}
}

func TestOverviewStatesAPendingFamilysConditionOnlyOnceAccepted(t *testing.T) {
	req.Covers(t, "FR-BRIEF-008", "SEC-030")
	notes := map[string][]provenance.Note{
		"p2": {{Kind: provenance.Condition, Text: "p2 condition"}, {Kind: provenance.Purpose, Text: "p2 purpose"}},
		"pl": {{Kind: provenance.Condition, Text: "pl condition"}},
	}
	ask := func(ctx context.Context) features.Overview {
		t.Helper()
		f := &fake{place: berlin, parks: []parking.Site{{ID: "a", Family: "p1"}, {ID: "b", Family: "p2", Occupancy: free(5, "p2")}}, notes: notes}
		o, err := service(f, nil).Overview(ctx, key("commercial", allScopes, everything...), features.OverviewQuery{PlaceID: "pl-1", Needs: []features.Part{features.PartParking}})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	// Pending: p2's site is not listed and p2 is not credited, so none of its
	// notes is stated; p1's site and the place's condition are kept.
	o := ask(context.Background())
	if got := provenance.Notes(o.Families, provenance.Condition); !slices.Equal(got, []string{"pl condition"}) {
		t.Errorf("pending: conditions %v", got)
	}
	if got := provenance.Notes(o.Families, provenance.Purpose); len(got) != 0 || !slices.Equal(names(o.Families), []string{"p1", "pl"}) {
		t.Errorf("pending: purposes %v, credited %v", got, names(o.Families))
	}
	if !slices.Equal(o.Parking.Withheld, []string{"p2"}) {
		t.Errorf("pending: withheld %v", o.Parking.Withheld)
	}
	// Accepted: the reading is shown, and its condition with it.
	o = ask(access.WithAccepted(context.Background(), []string{"p2-terms-1"}))
	if got := provenance.Notes(o.Families, provenance.Condition); !slices.Equal(got, []string{"p2 condition", "pl condition"}) {
		t.Errorf("accepted: conditions %v", got)
	}
}

func TestOverviewReadsTheRegisterOnce(t *testing.T) {
	req.Covers(t, "FR-BRIEF-002", "FR-BRIEF-008")
	f := &fake{place: berlin, sites: []energy.Site{site("e1")}, parks: []parking.Site{{Family: "p1"}}}
	if _, err := service(f, nil).Overview(context.Background(), key("commercial", allScopes, everything...), features.OverviewQuery{}); err != nil {
		t.Fatal(err)
	}
	if f.regCalls != 1 {
		t.Errorf("the register was read %d times for one overview", f.regCalls)
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
		{"provenance of the whole", &fake{place: berlin, provErr: errStore, provErrOnCall: 2}, k, errStore},
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

// TestAWrongTermsVersionIsRefusedWithTheCurrentTexts: a request that accepts
// terms which are not the current ones is refused, by parking and the
// overview alike, and the refusal carries the current texts; a failing
// read of the texts fails the answer (owner 2026-11-16).
func TestAWrongTermsVersionIsRefusedWithTheCurrentTexts(t *testing.T) {
	req.Covers(t, "FR-PARK-003", "SEC-030")
	stale := access.WithAccepted(context.Background(), []string{"p2-terms-0"})
	k := key("commercial", allScopes, everything...)
	check := func(name string, err error, f *fake) {
		t.Helper()
		var r *features.TermsRefusal
		if !errors.Is(err, access.ErrTermsVersion) || !errors.As(err, &r) || len(r.Texts) != 1 || r.Texts[0].Gate != "p2-terms-1" || err.Error() != access.ErrTermsVersion.Error() {
			t.Errorf("%s: %v", name, err)
		}
		if !slices.Equal(f.gotTerms, []string{"p2-terms-1"}) {
			t.Errorf("%s: texts read for %v", name, f.gotTerms)
		}
	}
	f := &fake{parks: []parking.Site{{ID: "b", Family: "p2", Occupancy: free(5, "p2")}}}
	_, err := service(f, nil).Parking(stale, k, parking.Query{})
	check("parking", err, f)
	f = &fake{place: berlin}
	_, err = service(f, nil).Overview(stale, k, features.OverviewQuery{PlaceID: "pl-1"})
	check("overview", err, f)

	boom := errors.New("terms unreadable")
	if _, err := service(&fake{termsErr: boom}, nil).Parking(stale, k, parking.Query{}); !errors.Is(err, boom) {
		t.Errorf("a failed read of the current texts: %v", err)
	}
	if _, err := service(&fake{termsErr: boom, parks: []parking.Site{{ID: "b", Family: "p2"}}}, nil).Parking(context.Background(), k, parking.Query{}); !errors.Is(err, boom) {
		t.Errorf("a failed read of the pending family's texts: %v", err)
	}
	// Elsewhere the parameter is ignored: charging answers whatever it says.
	if _, err := service(&fake{sites: []energy.Site{site("e1")}}, nil).Charging(stale, k, energy.Query{}); err != nil {
		t.Errorf("charging with a stale acceptance: %v", err)
	}
}

func TestChargingOnlyAvailable(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "ENV-003")
	report := func(fam string, s energy.State, at time.Time) energy.Site {
		return energy.Site{ID: "s-" + fam + string(s), Family: fam,
			Points: []energy.ChargePoint{{Availability: &energy.Availability{State: s, ObservedAt: at, Family: fam}}}}
	}
	sites := []energy.Site{report("e1", energy.StateAvailable, now), report("e1", energy.StateOccupied, now),
		report("e2", energy.StateAvailable, now.Add(-time.Minute)), site("e2"), report("e1", energy.StateAvailable, now)}
	q := energy.Query{Limit: 1, Filter: energy.Filter{OnlyAvailable: true, StatusMaxAge: time.Hour}}
	a, err := service(&fake{sites: sites}, nil).Charging(context.Background(), key("consumer", allScopes, everything...), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 1 || !a.HasMore || *a.Dropped != (energy.Dropped{NotFree: 1, Stale: 1, NoStatus: 1}) || !slices.Equal(names(a.Families), []string{"e1"}) {
		t.Errorf("kept %+v more %v dropped %+v credited %v", a.Items, a.HasMore, a.Dropped, names(a.Families))
	}
	a, err = service(&fake{sites: sites[1:2]}, nil).Charging(context.Background(), key("consumer", allScopes, everything...), q)
	if err != nil || len(a.Items) != 0 || a.HasMore || !slices.Equal(names(a.Families), []string{"e1"}) {
		t.Errorf("none free names the consulted: %+v %v", a, err)
	}
}

func TestStatusLimits(t *testing.T) {
	req.Covers(t, "FR-ENERGY-005")
	limit := features.StatusLimits(time.Hour, []provenance.Family{
		{Source: access.Source{Family: "short"}, StaleAfter: time.Minute}, {Source: access.Source{Family: "none"}, NeverStale: true}})
	if limit("short") != time.Minute || limit("none") != time.Hour || limit("unknown") != 0 {
		t.Errorf("limits %v %v %v", limit("short"), limit("none"), limit("unknown"))
	}
}
