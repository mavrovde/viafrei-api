package httpapi

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// StationPageInput asks for a station's page.
type StationPageInput struct {
	StationID  string `path:"stationId" minLength:"1" maxLength:"120" example:"1:de:02000:10950" doc:"The station: its id as listStations answers it, DB's register number or an EVA number."`
	Departures int    `query:"departures" minimum:"1" maximum:"50" doc:"Most departures the departures section lists, earliest first; default 10."`
}

// PageSection is one section of a station page, in the page's order.
type PageSection struct {
	ID      string     `json:"id" enum:"header,rail_ampel,step_free_now,lifts,platforms,departures,service_alerts,parking_charging"`
	AsOf    *time.Time `json:"asOf" nullable:"true" doc:"The instant the section's data stands at (UTC); null when its read had no data, its content then empty."`
	Sources []string   `json:"sources" doc:"The source families the section was read from; each is credited once in meta.attribution."`
	// One of these is set, by id.
	Header     *PageHeader     `json:"header,omitempty"`
	RailAmpel  *PageRailAmpel  `json:"railAmpel,omitempty"`
	StepFree   *PageStepFree   `json:"stepFreeNow,omitempty"`
	Lifts      *[]PageLift     `json:"lifts,omitempty" doc:"The station's lifts and escalators, out of service first."`
	Platforms  *Platforms      `json:"platforms,omitempty"`
	Departures *[]Departure    `json:"departures,omitempty"`
	Alerts     *[]PageAlert    `json:"alerts,omitempty"`
	Facilities *PageFacilities `json:"facilities,omitempty"`
}

// PageHeader is the register's record of the station.
type PageHeader struct {
	Name       string   `json:"name"`
	StationNo  string   `json:"stationNo" doc:"DB's register number."`
	EVANo      *string  `json:"eva" nullable:"true" doc:"The main EVA number; null when the register gives none."`
	EVANumbers []string `json:"evaNumbers"`
	Slug       string   `json:"slug" doc:"The page's slug on viafrei.de."`
	Street     *string  `json:"street" nullable:"true"`
	Zipcode    *string  `json:"zipcode" nullable:"true"`
	City       *string  `json:"city" nullable:"true"`
	Land       *string  `json:"land" nullable:"true" doc:"The Land, named in the language asked."`
	Category   *int     `json:"category" nullable:"true" doc:"DB's station category, 1 to 7."`
}

// PageRailAmpel is the station's rail lamp.
type PageRailAmpel struct {
	Lamp    string           `json:"lamp" enum:"green,yellow,red,unknown" doc:"Over the next hour's trains (no bus): red when a fifth is cancelled, two fifths are more than 5 minutes late or the median delay is 15 minutes; yellow on a cancellation, a tenth late, three platform changes, an alert or a source not read; green with three trains and none of that; else unknown."`
	Trips   int              `json:"trips"`
	Reasons []PageRailReason `json:"reasons" doc:"Red reasons first."`
	Lines   []PageRailLine   `json:"lines" doc:"The lamp per line, long-distance trains as one, long-distance first, then by name."`
}

// PageRailReason is one reason of a lamp.
type PageRailReason struct {
	Kind    string   `json:"kind" enum:"cancelled,late,median,platform,alert,sight,line"`
	Lamp    string   `json:"lamp" enum:"red,yellow"`
	N       int      `json:"n,omitempty" doc:"How many trains (cancelled, late, platform) or alerts."`
	Of      int      `json:"of,omitempty" doc:"Of how many trains."`
	Min     int      `json:"min,omitempty" doc:"The median delay in minutes."`
	Sources []string `json:"sources,omitempty" doc:"What could not be read (sight)."`
	Line    string   `json:"line,omitempty" doc:"The line whose lamp raised the station's (line)."`
	Long    bool     `json:"long,omitempty"`
}

// PageRailLine is one line's lamp.
type PageRailLine struct {
	Line    string           `json:"line"`
	Long    bool             `json:"long"`
	Lamp    string           `json:"lamp" enum:"green,yellow,red,unknown"`
	Trips   int              `json:"trips"`
	Reasons []PageRailReason `json:"reasons"`
}

// PageStepFree is the step-free platforms now.
type PageStepFree struct {
	Platforms      []PageStepFreePlatform `json:"platforms" doc:"The platforms the inventory calls step-free whose lift is not out of service."`
	Blocked        []PageStepFreePlatform `json:"blocked" doc:"Those whose lift is out of service now."`
	SteplessAccess *string                `json:"steplessAccess" nullable:"true" doc:"The register's stepless access word (yes, no, partial); null when it states none."`
}

// PageStepFreePlatform is one step-free platform.
type PageStepFreePlatform struct {
	Tracks []string `json:"tracks"`
	Name   *string  `json:"name" nullable:"true"`
	Lift   string   `json:"lift" enum:"ok,out,unknown,none" doc:"The state of the lifts serving its tracks; none when no lift names them."`
}

// PageLift is one lift or escalator.
type PageLift struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Position    *string `json:"position" nullable:"true"`
	State       string  `json:"state" enum:"active,inactive,unknown"`
	EquipmentNo *string `json:"equipmentNo" nullable:"true"`
}

// PageAlert is one service alert as the station is told it.
type PageAlert struct {
	Header    string     `json:"header" doc:"The publisher's headline, verbatim."`
	Lines     []string   `json:"lines" doc:"The station's lines the alert names."`
	AtStation bool       `json:"atStation" doc:"It names one of the station's stops, not only one of its lines."`
	Until     *time.Time `json:"until" nullable:"true"`
}

// PageFacilities are DB's facility flags and the station's point.
type PageFacilities struct {
	Flags    []string  `json:"flags" doc:"The facilities DB's register states the station has."`
	Location *Position `json:"location" doc:"The station's point; null when the register gives none."`
}

// StationPageData is the data of getStationPage.
type StationPageData struct {
	PageURL  string        `json:"pageUrl" doc:"The station's page on viafrei.de, in the language asked."`
	Sections []PageSection `json:"sections" doc:"header, rail_ampel, step_free_now, lifts, platforms, departures, service_alerts, parking_charging, in this order, each present."`
}

// StationPageEnvelope is the enveloped answer of getStationPage.
type StationPageEnvelope struct {
	Data  *StationPageData `json:"data" doc:"Null when no station of DB's register has that id."`
	Meta  DataMeta         `json:"meta"`
	Links Links            `json:"links"`
}

// StationPageOutput is the response of getStationPage.
type StationPageOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StationPageEnvelope
}

// landsEn are the Länder in English, as the station page names them.
var landsEn = map[string]string{
	"Baden-Württemberg": "Baden-Württemberg", "Bayern": "Bavaria", "Berlin": "Berlin", "Brandenburg": "Brandenburg", "Bremen": "Bremen",
	"Hamburg": "Hamburg", "Hessen": "Hesse", "Mecklenburg-Vorpommern": "Mecklenburg-Western Pomerania", "Niedersachsen": "Lower Saxony",
	"Nordrhein-Westfalen": "North Rhine-Westphalia", "Rheinland-Pfalz": "Rhineland-Palatinate", "Saarland": "Saarland", "Sachsen": "Saxony",
	"Sachsen-Anhalt": "Saxony-Anhalt", "Schleswig-Holstein": "Schleswig-Holstein", "Thüringen": "Thuringia",
}

const stationPageOrigin = "https://viafrei.de/"

func registerStationPage(api huma.API, s *server, op huma.Operation) {
	op.Summary = "A station's page"
	op.Description = "A station's page as the platform's station page shows it, section by section: the register's record, the rail lamp, the step-free platforms now, the lifts, the platforms, the next departures, the service alerts and DB's facility flags. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "station page"
	huma.Register(api, op, func(ctx context.Context, in *StationPageInput) (*StationPageOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		p, err := s.features.StationPage(ctx, principalFrom(ctx).Key, features.StationPageQuery{StationID: in.StationID, Departures: cmp.Or(in.Departures, transit.PageDepartures)})
		if errors.Is(err, transit.ErrNotFound) {
			return &StationPageOutput{CacheControl: noStore, Body: StationPageEnvelope{Meta: s.dataMeta(ctx, nil, s.at(), false, partialDetail), Links: s.selfLink(ctx)}}, nil
		}
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		lang, _ := features.LanguageOf(ctx)
		lang = cmp.Or(lang, "de")
		return &StationPageOutput{CacheControl: noStore, Body: StationPageEnvelope{Data: stationPageData(p, lang),
			Meta: s.dataMeta(ctx, p.Meta.Families, p.Meta.At, p.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}

func pageReasons(rs []transit.RailReason) []PageRailReason {
	out := make([]PageRailReason, len(rs))
	for i, r := range rs {
		out[i] = PageRailReason{Kind: r.Kind, Lamp: r.Lamp, N: r.N, Of: r.Of, Min: r.Min, Sources: r.Sources, Line: r.Line, Long: r.Long}
	}
	return out
}

func stepFreeRows(ps []transit.StepFreePlatform) []PageStepFreePlatform {
	out := make([]PageStepFreePlatform, len(ps))
	for i, p := range ps {
		out[i] = PageStepFreePlatform{Tracks: nonNil(p.Tracks), Name: p.Name, Lift: p.Lift}
	}
	return out
}

func stationPageData(p features.StationPage, lang string) *StationPageData {
	r := p.Record
	land := r.Land
	if en, ok := landsEn[strOr(land)]; ok && lang == "en" {
		land = &en
	}
	slug := strOr(r.Slug)
	out := &StationPageData{PageURL: stationPageOrigin + lang + "/station/" + slug}
	for _, id := range features.PageSections {
		sec := p.Sections[id]
		ps := PageSection{ID: id, AsOf: utc(sec.At), Sources: nonNil(sec.Sources)}
		switch id {
		case "header":
			ps.Header = &PageHeader{Name: r.Name, StationNo: r.StationNo, EVANo: r.EVANo, EVANumbers: nonNil(r.EVANumbers), Slug: slug, Street: r.Street,
				Zipcode: r.Zipcode, City: r.City, Land: land, Category: r.Category}
		case "rail_ampel":
			lines := make([]PageRailLine, len(p.Ampel.Lines))
			for i, l := range p.Ampel.Lines {
				lines[i] = PageRailLine{Line: l.Line, Long: l.Long, Lamp: l.Lamp, Trips: l.Trips, Reasons: pageReasons(l.Reasons)}
			}
			ps.RailAmpel = &PageRailAmpel{Lamp: p.Ampel.Lamp, Trips: p.Ampel.Trips, Reasons: pageReasons(p.Ampel.Reasons), Lines: lines}
		case "step_free_now":
			ps.StepFree = &PageStepFree{Platforms: stepFreeRows(p.StepFree), Blocked: stepFreeRows(p.Blocked), SteplessAccess: r.SteplessAccess}
		case "lifts":
			lifts := make([]PageLift, len(p.Lifts))
			ps.Lifts = &lifts
			for i, f := range p.Lifts {
				lifts[i] = PageLift{ID: f.ID, Kind: f.Kind, Position: f.Position, State: f.State, EquipmentNo: f.EquipmentNo}
			}
		case "platforms":
			ps.Platforms = platformsOf(p.Platforms, sec.At)
		case "departures":
			deps := make([]Departure, len(p.Departures))
			ps.Departures = &deps
			for i, d := range p.Departures {
				deps[i] = departureRow(d)
			}
		case "service_alerts":
			alerts := make([]PageAlert, len(p.Alerts))
			ps.Alerts = &alerts
			for i, a := range p.Alerts {
				alerts[i] = PageAlert{Header: a.Header, Lines: nonNil(a.Lines), AtStation: a.AtStation, Until: utc(a.Until)}
			}
		default:
			f := &PageFacilities{Flags: nonNil(r.Flags)}
			if r.At != nil {
				pos := position(*r.At)
				f.Location = &pos
			}
			ps.Facilities = f
		}
		out.Sections = append(out.Sections, ps)
	}
	return out
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
