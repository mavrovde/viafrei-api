package places

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Needs are the everyday needs of the platform's place cards, each answered
// by the places of interest of its kinds (category=value).
var Needs = []string{"groceries", "health", "money", "food", "lodging", "garage", "police", "toilets"}

// NeedKinds are the kinds that answer each need, as the platform's.
var NeedKinds = map[string][]string{
	"groceries": {"shop=supermarket", "building=supermarket", "shop=convenience", "shop=bakery", "shop=butcher", "shop=kiosk", "shop=chemist"},
	"health": {"amenity=pharmacy", "healthcare=pharmacy", "amenity=doctors", "healthcare=doctor", "amenity=dentist", "healthcare=dentist",
		"amenity=hospital", "healthcare=hospital", "building=hospital", "amenity=clinic", "healthcare=clinic"},
	"money":   {"amenity=atm", "amenity=bank", "amenity=post_office"},
	"food":    {"amenity=restaurant", "amenity=cafe", "amenity=fast_food"},
	"lodging": {"tourism=hotel", "tourism=guest_house", "tourism=motel", "tourism=hostel"},
	"garage":  {"shop=car_repair", "shop=tyres", "amenity=car_wash"},
	"police":  {"amenity=police"},
	"toilets": {"amenity=toilets"},
}

// needBaseM is each need's first radius; NeedMaxM the widest; a need is
// widened until NeedEnough places are inside, NeedRows at most are shown.
var needBaseM = map[string]int{"groceries": 1000, "health": 2000, "money": 1000, "food": 1000, "lodging": 2000, "garage": 2000, "police": 2000, "toilets": 2000}

// The widening of a need, as the platform's.
const (
	NeedMaxM   = 5000
	NeedEnough = 3
	NeedRows   = 5
)

// NeedPlace is a place of interest answering a need.
type NeedPlace struct {
	ID          string
	Name        string
	Kind        string
	Street      *string
	HouseNumber *string
	Location    geo.Point
	DistanceM   int
	Family      string
}

// NeedPick is a need's answer: the radius it was read in, whether that is
// wider than its first one, and the places inside, nearest first.
type NeedPick struct {
	Need    string
	RadiusM int
	Widened bool
	Places  []NeedPlace
}

// PickNeed picks a need's places from those read within NeedMaxM, as the
// platform's place cards: the first radius when NeedEnough are inside it,
// else the next whole kilometre that holds the NeedEnough-th, at most
// NeedMaxM; nearest first, then by name, at most NeedRows.
func PickNeed(need string, found []NeedPlace) NeedPick {
	found = slices.Clone(found)
	slices.SortStableFunc(found, func(a, b NeedPlace) int {
		return cmp.Or(cmp.Compare(a.DistanceM, b.DistanceM), strings.Compare(a.Name, b.Name))
	})
	base, radius := needBaseM[need], NeedMaxM
	inBase := 0
	for _, p := range found {
		if p.DistanceM <= base {
			inBase++
		}
	}
	switch {
	case inBase >= NeedEnough:
		radius = base
	case len(found) >= NeedEnough:
		radius = min(NeedMaxM, max(base, (found[NeedEnough-1].DistanceM+999)/1000*1000))
	}
	pick := NeedPick{Need: need, RadiusM: radius, Widened: radius > base, Places: []NeedPlace{}}
	for _, p := range found {
		if p.DistanceM <= radius && len(pick.Places) < NeedRows {
			pick.Places = append(pick.Places, p)
		}
	}
	return pick
}

// restArea is the platform's rest-area name rule, on a name folded by
// NameFold: a services area or a car park is no junction.
var restArea = regexp.MustCompile(`^(tank und )?(rastplatz|rastplatze|rastanlage|raststatte|rasthof|rasthaus|parkplatz|pwc|autohof|tankstelle|servicebereich|serviceplatz)( |$)`)

// IsRestAreaName reports whether a name names a services area or a car park
// rather than a junction.
func IsRestAreaName(name string) bool { return restArea.MatchString(NameFold(name)) }

// Junction is a motorway junction near a position.
type Junction struct {
	ID        string
	Road      *string
	Number    *string
	Name      string
	Location  geo.Point
	DistanceM int
	Family    string
	// RoadBy is the family the road was read from when it is not the
	// row's own (the LCL's, for an OpenStreetMap node); "" else.
	RoadBy string
}

// NearbyQuery asks what is within RadiusM of At: the junctions, at most
// Limit, and the places of the needs Needs.
type NearbyQuery struct {
	At      geo.Point
	RadiusM int
	Needs   []string
	Limit   int
}
