package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// The energy objects read by their id: a filling station with its prices and
// a charging site with its points (the site's station and site pages).

// FuelStationInput is the input of getFuelStation.
type FuelStationInput struct {
	ID string `path:"fuelId" maxLength:"120" pattern:"^[A-Za-z0-9:._-]+$" example:"tk-000001" doc:"A filling station's id, as listFuelOptions answers it."`
}

// PostalAddress is a station's published postal address.
type PostalAddress struct {
	Street   *string `json:"street" nullable:"true" doc:"The street; null when not published."`
	Number   *string `json:"number" nullable:"true" doc:"The house number; null when not published."`
	Postcode *string `json:"postcode" nullable:"true" doc:"The postcode; null when not published."`
	Place    *string `json:"place" nullable:"true" doc:"The town; null when not published."`
}

// FuelStationData is one filling station read by its id.
type FuelStationData struct {
	ID           string        `json:"id"`
	Name         *string       `json:"name" nullable:"true" doc:"The station's name; null when not published."`
	Brand        *string       `json:"brand" nullable:"true" doc:"The brand; null when not published."`
	Address      PostalAddress `json:"address"`
	Location     Position      `json:"location"`
	OpenState    string        `json:"openingState" doc:"open, closed or unknown now, read from the published opening hours: unknown when there are none or they cannot be read. Open enumeration."`
	AroundClock  bool          `json:"aroundTheClock" doc:"True when the provider flags the station as open around the clock."`
	Prices       []FuelPrice   `json:"prices" doc:"Per grade, the newest price reported in the last 72 hours (the platform's held window), by grade; a grade with no price in it is left out, and current says whether a price is inside fuel.price_fresh."`
	SourceFamily string        `json:"sourceFamily"`
}

// FuelStationEnvelope is the enveloped answer of getFuelStation.
type FuelStationEnvelope struct {
	Data  FuelStationData `json:"data"`
	Meta  DataMeta        `json:"meta"`
	Links Links           `json:"links"`
}

// FuelStationOutput is the response of getFuelStation.
type FuelStationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         FuelStationEnvelope
}

func registerFuelStation(api huma.API, s *server, op huma.Operation) {
	op.Summary = "One filling station and its prices"
	op.Description = "One filling station by its id: its name, brand, address, opening state now and, per grade, the newest price it reported in the last 72 hours (the site's station page and its price block). A withdrawn station, or one of a source family the key may not read, is not found. Needs the scope energy."
	op.Extensions[counterpartExtension] = "filling station search"
	featureResponses(api, &op, "No filling station with this id (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *FuelStationInput) (*FuelStationOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		st, a, err := s.features.FuelStation(ctx, principalFrom(ctx).Key, in.ID, s.cfg.Fuel.PriceHeld)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		prices := make([]FuelPrice, len(st.Prices))
		for i, p := range st.Prices {
			prices[i] = FuelPrice{Fuel: p.Product, PriceEur: p.Price, ReportedAt: p.ObservedAt.UTC(),
				ReportedAtLocal: local(p.ObservedAt), Current: p.Current(a.At, s.cfg.Fuel.PriceFresh)}
		}
		return &FuelStationOutput{CacheControl: noStore, Body: FuelStationEnvelope{
			Data: FuelStationData{ID: st.ID, Name: st.Name, Brand: st.Brand, Location: position(st.Location),
				Address:   PostalAddress{Street: st.Address.Street, Number: st.Address.HouseNumber, Postcode: st.Address.Postcode, Place: st.Address.City},
				OpenState: st.OpenStateAt(a.At), AroundClock: st.Hours.WholeDay != nil && *st.Hours.WholeDay, Prices: prices, SourceFamily: st.Family},
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ChargingSiteInput is the input of getChargingSite.
type ChargingSiteInput struct {
	ID string `path:"chargingId" maxLength:"200" pattern:"^[A-Za-z0-9:._/-]+$" example:"cs-000001" doc:"A charging site's id, as listChargingOptions answers it."`
}

// ChargingSiteEnvelope is the enveloped answer of getChargingSite.
type ChargingSiteEnvelope struct {
	Data  ChargingSite `json:"data"`
	Meta  DataMeta     `json:"meta"`
	Links Links        `json:"links"`
}

// ChargingSiteOutput is the response of getChargingSite.
type ChargingSiteOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ChargingSiteEnvelope
}

func registerChargingSite(api huma.API, s *server, op huma.Operation) {
	op.Summary = "One charging site and its points"
	op.Description = "One charging site by its id with its points and their last reported availability (the site's own page). distanceM is 0. A withdrawn site, or one of a source family the key may not read, is not found. Needs the scope energy."
	op.Extensions[counterpartExtension] = "charging station search"
	featureResponses(api, &op, "No charging site with this id (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *ChargingSiteInput) (*ChargingSiteOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.ChargingSite(ctx, principalFrom(ctx).Key, in.ID, s.cfg.Charging.StatusMaxAge)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		a.Items[0].DistanceM = 0
		site := chargingSites(ctx, a, s.cfg.Charging.StatusMaxAge)[0]
		return &ChargingSiteOutput{CacheControl: noStore, Body: ChargingSiteEnvelope{
			Data:  site,
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}
