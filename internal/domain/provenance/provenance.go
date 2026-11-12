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
	// DataAsOf is the instant of the family's last successful read from its
	// publisher (contract.source_freshness.data_as_of), not of the last
	// change in its data: a source that answers unchanged stays fresh. Nil
	// when the family has never been read successfully.
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

// Without returns the family without its notes of kind. A family whose gated
// data an answer withholds keeps its credit but states no condition of that
// data: the condition goes with the data it conditions.
func (f Family) Without(kind NoteKind) Family {
	f.Notes = slices.DeleteFunc(slices.Clone(f.Notes), func(n Note) bool { return n.Kind == kind })
	return f
}

// AnyStale reports whether any entry is stale.
func AnyStale(fs []Freshness) bool {
	return slices.ContainsFunc(fs, func(f Freshness) bool { return f.Stale })
}

// Versions is one read of what an answer's validity rests on: the version of
// every source family's data and a fingerprint of the licence register (each
// family's terms, attribution, licence, publisher and notes). An answer read
// under one Versions is current while a later read finds the same family
// versions and the same fingerprint.
type Versions struct {
	Families map[string]string
	Register string
	// Confirmations is, by family with a freshness row, the version of its
	// newest confirmation (the instant, the interval and whether its updates
	// are paused). Only an answer that judges a family by its confirmation
	// (the road lamp's sight) rests on it.
	Confirmations map[string]string
	// LampReference is a fingerprint of the reference rows the road lamp
	// judges its sight by: which families cover each state's traffic
	// messages, and the state of each junction.
	LampReference string
	// RulesReference is a fingerprint of the curated rule rows (every topic
	// and every rule): a rules answer rests on them, and they carry no
	// freshness version of their own.
	RulesReference string
}
