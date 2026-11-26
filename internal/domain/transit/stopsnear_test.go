package transit_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestPickStopsAsToday(t *testing.T) {
	at := geo.Point{Lat: 50.94, Lon: 6.96}
	far := geo.Point{Lat: 50.95, Lon: 6.96}
	rows := []transit.StopNear{
		{Root: "1:hbf", Name: "Köln Hbf", Modes: []string{"rail", "sbahn"}, Metres: 900, At: far},
		{Root: "1:dom", Name: "Dom/Hbf", Modes: []string{"tram"}, Metres: 100, At: at},
		{Root: "1:dom2", Name: "Dom/Hbf", Modes: []string{"bus"}, Metres: 120, At: at},
		{Root: "1:dom", Name: "Dom/Hbf", Modes: []string{"tram"}, Metres: 100, At: at},
		{Root: "1:markt", Name: "Markt", Modes: []string{"bus"}, Metres: 300, At: at},
		{Root: "1:markt2", Name: "Markt", Modes: []string{"bus"}, Metres: 2000, At: far},
		{Root: "1:aaa", Name: "Altstadt", Modes: []string{"bus"}, Metres: 300, At: at},
	}
	got := transit.PickStops(rows, 3)
	ids := func(ss []transit.NearStop) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.Root)
		}
		return out
	}
	// the station first, then the nearest; twins within 300 m merged, a far
	// namesake kept apart; a tie by German name order.
	if !slices.Equal(ids(got), []string{"1:hbf", "1:dom", "1:aaa"}) || !slices.Equal(got[1].Roots, []string{"1:dom", "1:dom2"}) ||
		!slices.Equal(got[1].Modes, []string{"tram", "bus"}) {
		t.Errorf("%+v", got)
	}
	if got := transit.PickStops(rows[1:], 2); !slices.Equal(ids(got), []string{"1:dom", "1:aaa"}) {
		t.Errorf("no station: %v", ids(got))
	}
	if got := transit.PickStops(nil, 3); len(got) != 0 {
		t.Errorf("none: %v", got)
	}
}

func TestLandOf(t *testing.T) {
	if transit.LandOf("05315") != "05" || transit.LandOf("5") != "5" {
		t.Error(transit.LandOf("05315"))
	}
}
