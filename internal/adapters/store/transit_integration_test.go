//go:build integration

package store_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func stationIDs(ss []transit.Station) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

// noFold is the ask that blocks a search by a name with an umlaut, a space
// or a hyphen.
const noFold = "contract.stop_place.search_fold is lower(name) in 0312, not the contract's fold (umlauts spelled out, ß as ss, diacritics dropped, only letters and digits): a station whose name has an umlaut, a space or a hyphen is not found by its name"

func TestStationsByNameStartWithTheTextFirst(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "SEC-031")
	st := seeded(t)
	fams := families(t, st, "transit")
	var name, fold string
	owner(t, `select name, search_fold from contract.stop_place where withdrawn_at is null and location is not null order by stop_place_id limit 1`, &name, &fold)
	// The first folded letters, up to four and before any letter the read
	// contract does not fold (search_fold is lower(name), see below): a
	// prefix of this station, and of others that contain it later.
	text := fold[:strings.IndexFunc(fold+"\x00", func(r rune) bool { return r < 'a' || r > 'z' })]
	text = text[:min(len(text), 4)]
	if len(text) < 2 {
		t.Fatalf("the fixture's first station %q starts with no letters to search", name)
	}
	ss, more, err := st.Stations(bounded(t), transit.Query{Text: strings.ToUpper(text), Limit: 100}, fams)
	if err != nil || more {
		t.Fatalf("%d stations, more %v: %v", len(ss), more, err)
	}
	var want string
	owner(t, fmt.Sprintf(`select coalesce(string_agg(stop_place_id, ',' order by (search_fold not like '%[1]s%%'), name, stop_place_id), '')
from contract.stop_place where withdrawn_at is null and location is not null and source_ref in (%[2]s) and search_fold like '%%%[1]s%%'`,
		text, quoted(fams)), &want)
	if got := strings.Join(stationIDs(ss), ","); got != want || !slices.ContainsFunc(ss, func(s transit.Station) bool { return s.Name == name }) {
		t.Fatalf("stations for %q: %s, want %s (with %s)", text, got, want, name)
	}
	for _, s := range ss {
		if s.DistanceM != nil || s.Family == "" {
			t.Errorf("%s: %+v", s.ID, s)
		}
		// A station placed in a district names the family that placed it.
		if (s.RegionCode != nil) != (s.PlacedBy != nil && *s.PlacedBy == "bkg_gvisys") {
			t.Errorf("%s: district %v, placed by %v", s.ID, s.RegionCode, s.PlacedBy)
		}
	}
	if one, more, err := st.Stations(bounded(t), transit.Query{Text: text, Limit: 1}, fams); err != nil || len(one) != 1 || more != (want != one[0].ID) {
		t.Errorf("limit 1: %v, more %v: %v", stationIDs(one), more, err)
	}
	if none, _, err := st.Stations(bounded(t), transit.Query{Text: text, Limit: 100}, nil); err != nil || len(none) != 0 {
		t.Errorf("no family: %v %v", stationIDs(none), err)
	}
	if fold != transit.Fold(name) {
		dbAsk(t, noFold)
	}
	for _, s := range ss {
		if len(s.Modes) == 0 {
			dbAsk(t, "contract.stop_place.modes is empty: 0312 fills no mode of a stop, so no station answers its modes")
		}
	}
}

func TestStationsNearAPositionAreNearestFirst(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	st := seeded(t)
	fams := families(t, st, "transit")
	var at geo.Point
	owner(t, `select st_y(location), st_x(location) from contract.stop_place where withdrawn_at is null and location is not null order by stop_place_id limit 1`, &at.Lat, &at.Lon)
	ss, _, err := st.Stations(bounded(t), transit.Query{At: &at, RadiusM: 25000, Limit: 100}, fams)
	if err != nil || len(ss) == 0 || *ss[0].DistanceM != 0 {
		t.Fatalf("near %v: %v %v", at, stationIDs(ss), err)
	}
	for i := 1; i < len(ss); i++ {
		if *ss[i].DistanceM < *ss[i-1].DistanceM || *ss[i].DistanceM > 25000 {
			t.Errorf("%s at %d m after %d m", ss[i].ID, *ss[i].DistanceM, *ss[i-1].DistanceM)
		}
	}
	// A name and a position: the name search within the radius.
	var fold string
	owner(t, `select search_fold from contract.stop_place where stop_place_id = '`+ss[0].ID+`'`, &fold)
	if fold != transit.Fold(ss[0].Name) {
		dbAsk(t, noFold)
	}
	both, _, err := st.Stations(bounded(t), transit.Query{Text: ss[0].Name, At: &at, RadiusM: 25000, Limit: 100}, fams)
	if err != nil || len(both) == 0 || both[0].ID != ss[0].ID {
		t.Errorf("name and position: %v %v", stationIDs(both), err)
	}
}

// A DB station is found by its EVA number and states every number of its
// parts, the main one first; a stop that is no DB station states none (G2).
func TestStationsStateTheirEVANumbers(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	st := seeded(t)
	fams := families(t, st, "transit")
	main := fixtures.TrainBoardEVA("01")
	ss, _, err := st.Stations(bounded(t), transit.Query{Code: main, Limit: 5}, fams)
	if err != nil || len(ss) != 1 || ss[0].EVA == nil || *ss[0].EVA != main || !slices.Equal(ss[0].EVANumbers, []string{main, "8001002", "8001003"}) {
		t.Fatalf("by EVA %+v, %v", ss, err)
	}
	all, _, err := st.Stations(bounded(t), transit.Query{Text: "a", Limit: 100}, fams)
	if err != nil {
		t.Fatal(err)
	}
	// The disruption test plants a DB station of its own in area 09999
	// (insert-only seed); every fixture stop but the board's states none.
	for _, s := range all {
		if s.ID != ss[0].ID && !strings.HasPrefix(s.ID, "1:de:09999:") && (s.EVA != nil || len(s.EVANumbers) != 0) {
			t.Errorf("%s states numbers %v %v", s.ID, s.EVA, s.EVANumbers)
		}
	}
}

// The eva lookup a live source's key rule names resolves a
// station's name to its EVA number: the exact folded name first, else the
// shortest it starts; a name of none, a blank one and an unknown lookup
// resolve nothing.
func TestLookupResolvesAStationNameToItsEVA(t *testing.T) {
	st := seeded(t)
	fams := families(t, st, "transit")
	main := fixtures.TrainBoardEVA("01")
	ss, _, err := st.Stations(bounded(t), transit.Query{Code: main, Limit: 1}, fams)
	if err != nil || len(ss) != 1 {
		t.Fatalf("the board's station: %+v %v", ss, err)
	}
	for _, name := range []string{ss[0].Name, strings.ToUpper(ss[0].Name), ss[0].Name[:len(ss[0].Name)-1]} {
		if eva, err := st.Lookup(bounded(t), store.LookupStationEVA, name); err != nil || eva != main {
			t.Errorf("%q: %q %v", name, eva, err)
		}
	}
	for _, name := range []string{"No such station anywhere", " ", ""} {
		if eva, err := st.Lookup(bounded(t), store.LookupStationEVA, name); err != nil || eva != "" {
			t.Errorf("%q: %q %v", name, eva, err)
		}
	}
	if _, err := st.Lookup(bounded(t), "postcode_ags", "Berlin"); err == nil {
		t.Error("an unknown lookup answered")
	}
}

// The ags lookup resolves a position, "lat,lon", to the AGS of its Gemeinde:
// the nearest BKG municipality within the municipality reach. A position
// out of every Gemeinde's reach, a malformed one and one out of range
// resolve nothing. It is how a fuel subject's point is stored: as its AGS.
func TestLookupResolvesAPositionToItsGemeinde(t *testing.T) {
	st := seeded(t)
	var key string
	var lat, lon float64
	owner(t, `select official_key, st_y(location), st_x(location) from contract.place
		where source_ref = 'bkg_gvisys' and place_kind = 'municipality' and withdrawn_at is null and official_key ~ '^[0-9]{8}$'
		order by official_key limit 1`, &key, &lat, &lon)
	at := strconv.FormatFloat(lat, 'f', 6, 64) + "," + strconv.FormatFloat(lon, 'f', 6, 64)
	if ags, err := st.Lookup(bounded(t), store.LookupAGS, at); err != nil || ags != key {
		t.Fatalf("%s: %q %v, want %s", at, ags, err, key)
	}
	for _, v := range []string{"0,0", "48.1", "x,y", "91,10", "48,181", ""} {
		if ags, err := st.Lookup(bounded(t), store.LookupAGS, v); err != nil || ags != "" {
			t.Errorf("%q: %q %v", v, ags, err)
		}
	}
}

// The anchor lookup resolves a motorway page's anchor, "lat,lon", to the
// BASt LCL location code of the motorway point it lies on (lead ruling 1 of
// 2026-11-17): the nearest LCL junction within 15 m; a position farther from
// every one, a malformed one and one out of range resolve nothing.
func TestLookupResolvesAnAnchorToItsLCLCode(t *testing.T) {
	st := seeded(t)
	var id string
	var lat, lon float64
	owner(t, `select junction_id, st_y(location), st_x(location) from contract.junction
		where source_ref = 'bast_lcl' and location is not null order by junction_id limit 1`, &id, &lat, &lon)
	code := id[strings.LastIndex(id, ":")+1:]
	at := strconv.FormatFloat(lat, 'f', 4, 64) + "," + strconv.FormatFloat(lon, 'f', 4, 64)
	if got, err := st.Lookup(bounded(t), store.LookupAnchor, at); err != nil || got != code {
		t.Fatalf("%s: %q %v, want %s", at, got, err, code)
	}
	far := strconv.FormatFloat(lat+0.001, 'f', 4, 64) + "," + strconv.FormatFloat(lon, 'f', 4, 64)
	for _, v := range []string{far, "0,0", "48.1", "x,y", "91,10", ""} {
		if got, err := st.Lookup(bounded(t), store.LookupAnchor, v); err != nil || got != "" {
			t.Errorf("%q: %q %v", v, got, err)
		}
	}
}
