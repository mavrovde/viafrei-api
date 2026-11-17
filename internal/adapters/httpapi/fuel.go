package httpapi

import (
	"context"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// The filling-station operation (FR-ENERGY).

// The defaults of a fuel search, inside the provider's own ceiling of 25 km
// and ten stations.
const (
	fuelDefaultRadiusM = 5000
	fuelDefaultLimit   = 5
)

// FuelInput is the input of listFuelOptions.
type FuelInput struct {
	AtPosition
	RadiusM  int    `query:"radius_m" minimum:"1000" maximum:"25000" doc:"Search radius in metres (1000 to 25000, the provider's ceiling); default 5000."`
	Fuel     string `query:"fuel" enum:"e5,e10,diesel" doc:"Only stations with a price for this grade. Required with sort=price."`
	Brand    string `query:"brand" maxLength:"60" doc:"Only stations of this brand: a part of it, any case."`
	Name     string `query:"name" maxLength:"60" doc:"Only stations whose name or brand holds this text, any case."`
	OpenNow  bool   `query:"open_now" doc:"Only stations reported open now; meta.query counts those left out as closed and as of unknown state."`
	OpenAt   string `query:"open_at" maxLength:"16" pattern:"^([0-9]{4}-[0-9]{2}-[0-9]{2}[T ])?[0-9]{1,2}:[0-9]{2}$" example:"23:30" doc:"Only stations whose published opening hours have them open at that time in Germany (Europe/Berlin): 23:30 is the next time the clock shows 23:30, 2026-09-30 06:15 that date and time. Not with open_now. A station whose hours are unknown or unreadable is counted in meta.query.droppedUnknown, never guessed open."`
	WholeDay bool   `query:"whole_day" doc:"Only stations the provider flags as open around the clock; meta.query.droppedNotWholeDay counts the others."`
	Sort     string `query:"sort" enum:"distance,price,name" doc:"distance (default, nearest first), price (every current price cheapest first, then the last known prices nearest first; needs fuel) or name (alphabetical, as German sorts it)."`
	Limit    int    `query:"limit" minimum:"1" maximum:"10" doc:"Most stations to return; default 5."`
}

// Resolve reads the position and requires fuel with sort=price.
func (in *FuelInput) Resolve(ctx huma.Context) []error {
	if errs := in.AtPosition.Resolve(ctx); errs != nil {
		return errs
	}
	var errs []error
	if in.Sort == "price" && in.Fuel == "" {
		errs = append(errs, &huma.ErrorDetail{Location: "query.fuel", Message: "fuel is required with sort=price"})
	}
	if in.OpenAt != "" {
		if in.OpenNow {
			errs = append(errs, &huma.ErrorDetail{Location: "query.open_at", Message: "give either open_now or open_at, not both", Value: in.OpenAt})
		} else if _, ok := energy.ResolveOpenAt(in.OpenAt, time.Now()); !ok {
			errs = append(errs, &huma.ErrorDetail{Location: "query.open_at", Message: "not a time: use HH:MM or YYYY-MM-DD HH:MM", Value: in.OpenAt})
		}
	}
	return errs
}

// FuelPrice is a station's newest price for a grade.
type FuelPrice struct {
	Fuel            string    `json:"fuel" doc:"The grade: e5, e10, diesel. Open enumeration."`
	PriceEur        float64   `json:"priceEur" doc:"Euro per litre, three decimals."`
	ReportedAt      time.Time `json:"reportedAt" doc:"When the price was reported."`
	ReportedAtLocal string    `json:"reportedAtLocal" format:"date-time" doc:"reportedAt in Europe/Berlin time."`
	Current         bool      `json:"current" doc:"True when the price was reported inside fuel.price_fresh (30 minutes by default, the platform's price cache window). Ranking by price uses fuel.price_current (24 hours): an older price is the last known one, never ranked."`
}

// FuelStation is a filling station of an answer.
type FuelStation struct {
	ID           string      `json:"id"`
	Name         *string     `json:"name" nullable:"true" doc:"The station's name; null when not published."`
	Brand        *string     `json:"brand" nullable:"true" doc:"The brand; null when not published."`
	Location     Position    `json:"location"`
	DistanceM    int         `json:"distanceM" doc:"Distance from the asked position in metres, to the nearest 100 m: the platform's filling-station search states it in kilometres to one decimal (0.1 km). The order is by the exact distance."`
	OpenState    string      `json:"openingState" doc:"open, closed or unknown at the answer's instant (open_at's, when asked), read from the published opening hours: unknown when there are none or they cannot be read. Under sort=price, the state the station reported, as the platform's cheapest-fuel search states it. Open enumeration."`
	Prices       []FuelPrice `json:"prices" doc:"The newest price per grade the station reported, by grade; empty when it reported none. Under sort=price, the asked grade's only."`
	SourceFamily string      `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// FuelQueryMeta is meta.query of listFuelOptions.
type FuelQueryMeta struct {
	At                 Position   `json:"at"`
	RadiusM            int        `json:"radiusM"`
	Sort               string     `json:"sort"`
	Matched            int        `json:"matched" doc:"Stations the filters kept; more than listed when meta.page.hasMore."`
	DroppedClosed      int        `json:"droppedClosed" doc:"Stations open_now left out as reported closed."`
	DroppedUnknown     int        `json:"droppedUnknown" doc:"Stations open_now left out as of unknown state, or open_at as of unknown or unreadable hours."`
	DroppedNotWholeDay int        `json:"droppedNotWholeDay" doc:"Stations whole_day left out."`
	OpenAt             *time.Time `json:"openAt,omitempty" doc:"The instant open_at named."`
}

// FuelMeta is the meta member of listFuelOptions.
type FuelMeta struct {
	DataMeta
	Page  Page          `json:"page"`
	Query FuelQueryMeta `json:"query"`
}

// FuelEnvelope is the enveloped answer of listFuelOptions.
type FuelEnvelope struct {
	Data  []FuelStation `json:"data"`
	Meta  FuelMeta      `json:"meta"`
	Links Links         `json:"links"`
}

// FuelOutput is the response of listFuelOptions.
type FuelOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         FuelEnvelope
}

func registerFuel(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Filling stations and their prices near a position"
	op.Description = "The filling stations within the radius with the newest price each reported per grade, filtered by grade, brand, name and open state, ordered by distance, by price or by name. Needs the scope energy."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "filling station search"
	huma.Register(api, op, func(ctx context.Context, in *FuelInput) (*FuelOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := energy.FuelQuery{At: in.point, RadiusM: in.RadiusM, Fuel: in.Fuel, Brand: in.Brand, Name: in.Name,
			OpenNow: in.OpenNow, OpenAt: in.OpenAt, WholeDay: in.WholeDay, Sort: in.Sort, Limit: in.Limit, Window: s.cfg.Fuel.PriceCurrent}
		if q.RadiusM == 0 {
			q.RadiusM = fuelDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = fuelDefaultLimit
		}
		if q.Sort == "" {
			q.Sort = "distance"
		}
		a, err := s.features.FuelStations(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := fuelStations(a, s.cfg.Fuel.PriceFresh)
		if q.Sort == "price" {
			data = cheapest(a, data, q.Fuel)
		}
		return &FuelOutput{CacheControl: noStore, Body: FuelEnvelope{
			Data: data,
			Meta: FuelMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query: FuelQueryMeta{At: position(q.At), RadiusM: q.RadiusM, Sort: q.Sort, Matched: a.Matched,
					DroppedClosed: a.DroppedClosed, DroppedUnknown: a.DroppedUnknown, DroppedNotWholeDay: a.DroppedNotWholeDay, OpenAt: a.OpenAt},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// fuelStations are the stations of a fuel answer, each price current when
// reported within window of the answer's instant, each station's opening
// state read from its published hours at the instant open_at named, else at
// the answer's.
func fuelStations(a features.FuelStations, window time.Duration) []FuelStation {
	data := make([]FuelStation, len(a.Items))
	for i, st := range a.Items {
		prices := make([]FuelPrice, len(st.Prices))
		for j, p := range st.Prices {
			prices[j] = FuelPrice{Fuel: p.Product, PriceEur: p.Price, ReportedAt: p.ObservedAt.UTC(),
				ReportedAtLocal: local(p.ObservedAt), Current: p.Current(a.At, window)}
		}
		data[i] = FuelStation{ID: st.ID, Name: st.Name, Brand: st.Brand, Location: position(st.Location),
			DistanceM: tenthKm(st.DistanceM), OpenState: st.OpenStateAt(openStateInstant(a)), Prices: prices, SourceFamily: st.Family}
	}
	return data
}

// cheapest restates a price-ordered answer as the platform's cheapest-fuel
// search states it: each station with the asked grade's price only, and its
// reported opening state (the price row's), not the published hours'. The
// cheapest-fuel search reads no hours.
func cheapest(a features.FuelStations, data []FuelStation, fuel string) []FuelStation {
	for i, st := range a.Items {
		data[i].Prices = slices.DeleteFunc(data[i].Prices, func(p FuelPrice) bool { return p.Fuel != fuel })
		data[i].OpenState = reportedOpenState(st.Open)
	}
	return data
}

// reportedOpenState is a station's reported state as an opening state: open,
// closed, or unknown when the station reported neither.
func reportedOpenState(open string) string {
	if open == "open" || open == "closed" {
		return open
	}
	return "unknown"
}

// tenthKm states a distance as the platform's filling-station search states
// it: kilometres to one decimal (Math.round(km*10)/10, halves up), here in
// metres, so 110 m is 100 and 150 m is 200.
func tenthKm(m int) int {
	return (m + 50) / 100 * 100
}

// openStateInstant is the instant a fuel answer's opening states are read at.
func openStateInstant(a features.FuelStations) time.Time {
	if a.OpenAt != nil {
		return *a.OpenAt
	}
	return a.At
}
