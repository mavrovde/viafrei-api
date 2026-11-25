package transit

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// PlatformRow is one element of DB's station inventory (OpenStation):
// a stop place or one of its quays, as today's platform read takes it
// (PLATFORM_ACCESS_AT_EVAS).
type PlatformRow struct {
	ElementID     string
	StopPlace     bool
	StopPlaceID   string
	StationName   *string
	QuayName      *string
	PlateCode     *string
	ParentQuayID  *string
	HeightCm      *int
	HasAssessment bool
	StepFree      *string
	Wheelchair    *string
	Tactile       *string
	LevelBoarding *string
	UpdatedAt     *time.Time
	Family        string
}

// PlatformFacts are the four facts stated of a platform, each yes, partial,
// no or unknown (foldLimitation).
type PlatformFacts struct {
	StepFree, Wheelchair, TactileGuidance, LevelBoarding string
}

// HeightRange is the lowest and highest platform height published.
type HeightRange struct{ Min, Max int }

// Platform is one platform as today's answer states it: its name, the
// numbers of its track edges, its facts (nil when it carries no
// assessment) and its height range (nil when none is published).
type Platform struct {
	ID     string
	Name   *string
	Tracks []string
	Facts  *PlatformFacts
	Height *HeightRange
}

// StationPlatforms is one stop place's platforms, as platformAccessOf.
type StationPlatforms struct {
	StopPlaceID    string
	StationName    *string
	Station        *PlatformFacts
	Platforms      []Platform
	StepFreeCounts map[string]int
}

// Limitation folds an inventory word: true yes, false no, partial
// partial, any other word and none unknown.
func Limitation(raw *string) string {
	t := ""
	if raw != nil {
		t = strings.ToLower(strings.TrimSpace(*raw))
	}
	switch t {
	case "true":
		return "yes"
	case "false":
		return "no"
	case "partial":
		return "partial"
	}
	return "unknown"
}

func factsOf(r PlatformRow) *PlatformFacts {
	if !r.HasAssessment {
		return nil
	}
	return &PlatformFacts{Limitation(r.StepFree), Limitation(r.Wheelchair), Limitation(r.Tactile), Limitation(r.LevelBoarding)}
}

var firstNumber = regexp.MustCompile(`[0-9]+`)

// trackNumber is the leading number of a platform code; none sorts last.
func trackNumber(code string) float64 {
	m := firstNumber.FindString(code)
	if m == "" {
		return float64(1 << 53)
	}
	n, _ := strconv.Atoi(m)
	return float64(n)
}

// PlatformsOf are the platforms of the inventory's rows, one answer per stop
// place in the order the stop places first appear, as today's
// platformAccessOf: a quay with no known parent is a platform, its direct
// children with a number its track edges, every member's height its
// platform's.
func PlatformsOf(rows []PlatformRow) []StationPlatforms {
	german := collate.New(language.German)
	byTrack := func(a, b string) int {
		return cmp.Or(cmp.Compare(trackNumber(a), trackNumber(b)), german.CompareString(a, b))
	}
	var order []string
	byStop := map[string][]PlatformRow{}
	for _, r := range rows {
		if _, ok := byStop[r.StopPlaceID]; !ok {
			order = append(order, r.StopPlaceID)
		}
		byStop[r.StopPlaceID] = append(byStop[r.StopPlaceID], r)
	}
	out := make([]StationPlatforms, 0, len(order))
	for _, id := range order {
		mine := byStop[id]
		quays := map[string]PlatformRow{}
		var stop *PlatformRow
		for i, r := range mine {
			if r.StopPlace {
				if stop == nil {
					stop = &mine[i]
				}
				continue
			}
			quays[r.ElementID] = r
		}
		top := func(q PlatformRow) PlatformRow {
			for range 8 {
				up, ok := PlatformRow{}, false
				if q.ParentQuayID != nil {
					up, ok = quays[*q.ParentQuayID]
				}
				if !ok {
					break
				}
				q = up
			}
			return q
		}
		var tops []string
		members := map[string][]PlatformRow{}
		for _, r := range mine {
			if r.StopPlace {
				continue
			}
			t := top(r)
			if _, ok := members[t.ElementID]; !ok {
				tops = append(tops, t.ElementID)
			}
			members[t.ElementID] = append(members[t.ElementID], r)
		}
		sp := StationPlatforms{StopPlaceID: id, StationName: mine[0].StationName, StepFreeCounts: map[string]int{"yes": 0, "partial": 0, "no": 0, "unknown": 0}}
		if stop != nil {
			sp.Station = factsOf(*stop)
			if stop.StationName != nil {
				sp.StationName = stop.StationName
			}
		}
		for _, tid := range tops {
			t := quays[tid]
			var codes []string
			lo, hi, heights := 0, 0, false
			for _, m := range members[tid] {
				if m.ParentQuayID != nil && *m.ParentQuayID == tid && m.PlateCode != nil && !slices.Contains(codes, *m.PlateCode) {
					codes = append(codes, *m.PlateCode)
				}
				if h := m.HeightCm; h != nil {
					if !heights || *h < lo {
						lo = *h
					}
					if !heights || *h > hi {
						hi = *h
					}
					heights = true
				}
			}
			if len(codes) == 0 && t.PlateCode != nil {
				codes = []string{*t.PlateCode}
			}
			slices.SortStableFunc(codes, byTrack)
			p := Platform{ID: tid, Name: t.QuayName, Tracks: codes, Facts: factsOf(t)}
			if codes == nil {
				p.Tracks = []string{}
			}
			if heights {
				p.Height = &HeightRange{lo, hi}
			}
			sp.Platforms = append(sp.Platforms, p)
		}
		slices.SortStableFunc(sp.Platforms, func(a, b Platform) int {
			first := func(p Platform) string {
				if len(p.Tracks) == 0 {
					return ""
				}
				return p.Tracks[0]
			}
			name := func(p Platform) string {
				if p.Name == nil {
					return ""
				}
				return *p.Name
			}
			return cmp.Or(cmp.Compare(trackNumber(first(a)), trackNumber(first(b))), german.CompareString(name(a), name(b)))
		})
		for _, p := range sp.Platforms {
			f := "unknown"
			if p.Facts != nil {
				f = p.Facts.StepFree
			}
			sp.StepFreeCounts[f]++
		}
		out = append(out, sp)
	}
	return out
}

var (
	unitPlatform = regexp.MustCompile(`(?i)\bGleis(?:e|en)?\s+(\d{1,3}[a-z]?(?:\s*(?:/|-|,|und|u\.)\s*\d{1,3}[a-z]?)*)`)
	unitAnd      = regexp.MustCompile(`(?i)\s*(?:und|u\.)\s*`)
	unitRange    = regexp.MustCompile(`^(\d{1,3})-(\d{1,3})$`)
	unitTrack    = regexp.MustCompile(`(?i)^\d{1,3}[a-z]?$`)
	spaces       = regexp.MustCompile(`\s+`)
)

// UnitTracks are the tracks a unit's German description names, one each,
// as today's unitTracksOf ("zu Gleis 4/5" is 4 and 5, "Gleis 1-3" 1, 2 and
// 3); none when it names no platform.
func UnitTracks(description *string) []string {
	if description == nil {
		return nil
	}
	m := unitPlatform.FindStringSubmatch(*description)
	if m == nil {
		return nil
	}
	var out []string
	add := func(t string) {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	for _, part := range strings.FieldsFunc(spaces.ReplaceAllString(unitAnd.ReplaceAllString(m[1], "/"), ""), func(r rune) bool { return r == '/' || r == ',' }) {
		if r := unitRange.FindStringSubmatch(part); r != nil {
			a, _ := strconv.Atoi(r[1])
			b, _ := strconv.Atoi(r[2])
			if b >= a && b-a <= 12 {
				for t := a; t <= b; t++ {
					add(strconv.Itoa(t))
				}
			}
			continue
		}
		if unitTrack.MatchString(part) {
			add(strings.ToLower(part))
		}
	}
	return out
}

// StepFreePlatform is a platform of the step-free list and the state of
// the lifts serving it: out, ok, unknown or none.
type StepFreePlatform struct {
	Tracks []string
	Name   *string
	Lift   string
}

// PlatformLift is the state of the lifts serving the tracks, as today's
// platformLiftOf: none when no elevator names one of them, out when one is
// out of service, ok when every one works, else unknown.
func PlatformLift(units []Facility, tracks []string) string {
	var lifts []Facility
	for _, u := range units {
		if u.Kind == "elevator" && slices.ContainsFunc(UnitTracks(u.Position), func(t string) bool {
			return slices.ContainsFunc(tracks, func(x string) bool { return strings.ToLower(x) == t })
		}) {
			lifts = append(lifts, u)
		}
	}
	switch {
	case len(lifts) == 0:
		return "none"
	case slices.ContainsFunc(lifts, func(u Facility) bool { return u.State == "inactive" }):
		return "out"
	case !slices.ContainsFunc(lifts, func(u Facility) bool { return u.State != "active" }):
		return "ok"
	}
	return "unknown"
}

// StepFreeNow is today's step-free-now rule (stepFreePlatformsNow): the
// platforms the inventory calls step-free, those whose lift is out of
// service now blocked; an unknown lift keeps its platform on the list.
func StepFreeNow(stops []StationPlatforms, units []Facility) (ok, blocked []StepFreePlatform) {
	ok, blocked = []StepFreePlatform{}, []StepFreePlatform{}
	for _, s := range stops {
		for _, p := range s.Platforms {
			if p.Facts == nil || p.Facts.StepFree != "yes" {
				continue
			}
			item := StepFreePlatform{Tracks: p.Tracks, Name: p.Name, Lift: PlatformLift(units, p.Tracks)}
			if item.Lift == "out" {
				blocked = append(blocked, item)
			} else {
				ok = append(ok, item)
			}
		}
	}
	return ok, blocked
}
