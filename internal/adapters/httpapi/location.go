package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

// LocationInput is the input of describeLocation.
type LocationInput struct {
	AtPosition
}

// LocationAddress is the nearest address to a position.
type LocationAddress struct {
	Address
	DistanceM int `json:"distanceM" doc:"Distance from the asked position in metres."`
}

// LocationPlace is a place a position lies in or near.
type LocationPlace struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Code         *string  `json:"code" nullable:"true" doc:"The official region code; null when none."`
	Location     Position `json:"location"`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from the asked position in metres, to 100 m (under 50 m to the metre); null for the Land of a district."`
	SourceFamily string   `json:"sourceFamily"`
}

// LocationSettlement is the settlement nearest a position: the platform
// names it by its name and point, without a code.
type LocationSettlement struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Location     Position `json:"location"`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from the asked position in metres, to 100 m (under 50 m to the metre)."`
	SourceFamily string   `json:"sourceFamily"`
}

// LocationData is the data of describeLocation: independent facts, each
// null when none is within its reach.
type LocationData struct {
	Address              *LocationAddress    `json:"address" doc:"The nearest address within 5 km; null when none."`
	Municipality         *LocationSettlement `json:"municipality" doc:"The nearest municipality within 50 km; null when none."`
	District             *LocationPlace      `json:"district" doc:"The nearest district within 100 km; null when none."`
	State                *LocationPlace      `json:"state" doc:"The Land of the district; with no district, the nearest Land within 250 km; null when none."`
	MayLieOutsideGermany bool                `json:"mayLieOutsideGermany" doc:"True when the position may lie outside Germany, off the coast or near the border: no address within 1.5 km, and at least half of the circle of 15 km around it, in neighbouring eighths, holds none. The facts are then the nearest in Germany and may not describe the position itself."`
	Gemeinde             *LocationGemeinde   `json:"gemeinde" doc:"The Gemeinde the position lies in: the one the nearest German address names as its town (inside true), else the nearest Gemeinde within 15 km (inside false); null when none, or when the position may lie outside Germany. It decides the Kreis and the Regierungsbezirk, by its key."`
	GovernmentRegion     *string             `json:"governmentRegion" nullable:"true" doc:"The Regierungsbezirk the Gemeinde lies in; null where its Land has none, or with no Gemeinde."`
	Zone                 *LocationZone       `json:"lowEmissionZone" doc:"The low-emission zone the position lies in, as the platform's description of a position states it; null when the key may read no zone source."`
}

// LocationZone is the low-emission-zone verdict at a position.
type LocationZone struct {
	Verdict      string  `json:"verdict" enum:"inside,transfer,outside,unknown" doc:"inside a zone; in an area released at its edge; outside (zone names the nearest within 25 km); unknown: none within 25 km, which is not that there is no zone."`
	Zone         *string `json:"zone" nullable:"true" doc:"The zone's name as the platform prints it (a register zone by its city): the zone it is inside, or the nearest; in a released area, that area's name."`
	ZoneKey      *string `json:"zoneKey" nullable:"true" doc:"The key of that zone row, as getDrivingRules lists it."`
	DistanceM    *int    `json:"distanceM" nullable:"true" doc:"Outside: the distance to the nearest zone in metres; else null."`
	ExemptRoad   *string `json:"exemptRoad" nullable:"true" doc:"The name of an exempt road within 60 m, a road the zone does not apply to."`
	ReleasedArea *string `json:"releasedArea" nullable:"true" doc:"The name of the released area at the zone's edge the position is in."`
	ZoneFacts
}

func locationZone(v *rules.Verdict, a *rules.ZoneAnswer) *LocationZone {
	if v == nil {
		return nil
	}
	name := func(z *rules.Zone) *string {
		if z == nil {
			return nil
		}
		return &z.Name
	}
	out := &LocationZone{Verdict: v.Kind, Zone: name(v.Zone), ZoneKey: zoneKey(v.Zone), ExemptRoad: name(v.ExemptRoad), ReleasedArea: name(v.Transfer), ZoneFacts: zoneFacts(a)}
	if a != nil {
		out.Zone = a.Name
	}
	if v.Kind == "outside" {
		out.DistanceM = &v.Zone.DistanceM
	}
	return out
}

// LocationEnvelope is the enveloped answer of describeLocation.
type LocationEnvelope struct {
	Data  LocationData `json:"data"`
	Meta  DataMeta     `json:"meta"`
	Links Links        `json:"links"`
}

// LocationOutput is the response of describeLocation.
type LocationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LocationEnvelope
}

func locationPlace(p *places.Place) *LocationPlace {
	if p == nil {
		return nil
	}
	return &LocationPlace{ID: p.ID, Name: p.Name, Code: p.RegionCode, Location: position(p.Location), DistanceM: p.DistanceM, SourceFamily: p.Family}
}

func registerLocation(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What lies at a position"
	op.Description = "A coordinate as a person understands it: the nearest address, municipality and district, and the district's Land (with no district, the nearest Land); each fact on its own, null when none is within its reach; the low-emission zone it lies in; and whether the position may lie outside Germany. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "location description"
	huma.Register(api, op, func(ctx context.Context, in *LocationInput) (*LocationOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.Location(ctx, principalFrom(ctx).Key, places.LocationQuery{At: in.point})
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &LocationOutput{CacheControl: noStore, Body: locationEnvelope(s, ctx, a)}, nil
	})
}

// LocationGemeinde is the Gemeinde a position lies in.
type LocationGemeinde struct {
	Name         string  `json:"name"`
	Code         string  `json:"code" doc:"Its eight-digit official key (AGS)."`
	Inside       bool    `json:"inside" doc:"True when the nearest German address names it as its town; false when it is only the nearest Gemeinde."`
	Kreis        *string `json:"kreis" nullable:"true" doc:"The Kreis its key names; null when the gazetteer holds none."`
	Bezirk       *string `json:"bezirk" nullable:"true" doc:"The Regierungsbezirk its key names; null where there is none."`
	SourceFamily string  `json:"sourceFamily"`
}

func locationEnvelope(s *server, ctx context.Context, a features.LocationAnswer) LocationEnvelope {
	data := LocationData{District: locationPlace(a.District), State: locationPlace(a.State), MayLieOutsideGermany: a.MayLieAbroad, Zone: locationZone(a.Zone, a.ZoneAnswer)}
	if g := a.Gemeinde; g != nil {
		data.Gemeinde = &LocationGemeinde{Name: g.Name, Code: g.Code, Inside: g.Inside, Kreis: g.Kreis, Bezirk: g.Bezirk, SourceFamily: g.Family}
		data.GovernmentRegion = g.Bezirk
	}
	if p := a.Municipality; p != nil {
		data.Municipality = &LocationSettlement{ID: p.ID, Name: p.Name, Location: position(p.Location), DistanceM: p.DistanceM, SourceFamily: p.Family}
	}
	if v := a.Address; v != nil {
		data.Address = &LocationAddress{Address: Address{Street: v.Street, HouseNumber: v.HouseNumber, Postcode: v.Postcode, Place: v.Place,
			Land: v.Land, Location: position(v.Location), DataAsOf: day(v.DataAsOf), SourceFamily: v.Family}}
		if v.DistanceM != nil {
			data.Address.DistanceM = *v.DistanceM
		}
	}
	return LocationEnvelope{Data: data, Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}
}
