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

// FuelSorts are the orders of a fuel answer.
var FuelSorts = []string{"distance", "price", "name"}

// FuelPrice is the newest price a station reported for one grade.
type FuelPrice struct {
	Product    string
	Price      float64
	ObservedAt time.Time
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
	// Open is the station's reported state: open, closed or unknown.
	Open   string
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
	Sort    string
	Limit   int
	// Window is how long a reported price is current.
	Window time.Duration
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
}

// RankFuel is the answer to q from the stations within its radius, nearest
// first, as the platform's filling-station search ranks them: the grade,
// brand and name filters, the open filter, then the order — by distance; by
// name, as German sorts it; or by price, every current price cheapest first, the last known
// ones after them, nearest first.
func RankFuel(stations []FuelStation, q FuelQuery, now time.Time) FuelAnswer {
	out := FuelAnswer{Stations: []FuelStation{}}
	brand, name := strings.ToLower(q.Brand), strings.ToLower(q.Name)
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
		if q.OpenNow && s.Open != "open" {
			if s.Open == "closed" {
				out.DroppedClosed++
			} else {
				out.DroppedUnknown++
			}
			continue
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

func contains(s *string, part string) bool {
	return s != nil && strings.Contains(strings.ToLower(*s), part)
}
