package roads

import (
	"math"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// StampIntervals is how many of its own cycles a source's Stand may age
// before the platform's road answers mark it possibly out of date (its source
// alarms' freshnessIntervals).
const StampIntervals = 3

// Stamp is one source's Stand, as the platform's road answers state it
// (sourceStamps): when we last knew it (its newest confirmation, never
// older than its data's instant), when its data last changed, and its own
// cycle, nil when none is stated.
type Stamp struct {
	Family         string
	CheckedAt      time.Time
	UnchangedSince *time.Time
	IntervalS      *float64
}

// StampOf is family f's Stand from its newest confirmation c (nil for
// none): the confirmation's instant, or the data's when that is newer or
// the source was never confirmed; false when neither is known.
func StampOf(f provenance.Family, c *ampel.Confirmation) (Stamp, bool) {
	s := Stamp{Family: f.Source.Family, UnchangedSince: f.DataAsOf}
	switch {
	case c != nil && c.At != nil && (f.DataAsOf == nil || !c.At.Before(*f.DataAsOf)):
		s.CheckedAt = *c.At
	case f.DataAsOf != nil:
		s.CheckedAt = *f.DataAsOf
	default:
		return Stamp{}, false
	}
	if c != nil && c.IntervalS != nil && *c.IntervalS > 0 {
		s.IntervalS = c.IntervalS
	}
	return s, true
}

// Stamps are the Stand of every family that has one, in the families'
// order, each from its confirmation in confirmations.
func Stamps(fams []provenance.Family, confirmations map[string]ampel.Confirmation) []Stamp {
	out := []Stamp{}
	for _, f := range fams {
		var c *ampel.Confirmation
		if v, ok := confirmations[f.Source.Family]; ok {
			c = &v
		}
		if s, ok := StampOf(f, c); ok {
			out = append(out, s)
		}
	}
	return out
}

// PossiblyStale says whether the Stand is older than StampIntervals of the
// source's own cycle at now, as the platform judges a source (more than the
// limit, the limit itself still current). A source with no stated cycle is
// never judged: nothing may invent a cadence.
func (s Stamp) PossiblyStale(now time.Time) bool {
	if s.IntervalS == nil {
		return false
	}
	return float64(now.Sub(s.CheckedAt).Milliseconds()) > StampIntervals**s.IntervalS*1000
}

// LapsesAt is the first instant PossiblyStale turns true with the clock
// alone: the first whole millisecond past the limit. Nil for a source with
// no stated cycle.
func (s Stamp) LapsesAt() *time.Time {
	if s.IntervalS == nil {
		return nil
	}
	t := s.CheckedAt.Add(time.Duration(math.Floor(StampIntervals**s.IntervalS*1000)+1) * time.Millisecond)
	return &t
}
