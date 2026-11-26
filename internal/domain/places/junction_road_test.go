package places

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// A node's road is every road whose LCL point's name agrees with it, else
// the nearest within 500 m, else none, as today's junctionRoad.
func TestAJunctionNodeLiesOnTheRoadItsNameAgreesWith(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, c := range []struct {
		name string
		near []LCLPoint
		want string
	}{
		{"Kreuz Nürnberg-Hafen", []LCLPoint{{Road: "A6", Name: s("Nürnberg-Süd"), DistanceM: 300}, {Road: "A73", Name: s("AK Nürnberg-Hafen (A73)"), DistanceM: 900},
			{Road: "A73", Name: s("Nürnberg-Hafen"), DistanceM: 950}, {Road: "A6", Name: nil, DistanceM: 990}}, "A73"},
		{"Kreuz Hilden", []LCLPoint{{Road: "A3", Name: s("Hilden"), DistanceM: 100}, {Road: "A46", Name: s("Kreuz Hilden"), DistanceM: 200}}, "A3/A46"},
		{"Steinhausen", []LCLPoint{{Road: "A94", Name: s("München-Ost"), DistanceM: 400}}, "A94"},
		{"Joachimsplatz", []LCLPoint{{Road: "A215", Name: s("Blumenthal"), DistanceM: 800}}, ""},
		{"Joachimsplatz", nil, ""},
		{"", []LCLPoint{{Road: "A1", Name: s("A1"), DistanceM: 600}}, ""},
		{"Hafen", []LCLPoint{{Road: "A1", Name: s(""), DistanceM: 600}}, ""},
	} {
		got := JunctionRoad(c.name, c.near)
		if (got == nil) != (c.want == "") || got != nil && *got != c.want {
			t.Errorf("%s: %v, want %q", c.name, got, c.want)
		}
	}
}

// Same-named nodes within the bound are one junction, the first kept; an
// unnamed node is never a twin.
func TestSameNamedJunctionNodesAreOne(t *testing.T) {
	at := func(lat float64) geo.Point { return geo.Point{Lat: lat, Lon: 11} }
	got := FoldSameNamed([]Junction{
		{ID: "a", Name: "Köln-Süd", Location: at(50)}, {ID: "b", Name: "Koeln Sued", Location: at(50.005)},
		{ID: "c", Name: "Köln-Süd", Location: at(50.1)}, {ID: "d", Location: at(50)}, {ID: "e", Location: at(50)},
	}, SameJunctionM)
	var ids string
	for _, j := range got {
		ids += j.ID
	}
	if ids != "acde" {
		t.Errorf("kept %s", ids)
	}
}
