// Package provenance holds what every data answer carries about where its
// data comes from: the attribution of each contributing source family, its
// licence notes and how fresh it is.
package provenance

import (
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// NoteKind is the kind of a licence note.
type NoteKind string

// The two kinds of licence note: a condition the data may only be used
// under, and the purpose it may only be used for.
const (
	Condition NoteKind = "condition"
	Purpose   NoteKind = "purpose"
)

// MapNoteKind maps a stored note kind. A kind the service does not know is
// treated as a condition: an unknown note is shown, never dropped.
func MapNoteKind(raw string) NoteKind {
	if raw == string(Purpose) {
		return Purpose
	}
	return Condition
}

// Note is one licence note, verbatim, in the answer's language.
type Note struct {
	Kind NoteKind
	Text string
}

// Family is one source family as an answer carries it.
type Family struct {
	Source access.Source
	// StaleAfter is the family's design threshold: older data is stale.
	StaleAfter time.Duration
	// DataAsOf is the instant the family's data is current to; nil when
	// the family has published none.
	DataAsOf *time.Time
	// Version changes whenever the family's data changes; empty when the
	// family has published none. An answer's ETag is built from it.
	Version string
	Notes   []Note
}

// Freshness is one entry of meta.freshness (ENV-003).
type Freshness struct {
	Family      string
	DataAsOf    *time.Time
	AgeS        *int64
	StaleAfterS int64
	Stale       bool
}

// Assess says how fresh the family is at now. A family with no instant is
// stale and has no age. Data dated after now has the age zero: the age is
// never negative.
func (f Family) Assess(now time.Time) Freshness {
	out := Freshness{Family: f.Source.Family, StaleAfterS: int64(f.StaleAfter / time.Second), Stale: true}
	if f.DataAsOf == nil {
		return out
	}
	asOf := f.DataAsOf.UTC()
	age := max(int64(now.Sub(asOf)/time.Second), 0)
	out.DataAsOf, out.AgeS = &asOf, &age
	out.Stale = age > out.StaleAfterS
	return out
}

// AsOf is meta.asOf (ENV-004): the oldest instant among the entries, or nil
// when there is no entry or one of them has no instant, since nothing is then
// known about the oldest.
func AsOf(fs []Freshness) *time.Time {
	var oldest *time.Time
	for _, f := range fs {
		if f.DataAsOf == nil {
			return nil
		}
		if oldest == nil || f.DataAsOf.Before(*oldest) {
			oldest = f.DataAsOf
		}
	}
	return oldest
}

// Notes returns the distinct texts of one kind across the families, in the
// order the families and their notes are given.
func Notes(fams []Family, kind NoteKind) []string {
	out := []string{}
	for _, f := range fams {
		for _, n := range f.Notes {
			if n.Kind == kind && !slices.Contains(out, n.Text) {
				out = append(out, n.Text)
			}
		}
	}
	return out
}

// AnyStale reports whether any entry is stale.
func AnyStale(fs []Freshness) bool {
	return slices.ContainsFunc(fs, func(f Freshness) bool { return f.Stale })
}
