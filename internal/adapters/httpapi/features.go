package httpapi

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
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
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// Features is the port to the feature use cases.
type Features interface {
	Charging(ctx context.Context, key access.Record, q energy.Query) (features.Answer[energy.Site], error)
	Parking(ctx context.Context, key access.Record, q parking.Query) (features.Answer[parking.Site], error)
	Overview(ctx context.Context, key access.Record, q features.OverviewQuery) (features.Overview, error)
	MotorwayConditions(ctx context.Context, key access.Record, q roads.CorridorQuery) (features.Conditions, error)
	RoadConditions(ctx context.Context, key access.Record, q roads.AreaQuery) (features.Conditions, error)
	RoadSituation(ctx context.Context, key access.Record, id string) (features.Answer[roads.Situation], error)
	WeatherRisk(ctx context.Context, key access.Record, q weather.Query) (features.WeatherRisk, error)
	DrivingRules(ctx context.Context, key access.Record, topic string) (rules.Topic, features.Answer[rules.Rule], error)
	LowEmissionZones(ctx context.Context, key access.Record, q rules.ZoneQuery) (features.ZonesAnswer, error)
	LorryRules(ctx context.Context, key access.Record, q roads.LorryQuery) (features.LorryAnswer, error)
	Motorways(ctx context.Context, key access.Record, q roads.CatalogueQuery) (features.MotorwaysAnswer, error)
	Sources(ctx context.Context, key access.Record, domain string) ([]provenance.Family, features.Answer[struct{}], error)
	Stations(ctx context.Context, key access.Record, q transit.Query) (features.Answer[transit.Station], error)
	Departures(ctx context.Context, key access.Record, q transit.DepartureQuery) (features.Board, error)
	StopBoards(ctx context.Context, key access.Record, q transit.StopBoardsQuery) (features.StopBoards, error)
	StationPage(ctx context.Context, key access.Record, q features.StationPageQuery) (features.StationPage, error)
	Facilities(ctx context.Context, key access.Record, q transit.FacilityQuery) (features.StationFacilities, error)
	RequestRefresh(ctx context.Context, key access.Record, a refresh.Ask) (refresh.Request, error)
	Refresh(ctx context.Context, key access.Record, id string) (refresh.Request, error)
	ParkingBays(ctx context.Context, key access.Record, q parking.BayQuery) (features.BaysAnswer, error)
	TrainBoard(ctx context.Context, key access.Record, eva string) (features.TrainBoardAnswer, error)
	Places(ctx context.Context, key access.Record, q places.Query) (features.Answer[places.Place], error)
	FuelStations(ctx context.Context, key access.Record, q energy.FuelQuery) (features.FuelStations, error)
	FuelStation(ctx context.Context, key access.Record, id string, held time.Duration) (energy.FuelStationPage, features.Answer[struct{}], error)
	ChargingSite(ctx context.Context, key access.Record, id string, statusMaxAge time.Duration) (features.Answer[energy.Site], error)
	HydrogenStations(ctx context.Context, key access.Record, q energy.HydrogenQuery) (features.HydrogenStations, error)
	SpeedCameras(ctx context.Context, key access.Record, q roads.SpeedQuery) (features.SpeedAnswer, error)
	Disruptions(ctx context.Context, key access.Record, q transit.DisruptionQuery) (features.DisruptionAnswer, error)
	Nearby(ctx context.Context, key access.Record, q features.NearbyQuery) (features.NearbyAnswer, error)
	Sharing(ctx context.Context, key access.Record, q transit.SharingQuery) (features.SharingAnswer, error)
	Addresses(ctx context.Context, key access.Record, q places.AddressQuery) (features.AddressAnswer, error)
	Pois(ctx context.Context, key access.Record, q places.PoiQuery) (features.PoiAnswer, error)
	Location(ctx context.Context, key access.Record, q places.LocationQuery) (features.LocationAnswer, error)
	National(ctx context.Context, key access.Record, fuelWindow time.Duration) (features.NationalAnswer, error)
	AffectedStations(ctx context.Context, key access.Record, limit int) ([]transit.Affected, features.Answer[struct{}], error)
	StateWarnings(ctx context.Context, key access.Record, land string) ([]weather.StateWarning, features.Answer[struct{}], error)
	StateAlerts(ctx context.Context, key access.Record, land string, limit int) (features.StateAlertsAnswer, error)
	PlacesCoverage(ctx context.Context, key access.Record) (places.Coverage, features.Answer[struct{}], error)
	WeatherCell(ctx context.Context, key access.Record, target string, at *geo.Point, minLevel int) (features.CellAnswer, error)
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
	{"MotorwayData", "motorway"},
	{"MotorwayData", "roadStatus"},
	{"MotorwayBlock", "measured"},
	{"MotorwayEntryView", "jam"},
	{"MotorwayJam", "trend"},
	{"MotorwayJam", "usual"},
	{"MotorwayJam", "howLong"},
	{"RefreshData", "queue"},
	{"HydrogenData", "nearest"},
	{"DisruptionData", "punctuality"},
	{"DisruptionData", "alertsCoverage"},
	{"LocationData", "gemeinde"},
	{"LocationData", "junction"},
	{"StationFacilitiesData", "platforms"},
	{"StationPageEnvelope", "data"},
	{"PageFacilities", "location"},
	{"PlatformsStation", "station"},
	{"PlatformAccess", "heightCm"},
	{"DisruptionData", "line"},
	{"SpeedCamera", "location"},
	{"LocationZone", "plaque"},
	{"LocationData", "address"},
	{"LocationData", "municipality"},
	{"LocationData", "district"},
	{"LocationData", "state"},
	{"LocationData", "lowEmissionZone"},
	{"MotorwayData", "coverage"},
	{"ParkingSite", "occupancy"},
	{"ParkingListing", "occupancy"},
	{"ParkingMeta", "around"},
	{"RoadSituation", "location"},
	{"RoadSituation", "lanes"},
	{"RoadSituation", "position"},
	{"StationsQuery", "at"},
	{"WarningArea", "geometry"},
	{"WeatherWarning", "english"},
	{"ZoneVerdict", "plaque"},
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
	inlined := []string{}
	for _, n := range nullableObjects {
		// Every operation of the catalogue is always registered, so the
		// schema exists. The referenced object is written inline, typed
		// object or null.
		s := reg[n.schema]
		ref := s.Properties[n.member]
		name := strings.TrimPrefix(ref.Ref, schemaPrefix)
		inline := *reg[name]
		inline.Nullable, inline.Description = true, ref.Description
		s.Properties[n.member] = &inline
		inlined = append(inlined, name)
	}
	// An inlined object that no operation reaches by reference any more
	// leaves components.schemas. Left registered, it makes huma marshal the
	// whole document twice, once to find the unused schemas and once
	// without them: most of what the first /v1/openapi.json costs (TST-012).
	reached := reachedSchemas(api.OpenAPI())
	for _, name := range inlined {
		if !reached[name] {
			delete(reg, name)
		}
	}
}

// everySchemaReached refuses a document whose components.schemas holds a
// schema no operation reaches: huma would marshal the whole document twice
// on every first /v1/openapi.json to leave it out (TST-012).
func everySchemaReached(o *huma.OpenAPI) error {
	reached := reachedSchemas(o)
	var unreached []string
	for name := range o.Components.Schemas.Map() {
		if !reached[name] {
			unreached = append(unreached, name)
		}
	}
	if len(unreached) > 0 {
		slices.Sort(unreached)
		return fmt.Errorf("httpapi: no operation reaches the schemas %v; huma would marshal the document twice to leave them out", unreached)
	}
	return nil
}

// reachedSchemas is the set of components.schemas names the operations
// reach by reference, directly or through other schemas.
func reachedSchemas(o *huma.OpenAPI) map[string]bool {
	reg := o.Components.Schemas.Map()
	reached := map[string]bool{}
	var walk func(s *huma.Schema)
	walk = func(s *huma.Schema) {
		if s == nil {
			return
		}
		if s.Ref != "" {
			name := strings.TrimPrefix(s.Ref, schemaPrefix)
			if !reached[name] {
				reached[name] = true
				walk(reg[name])
			}
			return
		}
		for _, p := range s.Properties {
			walk(p)
		}
		walk(s.Items)
		if a, ok := s.AdditionalProperties.(*huma.Schema); ok {
			walk(a)
		}
		for _, l := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
			for _, x := range l {
				walk(x)
			}
		}
		walk(s.Not)
	}
	content := func(c map[string]*huma.MediaType) {
		for _, m := range c {
			walk(m.Schema)
		}
	}
	params := func(ps []*huma.Param) {
		for _, p := range ps {
			walk(p.Schema)
		}
	}
	body := func(b *huma.RequestBody) {
		if b != nil {
			content(b.Content)
		}
	}
	response := func(r *huma.Response) {
		content(r.Content)
		for _, h := range r.Headers {
			walk(h.Schema)
		}
	}
	for _, item := range o.Paths {
		params(item.Parameters)
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op == nil {
				continue
			}
			params(op.Parameters)
			body(op.RequestBody)
			for _, r := range op.Responses {
				response(r)
			}
		}
	}
	// The shared definitions answers refer to (the shared headers among
	// them): huma counts every reference of the document, these too.
	for _, h := range o.Components.Headers {
		walk(h.Schema)
	}
	for _, p := range o.Components.Parameters {
		walk(p.Schema)
	}
	for _, b := range o.Components.RequestBodies {
		body(b)
	}
	for _, r := range o.Components.Responses {
		response(r)
	}
	return reached
}

func position(p geo.Point) Position {
	r := p.Rounded()
	return Position{Lat: r.Lat, Lon: r.Lon}
}

// FreshnessEntry is one entry of meta.freshness.
type FreshnessEntry struct {
	Family      string     `json:"family" doc:"The source family."`
	DataAsOf    *time.Time `json:"dataAsOf" nullable:"true" doc:"The instant up to which the family's data was last known current. It advances when the data is confirmed unchanged, so it is not the instant of the last change in the data. Null when the family's data has never been known current."`
	AgeS        *int64     `json:"ageS" nullable:"true" doc:"generatedAt minus dataAsOf, in whole seconds; null when dataAsOf is."`
	StaleAfterS int64      `json:"staleAfterS" doc:"The family's design threshold: data older than this is stale. A family with no fixed interval, which is never stale, has 9007199254740991 (2^53 - 1, the largest integer every JSON reader holds exactly), which no age passes: its age is shown only."`
	Stale       bool       `json:"stale" doc:"True when ageS is above staleAfterS, or dataAsOf is unknown; never for a family with no fixed interval."`
	// LastCheckedAt is the family's newest read: when its source was last
	// asked and its data found current, a new or a confirmed unchanged copy.
	LastCheckedAt *time.Time `json:"lastCheckedAt" nullable:"true" doc:"When the family's source was last asked and its data found current, whether it brought a new copy or confirmed the data unchanged (UTC); null when it never was. Beside dataAsOf it says how recently an unchanged answer was confirmed."`
}

// neverStaleS is staleAfterS of a family with no fixed interval: the /v1
// contract states a threshold for every family (VER-001), and no age passes
// this one, so ageS > staleAfterS still says whether the family is stale.
const neverStaleS int64 = 1<<53 - 1

// staleAfterS is the threshold of a freshness entry: its own, or
// neverStaleS for a family that is never stale.
func staleAfterS(fr provenance.Freshness) int64 {
	if fr.StaleAfterS == nil {
		return neverStaleS
	}
	return *fr.StaleAfterS
}

// Notes are the verbatim licence notes of the contributing families.
type Notes struct {
	Condition []string `json:"condition" doc:"Conditions the data may only be used under; show them with the data. A dated condition names the Europe/Berlin day its data was read."`
	Purpose   []string `json:"purpose" doc:"Purposes the data may only be used for; show them with the data."`
}

// DataWarning is one entry of meta.warnings of a data answer.
type DataWarning struct {
	Code   string `json:"code" enum:"partial,data_stale,served_stale" doc:"Stable warning code; clients tolerate codes they do not know."`
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
		ShareAlikeOffer: r.ShareAlikeOffer, TermsURL: r.TermsURL, SourceURL: r.SourceURL,
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
	noteFamilies(ctx, fams)
	var fs []provenance.Freshness
	for _, f := range fams {
		fr := f.Assess(at)
		noteFreshness(ctx, f, fr, at)
		fs = append(fs, fr)
		m.Freshness = append(m.Freshness, FreshnessEntry{Family: fr.Family, DataAsOf: fr.DataAsOf, AgeS: fr.AgeS, StaleAfterS: staleAfterS(fr), Stale: fr.Stale,
			LastCheckedAt: utcOrNil(f.ReadAt)})
		m.Attribution = append(m.Attribution, attribution(f.Source))
		if !f.FilledUntil.IsZero() {
			// A filled year is wrong from the next new year on: no stored
			// answer states it past then, nor validates one that does not.
			noteBound(ctx, f.FilledUntil)
			noteShown(ctx, "filled "+f.Source.Family+" until "+f.FilledUntil.Format(time.RFC3339))
		}
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

// noteFreshness tells the cache how the family's freshness entry changes with
// the clock: its stale flag is part of the ETag, and a family not yet stale
// turns stale once its age passes staleAfterS whole seconds. Data dated after
// at counts from its own instant (Assess gives it the age zero until then).
func noteFreshness(ctx context.Context, f provenance.Family, fr provenance.Freshness, at time.Time) {
	noteShown(ctx, "freshness "+fr.Family+" stale="+strconv.FormatBool(fr.Stale))
	known := f.KnownCurrent()
	if fr.Stale || known == nil {
		return
	}
	if known.After(at) {
		noteChangesAt(ctx, *known)
	}
	if t, ok := f.TurnsStale(); ok {
		noteChangesAt(ctx, t)
	}
}

// featureProblem maps a refusal of the feature use cases to its problem.
func (s *server) featureProblem(ctx context.Context, err error) *Problem {
	if p := termsProblem(ctx, err); p != nil {
		return p
	}
	switch {
	case errors.Is(err, access.ErrTermsNotAccepted):
		return newProblem(ctx, SlugTermsNotAccepted, "Every source family that could answer needs its terms accepted first.", 0)
	case errors.Is(err, access.ErrNonePermitted):
		return newProblem(ctx, SlugSourceNotPermitted, "No source family that could answer is permitted to this key.", 0)
	case errors.Is(err, access.ErrForbiddenItem):
		gateViolations.Add(1)
		s.log.ErrorContext(ctx, "licence gate post-check aborted an answer", "requestId", RequestIDFrom(ctx), "error", err)
		return newProblem(ctx, SlugInternal, "An unexpected fault occurred; quote the request id.", 0)
	case errors.Is(err, transit.ErrNotFound):
		return newProblem(ctx, SlugNotFound, "No station with this id.", 0)
	case errors.Is(err, energy.ErrNotFound):
		return newProblem(ctx, SlugNotFound, "No filling station or charging site with this id.", 0)
	case errors.Is(err, places.ErrNotFound):
		return newProblem(ctx, SlugNotFound, "No place with this id.", 0)
	case errors.Is(err, roads.ErrUnknownRoad):
		return newProblem(ctx, SlugNotFound, "No road with this reference.", 0)
	case errors.Is(err, roads.ErrUnknownSituation):
		return newProblem(ctx, SlugNotFound, "No situation with this id.", 0)
	case errors.Is(err, rules.ErrUnknownTopic):
		return newProblem(ctx, SlugNotFound, "No rule topic with this code.", 0)
	case errors.Is(err, weather.ErrNoCell):
		return newProblem(ctx, SlugNotFound, "No DWD warn cell is held at this place or under this name.", 0)
	case errors.Is(err, features.ErrAreaNotCell):
		return newProblem(ctx, SlugInvalidParameter, "A Land, Regierungsbezirk or Kreis takes no single warn cell: name a town in it, or give a warn cell.", 0)
	case errors.Is(err, weather.ErrLevel):
		// A stored level outside 1 to 4 is never re-graded (FR-WEATHER-002):
		// the answer fails and the log names the warning.
		s.log.ErrorContext(ctx, "a stored weather warning level is outside 1 to 4", "requestId", RequestIDFrom(ctx), "error", err)
		return newProblem(ctx, SlugInternal, "An unexpected fault occurred; quote the request id.", 0)
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
		Description: "Storage did not answer in time (deadline-exhausted) or is unavailable (upstream-unavailable), or the admission refused the request under load (overloaded, when admission.shed_status is 503); retryable, with Retry-After. While storage cannot answer, a stored answer of the same request read no longer than the stale allowance of its domain (cache.stale_if_error) ago is sent instead, as a 200 with the served_stale warning and its Age.",
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

// AtPosition is the at query parameter every position operation shares.
type AtPosition struct {
	At    string `query:"at" required:"true" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"52.52,13.405" doc:"The position: lat,lon in decimal degrees (WGS 84)."`
	point geo.Point
}

// Resolve reads the position (huma calls it after the schema checks).
func (a *AtPosition) Resolve(huma.Context) []error {
	p, err := geo.ParsePoint(a.At)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: a.At}}
	}
	a.point = p
	return nil
}

// Near is the near-a-position input of the list operations.
type Near struct {
	AtPosition
	RadiusM int `query:"radius_m" minimum:"1" maximum:"50000" doc:"Search radius in metres. Default: geo.default_radius_m."`
	Limit   int `query:"limit" minimum:"1" maximum:"100" doc:"Most items to return. Default: pagination.default_limit."`
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
	MinKw         float64  `query:"min_kw" minimum:"0" maximum:"1000" doc:"Only sites with at least one point of at least this power, in kW, and of each only those points. Default 0."`
	MaxKw         float64  `query:"max_kw" minimum:"0" maximum:"1000" doc:"Only points of at most this power, in kW, with a stated power; never below min_kw. Default 0, no upper limit."`
	Connector     string   `query:"connector" enum:"ccs2,type2,chademo,mcs" doc:"Only points with this plug: ccs2 (CCS Combo 2), type2 (Typ 2, AC), chademo or mcs (Megawatt Charging System, for lorries)."`
	Operator      string   `query:"operator" minLength:"1" maxLength:"80" doc:"Only sites of this operator brand, as a person names it. Spellings of one brand match together: the brand key is the first word of three letters or more that is not a legal-form or generic word (GmbH, Stadtwerke, Mobility, ...). Sites whose source names no operator are left out."`
	Conditions    []string `query:"conditions" enum:"open247,card,free,public" doc:"Only sites with one point that meets every listed term: open247 (open around the clock), card (pay by card at the charger), free (free of charge; a zero ad-hoc price counts for 90 days after its update), public (open to the public, not customers only)."`
	OnlyAvailable bool     `query:"only_available" doc:"Only sites with a point reported available now. The nearest 100 sites are examined; meta.dropped counts the others by reason, so a site whose status is unknown is never silently hidden. Default false."`
	Access        string   `query:"access" enum:"public,restricted,all" default:"public" doc:"Who may use the site."`
	operatorKey   string
}

// Resolve reads the position, refuses a lower power limit above the upper
// one and an operator that names no brand, and keeps the operator's key.
func (in *ChargingInput) Resolve(ctx huma.Context) []error {
	errs := in.AtPosition.Resolve(ctx)
	if in.MaxKw > 0 && in.MinKw > in.MaxKw {
		errs = append(errs, &huma.ErrorDetail{Location: "query.max_kw", Message: "the upper power limit is below min_kw", Value: in.MaxKw})
	}
	if u := ctx.URL(); in.Operator != "" || u.Query().Has("operator") {
		k, ok := energy.OperatorKey(in.Operator)
		if !ok {
			errs = append(errs, &huma.ErrorDetail{Location: "query.operator", Message: "names no operator, only legal-form or generic words: name the brand", Value: in.Operator})
		}
		in.operatorKey = k
	}
	return errs
}

// Availability is the last reported state of a charge point.
type Availability struct {
	State      string    `json:"state" enum:"available,occupied,reserved,out_of_order,unknown,other,stale" doc:"Open enumeration; treat an unknown value as other. reserved is never sent: a point reported reserved is answered occupied, as the MCP capability answers it. stale: the report is older than the status age limit (30 minutes by default) or than its family's freshness threshold, and is not counted in summary.availableNow."`
	ObservedAt time.Time `json:"observedAt" doc:"When the state was reported."`
}

// ChargePoint is one point of a charging site.
type ChargePoint struct {
	ID           string        `json:"id"`
	Current      *string       `json:"current" nullable:"true" enum:"ac,dc,other" doc:"The current delivered; null when not published."`
	MaxPowerKw   *float64      `json:"maxPowerKw" nullable:"true" doc:"The point's highest power in kW; null when not published."`
	Availability *Availability `json:"availability" doc:"The last report; null when none is published, or when the last one says nothing (its state unknown or not a known code)."`
}

// ChargingSummary sums up a site.
type ChargingSummary struct {
	ChargePoints int      `json:"chargePoints" doc:"How many points are listed: every point of the site, or under min_kw those of at least that power."`
	AvailableNow *int     `json:"availableNow" nullable:"true" doc:"Listed points whose report is available and not stale; null when no listed point has a report that says something and is not stale."`
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
	MinKw         float64  `json:"minKw"`
	MaxKw         *float64 `json:"maxKw,omitempty"`
	Connector     string   `json:"connector,omitempty"`
	Operator      string   `json:"operator,omitempty" doc:"The asked operator's brand key."`
	Conditions    []string `json:"conditions,omitempty"`
	OnlyAvailable bool     `json:"onlyAvailable"`
	Access        string   `json:"access" enum:"public,restricted,all"`
}

// ChargingDropped counts the examined sites an only-available answer left
// out, by reason.
type ChargingDropped struct {
	NotFree  int `json:"notFree" doc:"Every live report says occupied or out of order."`
	Stale    int `json:"stale" doc:"Reports only older than their limit."`
	NoStatus int `json:"noStatus" doc:"No report at all."`
}

// ChargingMeta is the meta member of listChargingOptions.
type ChargingMeta struct {
	DataMeta
	Page     Page             `json:"page"`
	Query    ChargingQuery    `json:"query"`
	Dropped  *ChargingDropped `json:"dropped,omitempty" doc:"With only_available: the examined sites left out."`
	InRadius *int             `json:"inRadius,omitempty" doc:"Only when data is empty: how many charging sites the key may read within the radius, every filter dropped (the platform's \"I know N charging sites in that circle\")."`
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

func chargingSites(ctx context.Context, a features.Answer[energy.Site], maxAge time.Duration) []ChargingSite {
	noteNextReport(ctx, a.NextReport)
	// Each report is judged by the status age limit and its own family's
	// threshold, whichever is shorter (energy.StatusLimit); every family that
	// contributed is in a.Families (ENV-003).
	limit := features.StatusLimits(maxAge, a.Families)
	out := make([]ChargingSite, len(a.Items))
	for i, v := range a.Items {
		sum := v.Summarise(a.At, limit)
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
			if r := p.Availability; r != nil && r.Says() {
				cp.Availability = &Availability{State: string(r.Shown(a.At, limit(r.Family))), ObservedAt: r.ObservedAt.UTC()}
				// A live report is shown as stale once it is older than its
				// limit: the cached answer ends then, and the shown state is
				// part of its ETag.
				noteShown(ctx, "charging "+v.ID+"/"+p.ID+"="+cp.Availability.State)
				if r.Live(a.At, limit(r.Family)) {
					noteChangesAt(ctx, r.ObservedAt.Add(limit(r.Family)+time.Nanosecond))
				}
			}
			item.ChargePoints = append(item.ChargePoints, cp)
		}
		out[i] = item
	}
	return out
}

func positive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func chargingDropped(d *energy.Dropped) *ChargingDropped {
	if d == nil {
		return nil
	}
	return &ChargingDropped{NotFree: d.NotFree, Stale: d.Stale, NoStatus: d.NoStatus}
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
		q := energy.Query{At: in.point, RadiusM: s.radius(in.RadiusM), MinKw: in.MinKw, Access: energy.AccessFilter(in.Access), Limit: s.limit(in.Limit),
			Filter: energy.Filter{Connector: in.Connector, MaxKw: in.MaxKw, OperatorKey: in.operatorKey, Terms: in.Conditions,
				OnlyAvailable: in.OnlyAvailable, StatusMaxAge: s.cfg.Charging.StatusMaxAge}}
		a, err := s.features.Charging(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &ChargingOutput{CacheControl: noStore, Body: ChargingEnvelope{
			Data: chargingSites(ctx, a, s.cfg.Charging.StatusMaxAge),
			Meta: ChargingMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query: ChargingQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, MinKw: q.MinKw, Access: string(q.Access),
					MaxKw: positive(q.Filter.MaxKw), Connector: q.Filter.Connector, Operator: q.Filter.OperatorKey, Conditions: q.Filter.Terms,
					OnlyAvailable: q.Filter.OnlyAvailable},
				Dropped:  chargingDropped(a.Dropped),
				InRadius: a.InRadius,
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------------ parking

// ParkingFilters are the filters a parking search takes, near a position
// or along a motorway.
type ParkingFilters struct {
	FreeMin    int      `query:"free_min" minimum:"0" maximum:"10000" doc:"Only sites whose live report has at least this many free spaces. Default 0, every site."`
	Kind       string   `query:"kind" enum:"car_park,park_and_ride,truck,rest_area" doc:"Only sites of this kind as their source stores it: car_park, park_and_ride or truck. rest_area is accepted and answered with every kind, because no source classifies a rest area. Default: every kind."`
	Facilities []string `query:"facilities" maxItems:"5" uniqueItems:"true" enum:"toilet,accessible_toilet,shower,restaurant,snack,cafe,shop,fuel,charging,drinking_water,picnic,playground,wifi,security" doc:"Only sites whose own publisher states every one of these amenities, comma-separated. An amenity nobody stated is unknown, and such a site is left out."`
}

// query is the parking query of the filters.
func (f ParkingFilters) query(q parking.Query) parking.Query {
	q.FreeMin, q.Kind, q.Amenities = f.FreeMin, f.Kind, f.Facilities
	return q
}

// ParkingInput is the input of listParkingOptions.
type ParkingInput struct {
	Near
	TermsAcceptance
	ParkingFilters
}

// Resolve reads the position and the accepted terms: both embedded inputs
// resolve, and a promoted method would name only one of them.
func (in *ParkingInput) Resolve(ctx huma.Context) []error {
	return append(in.AtPosition.Resolve(ctx), in.TermsAcceptance.Resolve(ctx)...)
}

// Occupancy is the last reported occupancy of a parking site.
type Occupancy struct {
	Free       *int      `json:"free" nullable:"true" doc:"Free spaces; null when not published, never 0 for unknown."`
	Total      *int      `json:"total" nullable:"true" doc:"Spaces in total; null when not published."`
	State      string    `json:"state" enum:"free,filling,full,closed,unknown,other" doc:"Open enumeration; treat an unknown value as other."`
	ObservedAt time.Time `json:"observedAt" doc:"When the occupancy was reported."`
}

// ParkingListing is one parking site as the platform states it, with or
// without a distance: an item of listMotorwayParking and of the nearby
// answer.
type ParkingListing struct {
	ID              string           `json:"id"`
	Name            *string          `json:"name" nullable:"true"`
	Operator        *string          `json:"operator" nullable:"true"`
	Kind            string           `json:"kind" enum:"garage,lot,park_and_ride,on_street,rest_area,truck_parking,other" doc:"Open enumeration; treat an unknown value as other."`
	Location        Position         `json:"location"`
	DistanceM       *int             `json:"distanceM" nullable:"true" doc:"Distance from the asked position, in metres; null along a motorway (no point was asked) and for a site whose terms need accepting (a Toll Collect site), which the platform names without a distance."`
	Occupancy       *Occupancy       `json:"occupancy" doc:"The last report; null when none is published, or while the terms are not accepted."`
	TermsRequired   bool             `json:"termsRequired" doc:"True when the occupancy is withheld until the family's terms are accepted."`
	SourceFamily    string           `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
	Amenities       ParkingAmenities `json:"amenities" doc:"The amenities the site's own publisher states it has and states it lacks; an amenity in neither is unknown, never absent."`
	NearbyAmenities []NearbyAmenity  `json:"nearbyAmenities" nullable:"true" doc:"What OpenStreetMap maps near the site (500 m around a city car park, park and ride or other site, else 300 m; along a motorway 300 m): the nearest named entry of each amenity, none the site's publisher states, nearest first, five at most; null when not read (a nearby search, a place overview)."`
}

// ParkingAmenities are a site's stated amenities, in the platform's order.
type ParkingAmenities struct {
	Present []string `json:"present" doc:"Stated present."`
	Absent  []string `json:"absent" doc:"Stated absent."`
}

// parkingAmenities states a site's amenities.
func parkingAmenities(s parking.Stated) ParkingAmenities {
	out := ParkingAmenities{Present: s.Present, Absent: s.Absent}
	if out.Present == nil {
		out.Present = []string{}
	}
	if out.Absent == nil {
		out.Absent = []string{}
	}
	return out
}

// ParkingQuery is meta.query of listParkingOptions.
type ParkingQuery struct {
	NearQuery
	ParkingFilterQuery
}

// ParkingFilterQuery is the part of meta.query the parking filters fill.
type ParkingFilterQuery struct {
	FreeMin    int      `json:"freeMin"`
	Kind       string   `json:"kind,omitempty" doc:"The kind asked for; absent for every kind."`
	Facilities []string `json:"facilities,omitempty" doc:"The amenities asked for; absent for none."`
}

func parkingFilterQuery(q parking.Query) ParkingFilterQuery {
	return ParkingFilterQuery{FreeMin: q.FreeMin, Kind: q.Kind, Facilities: q.Amenities}
}

// ParkingMeta is the meta member of listParkingOptions.
type ParkingMeta struct {
	DataMeta
	Page  Page         `json:"page"`
	Query ParkingQuery `json:"query"`
	Terms []TermsOffer `json:"terms,omitempty" doc:"The terms of the sites marked termsRequired, with their text: show it to the end user, and pass accept_terms only once the user accepts."`
	// Around is what the platform's parking search shows beside its list.
	Around *ParkingAround `json:"around"`
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

func parkingSites(a features.Answer[parking.Site]) ([]ParkingListing, bool) {
	out := make([]ParkingListing, len(a.Items))
	withheld := false
	for i, v := range a.Items {
		item := ParkingListing{
			ID: v.ID, Name: v.Name, Operator: v.Operator, Kind: string(v.Kind), Location: position(v.Location),
			TermsRequired: v.TermsRequired, SourceFamily: v.Family, Amenities: parkingAmenities(v.Amenities), NearbyAmenities: siteNearby(a.Around, i),
		}
		if !v.Unranged {
			d := v.DistanceM
			item.DistanceM = &d
		}
		if o := v.Occupancy; o != nil {
			item.Occupancy = &Occupancy{Free: o.Free, Total: o.Total, State: string(o.State), ObservedAt: o.ObservedAt.UTC()}
		}
		withheld = withheld || v.TermsRequired
		out[i] = item
	}
	return out, withheld
}

// noteNextReport tells the cache that the answer may change when a report
// dated after the read takes effect: storage reads no report dated after now.
func noteNextReport(ctx context.Context, next *time.Time) {
	if next != nil {
		noteChangesAt(ctx, *next)
	}
}

// noteFreeMin tells the cache how a free_min answer changes with the clock:
// a site is kept only while its report is no older than its family's
// threshold, so each kept site leaves the answer once its report passes it
// (storage compares at microsecond precision). The kept sites are part of
// the ETag.
func noteFreeMin(ctx context.Context, a features.Answer[parking.Site], freeMin int) {
	if freeMin <= 0 {
		return
	}
	thresholds := map[string]*time.Duration{}
	for _, f := range a.Families {
		if !f.NeverStale {
			thresholds[f.Source.Family] = &f.StaleAfter
		}
	}
	for _, v := range a.Items {
		noteShown(ctx, "parking "+v.ID)
		if o := v.Occupancy; o != nil && thresholds[o.Family] != nil {
			noteChangesAt(ctx, o.ObservedAt.Add(*thresholds[o.Family]+time.Microsecond))
		}
	}
}

func registerParking(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Where to park near a position"
	op.Description = "Parking sites within the radius of a position, nearest first, then by id, each with its last reported occupancy. A site whose terms are not accepted (accept_terms) is not listed; meta.terms offers its terms. Needs the scope parking."
	op.Extensions[counterpartExtension] = "parking search"
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *ParkingInput) (*ParkingOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := in.query(parking.Query{At: in.point, RadiusM: s.radius(in.RadiusM), Limit: s.limit(in.Limit), CarParks: true, Around: true})
		a, meta, err := s.parkingAnswer(in.with(ctx), q)
		if err != nil {
			return nil, err
		}
		meta.Query = ParkingQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, ParkingFilterQuery: parkingFilterQuery(q)}
		meta.Around = parkingAround(a.Around)
		data, _ := rangedParkingSites(a)
		return &ParkingOutput{CacheControl: noStore, Body: ParkingEnvelope{Data: data, Meta: meta, Links: s.selfLink(ctx)}}, nil
	})
}

// parkingAnswer asks the parking search and builds its data and meta, but
// for meta.query.
func (s *server) parkingAnswer(ctx context.Context, q parking.Query) (features.Answer[parking.Site], ParkingMeta, error) {
	a, err := s.features.Parking(ctx, principalFrom(ctx).Key, q)
	if err != nil {
		return a, ParkingMeta{}, s.featureProblem(ctx, err)
	}
	withheld := slices.ContainsFunc(a.Items, func(v parking.Site) bool { return v.TermsRequired })
	noteNextReport(ctx, a.NextReport)
	noteFreeMin(ctx, a, q.FreeMin)
	detail := partialDetail
	if withheld {
		detail += " " + termsDetail
	}
	return a, ParkingMeta{
		DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, detail),
		Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
		Terms:    termsOffers(a.Terms),
	}, nil
}

// MotorwayParkingInput is the input of listMotorwayParking.
type MotorwayParkingInput struct {
	Road string `path:"road" maxLength:"5" pattern:"^[Aa] ?[0-9]{1,3}$" example:"A7" doc:"The motorway, case-insensitive, with an optional space (A7, a 7)."`
	TermsAcceptance
	ParkingFilters
	Limit int `query:"limit" minimum:"1" maximum:"100" doc:"Most items to return. Default: pagination.default_limit."`
	road  string
}

// Resolve reads the road into its canonical form and the accepted terms.
func (in *MotorwayParkingInput) Resolve(ctx huma.Context) []error {
	in.road = roads.Canonical(in.Road)
	return in.TermsAcceptance.Resolve(ctx)
}

// MotorwayParkingQuery is meta.query of listMotorwayParking.
type MotorwayParkingQuery struct {
	Road string `json:"road" doc:"The motorway without spaces."`
	ParkingFilterQuery
}

// MotorwayParkingMeta is the meta member of listMotorwayParking.
type MotorwayParkingMeta struct {
	DataMeta
	Page  Page                 `json:"page"`
	Query MotorwayParkingQuery `json:"query"`
	Terms []TermsOffer         `json:"terms,omitempty" doc:"The terms of the sites marked termsRequired, with their text: show it to the end user, and pass accept_terms only once the user accepts."`
}

// MotorwayParkingEnvelope is the enveloped answer of listMotorwayParking.
type MotorwayParkingEnvelope struct {
	Data  []ParkingListing    `json:"data"`
	Meta  MotorwayParkingMeta `json:"meta"`
	Links Links               `json:"links"`
}

// MotorwayParkingOutput is the response of listMotorwayParking.
type MotorwayParkingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         MotorwayParkingEnvelope
}

func registerMotorwayParking(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Where to park along a motorway"
	op.Description = "Parking sites along a motorway, in name order (unnamed last), then by id, each with its last reported occupancy: the sites within 1 500 m of one of the motorway's location-code points, and those their source files under the motorway. distanceM is null: no point was asked. A site whose terms are not accepted (accept_terms) is not listed; meta.terms offers its terms. Needs the scope parking."
	op.Extensions[counterpartExtension] = "parking search"
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *MotorwayParkingInput) (*MotorwayParkingOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := in.query(parking.Query{Road: in.road, Limit: s.limit(in.Limit), Around: true})
		a, meta, err := s.parkingAnswer(in.with(ctx), q)
		if err != nil {
			return nil, err
		}
		data, _ := parkingSites(a)
		return &MotorwayParkingOutput{CacheControl: noStore, Body: MotorwayParkingEnvelope{
			Data:  data,
			Meta:  MotorwayParkingMeta{DataMeta: meta.DataMeta, Page: meta.Page, Terms: meta.Terms, Query: MotorwayParkingQuery{Road: q.Road, ParkingFilterQuery: parkingFilterQuery(q)}},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ----------------------------------------------------------------- overview

// OverviewInput is the input of getPlaceOverview.
type OverviewInput struct {
	TermsAcceptance
	PlaceID      string   `path:"placeId" maxLength:"64" pattern:"^[A-Za-z0-9_.-]+$" example:"100001" doc:"The place's id."`
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

// ParkingSite is one item of listParkingOptions and of an overview's
// parking part. Both are asked around a position, so every site has its
// distance: the /v1 contract states one for each (VER-001), a Toll Collect
// site's too, which the platform names without one.
type ParkingSite struct {
	ID              string           `json:"id"`
	Name            *string          `json:"name" nullable:"true"`
	Operator        *string          `json:"operator" nullable:"true"`
	Kind            string           `json:"kind" enum:"garage,lot,park_and_ride,on_street,rest_area,truck_parking,other" doc:"Open enumeration; treat an unknown value as other."`
	Location        Position         `json:"location"`
	DistanceM       int              `json:"distanceM" doc:"Distance from the asked position, in metres."`
	Occupancy       *Occupancy       `json:"occupancy" doc:"The last report; null when none is published, or while the terms are not accepted."`
	TermsRequired   bool             `json:"termsRequired" doc:"True when the occupancy is withheld until the family's terms are accepted."`
	SourceFamily    string           `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
	Amenities       ParkingAmenities `json:"amenities" doc:"The amenities the site's own publisher states it has and states it lacks; an amenity in neither is unknown, never absent."`
	NearbyAmenities []NearbyAmenity  `json:"nearbyAmenities" nullable:"true" doc:"What OpenStreetMap maps near the site (500 m around a city car park, park and ride or other site, else 300 m; along a motorway 300 m): the nearest named entry of each amenity, none the site's publisher states, nearest first, five at most; null when not read (a nearby search, a place overview)."`
}

// rangedParkingSites are the sites of an answer around a position, each
// with its distance.
func rangedParkingSites(a features.Answer[parking.Site]) ([]ParkingSite, bool) {
	listed, withheld := parkingSites(a)
	out := make([]ParkingSite, len(listed))
	for i, v := range listed {
		out[i] = ParkingSite{ID: v.ID, Name: v.Name, Operator: v.Operator, Kind: v.Kind, Location: v.Location,
			DistanceM: a.Items[i].DistanceM, Occupancy: v.Occupancy, TermsRequired: v.TermsRequired, SourceFamily: v.SourceFamily, Amenities: v.Amenities,
			NearbyAmenities: v.NearbyAmenities}
	}
	return out, withheld
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
	Terms []TermsOffer  `json:"terms,omitempty" doc:"The terms of the parking sites marked termsRequired, with their text: show it to the end user, and pass accept_terms only once the user accepts."`
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
		o, err := s.features.Overview(in.with(ctx), principalFrom(ctx).Key, q)
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
		var terms []TermsOffer
		if o.Charging != nil {
			data.Charging = &ChargingPart{Sites: chargingSites(ctx, *o.Charging, s.cfg.Charging.StatusMaxAge)}
		}
		if o.Parking != nil {
			var sites []ParkingSite
			sites, withheld = rangedParkingSites(*o.Parking)
			terms = termsOffers(o.Parking.Terms)
			noteNextReport(ctx, o.Parking.NextReport)
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
			// A part that timed out or was unavailable may answer on the
			// next request: this answer is neither stored nor validated.
			noteDegraded(ctx)
		}
		meta := OverviewMeta{
			DataMeta: s.dataMeta(ctx, o.Families, s.at(), len(details) > 0, strings.Join(details, " ")),
			Parts:    []PartStatus{},
			Query:    OverviewQuery{PlaceID: q.PlaceID, RadiusM: q.RadiusM, PerPartLimit: q.PerPartLimit},
			Terms:    terms,
		}
		for _, r := range o.Parts {
			meta.Parts = append(meta.Parts, PartStatus{Part: string(r.Part), Status: string(r.Status), ProblemType: s.partProblem(ctx, r)})
		}
		return &OverviewOutput{CacheControl: noStore, Body: OverviewEnvelope{Data: data, Meta: meta, Links: s.selfLink(ctx)}}, nil
	})
}
