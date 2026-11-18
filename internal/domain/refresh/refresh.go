// Package refresh holds the on-demand refreshes of live sources (G8): a
// person's view asks for a refresh of one subject of a live source (the fuel
// prices about an area, a station's train board); the API records the ask,
// the live refresher claims it, calls the provider once and settles it, and
// everyone who asked about that subject while it was open shares it. A
// refresh carries no provider data: the data stays under its own resources.
package refresh

import (
	"context"
	"errors"
	"time"
)

// State is where a refresh stands.
type State string

// The refresh states.
const (
	// Pending: recorded and not yet settled, queued or running.
	Pending State = "pending"
	// Refreshed: the subject was refreshed; its data is served fresh.
	Refreshed State = "refreshed"
	// NoneDue: the subject's stored data was inside its cache window.
	NoneDue State = "none_due"
	// Refused: no refresh was made (a cap reached, no provider key); the
	// reason may say why.
	Refused State = "refused"
	// Failed: the provider did not answer the refresh.
	Failed State = "failed"
)

// Settled says whether the state is final.
func (s State) Settled() bool { return s != Pending }

// Ask is one refresh ask: a live source of the configured registry and the
// subject to refresh it about, a JSON object its registered schema admits.
// The API knows no source by name.
type Ask struct {
	Source  string
	Subject map[string]any
}

// Request is a refresh as a key sees it.
type Request struct {
	// ID is the request's id, decimal digits.
	ID     string
	Source string
	State  State
	// CreatedAt is when the first asker asked; SettledAt when it settled,
	// nil while open.
	CreatedAt time.Time
	SettledAt *time.Time
	// Reason says why it was refused or failed, when known.
	Reason string
	// Reused is true when the answer is a refresh asked before: one settled
	// inside the source's freshness window, an open one this ask joined, or
	// one the live refresher answered from a refresh already under way.
	Reused bool
	// Position is where an open request stands among the pending ones: 1
	// is next, 0 while it runs; nil once settled.
	Position *int
}

// New is a request to record: the source, the canonical subject (public ids
// only, never a raw position) with its key, the asking key and the instant
// of the ask.
type New struct {
	Source     string
	Subject    map[string]any
	SubjectKey string
	KeyID      string
	// RequestedBy is which of ViaFrei's own services asked: site, mcp or
	// dev, from the own key's plan (features.Door), never anything about the
	// person (the privacy ruling: "which of our own services made it").
	RequestedBy string
	At          time.Time
}

// ErrSource is the error of an ask naming no registered source.
var ErrSource = errors.New("refresh: no such live source")

// ErrPaused is the error of an ask naming a registered source that is
// switched off (its registry status is not enabled).
var ErrPaused = errors.New("refresh: the live source is switched off")

// ErrNotFound is the answer for a refresh the key did not ask for, or none.
var ErrNotFound = errors.New("refresh: no such refresh")

// SubjectError is the error of a subject the source refuses: the location
// in the ask (subject.at, say) and why.
type SubjectError struct {
	Location, Detail string
}

func (e *SubjectError) Error() string { return "refresh: " + e.Location + ": " + e.Detail }

// Lookup resolves value by the named lookup of the read store (eva: a
// station's name to its EVA number; ags: a position "lat,lon" to the AGS of
// its Gemeinde); "" when nothing answers.
type Lookup func(ctx context.Context, name, value string) (string, error)
