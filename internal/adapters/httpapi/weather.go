package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
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
	Warnings    []WeatherWarning         `json:"warnings" doc:"The warnings min_level and hazards keep, ordered by level descending, then validity.from ascending, then id; at most weather.max_warnings."`
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
	op.Description = "Every official weather warning in force now whose area contains the position, with the issuer's level and wording unchanged, as the platform's weather warnings check states them. MCP counterpart: weather warnings check. Needs the scope weather."
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
		noteWeatherLamp(ctx, a)
		ws := weatherWarnings(a.Items)
		return &WeatherRiskOutput{CacheControl: noStore, Body: WeatherRiskEnvelope{
			Data: WeatherRiskData{Warnings: ws, RoadWeather: []RoadWeatherObservation{}},
			Meta: WeatherRiskMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Query:    WeatherRiskQuery{At: position(q.At), MinLevel: in.MinLevel, Hazards: hazards},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// noteWeatherLamp tells the cache what a weather answer rests on besides the
// families it attributes: every family the lamp read, by its data; the lamp,
// which the clock alone can change; the earliest instant it does; and the
// next start or end of a warning at the position, past which the answer is
// never served, not even stale.
func noteWeatherLamp(ctx context.Context, a features.WeatherRisk) {
	noteRead(ctx, "weather", a.Read)
	noteShown(ctx, "lamp "+string(a.Lamp))
	noteChanges(ctx, a.ChangesAt)
	if a.WarningChange != nil {
		noteBound(ctx, *a.WarningChange)
	}
}

// ------------------------------------------------------------- driving rules

// DrivingRulesInput is the input of getDrivingRules.
type DrivingRulesInput struct {
	Topic string `query:"topic" required:"true" maxLength:"64" pattern:"^[a-z][a-z0-9_]*$" example:"winter_tyres" doc:"The rule topic, for example lorry_sunday_ban, holiday_ban, speed_limits, winter_tyres, emission_zones, emergency_corridor. Open enumeration."`
	At    string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"48.776,9.18" doc:"With topic lez: a position (lat,lon) checked against the published low-emission-zone boundaries: inside, in an area released at a zone's edge, outside with the nearest zone within 25 km, or unknown. Ignored for any other topic."`
	City  string `query:"city" minLength:"2" maxLength:"60" example:"Stuttgart" doc:"With topic lez: the city whose published zone is named. Ignored for any other topic."`
	point *geo.Point
}

// Resolve reads the position.
func (in *DrivingRulesInput) Resolve(huma.Context) []error {
	if in.At == "" {
		return nil
	}
	p, err := geo.ParsePoint(in.At)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: in.At}}
	}
	in.point = &p
	return nil
}

// LowEmissionZone is one published zone row of a rules answer.
type LowEmissionZone struct {
	Key          string   `json:"key" doc:"<source>/<zone id>."`
	Role         string   `json:"role" enum:"zone,exempt_road,transfer" doc:"A zone, a road the zone does not apply to, or an area released at its edge."`
	Name         string   `json:"name"`
	City         *string  `json:"city" nullable:"true"`
	Publisher    *string  `json:"publisher" nullable:"true"`
	Inside       bool     `json:"inside" doc:"Whether the position lies in it (its boundary counts); false without a position."`
	DistanceM    int      `json:"distanceM" doc:"0 when inside or without a position, else the distance to it in metres."`
	SameZone     []string `json:"sameZone" doc:"Other publishers' rows of the same zone in this answer (they share at least half of the smaller one's area)."`
	SourceFamily string   `json:"sourceFamily"`
}

// ZoneVerdict is the zone answer of a position.
type ZoneVerdict struct {
	Kind       string  `json:"kind" enum:"inside,transfer,outside,unknown" doc:"inside a zone; in an area released at its edge; outside (zone names the nearest within 25 km); unknown: none within 25 km, which is not that there is no zone."`
	Zone       *string `json:"zone" nullable:"true" doc:"The key of the zone it is inside, or of the nearest."`
	Transfer   *string `json:"transfer" nullable:"true" doc:"The key of the released area it is in."`
	ExemptRoad *string `json:"exemptRoad" nullable:"true" doc:"The key of an exempt road within 60 m."`
	ZoneFacts
}

func zoneKey(z *rules.Zone) *string {
	if z == nil {
		return nil
	}
	return &z.Key
}

// DrivingRule is one rule of a topic.
type DrivingRule struct {
	ID            string  `json:"id" doc:"The rule's key, stable across reviews."`
	Text          string  `json:"text"`
	LegalBasis    *string `json:"legalBasis" nullable:"true" doc:"The provision the rule rests on; null when none is recorded."`
	LegalBasisURL *string `json:"sourceUrl" nullable:"true" doc:"Where the provision is published; null when none is recorded."`
	ReviewedOn    string  `json:"reviewedOn" format:"date" doc:"When the rule was last read against its source."`
	ToVerify      *string `json:"unconfirmed" nullable:"true" doc:"What of the rule is not confirmed, to check with the source; null when all is."`
}

// DrivingRulesData is the answer of getDrivingRules.
type DrivingRulesData struct {
	Topic      string            `json:"topic"`
	Title      string            `json:"title"`
	Rules      []DrivingRule     `json:"rules" doc:"In their curated order."`
	ReviewedOn string            `json:"reviewedOn" format:"date" doc:"The oldest review of its rules: the topic is as current as its least recently read rule."`
	Disclaimer Disclaimer        `json:"disclaimer"`
	Zones      []LowEmissionZone `json:"zones,omitempty" doc:"With topic lez and a position or city: the published zone rows, inside first, then by distance."`
	Verdict    *ZoneVerdict      `json:"zoneVerdict,omitempty" doc:"With topic lez and a position: the platform's verdict."`
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
		noteRules(ctx)
		var zones []LowEmissionZone
		var verdict *ZoneVerdict
		if t.Code == "lez" && (in.point != nil || in.City != "") {
			z, err := s.features.LowEmissionZones(ctx, principalFrom(ctx).Key, rules.ZoneQuery{At: in.point, City: in.City})
			if err != nil {
				return nil, s.featureProblem(ctx, err)
			}
			zones = []LowEmissionZone{}
			for _, r := range z.Zones {
				zones = append(zones, LowEmissionZone{Key: r.Key, Role: r.Role, Name: r.Name, City: r.City, Publisher: r.Publisher,
					Inside: r.Inside, DistanceM: r.DistanceM, SameZone: append([]string{}, r.SameZone...), SourceFamily: r.Family})
			}
			if v := z.Verdict; v != nil {
				verdict = &ZoneVerdict{Kind: v.Kind, Zone: zoneKey(v.Zone), Transfer: zoneKey(v.Transfer), ExemptRoad: zoneKey(v.ExemptRoad), ZoneFacts: zoneFacts(z.Answer)}
			}
			a.Families = append(a.Families, z.Meta.Families...)
		}
		rs := make([]DrivingRule, len(t.Rules))
		for i, r := range t.Rules {
			rs[i] = DrivingRule{ID: r.ID, Text: r.Text, LegalBasis: r.LegalBasis, LegalBasisURL: r.LegalBasisURL,
				ReviewedOn: r.ReviewedOn.Format(time.DateOnly), ToVerify: r.ToVerify}
		}
		return &DrivingRulesOutput{CacheControl: noStore, Body: DrivingRulesEnvelope{
			Data: DrivingRulesData{
				Topic: t.Code, Title: t.Title, Rules: rs,
				ReviewedOn: t.ReviewedOn.Format(time.DateOnly), Disclaimer: rules.Disclaimer, Zones: zones, Verdict: verdict,
			},
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// weatherWarnings are the answers of the warnings.
func weatherWarnings(items []weather.Warning) []WeatherWarning {
	ws := make([]WeatherWarning, len(items))
	for i, w := range items {
		ws[i] = WeatherWarning{
			ID: w.ID, Hazard: string(w.Hazard), Level: int(w.Level), LevelName: w.LevelName, Issuer: w.Issuer,
			Headline: w.Headline, TextLanguage: w.TextLanguage, Validity: timeWindow(w.From, w.Until),
			Area: WarningArea{Name: w.AreaName}, SourceFamily: w.Family,
		}
	}
	return ws
}
