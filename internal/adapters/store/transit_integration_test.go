//go:build integration

package store_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func stationIDs(ss []transit.Station) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func TestStationsByNameStartWithTheTextFirst(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "SEC-031")
	st := seeded(t)
	fams := families(t, st, "transit")
	var name, fold string
	owner(t, `select name, search_fold from contract.stop_place where withdrawn_at is null and location is not null order by stop_place_id limit 1`, &name, &fold)
	// The first four folded letters: a prefix of this station, and of others
	// that contain it later.
	text := fold[:4]
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
		if s.DistanceM != nil || s.Family == "" || len(s.Modes) == 0 {
			t.Errorf("%s: %+v", s.ID, s)
		}
	}
	if ss, more, err = st.Stations(bounded(t), transit.Query{Text: text, Limit: 1}, fams); err != nil || len(ss) != 1 || more != (want != ss[0].ID) {
		t.Errorf("limit 1: %v, more %v: %v", stationIDs(ss), more, err)
	}
	if ss, _, err = st.Stations(bounded(t), transit.Query{Text: text, Limit: 100}, nil); err != nil || len(ss) != 0 {
		t.Errorf("no family: %v %v", stationIDs(ss), err)
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
	both, _, err := st.Stations(bounded(t), transit.Query{Text: ss[0].Name, At: &at, RadiusM: 25000, Limit: 100}, fams)
	if err != nil || len(both) == 0 || both[0].ID != ss[0].ID {
		t.Errorf("name and position: %v %v", stationIDs(both), err)
	}
}
