package httpapi

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/national"
)

// SummaryCount is one count of the national picture, of one family.
type SummaryCount struct {
	Key          string `json:"key" doc:"What is counted: a situation kind, a Land's two-digit key, a parking kind, a warning level."`
	Count        int    `json:"count"`
	SourceFamily string `json:"sourceFamily"`
}

// FuelGradeSummary is one grade's current prices across Germany, of one
// family.
type FuelGradeSummary struct {
	Fuel         string  `json:"fuel" doc:"The grade: e5, e10, diesel. Open enumeration."`
	Stations     int     `json:"stations" doc:"Stations with a current price for it (inside fuel.price_fresh)."`
	CheapestEur  float64 `json:"cheapestEur"`
	MedianEur    float64 `json:"medianEur"`
	SourceFamily string  `json:"sourceFamily"`
}

// ChargingFamilySummary is one family's charging sites and points.
type ChargingFamilySummary struct {
	Sites        int    `json:"sites"`
	ChargePoints int    `json:"chargePoints"`
	SourceFamily string `json:"sourceFamily"`
}

// FacilitySummary counts station lifts or escalators in one state.
type FacilitySummary struct {
	Kind         string `json:"kind" doc:"elevator or escalator. Open enumeration."`
	State        string `json:"state" doc:"active, inactive or unknown. Open enumeration."`
	Count        int    `json:"count"`
	Stations     int    `json:"stations" doc:"The stations they are at."`
	SourceFamily string `json:"sourceFamily"`
}

// AreaPunctuality is one area's punctuality now.
type AreaPunctuality struct {
	Level        string   `json:"level" doc:"The area's level: state (a Land) or district (a kreisfreie Stadt or Kreis). Open enumeration."`
	Code         string   `json:"code" doc:"The area's official key."`
	Name         *string  `json:"name" nullable:"true"`
	Trips        int      `json:"trips" doc:"Trips observed."`
	Late         *int     `json:"late" nullable:"true" doc:"Trips observed late."`
	MedianDelayS *float64 `json:"medianDelayS" nullable:"true" doc:"The median of each trip's worst delay, seconds."`
	SourceFamily string   `json:"sourceFamily"`
}

// WarningSummary counts the weather warnings in force at one level.
type WarningSummary struct {
	Level        string `json:"level" doc:"The warning level, 1 to 4."`
	Count        int    `json:"count"`
	Areas        int    `json:"areas" doc:"The warning areas they cover."`
	SourceFamily string `json:"sourceFamily"`
}

// NationalData is the national picture.
type NationalData struct {
	Situations  []SummaryCount          `json:"situations" doc:"Road situations in force now, by kind (closure, roadworks, jam, restriction, ...)."`
	States      []SummaryCount          `json:"states" doc:"Road situations in force now, by Land (the Land of each situation's point)."`
	Fuel        []FuelGradeSummary      `json:"fuel" doc:"Per grade, the stations with a current price, the cheapest and the median of those prices."`
	Charging    []ChargingFamilySummary `json:"charging" doc:"Charging sites and their points, per family."`
	Parking     []SummaryCount          `json:"parking" doc:"Parking sites, by kind."`
	Facilities  []FacilitySummary       `json:"facilities" doc:"Station lifts and escalators, by kind and state (needs the scope accessibility)."`
	Punctuality []AreaPunctuality       `json:"punctuality" doc:"Regional public-transport punctuality, per Land and per district."`
	Warnings    []WarningSummary        `json:"warnings" doc:"Official weather warnings in force now, by level."`
}

// NationalEnvelope is the enveloped answer of getNationalSummary.
type NationalEnvelope struct {
	Data  NationalData `json:"data"`
	Meta  DataMeta     `json:"meta"`
	Links Links        `json:"links"`
}

// NationalOutput is the response of getNationalSummary.
type NationalOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         NationalEnvelope
}

func registerNational(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Germany now: the national counts and boards"
	op.Description = "The national picture the site's home tiles, its live map and its national pages state: road situations in force by kind and by Land, current fuel prices by grade, charging sites and points, parking sites by kind, station lifts and escalators by state, regional punctuality and weather warnings by level. Each part is read from the families of its domain the key may read, for every domain the key has the scope of (roads, energy, parking, transit, accessibility, weather); a domain whose families are all withheld is left out and named in meta.warnings."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*NationalOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.National(ctx, principalFrom(ctx).Key, s.cfg.Fuel.PriceFresh)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &NationalOutput{CacheControl: noStore, Body: NationalEnvelope{
			Data:  nationalData(a.Rows),
			Meta:  s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// nationalData sorts the rows into their sections; every section is a list,
// empty when nothing is counted.
func nationalData(rows []national.Row) NationalData {
	d := NationalData{Situations: []SummaryCount{}, States: []SummaryCount{}, Fuel: []FuelGradeSummary{}, Charging: []ChargingFamilySummary{},
		Parking: []SummaryCount{}, Facilities: []FacilitySummary{}, Punctuality: []AreaPunctuality{}, Warnings: []WarningSummary{}}
	for _, r := range rows {
		count := SummaryCount{Key: r.Key, Count: r.N, SourceFamily: r.Family}
		switch r.Section {
		case national.SectionSituations:
			d.Situations = append(d.Situations, count)
		case national.SectionStates:
			d.States = append(d.States, count)
		case national.SectionParking:
			d.Parking = append(d.Parking, count)
		case national.SectionFuel:
			d.Fuel = append(d.Fuel, FuelGradeSummary{Fuel: r.Key, Stations: r.N, CheapestEur: deref(r.Low), MedianEur: deref(r.Mid), SourceFamily: r.Family})
		case national.SectionCharging:
			d.Charging = append(d.Charging, ChargingFamilySummary{Sites: r.N, ChargePoints: derefInt(r.M), SourceFamily: r.Family})
		case national.SectionFacilities:
			kind, state, _ := strings.Cut(r.Key, "/")
			d.Facilities = append(d.Facilities, FacilitySummary{Kind: kind, State: state, Count: r.N, Stations: derefInt(r.M), SourceFamily: r.Family})
		case national.SectionPunctuality:
			level, code, _ := strings.Cut(r.Key, ":")
			d.Punctuality = append(d.Punctuality, AreaPunctuality{Level: level, Code: code, Name: r.Name, Trips: r.N, Late: r.M, MedianDelayS: r.Mid, SourceFamily: r.Family})
		case national.SectionWarnings:
			d.Warnings = append(d.Warnings, WarningSummary{Level: r.Key, Count: r.N, Areas: derefInt(r.M), SourceFamily: r.Family})
		}
	}
	return d
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
