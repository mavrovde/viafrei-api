package httpapi

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// RoadStatusBlock is data.roadStatus: the platform's road status by road
// number.
type RoadStatusBlock struct {
	WindowEnd       time.Time              `json:"windowEnd" doc:"The end of the window the status covers: the window asked, else three days from now."`
	Counts          RoadStatusCounts       `json:"counts"`
	Items           []RoadStatusItem       `json:"items" doc:"The entries listed, closures first, at most the limit (and eleven), in the platform's order."`
	Hidden          int                    `json:"hidden" doc:"The entries counted and not listed: items plus hidden is counts.total."`
	HiddenClosures  int                    `json:"hiddenClosures" doc:"The closures among hidden."`
	CanRaise        bool                   `json:"canRaise" doc:"Whether a higher limit would list every entry."`
	SourcesAnswered []RoadStatusSource     `json:"sourcesAnswered" doc:"The sources that answered, in the register's order; a source that did not answer is not listed."`
	Guidance        []RoadStatusGuidance   `json:"guidance" doc:"The road operator's switched strategies in force on the motorway, said as its recommendation; empty when none or not a motorway."`
	GuidanceNotRead []RoadStatusStaleCheck `json:"guidanceNotCurrent" doc:"The strategy sources not checked within three of their cycles, whose strategies are not said."`
	PlannedNoCoords bool                   `json:"plannedWorksHaveNoCoordinates" doc:"The planning data carries no coordinates: planned works are filed by road and section."`
}

// RoadStatusCounts are the status's totals by bucket; they sum to total.
type RoadStatusCounts struct {
	Closed          int      `json:"closed"`
	Restricted      int      `json:"restricted"`
	Planned         int      `json:"planned"`
	Programme       int      `json:"programme" doc:"Started records of a works programme, no restriction in force."`
	Current         int      `json:"current" doc:"Plan works in force on a road the live sweep answers."`
	Total           int      `json:"total"`
	CurrentFamilies []string `json:"currentFamilies" doc:"The plan sources behind current, over every counted entry."`
}

// RoadStatusItem is one entry.
type RoadStatusItem struct {
	Status         string       `json:"status" enum:"closed,restricted,current,programme,planned"`
	Term           string       `json:"term" enum:"closure,roadworks,restriction" doc:"What the entry is called."`
	Family         string       `json:"sourceFamily"`
	Road           string       `json:"road" doc:"The road the entry is on: for the road operator's entry, the motorway its title names first."`
	ID             string       `json:"id"`
	Kind           *string      `json:"kind" nullable:"true" doc:"The road operator's kind (closure, roadworks); null for another source's entry."`
	Title          *string      `json:"title" nullable:"true"`
	Subtitle       *string      `json:"subtitle" nullable:"true"`
	Extent         *string      `json:"extent" nullable:"true"`
	Description    []string     `json:"description" doc:"The road operator's description lines, without its start line and since clauses; empty for another source's entry."`
	Segments       int          `json:"segments" doc:"The sections the road operator publishes the site as; 0 for another source's entry."`
	EventType      *string      `json:"eventType" nullable:"true"`
	Publisher      *string      `json:"publisher" nullable:"true"`
	PublicationID  *string      `json:"publicationId" nullable:"true"`
	PlaceText      *string      `json:"place" nullable:"true" doc:"The place a message's words name."`
	DetailText     *string      `json:"detail" nullable:"true"`
	SiteName       *string      `json:"siteName" nullable:"true"`
	SectionName    *string      `json:"sectionName" nullable:"true"`
	Cause          *string      `json:"cause" nullable:"true"`
	ReferentFrom   *string      `json:"referentFrom" nullable:"true"`
	ReferentTo     *string      `json:"referentTo" nullable:"true"`
	Detour         bool         `json:"detour" doc:"A rerouting record."`
	DetourVia      *string      `json:"detourVia" nullable:"true" doc:"The roads a rerouting record's alternative route takes."`
	DetourVehicles bool         `json:"detourForSomeVehicles" doc:"The rerouting applies to some vehicles only."`
	WindowStarts   []*time.Time `json:"windowStarts" doc:"The starts of the validity windows touching the window."`
	WindowEnds     []*time.Time `json:"windowEnds"`
	WindowCount    int          `json:"windowCount"`
	ClosureFrom    *time.Time   `json:"closureFrom" nullable:"true" doc:"A closure ahead within a restriction's validity: its start."`
	ClosureTo      *time.Time   `json:"closureTo" nullable:"true"`
	From           *time.Time   `json:"from" nullable:"true"`
	To             *time.Time   `json:"to" nullable:"true"`
	MeasureTo      *time.Time   `json:"measureTo" nullable:"true" doc:"The end of the whole measure the description states."`
	Lat            *float64     `json:"lat" nullable:"true"`
	Lon            *float64     `json:"lon" nullable:"true"`
}

// RoadStatusSource is a source that answered.
type RoadStatusSource struct {
	Family string     `json:"sourceFamily"`
	AsOf   *time.Time `json:"asOf" nullable:"true" doc:"When the source was last seen current: the live sweep, else the newest of its listed entries (its last good run when as new); null when none of its entries is listed."`
}

// RoadStatusGuidance is a switched strategy.
type RoadStatusGuidance struct {
	Road    string     `json:"road"`
	Family  string     `json:"sourceFamily"`
	ID      string     `json:"id"`
	Kind    string     `json:"kind" enum:"detour,guidance"`
	Via     *string    `json:"via" nullable:"true" doc:"The roads of the alternative route."`
	Words   *string    `json:"words" nullable:"true" doc:"The publisher's words of the route, as published."`
	Title   *string    `json:"title" nullable:"true"`
	TitleEn *string    `json:"titleEnglish" nullable:"true"`
	WordsEn *string    `json:"wordsEnglish" nullable:"true"`
	From    *string    `json:"from" nullable:"true"`
	To      *string    `json:"to" nullable:"true"`
	Since   *time.Time `json:"since" nullable:"true"`
	Until   *time.Time `json:"until" nullable:"true" doc:"Null when the publisher states none, or its stated end is its next pull."`
}

// RoadStatusStaleCheck is a strategy source not current.
type RoadStatusStaleCheck struct {
	Family    string     `json:"sourceFamily"`
	CheckedAt *time.Time `json:"lastChecked" nullable:"true"`
}

func roadStatusBlock(a *roads.RoadStatusAnswer) *RoadStatusBlock {
	if a == nil {
		return nil
	}
	k := a.Counts
	out := &RoadStatusBlock{WindowEnd: a.WindowEnd, Hidden: a.Hidden, HiddenClosures: a.HiddenClosed, CanRaise: a.CanRaise, PlannedNoCoords: true,
		Counts: RoadStatusCounts{Closed: k.Closed, Restricted: k.Restricted, Planned: k.Planned, Programme: k.Programme, Current: k.Current, Total: k.Total,
			CurrentFamilies: append([]string{}, k.CurrentFamilies...)},
		Items: make([]RoadStatusItem, len(a.Items)), SourcesAnswered: make([]RoadStatusSource, len(a.Answered)),
		Guidance: make([]RoadStatusGuidance, len(a.Strategies)), GuidanceNotRead: make([]RoadStatusStaleCheck, len(a.StrategiesStale))}
	for i, v := range a.Items {
		out.Items[i] = RoadStatusItem{Status: v.Status, Term: v.Term, Family: v.Family, Road: v.Road, ID: v.ID, Kind: v.Kind, Title: v.Title, Subtitle: v.Subtitle,
			Extent: v.Extent, Description: append([]string{}, v.Description...), Segments: v.Segments, EventType: v.EventType, Publisher: v.Publisher,
			PublicationID: v.PublicationID, PlaceText: v.PlaceText, DetailText: v.DetailText, SiteName: v.SiteName, SectionName: v.SectionName, Cause: v.Cause,
			ReferentFrom: v.RefFrom, ReferentTo: v.RefTo, Detour: v.Detour, DetourVia: v.DetourVia, DetourVehicles: v.DetourVehicles,
			WindowStarts: append([]*time.Time{}, v.WindowStarts...), WindowEnds: append([]*time.Time{}, v.WindowEnds...), WindowCount: v.WindowCount,
			ClosureFrom: v.ClosureFrom, ClosureTo: v.ClosureTo, From: v.From, To: v.To, MeasureTo: v.MeasureTo, Lat: v.Lat, Lon: v.Lon}
	}
	for i, v := range a.Answered {
		out.SourcesAnswered[i] = RoadStatusSource{Family: v.Family, AsOf: v.AsOf}
	}
	for i, v := range a.Strategies {
		out.Guidance[i] = RoadStatusGuidance{Road: v.Road, Family: v.Family, ID: v.ID, Kind: v.Kind, Via: v.Via, Words: v.Words, Title: v.Title,
			TitleEn: v.TitleEn, WordsEn: v.WordsEn, From: v.From, To: v.To, Since: v.Since, Until: v.Until}
	}
	for i, v := range a.StrategiesStale {
		out.GuidanceNotRead[i] = RoadStatusStaleCheck{Family: v.Family, CheckedAt: v.CheckedAt}
	}
	return out
}
