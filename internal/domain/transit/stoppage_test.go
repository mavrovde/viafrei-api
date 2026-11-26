package transit_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestStationDHIDAndStopKey(t *testing.T) {
	if transit.StationDHID("de:05315:11201:1:4") != "de:05315:11201" || transit.StationDHID("de:02000:10950_G") != "de:02000:10950" ||
		transit.StationDHID("x:1") != "" || transit.StopKey("de:05315:11202") != "de-05315-11202" {
		t.Fatal("level or key")
	}
}

// The nearest station that lists the stop names its page: a station whose
// own DHIDs hold the stop leaves it out, a family is matched by any of its
// stop ids or a merged twin's, and the key is the listed family's.
func TestListedStopIsTheNearestStationListingIt(t *testing.T) {
	at := geo.Point{Lat: 53.55, Lon: 9.99}
	fam := func(root, name string, m float64) transit.StopNear {
		return transit.StopNear{Root: root, Name: name, Modes: []string{"bus"}, Metres: m, At: at}
	}
	own := transit.StationAround{Slug: "jungfernstieg", Own: []string{"de:02000:11003"},
		Near:  []transit.StopNear{fam("de:02000:11003:2:110032", "Rathausmarkt", 118), fam("de:02000:11950:1:119004", "Jungfernstieg", 28)},
		Stops: map[string][]string{"de:02000:11003:2:110032": {"de:02000:11003:2:110032"}, "de:02000:11950:1:119004": {"de:02000:11950:1:119004"}}}
	if l := transit.ListedStop([]transit.StationAround{own}, "de:02000:11003:2:110032"); l != nil {
		t.Errorf("an own stop is listed: %+v", l)
	}
	ownByStop := own
	ownByStop.Own = []string{"de:09162:1:2"}
	ownByStop.Stops = map[string][]string{"de:02000:11003:2:110032": {"de:09162:1:1"}}
	if l := transit.ListedStop([]transit.StationAround{ownByStop}, "de:02000:11003:2:110032"); l != nil {
		t.Errorf("a family with an own stop id is listed: %+v", l)
	}
	far := make([]transit.StopNear, 0, 9)
	stops := map[string][]string{}
	for i, r := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		far = append(far, fam("de:1:"+r, "S"+r, float64(10*i)))
		stops["de:1:"+r] = []string{"de:1:" + r + ":p"}
	}
	twin := fam("de:1:twin", "Sa", 5)
	stops["de:1:twin"] = []string{"de:1:twin:p"}
	next := transit.StationAround{Slug: "hbf", Near: append(far, twin), Stops: stops}
	for stop, want := range map[string]string{"de:1:b:p": "de-1-b", "de:1:a": "de-1-a", "de:1:twin:p": "de-1-a"} {
		if l := transit.ListedStop([]transit.StationAround{own, next}, stop); l == nil || *l != (transit.StopLink{Slug: "hbf", Key: want}) {
			t.Errorf("%s: %+v, want hbf %s", stop, l, want)
		}
	}
	if l := transit.ListedStop([]transit.StationAround{own, next}, "de:1:i:p"); l != nil {
		t.Errorf("the ninth stop is listed: %+v", l)
	}
	if l := transit.ListedStop(nil, "de:1:a"); l != nil {
		t.Errorf("no station: %+v", l)
	}
}
