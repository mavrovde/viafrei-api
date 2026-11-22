package features

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// Nearby is the what-is-around port: the junctions and the everyday needs.
type Nearby interface {
	// Junctions returns the junctions within q's radius, nearest first, rest
	// areas left out, at most q.Limit.
	Junctions(ctx context.Context, q places.NearbyQuery, families []string) ([]places.Junction, error)
	// NeedPlaces returns, per need of q, the nearest places.NeedRows places
	// of its kinds within places.NeedMaxM.
	NeedPlaces(ctx context.Context, q places.NearbyQuery, families []string) (map[string][]places.NeedPlace, error)
}

// The vehicle categories of the what-is-around answer, as the platform's:
// what an omitted category list answers.
var VehicleCategories = []string{"fuel", "charging", "parking", "station", "junction"}

// NearbyPerCategory is how many items each vehicle category shows.
const NearbyPerCategory = 3

// NearbyQuery asks what is within RadiusM of At, for Categories (vehicle
// categories and needs); FuelWindow is how old a price may be and still be
// current.
type NearbyQuery struct {
	At         places.NearbyQuery
	Categories []string
	FuelWindow time.Duration
}

// NearbyAnswer is the what-is-around answer: each asked category's block,
// nil when not asked or not ok, and each category's outcome.
type NearbyAnswer struct {
	Fuel      *FuelStations
	Charging  *Answer[energy.Site]
	Parking   *Answer[parking.Site]
	Stations  *Answer[transit.Station]
	Junctions *Answer[places.Junction]
	Needs     map[string]places.NeedPick
	Parts     []PartResult
	Meta      Answer[struct{}]
}

// nearbyScope is the scope each category needs; a need needs places. The
// junctions are the motorway location table's, a roads source.
var nearbyScope = map[string]access.Scope{"fuel": "energy", "charging": "energy", "parking": "parking", "station": "transit", "junction": "roads"}

// Nearby answers "what is around me" for key as the platform's
// what-is-around answer: each asked category on its own, the nearest three
// of each vehicle category within the radius, cheapest current E5 first for
// fuel, and each need within its own widening radius. The answer stands
// when at least one category is ok.
func (s *Service) Nearby(ctx context.Context, key access.Record, q NearbyQuery) (NearbyAnswer, error) {
	out := NearbyAnswer{}
	var families []string
	partial, ok := false, false
	var needs []string
	for _, c := range q.Categories {
		if slices.Contains(places.Needs, c) {
			needs = append(needs, c)
			continue
		}
		a, err := s.nearbyPart(ctx, key, c, q, &out)
		out.Parts = append(out.Parts, PartResult{Part: Part(c), Status: s.status(err), Err: err})
		if errors.Is(err, ErrGateViolation) {
			return NearbyAnswer{}, err
		}
		if err == nil {
			ok, partial = true, partial || a.Partial
			for _, f := range a.Families {
				families = append(families, f.Source.Family)
			}
		}
	}
	if len(needs) > 0 {
		a, err := s.nearbyNeeds(ctx, key, q, needs, &out)
		out.Parts = append(out.Parts, PartResult{Part: "needs", Status: s.status(err), Err: err})
		if errors.Is(err, ErrGateViolation) {
			return NearbyAnswer{}, err
		}
		if err == nil {
			ok, partial = true, partial || a.Partial
			for _, f := range a.Families {
				families = append(families, f.Source.Family)
			}
		}
	}
	if !ok {
		return out, ErrNoPart
	}
	slices.Sort(families)
	fams, err := s.attributed(ctx, slices.Compact(families))
	out.Meta = Answer[struct{}]{Families: fams, Partial: partial, At: s.now()}
	return out, err
}

// nearbyPart answers one vehicle category into out and returns its meta.
func (s *Service) nearbyPart(ctx context.Context, key access.Record, c string, q NearbyQuery, out *NearbyAnswer) (Answer[struct{}], error) {
	if !key.Plan.Has(nearbyScope[c]) {
		return Answer[struct{}]{}, ErrScopeDenied
	}
	at, r, n := q.At.At, q.At.RadiusM, NearbyPerCategory
	switch c {
	case "fuel":
		a, err := s.FuelStations(ctx, key, energy.FuelQuery{At: at, RadiusM: r, Fuel: "e5", Sort: "price", Limit: n, Window: q.FuelWindow})
		if err == nil {
			out.Fuel = &a
		}
		return meta(a.Answer), err
	case "charging":
		a, err := s.Charging(ctx, key, energy.Query{At: at, RadiusM: r, Access: energy.FilterPublic, Limit: n})
		if err == nil {
			out.Charging = &a
		}
		return meta(a), err
	case "parking":
		a, err := s.Parking(ctx, key, parking.Query{At: at, RadiusM: r, Limit: n})
		if err == nil {
			out.Parking = &a
		}
		return meta(a), err
	case "station":
		a, err := s.Stations(ctx, key, transit.Query{At: &at, RadiusM: r, Limit: n, Railway: true})
		if err == nil {
			out.Stations = &a
		}
		return meta(a), err
	}
	jq := q.At
	jq.Limit = n
	js, m, err := listedOwn(ctx, s, key, "roads", func(permitted []string) ([]places.Junction, []string, error) {
		js, err := s.repos.Nearby.Junctions(ctx, jq, permitted)
		var contributed []string
		for _, j := range js {
			contributed = append(contributed, j.Family)
		}
		return js, contributed, err
	})
	if err == nil {
		out.Junctions = &Answer[places.Junction]{Items: js, Families: m.Families, Partial: m.Partial, At: m.At}
	}
	return m, err
}

// nearbyNeeds answers the asked needs into out.
func (s *Service) nearbyNeeds(ctx context.Context, key access.Record, q NearbyQuery, needs []string, out *NearbyAnswer) (Answer[struct{}], error) {
	if !key.Plan.Has("places") {
		return Answer[struct{}]{}, ErrScopeDenied
	}
	nq := q.At
	nq.Needs = needs
	found, m, err := listed(ctx, s, key, "places", "", func(permitted []string) (map[string][]places.NeedPlace, []string, error) {
		found, err := s.repos.Nearby.NeedPlaces(ctx, nq, permitted)
		var contributed []string
		for _, ps := range found {
			for _, p := range ps {
				contributed = append(contributed, p.Family)
			}
		}
		return found, contributed, err
	})
	if err != nil {
		return m, err
	}
	out.Needs = map[string]places.NeedPick{}
	for _, n := range needs {
		out.Needs[n] = places.PickNeed(n, found[n])
	}
	return m, nil
}

// meta is a vehicle category's meta in the what-is-around answer: a
// category that lists nothing credits no family, as the platform's nearby
// search credits the rows it states (docs/parity.md).
func meta[T any](a Answer[T]) Answer[struct{}] {
	if len(a.Items) == 0 {
		return Answer[struct{}]{Partial: a.Partial, At: a.At}
	}
	return Answer[struct{}]{Families: a.Families, Partial: a.Partial, At: a.At}
}
