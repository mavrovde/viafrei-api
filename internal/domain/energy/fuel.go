package energy

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The fuel domain (FR-ENERGY): filling stations and their prices.

// Fuels are the grades a fuel answer asks for.
var Fuels = []string{"e5", "e10", "diesel"}

// MaxFuelScan bounds the stations a fuel search reads, nearest first,
// before its filters: the platform's scan.
const MaxFuelScan = 200

// MaxFuelRadiusM is the provider's ceiling on a fuel search's radius: the
// circle in which a coverage read looks for the nearest priced station.
const MaxFuelRadiusM = 25000

// The coverage states of a fuel area, as the platform's fuel surfaces name
// them: no station held within the radius and none priced within the
// ceiling; none held within the radius but one priced within the ceiling;
// a station held within the radius.
const (
	CoverageUnknown      = "unknown"
	CoveragePricedBeyond = "priced_beyond"
	CoverageHeld         = "held"
)

// FuelCoverage is what we hold around a fuel search's position, whatever its
// filters but the grade: the stations within the radius, priced or not; the
// metres to the nearest station with a price for the grade (any grade when
// none was asked) within MaxFuelRadiusM, nil for none; and the stations
// within the radius with no price for the grade reported within the fresh
// window (zero when no grade was asked).
type FuelCoverage struct {
	StationsInRadius int
	NearestPricedM   *float64
	WithoutCurrent   int
}

// State is the coverage state, the platform's one classification: held
// when a station is held within the radius, else priced_beyond when one is
// priced within the ceiling, else unknown.
func (c FuelCoverage) State() string {
	switch {
	case c.StationsInRadius > 0:
		return CoverageHeld
	case c.NearestPricedM != nil:
		return CoveragePricedBeyond
	}
	return CoverageUnknown
}

// FuelSorts are the orders of a fuel answer.
var FuelSorts = []string{"distance", "price", "name"}

// FuelPrice is the newest price a station reported for one grade.
type FuelPrice struct {
	Product    string
	Price      float64
	ObservedAt time.Time
	// Open is the open state the station reported with the price; nil when
	// it stated none.
	Open *bool
}

// Current says whether the price is current at now, under a window: an
// older one is the last known price, listed but never ranked.
func (p FuelPrice) Current(now time.Time, window time.Duration) bool {
	return p.ObservedAt.After(now.Add(-window))
}

// FuelStation is a filling station with its newest price per grade.
type FuelStation struct {
	ID        string
	Name      *string
	Brand     *string
	Location  geo.Point
	DistanceM int
	// Open is the station's reported state: open, closed or unknown. The
	// answer states the published hours' state instead (OpenStateAt), as the
	// platform does; the reported one is kept for the reader that has no hours.
	Open string
	// OpenObservedAt is when the station reported Open: the newest price
	// observation that stated it; nil when none did.
	OpenObservedAt *time.Time
	// Hours are the published opening hours.
	Hours  Hours
	Prices []FuelPrice
	Family string
}

// Price is the station's price for a grade, or nil.
func (s FuelStation) Price(product string) *FuelPrice {
	for i := range s.Prices {
		if s.Prices[i].Product == product {
			return &s.Prices[i]
		}
	}
	return nil
}

// OpenStateAt is open, closed or unknown at at, from the published hours,
// as the platform's filling-station search states it: open when the hours
// have the station open; closed when they were read and have it shut;
// unknown otherwise (no hours, or hours that could not be read).
func (s FuelStation) OpenStateAt(at time.Time) string {
	sch, read := ReadSchedule(s.Hours)
	switch open, known := sch.OpenAt(at); {
	case open:
		return "open"
	case read && known:
		return "closed"
	}
	return "unknown"
}

// Label is how a person reads the station: its name, else its brand, else
// its id.
func (s FuelStation) Label() string {
	switch {
	case s.Name != nil && *s.Name != "":
		return *s.Name
	case s.Brand != nil && *s.Brand != "":
		return *s.Brand
	}
	return s.ID
}

// FuelQuery asks for the filling stations within RadiusM of At: of a grade
// (empty: any), a brand and a name (empty: any; a part of it, any case),
// open now only, in an order, at most Limit.
type FuelQuery struct {
	At      geo.Point
	RadiusM int
	Fuel    string
	Brand   string
	Name    string
	OpenNow bool
	// OpenAt keeps the stations the published hours have open at that
	// Berlin time (ResolveOpenAt); WholeDay those open around the clock.
	OpenAt   string
	WholeDay bool
	Sort     string
	Limit    int
	// Window is how long a reported price is current.
	Window time.Duration
	// Coverage asks for the coverage read (FuelCoverage) where the
	// platform reads it: on every answer ordered by price (its cheapest-fuel
	// search), else on an empty one (its filling-station search). Its
	// stations without a current price are counted over Fresh, the price
	// cache window.
	Coverage bool
	Fresh    time.Duration
}

// FuelAnswer is the stations shown and how many the open filter left out.
type FuelAnswer struct {
	Stations []FuelStation
	// Matched is how many stations the filters kept, before the limit.
	Matched int
	// DroppedClosed and DroppedUnknown count the stations the open filter
	// left out: reported closed, and with no reported state.
	DroppedClosed  int
	DroppedUnknown int
	// DroppedNotWholeDay counts the stations WholeDay left out.
	DroppedNotWholeDay int
	// OpenAt is the instant OpenAt named, when asked.
	OpenAt *time.Time
}

// RankFuel is the answer to q from the stations within its radius, nearest
// first, as the platform's filling-station search ranks them: the grade,
// brand and name filters, the open filter, then the order — by distance; by
// name, as German sorts it; or by price, every current price cheapest first, the last known
// ones after them, nearest first.
func RankFuel(stations []FuelStation, q FuelQuery, now time.Time) FuelAnswer {
	out := FuelAnswer{Stations: []FuelStation{}}
	brand, name := strings.ToLower(q.Brand), strings.ToLower(q.Name)
	if q.OpenAt != "" {
		if at, ok := ResolveOpenAt(q.OpenAt, now); ok {
			out.OpenAt = &at
		}
	}
	for _, s := range stations {
		if q.Fuel != "" && s.Price(q.Fuel) == nil {
			continue
		}
		if brand != "" && (s.Brand == nil || !strings.Contains(strings.ToLower(*s.Brand), brand)) {
			continue
		}
		if name != "" && !contains(s.Name, name) && !contains(s.Brand, name) {
			continue
		}
		if q.WholeDay && (s.Hours.WholeDay == nil || !*s.Hours.WholeDay) {
			out.DroppedNotWholeDay++
			continue
		}
		// open_now and open_at are one filter, as on the platform: the
		// published hours at the instant asked (open_at, else now).
		if q.OpenNow || out.OpenAt != nil {
			switch s.OpenStateAt(openAt(out.OpenAt, now)) {
			case "open":
			case "closed":
				out.DroppedClosed++
				continue
			default:
				out.DroppedUnknown++
				continue
			}
		}
		out.Stations = append(out.Stations, s)
	}
	slices.SortStableFunc(out.Stations, func(a, b FuelStation) int {
		switch q.Sort {
		case "name":
			return collate.New(language.German).CompareString(a.Label(), b.Label())
		case "price":
			pa, pb := a.Price(q.Fuel), b.Price(q.Fuel)
			ca, cb := pa != nil && pa.Current(now, q.Window), pb != nil && pb.Current(now, q.Window)
			if ca != cb {
				if ca {
					return -1
				}
				return 1
			}
			if ca {
				if c := cmp.Compare(pa.Price, pb.Price); c != 0 {
					return c
				}
			}
		}
		return cmp.Compare(a.DistanceM, b.DistanceM)
	})
	out.Matched = len(out.Stations)
	if len(out.Stations) > q.Limit {
		out.Stations = out.Stations[:q.Limit]
	}
	return out
}

// openAt is the instant an answer states the open state at: the one open_at
// named, else now.
func openAt(named *time.Time, now time.Time) time.Time {
	if named != nil {
		return *named
	}
	return now
}

func contains(s *string, part string) bool {
	return s != nil && strings.Contains(strings.ToLower(*s), part)
}

// Reported is the open state the newest price observation stated, open or
// closed, and its time, as today's cheapest-fuel line states it ("open when
// last seen, as of"); unknown and nil when no observation stated one.
func Reported(prices []FuelPrice) (string, *time.Time) {
	var at *time.Time
	state := "unknown"
	for i := range prices {
		p := &prices[i]
		if p.Open == nil || (at != nil && !p.ObservedAt.After(*at)) {
			continue
		}
		at, state = &p.ObservedAt, "closed"
		if *p.Open {
			state = "open"
		}
	}
	return state, at
}
