package transit

import (
	"cmp"
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The stops around a position, as today's departures around a place pick
// them (stops-near): each mode's nearest stop families within the radius,
// StopsPerMode of each, in StopModes' order.
const (
	StopsRadiusM = 5_000
	StopsPerMode = 8
	// SameStopM is how close two same-named families must lie to be one
	// stop (the EVA pairing radius).
	SameStopM = 300
)

// StopModes are the modes the stops around a position are found for, in
// listing order (STATION_MODES).
var StopModes = []string{"rail", "sbahn", "subway", "tram", "bus"}

// StopNear is one stop family a mode's probe found: its root stop
// (source:id), name, modes, distance and point, and its source family.
type StopNear struct {
	Root   string
	Name   string
	Modes  []string
	Metres float64
	At     geo.Point
	Family string
}

// NearStop is one stop listed around a position: a stop family, or two
// same-named families within SameStopM merged into the nearer (DELFI files
// some stops once per side of the street).
type NearStop struct {
	StopNear
	Roots []string
}

// StopBoardsQuery asks for the stops around At with their next departures:
// at most Limit stops within RadiusM, PerStop departures each, planned in
// the window from From of Window, of Modes (every mode when empty).
type StopBoardsQuery struct {
	At      geo.Point
	RadiusM int
	Limit   int
	PerStop int
	From    time.Time
	Window  time.Duration
	Modes   []Mode
}

// StopBoard is one listed stop with its departures as its board shows them.
type StopBoard struct {
	Stop  NearStop
	Board BoardAnswer
}

// PickStops are the stops listed around a position, as today's pickFamilies:
// one row per family, same-named twins within SameStopM merged into the
// nearer, by distance (then name, German order), at most limit, with the
// nearest family serving rail or S-Bahn first wherever it lies.
func PickStops(rows []StopNear, limit int) []NearStop {
	var fams []NearStop
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.Root] {
			seen[r.Root] = true
			fams = append(fams, NearStop{StopNear: r, Roots: []string{r.Root}})
		}
	}
	slices.SortStableFunc(fams, func(a, b NearStop) int {
		return cmp.Or(cmp.Compare(a.Metres, b.Metres), cmp.Compare(a.Root, b.Root))
	})
	var kept []NearStop
	for _, f := range fams {
		i := slices.IndexFunc(kept, func(k NearStop) bool {
			return k.Name == f.Name && geo.MetresBetween(k.At, f.At) <= SameStopM
		})
		if i < 0 {
			kept = append(kept, f)
			continue
		}
		k := &kept[i]
		k.Roots = append(k.Roots, f.Root)
		for _, m := range f.Modes {
			if !slices.Contains(k.Modes, m) {
				k.Modes = append(slices.Clone(k.Modes), m)
			}
		}
	}
	german := collate.New(language.German)
	slices.SortStableFunc(kept, func(a, b NearStop) int {
		return cmp.Or(cmp.Compare(a.Metres, b.Metres), german.CompareString(a.Name, b.Name))
	})
	s := slices.IndexFunc(kept, func(f NearStop) bool {
		return slices.Contains(f.Modes, "rail") || slices.Contains(f.Modes, "sbahn")
	})
	if s < 0 {
		return kept[:min(limit, len(kept))]
	}
	rest := slices.Delete(slices.Clone(kept), s, s+1)
	return append([]NearStop{kept[s]}, rest[:min(limit-1, len(rest))]...)
}
