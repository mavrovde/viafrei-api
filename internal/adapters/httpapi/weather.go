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
	MinLevel int      `query:"min_level" minimum:"0" maximum:"4" default:"0" doc:"Only warnings at or above this official level."`
	Hazards  []string `query:"hazards" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"Only these hazards, comma-separated; other keeps the hazards that cannot be mapped. Default: every hazard."`
}

// WeatherWarning is one official weather warning, in the issuer's words.
type WeatherWarning struct {
	ID             string       `json:"id"`
	Hazard         string       `json:"hazard" enum:"thunderstorm,wind,rain,snow,ice,fog,heat,frost,uv,flood,other" doc:"What it warns of. Open enumeration; treat an unknown value as other."`
	Level          int          `json:"level" minimum:"0" maximum:"4" doc:"The official level, 1 (minor) to 4 (extreme), or 0 for an advance notice (Vorabinformation Unwetter), as issued; never re-graded. A level-0 notice does not light the lamp."`
	LevelName      string       `json:"levelName" doc:"The issuer's name of the level, as issued."`
	Issuer         string       `json:"issuer" doc:"The issuing service."`
	Headline       string       `json:"headline" doc:"The official wording, in textLanguage."`
	TextLanguage   string       `json:"textLanguage" doc:"The language of headline (BCP 47)."`
	Validity       TimeWindow   `json:"validity"`
	InForce        bool         `json:"inForce" doc:"Whether the warning is in force at the answer's instant; false for one that starts within weather.lookahead (24 hours by default), which is listed beside those in force as the platform lists it (the lamp reads only those in force)."`
	Event          *string      `json:"event" nullable:"true" doc:"The issuer's event name, as issued (the DWD's EVENT, e.g. FROST); null when not given."`
	Identifier     *string      `json:"identifier" nullable:"true" doc:"The issuer's message identifier, as issued; null when not given."`
	Published      *time.Time   `json:"published" nullable:"true" format:"date-time" doc:"When the issuer published the warning (its sent time); null when not given."`
	PublishedLocal *string      `json:"publishedLocal" nullable:"true" format:"date-time" doc:"published in Europe/Berlin time; null when published is."`
	Area           WarningArea  `json:"area"`
	SourceFamily   string       `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
	Description    *string      `json:"description" nullable:"true" doc:"The issuer's full text of the warning, in textLanguage, unaltered; null when not given."`
	Instruction    *string      `json:"instruction" nullable:"true" doc:"The issuer's advice on what to do, in textLanguage, unaltered; null when not given."`
	Unaltered      bool         `json:"unaltered" doc:"Whether description is, provably, the text the issuer published: not empty, and its UTF-8 bytes still hash (SHA-256) to the hash taken over them when it was received, as the platform checks before it quotes the issuer's wording under the issuer's source note. False when description is null or changed since; such a text may not be passed on as the issuer's."`
	Urgency        *string      `json:"urgency" nullable:"true" doc:"The issuer's CAP urgency (e.g. Immediate, Future), as issued; null when not given."`
	Certainty      *string      `json:"certainty" nullable:"true" doc:"The issuer's CAP certainty (e.g. Observed, Likely), as issued; null when not given."`
	English        *WarningText `json:"english" doc:"The issuer's own English text of the warning, its English message for the same alert, unaltered; null when the issuer gave none or its English source is not read for the key. The source is credited in meta.attribution when given."`
}

// WarningText is a warning's wording in one more language, as issued.
type WarningText struct {
	Headline     *string `json:"headline" nullable:"true" doc:"The headline, unaltered; null when not given."`
	Description  *string `json:"description" nullable:"true" doc:"The full text, unaltered; null when not given."`
	Instruction  *string `json:"instruction" nullable:"true" doc:"The advice on what to do, unaltered; null when not given."`
	Unaltered    bool    `json:"unaltered" doc:"Whether the text is, provably, the issuer's own: a headline or a description given, and the headline, description and instruction, NUL-separated, still hash (SHA-256) to the hash taken when it was received, as the platform checks before it quotes the issuer's English. False else; such a text may not be passed on as the issuer's."`
	SourceFamily string  `json:"sourceFamily" doc:"The source family of the text; its attribution is in meta.attribution."`
}

// WarningArea is the area a warning is issued for.
type WarningArea struct {
	Name     string           `json:"name" doc:"The name of the warning area that contains the position."`
	CellID   *string          `json:"cellId" nullable:"true" doc:"The issuer's warning cell of the area (the DWD warn cell id), as issued; null when not given."`
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
//
// lamp and truncated are API-only: the platform's weather warnings check
// states neither, and the /v1 contract keeps both (VER-001), so the parity
// harness leaves them out (parity.Default).
type WeatherRiskData struct {
	Lamp               string                   `json:"lamp" enum:"green,amber,red" doc:"Over every warning in force at the position, whatever min_level and hazards keep (they filter only the list), first match: red when a warning of level 3 or 4 is in force; amber for a warning of level 1 or 2, or, with no warning in force, when the sight is limited: a weather family the key may not read was left out (meta.partial), or a weather family read has no data or stale data (meta.freshness), since no warning cannot then be told from none heard; green when no warning is in force and every family is fresh."`
	Warnings           []WeatherWarning         `json:"warnings" doc:"The warnings min_level and hazards keep, ordered by level descending, then validity.from ascending, then id; at most weather.max_warnings."`
	Truncated          bool                     `json:"truncated" doc:"True when more warnings answer than weather.max_warnings and only the first are listed; the lamp reads them all."`
	RoadWeather        []RoadWeatherObservation `json:"roadWeather" doc:"Road-weather observations near a corridor; present for along questions only, so always empty on a question about one position."`
	RoadForecast       []RoadForecastStation    `json:"roadForecast" doc:"The issuer's road-surface forecast near the position, as the platform's weather warnings check reads it: the nearest open, active road-weather stations within 25 km, at most 3, nearest first, each with its forecast hours from 60 minutes before the answer's instant to 24 hours after it, earliest first. A station with no hour in that window is not listed; empty when no station of a family the key may read answers. Its sources and their conditions are in meta.attribution and meta.notes when a station is listed."`
	RoadForecastLoaded bool                     `json:"roadForecastLoaded" doc:"Whether the road-weather station catalogue is loaded and the key may read it and the forecast, as the platform's weather warnings check tells its two empty answers apart: with roadForecast empty, true means no station within 25 km answers (reason no_station_within_radius), false that the forecast is not offered here (reason not_loaded). True whenever a station is listed."`
}

// RoadForecastStation is a road-weather station and its forecast hours.
type RoadForecastStation struct {
	ID             string             `json:"id" doc:"The issuer's station id."`
	Name           string             `json:"name" doc:"The issuer's station name."`
	SegmentType    *int               `json:"segmentType" nullable:"true" doc:"The issuer's road segment type of the station, as issued; null when not given."`
	PositionSource *string            `json:"positionSource" nullable:"true" doc:"How the issuer took the station's position, as issued; null when not given."`
	Location       Position           `json:"location"`
	DistanceM      int                `json:"distanceM" doc:"Distance from the position in metres, rounded."`
	Hours          []RoadForecastHour `json:"hours" doc:"The forecast hours, earliest first."`
	SourceFamily   string             `json:"sourceFamily" doc:"The source family of the station; its attribution is in meta.attribution."`
	ForecastFamily string             `json:"forecastFamily" doc:"The source family of the forecast hours; its attribution is in meta.attribution."`
}

// RoadForecastHour is one forecast hour of a road-weather station.
type RoadForecastHour struct {
	ValidAt       time.Time `json:"validAt" format:"date-time" doc:"The hour the forecast is for (UTC)."`
	ValidAtLocal  string    `json:"validAtLocal" format:"date-time" doc:"validAt in Europe/Berlin time."`
	RunAt         time.Time `json:"runAt" format:"date-time" doc:"When the issuer's forecast run that gave this hour was made (UTC)."`
	RoadCondition *int      `json:"roadCondition" nullable:"true" minimum:"1" maximum:"6" doc:"The issuer's road-condition class, as issued: 1 dry, 2 damp, 3 snow, 4 hoarfrost, 5 freezing wet, 6 black ice; 3 and above is slippery as the platform reads it. Null when not given."`
	SurfaceTempC  *float64  `json:"surfaceTempC" nullable:"true" doc:"The forecast road-surface temperature in degrees Celsius, as issued; null when not given."`
}

// roadForecast is the answer of the road-surface forecast.
func roadForecast(stations []weather.RoadStation) []RoadForecastStation {
	out := make([]RoadForecastStation, len(stations))
	for i, st := range stations {
		hs := make([]RoadForecastHour, len(st.Hours))
		for j, h := range st.Hours {
			hs[j] = RoadForecastHour{ValidAt: h.ValidAt, ValidAtLocal: local(h.ValidAt), RunAt: h.RunAt, RoadCondition: h.Class, SurfaceTempC: h.SurfaceTempC}
		}
		out[i] = RoadForecastStation{ID: st.ID, Name: st.Name, SegmentType: st.Segment, PositionSource: st.PositionSource,
			Location: position(st.Location), DistanceM: st.DistanceM, Hours: hs, SourceFamily: st.Family, ForecastFamily: st.ForecastFamily}
	}
	return out
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
	op.Description = "Every official weather warning in force now, or starting within the next 24 hours (inForce false), whose area contains the position, with the issuer's level and wording unchanged, as the platform's weather warnings check states them, and the lamp over every warning in force there, whatever min_level and hazards keep (API-only: the check states no lamp). MCP counterpart: weather warnings check. Needs the scope weather."
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
		ws := weatherWarnings(a.Items, a.At)
		return &WeatherRiskOutput{CacheControl: noStore, Body: WeatherRiskEnvelope{
			Data: WeatherRiskData{Lamp: string(a.Lamp), Warnings: ws, Truncated: a.HasMore, RoadWeather: []RoadWeatherObservation{},
				RoadForecast: roadForecast(a.Road.Stations), RoadForecastLoaded: a.Road.Loaded},
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
	DistanceM  *int    `json:"distanceM" nullable:"true" doc:"Outside: the distance to the nearest zone in metres; null otherwise."`
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
	SourceNote    *string `json:"basisNote" nullable:"true" doc:"The note on the provision, in the asked language (German when no English one is recorded), as the platform's driving rules state it; null when none is recorded."`
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
	Zones      []LowEmissionZone `json:"zones,omitempty" doc:"With topic lez and a position: the published zone rows, inside first, then by distance."`
	Verdict    *ZoneVerdict      `json:"zoneVerdict,omitempty" doc:"With topic lez and a position: the platform's verdict."`
	ZoneCity   *ZoneCity         `json:"zoneCity,omitempty" doc:"With topic lez and a city: the curated list's entry for it, or the city uncovered (no entry is not that the city has no zone; the condition note says so)."`
}

// ZoneCity is the curated zone list's entry for an asked city.
type ZoneCity struct {
	Name     string  `json:"name" doc:"The entry's name, or the asked city when the list holds none."`
	Zone     *string `json:"zone" nullable:"true" doc:"The entry's description of its zone; null while the list carries none."`
	Plakette *string `json:"plakette" nullable:"true" doc:"The plaque the zone takes (green); null when none is stated."`
	Covered  bool    `json:"covered" doc:"Whether the list holds an entry whose zone it states."`
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
		var city *ZoneCity
		var caveat string
		if t.Code == "lez" && (in.point != nil || in.City != "") {
			z, err := s.features.LowEmissionZones(ctx, principalFrom(ctx).Key, rules.ZoneQuery{At: in.point, City: in.City})
			if err != nil {
				return nil, s.featureProblem(ctx, err)
			}
			if c := z.City; c != nil {
				city, caveat = &ZoneCity{Name: c.Name, Zone: c.Zone, Plakette: c.Plakette, Covered: c.Covered}, c.Caveat
			}
			if in.point != nil {
				zones = []LowEmissionZone{}
			}
			for _, r := range z.Zones {
				zones = append(zones, LowEmissionZone{Key: r.Key, Role: r.Role, Name: r.Name, City: r.City, Publisher: r.Publisher,
					Inside: r.Inside, DistanceM: r.DistanceM, SameZone: append([]string{}, r.SameZone...), SourceFamily: r.Family})
			}
			if v := z.Verdict; v != nil {
				verdict = &ZoneVerdict{Kind: v.Kind, Zone: zoneKey(v.Zone), Transfer: zoneKey(v.Transfer), ExemptRoad: zoneKey(v.ExemptRoad), ZoneFacts: zoneFacts(z.Answer)}
				if v.Kind == "outside" && v.Zone != nil {
					d := v.Zone.DistanceM
					verdict.DistanceM = &d
				}
			}
			a.Families = append(a.Families, z.Meta.Families...)
		}
		rs := make([]DrivingRule, len(t.Rules))
		for i, r := range t.Rules {
			rs[i] = DrivingRule{ID: r.ID, Text: r.Text, LegalBasis: r.LegalBasis, LegalBasisURL: r.LegalBasisURL, SourceNote: r.LegalBasisNote,
				ReviewedOn: r.ReviewedOn.Format(time.DateOnly), ToVerify: r.ToVerify}
		}
		return &DrivingRulesOutput{CacheControl: noStore, Body: DrivingRulesEnvelope{
			Data: DrivingRulesData{
				Topic: t.Code, Title: t.Title, Rules: rs,
				ReviewedOn: t.ReviewedOn.Format(time.DateOnly), Disclaimer: rules.Disclaimer, Zones: zones, Verdict: verdict, ZoneCity: city,
			},
			Meta:  rulesMeta(s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), caveat),
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// rulesMeta states the caveat of a city the zone list holds no entry for
// as a condition: no entry is not that there is no zone.
func rulesMeta(m DataMeta, caveat string) DataMeta {
	if caveat != "" {
		m.Notes.Condition = append(m.Notes.Condition, caveat)
	}
	return m
}

// weatherWarnings are the answers of the warnings.
func weatherWarnings(items []weather.Warning, at time.Time) []WeatherWarning {
	ws := make([]WeatherWarning, len(items))
	for i, w := range items {
		ws[i] = WeatherWarning{
			ID: w.ID, Hazard: string(w.Hazard), Level: int(w.Level), LevelName: w.LevelName, Issuer: w.Issuer,
			Headline: w.Headline, TextLanguage: w.TextLanguage, Validity: timeWindow(w.From, w.Until),
			InForce: !w.From.After(at), Area: WarningArea{Name: w.AreaName, CellID: w.CellID}, SourceFamily: w.Family,
			Event: w.Event, Identifier: w.Identifier,
			Description: w.Description, Instruction: w.Instruction, Urgency: w.Urgency, Certainty: w.Certainty, Unaltered: w.Unaltered,
		}
		if e := w.English; e != nil {
			ws[i].English = &WarningText{Headline: e.Headline, Description: e.Description, Instruction: e.Instruction, SourceFamily: e.Family, Unaltered: e.Unaltered}
		}
		if w.Published != nil {
			p, l := w.Published.UTC(), local(*w.Published)
			ws[i].Published, ws[i].PublishedLocal = &p, &l
		}
	}
	return ws
}
