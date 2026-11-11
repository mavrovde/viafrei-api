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
	At       string   `query:"at" required:"true" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"52.52,13.405" doc:"The position: lat,lon in decimal degrees (WGS 84)."`
	MinLevel int      `query:"min_level" minimum:"1" maximum:"4" default:"1" doc:"Only warnings at or above this official level."`
	Hazards  []string `query:"hazards" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"Only these hazards, comma-separated; other keeps the hazards that cannot be mapped. Default: every hazard."`
	point    Position
}

// Resolve reads the position (huma calls it after the schema checks).
func (in *WeatherRiskInput) Resolve(ctx huma.Context) []error {
	n := Near{At: in.At}
	if errs := n.Resolve(ctx); errs != nil {
		return errs
	}
	in.point = Position{Lat: n.point.Lat, Lon: n.point.Lon}
	return nil
}

// WeatherWarning is one official weather warning, in the issuer's words.
type WeatherWarning struct {
	ID           string     `json:"id"`
	Hazard       string     `json:"hazard" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"What it warns of. Open enumeration; treat an unknown value as other."`
	Level        int        `json:"level" minimum:"1" maximum:"4" doc:"The official level, 1 (minor) to 4 (extreme), as issued; never re-graded."`
	LevelName    string     `json:"levelName" doc:"The issuer's name of the level, as issued."`
	Issuer       string     `json:"issuer" doc:"The issuing service."`
	Headline     string     `json:"headline" doc:"The official wording, in textLanguage."`
	TextLanguage string     `json:"textLanguage" doc:"The language of headline (BCP 47)."`
	Validity     TimeWindow `json:"validity"`
	Area         string     `json:"area" doc:"The name of the warning area that contains the position."`
	SourceFamily string     `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// WeatherRiskData is the answer of getWeatherRisk.
type WeatherRiskData struct {
	Lamp     string           `json:"lamp" enum:"green,amber,red" doc:"red when a warning of level 3 or 4 is in force, amber for level 2, green for level 1 or no warning; over every warning that answers the question."`
	Warnings []WeatherWarning `json:"warnings" doc:"Ordered by level descending, then validity.from ascending, then id."`
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
	op.Description = "Every official weather warning in force now whose area contains the position, with the issuer's level and wording unchanged, and the lamp over them. MCP counterpart: weather warnings check. Needs the scope weather."
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
				Area: w.AreaName, SourceFamily: w.Family,
			}
		}
		return &WeatherRiskOutput{CacheControl: noStore, Body: WeatherRiskEnvelope{
			Data: WeatherRiskData{Lamp: string(a.Lamp), Warnings: ws},
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
	Disclaimer string        `json:"disclaimer" doc:"Always: the rules are informational, not legal advice."`
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
