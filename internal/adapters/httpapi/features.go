package httpapi

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// Features is the port to the feature use cases.
type Features interface {
	Charging(ctx context.Context, key access.Record, q energy.Query) (features.Answer[energy.Site], error)
	Parking(ctx context.Context, key access.Record, q parking.Query) (features.Answer[parking.Site], error)
	Overview(ctx context.Context, key access.Record, q features.OverviewQuery) (features.Overview, error)
}

// counterpartExtension names the MCP capability an operation answers the
// same facts as (TEST-009): the parity harness compares the two.
const counterpartExtension = "x-mcp-counterpart"

// Position is a WGS 84 position, rounded to six decimals.
type Position struct {
	Lat float64 `json:"lat" minimum:"-90" maximum:"90" doc:"Latitude in decimal degrees."`
	Lon float64 `json:"lon" minimum:"-180" maximum:"180" doc:"Longitude in decimal degrees."`
}

// nullableObjects are the object members that are null when unknown
// (ENV-040). huma cannot mark a referenced schema nullable, so the document is
// amended once every operation is registered.
const schemaPrefix = "#/components/schemas/"

var nullableObjects = []struct{ schema, member string }{
	{"ChargePoint", "availability"},
	{"ParkingSite", "occupancy"},
}

func amendNullableObjects(api huma.API) {
	reg := api.OpenAPI().Components.Schemas.Map()
	// A nullable enumeration must list null, or null fails it.
	for _, s := range reg {
		for _, p := range s.Properties {
			if p.Nullable && len(p.Enum) > 0 && !slices.Contains(p.Enum, nil) {
				p.Enum = append(p.Enum, nil)
			}
		}
	}
	for _, n := range nullableObjects {
		// Every operation of the catalogue is always registered, so the
		// schema exists. The referenced object is written inline, typed
		// object or null.
		s := reg[n.schema]
		ref := s.Properties[n.member]
		inline := *reg[strings.TrimPrefix(ref.Ref, schemaPrefix)]
		inline.Nullable, inline.Description = true, ref.Description
		s.Properties[n.member] = &inline
	}
}

func position(p geo.Point) Position {
	r := p.Rounded()
	return Position{Lat: r.Lat, Lon: r.Lon}
}

// FreshnessEntry is one entry of meta.freshness.
type FreshnessEntry struct {
	Family      string     `json:"family" doc:"The source family."`
	DataAsOf    *time.Time `json:"dataAsOf" nullable:"true" doc:"The instant the family's data is current to; null when it published none."`
	AgeS        *int64     `json:"ageS" nullable:"true" doc:"generatedAt minus dataAsOf, in whole seconds; null when dataAsOf is."`
	StaleAfterS int64      `json:"staleAfterS" doc:"The family's design threshold: data older than this is stale."`
	Stale       bool       `json:"stale" doc:"True when ageS is above staleAfterS, or dataAsOf is unknown."`
}

// Notes are the verbatim licence notes of the contributing families.
type Notes struct {
	Condition []string `json:"condition" doc:"Conditions the data may only be used under; show them with the data."`
	Purpose   []string `json:"purpose" doc:"Purposes the data may only be used for; show them with the data."`
}

// DataWarning is one entry of meta.warnings of a data answer.
type DataWarning struct {
	Code   string `json:"code" enum:"partial,data_stale" doc:"Stable warning code; clients tolerate codes they do not know."`
	Detail string `json:"detail" doc:"What the warning means for this answer."`
}

// DataMeta is the meta member of every data answer (ENV-001 to ENV-006).
type DataMeta struct {
	ResponseMeta
	AsOf        *time.Time       `json:"asOf" nullable:"true" doc:"The oldest dataAsOf of meta.freshness; null when one is unknown."`
	Freshness   []FreshnessEntry `json:"freshness" doc:"One entry per contributing source family."`
	Attribution []Attribution    `json:"attribution" doc:"One entry per contributing source family; clients displaying the data must show its text."`
	Notes       Notes            `json:"notes"`
	Warnings    []DataWarning    `json:"warnings" doc:"Warnings about this answer; empty when there is none."`
}

// Page is the paging member of a list answer.
type Page struct {
	Limit   int  `json:"limit" doc:"The limit applied."`
	HasMore bool `json:"hasMore" doc:"True when more items lie within the radius than were returned."`
}

func attribution(r access.Source) Attribution {
	return Attribution{
		Family: r.Family, Domain: r.Domain, Publisher: r.Publisher, Text: r.AttributionText,
		Licence: r.Licence, LicenceURL: r.LicenceURL, ShareAlike: r.ShareAlike,
		ShareAlikeOffer: r.ShareAlikeOffer, TermsURL: r.TermsURL,
	}
}

const (
	partialDetail = "A source family that could answer was withheld under the key's licence terms."
	termsDetail   = "Sites marked termsRequired are listed without their occupancy until their terms are accepted."
	partDetail    = "At least one asked part is not ok; meta.parts says why."
	staleDetail   = "The data of at least one family is older than its design threshold; see meta.freshness."
)

func (s *server) dataMeta(ctx context.Context, fams []provenance.Family, at time.Time, partial bool, partialText string) DataMeta {
	m := DataMeta{ResponseMeta: s.envelopeMeta(ctx), Freshness: []FreshnessEntry{}, Attribution: []Attribution{}, Warnings: []DataWarning{}}
	var fs []provenance.Freshness
	for _, f := range fams {
		fr := f.Assess(at)
		fs = append(fs, fr)
		m.Freshness = append(m.Freshness, FreshnessEntry{Family: fr.Family, DataAsOf: fr.DataAsOf, AgeS: fr.AgeS, StaleAfterS: fr.StaleAfterS, Stale: fr.Stale})
		m.Attribution = append(m.Attribution, attribution(f.Source))
	}
	m.AsOf = provenance.AsOf(fs)
	m.Notes = Notes{Condition: provenance.Notes(fams, provenance.Condition), Purpose: provenance.Notes(fams, provenance.Purpose)}
	if partial {
		m.Warnings = append(m.Warnings, DataWarning{Code: "partial", Detail: partialText})
	}
	if provenance.AnyStale(fs) {
		m.Warnings = append(m.Warnings, DataWarning{Code: "data_stale", Detail: staleDetail})
	}
	return m
}

// featureProblem maps a refusal of the feature use cases to its problem.
func (s *server) featureProblem(ctx context.Context, err error) *Problem {
	switch {
	case errors.Is(err, access.ErrTermsNotAccepted):
		return newProblem(ctx, SlugTermsNotAccepted, "Every source family that could answer needs its terms accepted first.", 0)
	case errors.Is(err, access.ErrNonePermitted):
		return newProblem(ctx, SlugSourceNotPermitted, "No source family that could answer is permitted to this key.", 0)
	case errors.Is(err, access.ErrForbiddenItem):
		gateViolations.Add(1)
		s.log.ErrorContext(ctx, "licence gate post-check aborted an answer", "requestId", RequestIDFrom(ctx), "error", err)
		return newProblem(ctx, SlugInternal, "An unexpected fault occurred; quote the request id.", 0)
	case errors.Is(err, places.ErrNotFound):
		return newProblem(ctx, SlugNotFound, "No place with this id.", 0)
	default:
		return s.storageProblem(ctx, err)
	}
}

// featureResponses adds the answers every feature operation can give to the
// keyed ones, so the document lists them by status (ERR-001): notFound says
// what a 404 means for the operation.
func featureResponses(api huma.API, op *huma.Operation, notFound string) {
	problem := map[string]*huma.MediaType{ProblemContentType: {
		Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "Problem"),
	}}
	op.Responses["403"] = &huma.Response{
		Description: "The key's plan lacks the operation's scope (scope-denied), or no source family that could answer is permitted to it (source-not-permitted, or terms-not-accepted when only the acceptance of terms is missing).",
		Content:     problem,
	}
	op.Responses["404"] = &huma.Response{Description: notFound, Content: problem}
	op.Responses["503"] = &huma.Response{
		Description: "Storage did not answer in time (deadline-exhausted) or is unavailable (upstream-unavailable), or the admission refused the request under load (overloaded, when admission.shed_status is 503); retryable, with Retry-After.",
		Headers:     map[string]*huma.Param{"Retry-After": {Schema: &huma.Schema{Type: huma.TypeInteger}}},
		Content:     problem,
	}
}

const featureOffDoc = "The operation is switched off on this instance (feature-disabled)."

// featuresOff answers a feature operation of an instance without storage.
func (s *server) featuresOff(ctx context.Context) error {
	if s.features == nil {
		return newProblem(ctx, SlugFeatureDisabled, "Feature operations are switched off on this instance.", 0)
	}
	return nil
}

// Near is the near-a-position input of the list operations.
type Near struct {
	At      string `query:"at" required:"true" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"52.52,13.405" doc:"The position to search around: lat,lon in decimal degrees (WGS 84)."`
	RadiusM int    `query:"radius_m" minimum:"1" maximum:"50000" doc:"Search radius in metres. Default: geo.default_radius_m."`
	Limit   int    `query:"limit" minimum:"1" maximum:"100" doc:"Most items to return. Default: pagination.default_limit."`
	point   geo.Point
}

// Resolve reads the position (huma calls it after the schema checks).
func (n *Near) Resolve(huma.Context) []error {
	p, err := geo.ParsePoint(n.At)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: n.At}}
	}
	n.point = p
	return nil
}

func (s *server) radius(r int) int {
	if r == 0 {
		return s.cfg.Geo.DefaultRadiusM
	}
	return r
}

func (s *server) limit(l int) int {
	if l == 0 {
		return s.cfg.Pagination.DefaultLimit
	}
	return l
}

// NearQuery is meta.query of a near search: the input actually answered.
type NearQuery struct {
	At      Position `json:"at"`
	RadiusM int      `json:"radiusM" doc:"The radius applied, in metres."`
}

// ----------------------------------------------------------------- charging

// ChargingInput is the input of listChargingOptions.
type ChargingInput struct {
	Near
	MinKw  float64 `query:"min_kw" minimum:"0" maximum:"1000" doc:"Only sites with at least one point of at least this power, in kW. Default 0."`
	Access string  `query:"access" enum:"public,restricted,all" default:"public" doc:"Who may use the site."`
}

// Availability is the last reported state of a charge point.
type Availability struct {
	State      string    `json:"state" enum:"available,occupied,reserved,out_of_order,unknown,other" doc:"Open enumeration; treat an unknown value as other."`
	ObservedAt time.Time `json:"observedAt" doc:"When the state was reported."`
}

// ChargePoint is one point of a charging site.
type ChargePoint struct {
	ID           string        `json:"id"`
	Current      *string       `json:"current" nullable:"true" enum:"ac,dc,other" doc:"The current delivered; null when not published."`
	MaxPowerKw   *float64      `json:"maxPowerKw" nullable:"true" doc:"The point's highest power in kW; null when not published."`
	Availability *Availability `json:"availability" doc:"The last report; null when none is published."`
}

// ChargingSummary sums up a site.
type ChargingSummary struct {
	ChargePoints int      `json:"chargePoints" doc:"How many points the site has."`
	AvailableNow *int     `json:"availableNow" nullable:"true" doc:"Points reported available within the family's freshness threshold; null when no point has such a report."`
	MaxPowerKw   *float64 `json:"maxPowerKw" nullable:"true" doc:"The highest power of any point, in kW; null when none is published."`
}

// ChargingSite is one item of listChargingOptions.
type ChargingSite struct {
	ID           string          `json:"id"`
	Name         *string         `json:"name" nullable:"true"`
	Operator     *string         `json:"operator" nullable:"true"`
	Location     Position        `json:"location"`
	DistanceM    int             `json:"distanceM" doc:"Distance from the asked position, in metres."`
	Access       string          `json:"access" enum:"public,restricted,unknown,other" doc:"Open enumeration; treat an unknown value as other."`
	Summary      ChargingSummary `json:"summary"`
	ChargePoints []ChargePoint   `json:"chargePoints"`
	SourceFamily string          `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// ChargingQuery is meta.query of listChargingOptions.
type ChargingQuery struct {
	NearQuery
	MinKw  float64 `json:"minKw"`
	Access string  `json:"access" enum:"public,restricted,all"`
}

// ChargingMeta is the meta member of listChargingOptions.
type ChargingMeta struct {
	DataMeta
	Page  Page          `json:"page"`
	Query ChargingQuery `json:"query"`
}

// ChargingEnvelope is the enveloped answer of listChargingOptions.
type ChargingEnvelope struct {
	Data  []ChargingSite `json:"data"`
	Meta  ChargingMeta   `json:"meta"`
	Links Links          `json:"links"`
}

// ChargingOutput is the response of listChargingOptions.
type ChargingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ChargingEnvelope
}

func chargingSites(a features.Answer[energy.Site]) []ChargingSite {
	// Each report is judged by its own family's threshold; every family that
	// contributed is in a.Families (ENV-003).
	thresholds := map[string]time.Duration{}
	for _, f := range a.Families {
		thresholds[f.Source.Family] = f.StaleAfter
	}
	staleAfter := func(family string) time.Duration { return thresholds[family] }
	out := make([]ChargingSite, len(a.Items))
	for i, v := range a.Items {
		sum := v.Summarise(a.At, staleAfter)
		item := ChargingSite{
			ID: v.ID, Name: v.Name, Operator: v.Operator, Location: position(v.Location), DistanceM: v.DistanceM,
			Access: string(v.Access), ChargePoints: []ChargePoint{}, SourceFamily: v.Family,
			Summary: ChargingSummary{ChargePoints: sum.ChargePoints, AvailableNow: sum.AvailableNow, MaxPowerKw: sum.MaxPowerKw},
		}
		for _, p := range v.Points {
			cp := ChargePoint{ID: p.ID, MaxPowerKw: p.MaxPowerKw}
			if p.Current != nil {
				c := string(*p.Current)
				cp.Current = &c
			}
			if p.Availability != nil {
				cp.Availability = &Availability{State: string(p.Availability.State), ObservedAt: p.Availability.ObservedAt.UTC()}
			}
			item.ChargePoints = append(item.ChargePoints, cp)
		}
		out[i] = item
	}
	return out
}

func registerCharging(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Where to charge near a position"
	op.Description = "Charging sites within the radius of a position, nearest first, then by id, each with its points and their last reported availability. Needs the scope energy."
	op.Extensions[counterpartExtension] = "charging station search" // a keyed operation carries its scope extension already
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *ChargingInput) (*ChargingOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := energy.Query{At: in.point, RadiusM: s.radius(in.RadiusM), MinKw: in.MinKw, Access: energy.AccessFilter(in.Access), Limit: s.limit(in.Limit)}
		a, err := s.features.Charging(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &ChargingOutput{CacheControl: noStore, Body: ChargingEnvelope{
			Data: chargingSites(a),
			Meta: ChargingMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query:    ChargingQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, MinKw: q.MinKw, Access: string(q.Access)},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------------ parking

// ParkingInput is the input of listParkingOptions.
type ParkingInput struct {
	Near
	FreeMin int `query:"free_min" minimum:"0" maximum:"10000" doc:"Only sites whose live report has at least this many free spaces. Default 0, every site."`
}

// Occupancy is the last reported occupancy of a parking site.
type Occupancy struct {
	Free       *int      `json:"free" nullable:"true" doc:"Free spaces; null when not published, never 0 for unknown."`
	Total      *int      `json:"total" nullable:"true" doc:"Spaces in total; null when not published."`
	State      string    `json:"state" enum:"free,filling,full,closed,unknown,other" doc:"Open enumeration; treat an unknown value as other."`
	ObservedAt time.Time `json:"observedAt" doc:"When the occupancy was reported."`
}

// ParkingSite is one item of listParkingOptions.
type ParkingSite struct {
	ID            string     `json:"id"`
	Name          *string    `json:"name" nullable:"true"`
	Operator      *string    `json:"operator" nullable:"true"`
	Kind          string     `json:"kind" enum:"garage,lot,park_and_ride,on_street,rest_area,truck_parking,other" doc:"Open enumeration; treat an unknown value as other."`
	Location      Position   `json:"location"`
	DistanceM     int        `json:"distanceM" doc:"Distance from the asked position, in metres."`
	Occupancy     *Occupancy `json:"occupancy" doc:"The last report; null when none is published, or while the terms are not accepted."`
	TermsRequired bool       `json:"termsRequired" doc:"True when the occupancy is withheld until the family's terms are accepted."`
	SourceFamily  string     `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// ParkingQuery is meta.query of listParkingOptions.
type ParkingQuery struct {
	NearQuery
	FreeMin int `json:"freeMin"`
}

// ParkingMeta is the meta member of listParkingOptions.
type ParkingMeta struct {
	DataMeta
	Page  Page         `json:"page"`
	Query ParkingQuery `json:"query"`
}

// ParkingEnvelope is the enveloped answer of listParkingOptions.
type ParkingEnvelope struct {
	Data  []ParkingSite `json:"data"`
	Meta  ParkingMeta   `json:"meta"`
	Links Links         `json:"links"`
}

// ParkingOutput is the response of listParkingOptions.
type ParkingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ParkingEnvelope
}

func parkingSites(a features.Answer[parking.Site]) ([]ParkingSite, bool) {
	out := make([]ParkingSite, len(a.Items))
	withheld := false
	for i, v := range a.Items {
		item := ParkingSite{
			ID: v.ID, Name: v.Name, Operator: v.Operator, Kind: string(v.Kind), Location: position(v.Location),
			DistanceM: v.DistanceM, TermsRequired: v.TermsRequired, SourceFamily: v.Family,
		}
		if o := v.Occupancy; o != nil {
			item.Occupancy = &Occupancy{Free: o.Free, Total: o.Total, State: string(o.State), ObservedAt: o.ObservedAt.UTC()}
		}
		withheld = withheld || v.TermsRequired
		out[i] = item
	}
	return out, withheld
}

func registerParking(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Where to park near a position"
	op.Description = "Parking sites within the radius of a position, nearest first, then by id, each with its last reported occupancy. A site whose terms need accepting first is listed without its occupancy and marked termsRequired. Needs the scope parking."
	op.Extensions[counterpartExtension] = "parking search"
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *ParkingInput) (*ParkingOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := parking.Query{At: in.point, RadiusM: s.radius(in.RadiusM), FreeMin: in.FreeMin, Limit: s.limit(in.Limit)}
		a, err := s.features.Parking(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data, withheld := parkingSites(a)
		detail := partialDetail
		if withheld {
			detail += " " + termsDetail
		}
		return &ParkingOutput{CacheControl: noStore, Body: ParkingEnvelope{
			Data: data,
			Meta: ParkingMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, detail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query:    ParkingQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, FreeMin: q.FreeMin},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ----------------------------------------------------------------- overview

// OverviewInput is the input of getPlaceOverview.
type OverviewInput struct {
	PlaceID      string   `path:"placeId" maxLength:"64" pattern:"^[A-Za-z0-9_.-]+$" example:"pl-01-0001" doc:"The place's id."`
	Needs        []string `query:"needs" enum:"charging,parking" doc:"The parts to compute, comma-separated. Default: every part."`
	RadiusM      int      `query:"radius_m" minimum:"1" maximum:"50000" doc:"Radius around the place, in metres. Default: geo.default_radius_m."`
	PerPartLimit int      `query:"per_part_limit" minimum:"1" maximum:"20" doc:"Most items per part. Default: compose.per_part_limit."`
}

// PlaceSummary is the place an overview is about.
type PlaceSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind" enum:"municipality,district,locality,region,state,station_area,other" doc:"Open enumeration; treat an unknown value as other."`
	Location     Position `json:"location"`
	SourceFamily string   `json:"sourceFamily"`
}

// ChargingPart is the charging part of an overview.
type ChargingPart struct {
	Sites []ChargingSite `json:"sites"`
}

// ParkingPart is the parking part of an overview.
type ParkingPart struct {
	Sites []ParkingSite `json:"sites"`
}

// OverviewData is the composed answer about a place. A part that is not ok
// is absent.
type OverviewData struct {
	Place    PlaceSummary  `json:"place"`
	Charging *ChargingPart `json:"charging,omitempty" doc:"Present when the part is ok."`
	Parking  *ParkingPart  `json:"parking,omitempty" doc:"Present when the part is ok."`
}

// PartStatus is one entry of meta.parts.
type PartStatus struct {
	Part        string `json:"part" enum:"charging,parking"`
	Status      string `json:"status" enum:"ok,timeout,not_permitted,unavailable,not_requested"`
	ProblemType string `json:"problemType,omitempty" format:"uri" doc:"The problem type of a part that is not ok, where one applies."`
}

// OverviewQuery is meta.query of getPlaceOverview.
type OverviewQuery struct {
	PlaceID      string `json:"placeId"`
	RadiusM      int    `json:"radiusM"`
	PerPartLimit int    `json:"perPartLimit"`
}

// OverviewMeta is the meta member of getPlaceOverview.
type OverviewMeta struct {
	DataMeta
	Parts []PartStatus  `json:"parts" doc:"One entry per part."`
	Query OverviewQuery `json:"query"`
}

// OverviewEnvelope is the enveloped answer of getPlaceOverview.
type OverviewEnvelope struct {
	Data  OverviewData `json:"data"`
	Meta  OverviewMeta `json:"meta"`
	Links Links        `json:"links"`
}

// OverviewOutput is the response of getPlaceOverview.
type OverviewOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         OverviewEnvelope
}

// partProblem is the problem a part's error stands for.
func (s *server) partProblem(ctx context.Context, r features.PartResult) string {
	switch r.Status {
	case features.StatusNotPermitted:
		slug := SlugSourceNotPermitted
		if errors.Is(r.Err, features.ErrScopeDenied) {
			slug = SlugScopeDenied
		} else if errors.Is(r.Err, access.ErrTermsNotAccepted) {
			slug = SlugTermsNotAccepted
		}
		return requestFrom(ctx).baseURL + "/problems/" + slug
	case features.StatusTimeout:
		return requestFrom(ctx).baseURL + "/problems/" + SlugDeadlineExhausted
	case features.StatusUnavailable:
		return requestFrom(ctx).baseURL + "/problems/" + SlugUpstreamUnavailable
	default:
		return ""
	}
}

// noPartProblem is the answer when no part is ok: 403 when every asked part
// is refused under the key's terms, else 503 (FR-BRIEF-005).
func noPartProblem(ctx context.Context, parts []features.PartResult) *Problem {
	refused, timeout := true, false
	for _, r := range parts {
		refused = refused && (r.Status == features.StatusNotPermitted || r.Status == features.StatusNotRequested)
		timeout = timeout || r.Status == features.StatusTimeout
	}
	switch {
	case refused:
		return newProblem(ctx, SlugSourceNotPermitted, "No asked part is permitted to this key.", 0)
	case timeout:
		return newProblem(ctx, SlugDeadlineExhausted, "No part of the answer finished in time.", 1)
	default:
		return newProblem(ctx, SlugUpstreamUnavailable, "No part of the answer is available.", 1)
	}
}

// missingPart reports whether a part that was asked for is not ok.
func missingPart(parts []features.PartResult) bool {
	for _, r := range parts {
		if r.Status != features.StatusOK && r.Status != features.StatusNotRequested {
			return true
		}
	}
	return false
}

func registerOverview(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is around a place"
	op.Description = "A composed answer: the place, then each asked part (charging, parking) around it, each under the licence gate on its own and within its own sub-deadline (compose.part_deadline). A part that is not ok is absent from data and named in meta.parts. 200 when at least one part is ok. Needs the scope places; each part also needs its own scope."
	featureResponses(api, &op, "No place with this id (not-found), or the operation is switched off on this instance (feature-disabled).")
	op.Responses["403"].Description = "The key's plan lacks the scope places (scope-denied), no places family is permitted to it (source-not-permitted, terms-not-accepted), or no asked part is permitted (source-not-permitted)."
	op.Responses["503"].Description = "Storage did not answer in time or is unavailable for the place, or no asked part is ok (deadline-exhausted, upstream-unavailable), or the admission refused the request under load (overloaded, when admission.shed_status is 503); retryable, with Retry-After."
	huma.Register(api, op, func(ctx context.Context, in *OverviewInput) (*OverviewOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := features.OverviewQuery{PlaceID: in.PlaceID, RadiusM: s.radius(in.RadiusM), PerPartLimit: in.PerPartLimit}
		if q.PerPartLimit == 0 {
			q.PerPartLimit = s.cfg.Compose.PerPartLimit
		}
		for _, n := range in.Needs {
			if p := features.Part(n); !slices.Contains(q.Needs, p) {
				q.Needs = append(q.Needs, p)
			}
		}
		o, err := s.features.Overview(ctx, principalFrom(ctx).Key, q)
		if errors.Is(err, features.ErrNoPart) {
			return nil, noPartProblem(ctx, o.Parts)
		}
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := OverviewData{Place: PlaceSummary{
			ID: o.Place.ID, Name: o.Place.Name, Kind: string(o.Place.Kind), Location: position(o.Place.Location), SourceFamily: o.Place.Family,
		}}
		withheld := false
		if o.Charging != nil {
			data.Charging = &ChargingPart{Sites: chargingSites(*o.Charging)}
		}
		if o.Parking != nil {
			var sites []ParkingSite
			sites, withheld = parkingSites(*o.Parking)
			data.Parking = &ParkingPart{Sites: sites}
		}
		// A part asked for and not ok makes the answer partial as well
		// (FR-BRIEF-005, NFR-RES-003): it is 200 and says so in meta.warnings.
		var details []string
		if o.Partial {
			details = append(details, partialDetail)
		}
		if withheld {
			details = append(details, termsDetail)
		}
		if missingPart(o.Parts) {
			details = append(details, partDetail)
		}
		meta := OverviewMeta{
			DataMeta: s.dataMeta(ctx, o.Families, s.now(), len(details) > 0, strings.Join(details, " ")),
			Parts:    []PartStatus{},
			Query:    OverviewQuery{PlaceID: q.PlaceID, RadiusM: q.RadiusM, PerPartLimit: q.PerPartLimit},
		}
		for _, r := range o.Parts {
			meta.Parts = append(meta.Parts, PartStatus{Part: string(r.Part), Status: string(r.Status), ProblemType: s.partProblem(ctx, r)})
		}
		return &OverviewOutput{CacheControl: noStore, Body: OverviewEnvelope{Data: data, Meta: meta, Links: s.selfLink(ctx)}}, nil
	})
}
