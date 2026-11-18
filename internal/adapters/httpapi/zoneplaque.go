package httpapi

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

// ZonePlaque is the plaque a zone answer states, and on whose word.
type ZonePlaque struct {
	From     string  `json:"from" enum:"curated_entry,national_fact" doc:"curated_entry: the curated zone list's entry for the zone's Gemeinde; national_fact: no entry speaks for it, and every signposted zone today asks for the green plaque."`
	Entry    *string `json:"entry" nullable:"true" doc:"The list entry's name; null for the national fact."`
	Plakette *string `json:"plakette" nullable:"true" doc:"green; null where the entry deliberately states no plaque (a lifted zone, a zone whose data we may not carry)."`
	Covered  *bool   `json:"covered" nullable:"true" doc:"false where the zone's data is under a closed licence we may not pass on; null for the national fact."`
}

// ZoneFacts are what a zone answer states beside the verdict, as the
// platform's zone answer states them.
type ZoneFacts struct {
	Plaque         *ZonePlaque `json:"plaque" doc:"Stated only of a zone the position is in and not released from; else null."`
	LiftedOn       *string     `json:"liftedOn" nullable:"true" format:"date" doc:"The day the curated list says the deciding zone was lifted; null for a zone in force."`
	DataDate       *string     `json:"dataDate" nullable:"true" format:"date" doc:"The zone data's own date where the publisher states one."`
	BoundaryReadAt *time.Time  `json:"boundaryReadAt" nullable:"true" doc:"When the deciding publisher's document was last read; null for unknown."`
}

func zoneFacts(a *rules.ZoneAnswer) ZoneFacts {
	var f ZoneFacts
	if a == nil {
		return f
	}
	day := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		s := t.UTC().Format(time.DateOnly)
		return &s
	}
	if a.Plaque {
		green := "green"
		f.Plaque = &ZonePlaque{From: "national_fact", Plakette: &green}
		if e := a.Entry; e != nil {
			covered := e.Covered
			f.Plaque = &ZonePlaque{From: "curated_entry", Entry: &e.Name, Plakette: e.Plaque, Covered: &covered}
		}
	}
	f.LiftedOn, f.DataDate = day(a.LiftedOn), day(a.DataDate)
	if a.BoundaryReadAt != nil {
		t := a.BoundaryReadAt.UTC()
		f.BoundaryReadAt = &t
	}
	return f
}
