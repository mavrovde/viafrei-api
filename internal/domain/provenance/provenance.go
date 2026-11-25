// Package provenance holds what every data answer carries about where its
// data comes from: the attribution of each contributing source family, its
// licence notes and how fresh it is.
package provenance

import (
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Dataset names what one kind of answer reads: the families that hold its
// rows are the families such an answer consulted, which an empty answer
// credits (its notes included), as the platform's tools credit their own
// sources. A domain is wider: an empty fuel answer credits no charging
// family, a road lamp no road-weather family.
type Dataset string

// The datasets an answer's credit is narrowed to.
const (
	DatasetFuel         Dataset = "fuel"
	DatasetZones        Dataset = "zones"
	DatasetLorry        Dataset = "lorry"
	DatasetSpeedCameras Dataset = "speed_cameras"
	DatasetWarnings     Dataset = "warnings"
	DatasetAlerts       Dataset = "alerts"
	DatasetSharing      Dataset = "sharing"
	DatasetRoadEvents   Dataset = "events"
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
	// NeverStale marks a family with no fixed interval (the read contract
	// gives it no threshold): it is never stale, and an answer shows the age
	// of its data only. StaleAfter is then unused.
	NeverStale bool
	// DataAsOf is the instant the family's data last changed, as the platform
	// status states it; nil when the family has published none. It does not
	// advance when the data is confirmed unchanged: ReadAt does, and
	// KnownCurrent is the later of the two.
	DataAsOf *time.Time
	// Version changes whenever the family's data changes; empty when the
	// family has published none. An answer's ETag is built from it.
	Version string
	Notes   []Note
	// ReadAt is when the family's data was last read and found current (a
	// new or a confirmed file); nil when it never was. A dated note states
	// its day (Filled).
	ReadAt *time.Time
	// FilledUntil is when a text Filled gave the family stops being true:
	// the next Europe/Berlin new year for a filled year; zero when nothing
	// was filled.
	FilledUntil time.Time
}

// Freshness is one entry of meta.freshness (ENV-003).
type Freshness struct {
	Family   string
	DataAsOf *time.Time
	AgeS     *int64
	// StaleAfterS is the threshold in seconds; nil for a family that is
	// never stale.
	StaleAfterS *int64
	Stale       bool
}

// KnownCurrent is the instant up to which the family's data is known
// current: its newest confirmation (ReadAt) when that is later than the
// instant of its last change (DataAsOf), as a confirmed unchanged copy is as
// current as a new one; nil when the family's data has never been known
// current. The platform judges a family's freshness by the same instant.
func (f Family) KnownCurrent() *time.Time {
	if f.ReadAt != nil && (f.DataAsOf == nil || f.ReadAt.After(*f.DataAsOf)) {
		return f.ReadAt
	}
	return f.DataAsOf
}

// Assess says how fresh the family is at now, by the instant its data is
// known current (KnownCurrent): data confirmed unchanged is not stale. A
// family with no instant is stale and has no age, unless it is never stale.
// Data dated after now has the age zero: the age is never negative.
func (f Family) Assess(now time.Time) Freshness {
	out := Freshness{Family: f.Source.Family, Stale: !f.NeverStale}
	if !f.NeverStale {
		s := int64(f.StaleAfter / time.Second)
		out.StaleAfterS = &s
	}
	known := f.KnownCurrent()
	if known == nil {
		return out
	}
	asOf := known.UTC()
	age := max(int64(now.Sub(asOf)/time.Second), 0)
	out.DataAsOf, out.AgeS = &asOf, &age
	out.Stale = out.StaleAfterS != nil && age > *out.StaleAfterS
	return out
}

// TurnsStale is the instant the family's data turns stale: once the age of
// its known-current instant passes the threshold's whole seconds. False for
// a family that is never stale or has no instant.
func (f Family) TurnsStale() (time.Time, bool) {
	known := f.KnownCurrent()
	if f.NeverStale || known == nil {
		return time.Time{}, false
	}
	return known.Add((f.StaleAfter/time.Second + 1) * time.Second), true
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

// Unconditioned is fams without their condition notes: an answer that states
// no row names the families it consulted, but carries none of their
// conditions, as the platform's answers carry a register's condition only
// beside the rows it governs. The input is not changed.
func Unconditioned(fams []Family) []Family {
	out := make([]Family, len(fams))
	for i, f := range fams {
		f.Notes = slices.DeleteFunc(slices.Clone(f.Notes), func(n Note) bool { return n.Kind == Condition })
		out[i] = f
	}
	return out
}
