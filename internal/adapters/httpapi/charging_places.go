package httpapi

import (
	"context"
	"math"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// groupPlace is the group value that answers one item a place.
const groupPlace = "place"

// ChargingAddress is a place's published address; a member is null when
// the source states none.
type ChargingAddress struct {
	Street   *string `json:"street" nullable:"true"`
	Number   *string `json:"number" nullable:"true"`
	Postcode *string `json:"postcode" nullable:"true"`
	City     *string `json:"city" nullable:"true"`
}

// ChargingPlaceStatus is a place's points' status, combined.
type ChargingPlaceStatus struct {
	Free       int       `json:"free" doc:"Points reported available."`
	Occupied   int       `json:"occupied" doc:"Points reported occupied."`
	OutOfOrder int       `json:"outOfOrder" doc:"Points reported out of order."`
	Total      int       `json:"total" doc:"Every point of the place, those with no status included."`
	ObservedAt time.Time `json:"observedAt" doc:"When the newest status counted was last confirmed (UTC)."`
	Stale      bool      `json:"stale" doc:"true: no point has a current status, and the counts are the last ones known, as of observedAt; possibly out of date."`
}

// ChargingPrice is a place's cheapest ad-hoc price per kWh.
type ChargingPrice struct {
	Amount   float64 `json:"amount" doc:"EUR per kWh, three decimals."`
	Basis    string  `json:"basis" enum:"net,gross" doc:"gross: VAT included, as published or grossed up by the published rate; net: published without VAT and without a rate."`
	Currency string  `json:"currency" enum:"EUR"`
}

// ChargingConnector is one plug of a place.
type ChargingConnector struct {
	Type    string   `json:"type" doc:"The plug's name in the answer's language (CCS2, Typ 2 / Type 2, CHAdeMO, ...)."`
	PowerKw *float64 `json:"powerKw" nullable:"true" doc:"The highest power of the points with this plug, in kW; null when none is published."`
	Count   int      `json:"count" doc:"Points with this plug."`
}

// ChargingPlace is what an item of a place-grouped answer adds: the
// records folded into it and what they say together.
type ChargingPlace struct {
	SiteID             string               `json:"siteId" doc:"The platform's key of the place's first record: the site a charger watch (watch_situation, kind charger) takes."`
	SiteIDs            []string             `json:"siteIds" doc:"Every record id of the place, siteId first."`
	Address            *ChargingAddress     `json:"address" doc:"The first record's published address; null when it was filed under an address that is not where the place lies (see reportedAddress)."`
	ReportedAddress    *ChargingAddress     `json:"reportedAddress,omitempty" doc:"Only when the register row was filed under an address elsewhere (probably the operator's own): that address."`
	LocatedNear        *string              `json:"locatedNear,omitempty" doc:"With reportedAddress: the town the place lies in, when one is known."`
	ReportedLocation   *Position            `json:"reportedLocation,omitempty" doc:"Only when the place was located by its address: the point the operator published."`
	FromRegister       bool                 `json:"fromRegister" doc:"A Bundesnetzagentur register row is among the records."`
	RegisterReleasedOn *string              `json:"registerReleasedOn" nullable:"true" format:"date" doc:"The release (Berlin date) of the register file the place's register rows come from; null when none is among them."`
	SourceEnded        bool                 `json:"sourceEnded" doc:"The first record's source was ended by its publisher and answers until its window ends; its points carry no status."`
	Status             *ChargingPlaceStatus `json:"status" doc:"null: no point has a status (unknown, never 0)."`
	Price              *ChargingPrice       `json:"price" doc:"null: no record publishes a price per kWh (never 0)."`
	Connectors         []ChargingConnector  `json:"connectors"`
}

// ChargingPricesPublished counts the places of a place-grouped answer
// with a price.
type ChargingPricesPublished struct {
	Priced int `json:"priced" doc:"Places with a published price per kWh."`
	Places int `json:"places" doc:"Places listed."`
}

// refusedWithGroup are the inputs a place-grouped answer does not take:
// the platform's place answer reads public sites under these filters alone.
func (in *ChargingInput) refusedWithGroup() []error {
	if in.Group != groupPlace {
		return nil
	}
	refuse := func(name string, v any) error {
		return &huma.ErrorDetail{Location: "query." + name, Message: "not with group=place", Value: v}
	}
	var errs []error
	if in.MaxKw > 0 {
		errs = append(errs, refuse("max_kw", in.MaxKw))
	}
	if in.Operator != "" {
		errs = append(errs, refuse("operator", in.Operator))
	}
	if len(in.Conditions) > 0 {
		errs = append(errs, refuse("conditions", in.Conditions))
	}
	if in.Access != "" && in.Access != string(energy.FilterPublic) {
		errs = append(errs, refuse("access", in.Access))
	}
	return errs
}

func strOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func addressOf(r energy.PlaceRecord) *ChargingAddress {
	return &ChargingAddress{Street: strOrNil(r.Street), Number: strOrNil(r.HouseNumber), Postcode: strOrNil(r.Postcode), City: strOrNil(r.City)}
}

// placeStatus is the place's points' status: the current counts, else
// the last stale ones, else null.
func placeStatus(p energy.Place) *ChargingPlaceStatus {
	var states []energy.Verdict
	var asOf, staleAsOf *time.Time
	stale := energy.StaleStates{}
	for _, m := range p.Members {
		states = append(states, m.States...)
		if m.StatusAsOf != nil && (asOf == nil || m.StatusAsOf.After(*asOf)) {
			asOf = m.StatusAsOf
		}
		if m.StaleAsOf != nil && (staleAsOf == nil || m.StaleAsOf.After(*staleAsOf)) {
			staleAsOf = m.StaleAsOf
		}
		stale = energy.StaleStates{Available: stale.Available + m.StaleStates.Available, Occupied: stale.Occupied + m.StaleStates.Occupied,
			OutOfOrder: stale.OutOfOrder + m.StaleStates.OutOfOrder}
	}
	t := energy.TallyOf(states)
	if t.Available+t.Occupied+t.OutOfOrder > 0 {
		return &ChargingPlaceStatus{Free: t.Available, Occupied: t.Occupied, OutOfOrder: t.OutOfOrder, Total: t.Total, ObservedAt: asOf.UTC()}
	}
	if staleAsOf != nil {
		return &ChargingPlaceStatus{Free: stale.Available, Occupied: stale.Occupied, OutOfOrder: stale.OutOfOrder, Total: t.Total,
			ObservedAt: staleAsOf.UTC(), Stale: true}
	}
	return nil
}

// placePrice is the cheapest price of the place's records.
func placePrice(p energy.Place) *ChargingPrice {
	var best *energy.Price
	for _, m := range p.Members {
		if m.Price != nil && (best == nil || m.Price.Eur < best.Eur) {
			best = m.Price
		}
	}
	if best == nil {
		return nil
	}
	basis := "net"
	if best.IncludesVat {
		basis = "gross"
	}
	return &ChargingPrice{Amount: math.Round(best.Eur*1000) / 1000, Basis: basis, Currency: "EUR"}
}

// placeConnectors are the place's plugs, in the order first met, with the
// highest power and the count of the points that have each.
func placeConnectors(p energy.Place, lang string) []ChargingConnector {
	out := []ChargingConnector{}
	for _, m := range p.Members {
		for _, pt := range m.Record.Points {
			var seen []string
			for _, c := range pt.ConnectorTypes {
				l := energy.ConnectorLabel(c, lang)
				if containsStr(seen, l) {
					continue
				}
				seen = append(seen, l)
				i := connectorIndex(out, l)
				if i < 0 {
					out = append(out, ChargingConnector{Type: l})
					i = len(out) - 1
				}
				out[i].Count++
				if pt.MaxPowerKw != nil && (out[i].PowerKw == nil || *pt.MaxPowerKw > *out[i].PowerKw) {
					v := *pt.MaxPowerKw
					out[i].PowerKw = &v
				}
			}
		}
	}
	return out
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func connectorIndex(cs []ChargingConnector, t string) int {
	for i, c := range cs {
		if c.Type == t {
			return i
		}
	}
	return -1
}

// pointVerdictState is the availability state a judged point shows.
var pointVerdictState = map[energy.Verdict]energy.State{
	energy.VerdictAvailable: energy.StateAvailable, energy.VerdictOccupied: energy.StateOccupied,
	energy.VerdictOutOfOrder: energy.StateOutOfOrder, energy.VerdictStale: energy.StateStale,
}

// chargingPlaces is the place-grouped answer as items: the first record
// of each place is its item's site, every record's points its points.
func chargingPlaces(ctx context.Context, a features.PlacesAnswer) ([]ChargingSite, ChargingPricesPublished) {
	out := make([]ChargingSite, len(a.Items))
	published := ChargingPricesPublished{Places: len(a.Items)}
	var window time.Duration
	for _, f := range a.Register.Status {
		if window == 0 || f.StaleAfter < window {
			window = f.StaleAfter
		}
	}
	for i, p := range a.Items {
		lead := p.Members[0]
		label := p.Label
		item := ChargingSite{
			ID: lead.Record.Source + ":" + lead.Record.SiteID, Name: &label, Operator: lead.Record.Operator,
			Location: position(lead.Record.Location), DistanceM: int(math.Round(lead.Record.DistanceM)),
			Access: string(energy.AccessPublic), ChargePoints: []ChargePoint{}, SourceFamily: lead.Record.Source,
		}
		place := ChargingPlace{SiteID: lead.Record.SiteID, Address: addressOf(lead.Record), SourceEnded: lead.Ended,
			Status: placeStatus(p), Price: placePrice(p), Connectors: placeConnectors(p, a.Lang)}
		if p.Elsewhere != nil {
			place.Address, place.ReportedAddress, place.LocatedNear = nil, addressOf(lead.Record), p.Elsewhere.LocatedNear
		}
		if r := lead.Record.Reported; r != nil {
			at := position(*r)
			place.ReportedLocation = &at
		}
		var recs []energy.PlaceRecord
		var maxKw *float64
		available, fresh := 0, false
		for _, m := range p.Members {
			recs = append(recs, m.Record)
			ids := m.Record.SiteIDs
			if len(ids) == 0 {
				ids = []string{m.Record.SiteID}
			}
			for _, id := range ids {
				if !containsStr(place.SiteIDs, id) {
					place.SiteIDs = append(place.SiteIDs, id)
				}
			}
			for k, pt := range m.Record.Points {
				cp := ChargePoint{ID: m.Record.Source + ":" + pt.PointKey, MaxPowerKw: pt.MaxPowerKw}
				if pt.EvseID != nil {
					cp.ID = *pt.EvseID
				}
				if st, ok := pointVerdictState[m.States[k]]; ok && m.PointAsOf[k] != nil {
					cp.Availability = &Availability{State: string(st), ObservedAt: m.PointAsOf[k].UTC()}
					noteShown(ctx, "charging "+item.ID+"/"+cp.ID+"="+cp.Availability.State)
				}
				switch m.States[k] {
				case energy.VerdictAvailable:
					available++
					fresh = true
				case energy.VerdictOccupied, energy.VerdictOutOfOrder:
					fresh = true
				}
				if pt.MaxPowerKw != nil && (maxKw == nil || *pt.MaxPowerKw > *maxKw) {
					maxKw = pt.MaxPowerKw
				}
				item.ChargePoints = append(item.ChargePoints, cp)
			}
		}
		place.FromRegister = hasRegister(recs, a.Register)
		place.RegisterReleasedOn = energy.RegisterReleaseOf(recs, a.Register)
		item.Summary = ChargingSummary{ChargePoints: len(item.ChargePoints), MaxPowerKw: maxKw}
		if fresh {
			item.Summary.AvailableNow = &available
		}
		if place.Price != nil {
			published.Priced++
		}
		item.Place = &place
		out[i] = item
	}
	if window > 0 && len(a.Items) > 0 {
		// A current status turns stale with the clock: the answer changes
		// within the shortest status window at the latest.
		noteChangesAt(ctx, a.At.Add(window))
	}
	return out, published
}

func hasRegister(recs []energy.PlaceRecord, reg energy.SourceRegister) bool {
	for _, r := range recs {
		if reg.IsRegister(r.Source) {
			return true
		}
	}
	return false
}
