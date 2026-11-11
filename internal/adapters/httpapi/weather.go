package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// The weather-risk and driving-rules operations (FR-WEATHER, FR-RULES).

// -------------------------------------------------------------- weather risk

// WeatherRiskInput is the input of getWeatherRisk.
type WeatherRiskInput struct {
	AtPosition
	MinLevel int      `query:"min_level" minimum:"1" maximum:"4" default:"1" doc:"Only warnings at or above this official level."`
	Hazards  []string `query:"hazards" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"Only these hazards, comma-separated; other keeps the hazards that cannot be mapped. Default: every hazard."`
}

// WeatherWarning is one official weather warning, in the issuer's words.
type WeatherWarning struct {
	ID           string      `json:"id"`
	Hazard       string      `json:"hazard" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"What it warns of. Open enumeration; treat an unknown value as other."`
	Level        int         `json:"level" minimum:"1" maximum:"4" doc:"The official level, 1 (minor) to 4 (extreme), as issued; never re-graded."`
	LevelName    string      `json:"levelName" doc:"The issuer's name of the level, as issued."`
	Issuer       string      `json:"issuer" doc:"The issuing service."`
	Headline     string      `json:"headline" doc:"The official wording, in textLanguage."`
	TextLanguage string      `json:"textLanguage" doc:"The language of headline (BCP 47)."`
	Validity     TimeWindow  `json:"validity"`
	Area         WarningArea `json:"area"`
	SourceFamily string      `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// WarningArea is the area a warning is issued for.
type WarningArea struct {
	Name     string           `json:"name" doc:"The name of the warning area that contains the position."`
	Geometry *GeoJSONGeometry `json:"geometry" doc:"The outline of the area as a GeoJSON geometry; null while the service does not serve area outlines."`
}

// GeoJSONGeometry is a GeoJSON geometry object (RFC 7946) in WGS 84.
type GeoJSONGeometry struct {
	Type        string `json:"type" enum:"Polygon,MultiPolygon" doc:"The GeoJSON geometry type."`
	Coordinates []any  `json:"coordinates" doc:"The GeoJSON coordinates of type, nested arrays of positions, longitude before latitude."`
}

// RoadWeatherObservation is one road-weather station reading.
type RoadWeatherObservation struct {
	Location        Position `json:"location"`
	SurfaceState    string   `json:"surfaceState" enum:"dry,wet,ice,snow,other" doc:"The state of the road surface. Open enumeration; treat an unknown value as other."`
	SurfaceTempC    *float64 `json:"surfaceTempC" nullable:"true" doc:"Surface temperature in degrees Celsius; null when not measured."`
	AirTempC        *float64 `json:"airTempC" nullable:"true" doc:"Air temperature in degrees Celsius; null when not measured."`
	ObservedAt      string   `json:"observedAt" format:"date-time" doc:"When the reading was taken (UTC)."`
	ObservedAtLocal string   `json:"observedAtLocal" format:"date-time" doc:"observedAt in Europe/Berlin time."`
	SourceFamily    string   `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// WeatherRiskData is the answer of getWeatherRisk.
type WeatherRiskData struct {
	Lamp        string                   `json:"lamp" enum:"green,amber,red,unknown" doc:"Over every warning in force at the position, whatever min_level and hazards keep (they filter only the list), first match: red when a warning of level 3 or 4 is in force; unknown when a weather family the key may not read was left out (meta.partial); amber for a warning of level 1 or 2, or, with no warning in force, when a weather family read has no data or stale data (meta.freshness), since no warning cannot then be told from none heard; green when no warning is in force and every family is fresh."`
	Warnings    []WeatherWarning         `json:"warnings" doc:"The warnings min_level and hazards keep, ordered by level descending, then validity.from ascending, then id; at most 100."`
	Truncated   bool                     `json:"truncated" doc:"True when more than 100 warnings answer and only the first 100 are listed; the lamp reads them all."`
	RoadWeather []RoadWeatherObservation `json:"roadWeather" doc:"Road-weather observations near a corridor; present for along questions only, so always empty on a question about one position."`
}

// WeatherRiskQuery is meta.query of getWeatherRisk.
type WeatherRiskQuery struct {
	At       Position `json:"at"`
	MinLevel int      `json:"minLevel"`
	Hazards  []string `json:"hazards" doc:"The hazards asked for; empty for every hazard."`
}

// WeatherRiskMeta is the meta member of getWeatherRisk.
type WeatherRiskMeta struct {
	DataMeta
	Query WeatherRiskQuery `json:"query"`
}

// WeatherRiskEnvelope is the enveloped answer of getWeatherRisk.
type WeatherRiskEnvelope struct {
	Data  WeatherRiskData `json:"data"`
	Meta  WeatherRiskMeta `json:"meta"`
	Links Links           `json:"links"`
}

// WeatherRiskOutput is the response of getWeatherRisk.
type WeatherRiskOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         WeatherRiskEnvelope
}

func registerWeatherRisk(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The official weather warnings at a position"
	op.Description = "Every official weather warning in force now whose area contains the position, with the issuer's level and wording unchanged, and the lamp over every warning in force there, whatever min_level and hazards keep. MCP counterpart: weather warnings check. Needs the scope weather."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *WeatherRiskInput) (*WeatherRiskOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := weather.Query{MinLevel: weather.Level(in.MinLevel)}
		q.At.Lat, q.At.Lon = in.point.Lat, in.point.Lon
		hazards := []string{}
		for _, h := range in.Hazards {
			q.Hazards = append(q.Hazards, weather.Hazard(h))
			hazards = append(hazards, h)
		}
		a, err := s.features.WeatherRisk(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		ws := make([]WeatherWarning, len(a.Items))
		for i, w := range a.Items {
			ws[i] = WeatherWarning{
				ID: w.ID, Hazard: string(w.Hazard), Level: int(w.Level), LevelName: w.LevelName, Issuer: w.Issuer,
				Headline: w.Headline, TextLanguage: w.TextLanguage, Validity: timeWindow(w.From, w.Until),
				Area: WarningArea{Name: w.AreaName}, SourceFamily: w.Family,
			}
		}
		return &WeatherRiskOutput{CacheControl: noStore, Body: WeatherRiskEnvelope{
			Data: WeatherRiskData{Lamp: string(a.Lamp), Warnings: ws, Truncated: a.HasMore, RoadWeather: []RoadWeatherObservation{}},
			Meta: WeatherRiskMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Query:    WeatherRiskQuery{At: position(q.At), MinLevel: in.MinLevel, Hazards: hazards},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------- driving rules

// DrivingRulesInput is the input of getDrivingRules.
type DrivingRulesInput struct {
	Topic string `query:"topic" required:"true" maxLength:"64" pattern:"^[a-z][a-z0-9_]*$" example:"winter_tyres" doc:"The rule topic, for example lorry_sunday_ban, holiday_ban, speed_limits, winter_tyres, emission_zones, emergency_corridor. Open enumeration."`
}

// DrivingRule is one rule of a topic.
type DrivingRule struct {
	Text       string  `json:"text"`
	LegalBasis *string `json:"legalBasis" nullable:"true" doc:"The provision the rule rests on; null when none is recorded."`
}

// DrivingRulesData is the answer of getDrivingRules.
type DrivingRulesData struct {
	Topic      string        `json:"topic"`
	Title      string        `json:"title"`
	Rules      []DrivingRule `json:"rules" doc:"In their curated order."`
	ReviewedOn string        `json:"reviewedOn" format:"date" doc:"When the topic was last reviewed."`
	Disclaimer Disclaimer    `json:"disclaimer"`
}

// Disclaimer is the sentence every rules answer carries. Its schema is an
// enumeration of that one sentence, written by hand because the enum tag
// splits on the comma in it.
type Disclaimer string

// Schema is the one-value enumeration of the disclaimer.
func (Disclaimer) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Enum: []any{rules.Disclaimer},
		Description: "Always this sentence: the rules are informational, not legal advice."}
}

// DrivingRulesEnvelope is the enveloped answer of getDrivingRules.
type DrivingRulesEnvelope struct {
	Data  DrivingRulesData `json:"data"`
	Meta  DataMeta         `json:"meta"`
	Links Links            `json:"links"`
}

// DrivingRulesOutput is the response of getDrivingRules.
type DrivingRulesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         DrivingRulesEnvelope
}

func registerDrivingRules(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The German driving rules on a topic"
	op.Description = "The curated rules on one topic, in order, with the date of their last review and the disclaimer that they are informational, not legal advice (FR-RULES-001). MCP counterpart: driving rules. Needs the scope rules."
	featureResponses(api, &op, "No rule topic with this code (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *DrivingRulesInput) (*DrivingRulesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		t, a, err := s.features.DrivingRules(ctx, principalFrom(ctx).Key, in.Topic)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		rs := make([]DrivingRule, len(t.Rules))
		for i, r := range t.Rules {
			rs[i] = DrivingRule{Text: r.Text, LegalBasis: r.LegalBasis}
		}
		return &DrivingRulesOutput{CacheControl: noStore, Body: DrivingRulesEnvelope{
			Data: DrivingRulesData{
				Topic: t.Code, Title: t.Title, Rules: rs,
				ReviewedOn: t.ReviewedOn.Format(time.DateOnly), Disclaimer: rules.Disclaimer,
			},
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}
