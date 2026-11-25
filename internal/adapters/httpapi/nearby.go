package httpapi

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The what-is-around answer, as the platform's: 5 km by default, at most
// 15 km, a glance of three per vehicle category.
const nearbyDefaultRadiusM = 5000

// NearbyInput is the input of listNearby.
type NearbyInput struct {
	AtPosition
	RadiusM    int      `query:"radius_m" minimum:"1000" maximum:"15000" doc:"The vehicle categories' radius in metres (1000 to 15000); default 5000. A need uses its own radius."`
	Categories []string `query:"categories" enum:"fuel,charging,parking,station,junction,groceries,health,money,food,lodging,garage,police,toilets,safety" doc:"The categories, comma-separated. Default: fuel, charging, parking, station and junction. An everyday need (groceries to toilets) only when named, each in its own radius of 1 or 2 km, widened up to 5 km where fewer than three are near. safety, the former name of police and toilets, answers as both."`
}

// NearbyJunction is a motorway junction near the position.
type NearbyJunction struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Road         *string  `json:"road" nullable:"true" doc:"The motorway: the gazetteer point's own, or for an OpenStreetMap node the one the BASt Location Code List places it on (two for an interchange named on both, A3/A46); null for a junction off any motorway that can be named."`
	Number       *string  `json:"number" nullable:"true" doc:"The junction's number; null when not published."`
	Location     Position `json:"location"`
	DistanceM    int      `json:"distanceM"`
	SourceFamily string   `json:"sourceFamily"`
}

// NearbyFuelStation is a filling station near the position, as the
// platform's nearby search states it: its E5 price and no opening state.
type NearbyFuelStation struct {
	ID           string      `json:"id"`
	Name         *string     `json:"name" nullable:"true" doc:"The station's name; null when not published."`
	Brand        *string     `json:"brand" nullable:"true" doc:"The brand; null when not published."`
	Location     Position    `json:"location"`
	DistanceM    int         `json:"distanceM" doc:"Distance in metres, to 0.1 km as the platform's nearby search states it."`
	Prices       []FuelPrice `json:"prices" doc:"The station's E5 price."`
	SourceFamily string      `json:"sourceFamily"`
}

func nearbyFuel(in []FuelStation) []NearbyFuelStation {
	out := make([]NearbyFuelStation, len(in))
	for i, f := range in {
		out[i] = NearbyFuelStation{ID: f.ID, Name: f.Name, Brand: f.Brand, Location: f.Location, DistanceM: f.DistanceM, Prices: f.Prices, SourceFamily: f.SourceFamily}
	}
	return out
}

// NeedPlace is a place of interest answering an everyday need.
type NeedPlace struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind" doc:"The OpenStreetMap kind, category=value (shop=bakery)."`
	Street       *string  `json:"street" nullable:"true"`
	HouseNumber  *string  `json:"houseNo" nullable:"true"`
	Location     Position `json:"location"`
	DistanceM    int      `json:"distanceM" doc:"Distance in metres, to 0.1 km as the platform's nearby search states it."`
	SourceFamily string   `json:"sourceFamily"`
}

// NeedBlock is an everyday need's answer.
type NeedBlock struct {
	Need    string      `json:"need"`
	RadiusM int         `json:"radiusM" doc:"The radius read: the need's own, or wider when fewer than three were inside it."`
	Widened bool        `json:"widened"`
	Places  []NeedPlace `json:"places" doc:"Nearest first, at most five."`
}

// NearbyData is the data of listNearby: each asked category that is ok.
type NearbyData struct {
	Fuel      *[]NearbyFuelStation `json:"fuel,omitempty" doc:"The cheapest current E5 prices first, three at most, each with its E5 price only."`
	Charging  *[]ChargingSite      `json:"charging,omitempty" doc:"Public charging sites, nearest first, three at most."`
	Parking   *[]ParkingListing    `json:"parking,omitempty" doc:"Parking sites, nearest first, three at most."`
	Stations  *[]Station           `json:"stations,omitempty" doc:"Stations, nearest first, three at most."`
	Junctions *[]NearbyJunction    `json:"junctions,omitempty" doc:"Motorway junctions, nearest first, three at most; services areas and car parks are none. OpenStreetMap's junction nodes, same-named ones within 1.5 km as one; with none, the gazetteer's junction points."`
	// JunctionsFrom is the source the junctions came from.
	JunctionsFrom *string      `json:"junctionsFrom,omitempty" enum:"osm,gazetteer" doc:"Where the junctions came from: osm, or gazetteer, when no OpenStreetMap junction node is within the radius. With junctions only."`
	Needs         *[]NeedBlock `json:"needs,omitempty" doc:"The asked everyday needs, in the order asked."`
}

// NearbyPartStatus is the outcome of one category.
type NearbyPartStatus struct {
	Part        string `json:"part" enum:"fuel,charging,parking,station,junction,needs"`
	Status      string `json:"status" enum:"ok,timeout,not_permitted,unavailable,not_requested"`
	ProblemType string `json:"problemType,omitempty" format:"uri" doc:"The problem type of a category that is not ok, where one applies."`
}

// NearbyMeta is the meta of listNearby.
type NearbyMeta struct {
	DataMeta
	Parts []NearbyPartStatus `json:"parts"`
}

// NearbyEnvelope is the enveloped answer of listNearby.
type NearbyEnvelope struct {
	Data  NearbyData `json:"data"`
	Meta  NearbyMeta `json:"meta"`
	Links Links      `json:"links"`
}

// NearbyOutput is the response of listNearby.
type NearbyOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         NearbyEnvelope
}

// nearbyCategories are the asked categories, each once, safety as police
// and toilets; none asked is the vehicle categories.
func nearbyCategories(asked []string) []string {
	if len(asked) == 0 {
		return features.VehicleCategories
	}
	var out []string
	for _, c := range asked {
		for _, c := range strings.Split(strings.ReplaceAll(c, "safety", "police,toilets"), ",") {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

func registerNearby(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is around a position"
	op.Description = "A glance: the nearest three of each vehicle category within the radius (fuel, cheapest current E5 first; public charging; parking; stations; motorway junctions) and the everyday needs asked for, each category under the licence gate on its own. A category that is not ok is absent from data and named in meta.parts; 200 when at least one is ok. Needs the scope places; each category also needs its own scope (junctions: roads)."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "nearby search"
	huma.Register(api, op, func(ctx context.Context, in *NearbyInput) (*NearbyOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := features.NearbyQuery{At: places.NearbyQuery{At: in.point, RadiusM: in.RadiusM}, Categories: nearbyCategories(in.Categories),
			FuelWindow: s.cfg.Fuel.PriceCurrent}
		if q.At.RadiusM == 0 {
			q.At.RadiusM = nearbyDefaultRadiusM
		}
		a, err := s.features.Nearby(ctx, principalFrom(ctx).Key, q)
		if errors.Is(err, features.ErrNoPart) {
			return nil, noPartProblem(ctx, a.Parts)
		}
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &NearbyOutput{CacheControl: noStore, Body: nearbyEnvelope(s, ctx, a, q)}, nil
	})
}

func nearbyEnvelope(s *server, ctx context.Context, a features.NearbyAnswer, q features.NearbyQuery) NearbyEnvelope {
	var data NearbyData
	if a.Fuel != nil {
		v := nearbyFuel(cheapest(*a.Fuel, fuelStations(*a.Fuel, q.FuelWindow), "e5"))
		data.Fuel = &v
	}
	if a.Charging != nil {
		v := chargingSites(ctx, *a.Charging, s.cfg.Charging.StatusMaxAge)
		data.Charging = &v
	}
	if a.Parking != nil {
		v, _ := parkingSites(*a.Parking)
		data.Parking = &v
	}
	if a.Stations != nil {
		v := stations(*a.Stations)
		data.Stations = &v
	}
	if a.Junctions != nil {
		js := []NearbyJunction{}
		for _, j := range a.Junctions.Items {
			js = append(js, NearbyJunction{ID: j.ID, Name: j.Name, Road: j.Road, Number: j.Number,
				Location: position(j.Location), DistanceM: j.DistanceM, SourceFamily: j.Family})
		}
		data.Junctions = &js
		data.JunctionsFrom = &a.JunctionsFrom
	}
	var needs []NeedBlock
	for _, c := range q.Categories {
		p, ok := a.Needs[c]
		if !ok {
			continue
		}
		b := NeedBlock{Need: c, RadiusM: p.RadiusM, Widened: p.Widened, Places: []NeedPlace{}}
		for _, v := range p.Places {
			b.Places = append(b.Places, NeedPlace{Name: v.Name, Kind: v.Kind, Street: v.Street, HouseNumber: v.HouseNumber,
				Location: position(v.Location), DistanceM: tenthKm(v.DistanceM), SourceFamily: v.Family})
		}
		needs = append(needs, b)
	}
	if needs != nil {
		data.Needs = &needs
	}
	var details []string
	if a.Meta.Partial {
		details = append(details, partialDetail)
	}
	if missingPart(a.Parts) {
		details = append(details, partDetail)
		noteDegraded(ctx)
	}
	meta := NearbyMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, len(details) > 0, strings.Join(details, " ")), Parts: []NearbyPartStatus{}}
	for _, r := range a.Parts {
		meta.Parts = append(meta.Parts, NearbyPartStatus{Part: string(r.Part), Status: string(r.Status), ProblemType: s.partProblem(ctx, r)})
	}
	return NearbyEnvelope{Data: data, Meta: meta, Links: s.selfLink(ctx)}
}
