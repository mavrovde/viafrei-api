package energy

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// The on-demand fuel refresh (G8): a person's view of a fuel card asks the
// service that holds the provider key to refresh the prices about one area,
// as the platform's site asks it today. The API forwards the ask and relays
// the answer; it never calls the provider itself.

// RefreshState is the outcome of a refresh ask, the platform's states.
type RefreshState string

// The refresh states, in the platform's order.
const (
	// RefreshRefreshed: the prices were refreshed; SettledAt says when.
	RefreshRefreshed RefreshState = "refreshed"
	// RefreshPending: the ask is queued or running; Queue says where.
	RefreshPending RefreshState = "pending"
	// RefreshFailed: the provider did not answer the refresh.
	RefreshFailed RefreshState = "failed"
	// RefreshPaused: refreshing is paused by the operator.
	RefreshPaused RefreshState = "paused"
	// RefreshNoneDue: every price of the area is inside its cache window.
	RefreshNoneDue RefreshState = "none_due"
	// RefreshRefused: no refresh is made (no provider key, a cap reached,
	// or no refresher configured); Reason may say why.
	RefreshRefused RefreshState = "refused"
)

// RefreshStates lists every state an answer can carry.
var RefreshStates = []RefreshState{RefreshRefreshed, RefreshPending, RefreshFailed, RefreshPaused, RefreshNoneDue, RefreshRefused}

// ReasonNotConfigured is the reason of a refusal by an instance with no
// refresher configured.
const ReasonNotConfigured = "refresh not configured"

// ErrRefreshArea is the refresher's verdict that the area is no area it
// answers: not a key, outside Germany, or no such Gemeinde.
var ErrRefreshArea = errors.New("energy: the refresh area is not known")

// ErrRefreshAt is the error of an area key that cannot be read.
var ErrRefreshAt = errors.New("energy: at must be ags:<8 digits> or pt:lat,lon inside Germany")

// ErrRefreshMode is the error of an ask that is both snapped and anchored.
var ErrRefreshMode = errors.New("energy: snap and anchor exclude each other")

// refreshAt is the platform's area key: a Gemeinde's eight-digit AGS, or a
// point of at most six decimals.
var refreshAt = regexp.MustCompile(`^(?:ags:[0-9]{8}|pt:(-?[0-9]{1,2}(?:\.[0-9]{1,6})?),(-?[0-9]{1,3}(?:\.[0-9]{1,6})?))$`)

// Germany's box as the platform reads it, a little generous.
const (
	refreshLatMin, refreshLatMax = 47.2, 55.1
	refreshLonMin, refreshLonMax = 5.8, 15.1
)

// RefreshAsk is one refresh ask, as the site sends it.
type RefreshAsk struct {
	// At is the area key: ags:<8 digits> or pt:lat,lon.
	At string
	// Snap answers a point as its nearest Gemeinde's centroid; Anchor says
	// the point is a motorway page's public anchor. At most one is set.
	Snap, Anchor bool
}

// Validate checks the ask as the platform reads it: an area key of at most
// 40 characters, a point inside Germany's box, and not both snap and anchor.
func (a RefreshAsk) Validate() error {
	m := refreshAt.FindStringSubmatch(a.At)
	if len(a.At) > 40 || m == nil {
		return ErrRefreshAt
	}
	if m[1] != "" {
		lat, _ := strconv.ParseFloat(m[1], 64)
		lon, _ := strconv.ParseFloat(m[2], 64)
		if lat < refreshLatMin || lat > refreshLatMax || lon < refreshLonMin || lon > refreshLonMax {
			return ErrRefreshAt
		}
	}
	if a.Snap && a.Anchor {
		return ErrRefreshMode
	}
	return nil
}

// RefreshQueue is where a pending ask stands in the queue of person asks.
type RefreshQueue struct {
	// Position 1 is next; 0 while it runs.
	Position int
	// ETA is about how long until it starts.
	ETA time.Duration
}

// RefreshAnswer is the outcome of a refresh ask.
type RefreshAnswer struct {
	State RefreshState
	// SettledAt is when the ask settled; nil while it has not.
	SettledAt *time.Time
	// Reused says the answer is the area's remembered outcome: no request
	// or read was made.
	Reused bool
	// Queue is set on a pending ask the refresher placed in its queue.
	Queue *RefreshQueue
	// Reason says why a refusal was made, when the API knows it.
	Reason string
}

// KnownRefreshState says whether s is one of RefreshStates.
func KnownRefreshState(s string) bool {
	return slices.Contains(RefreshStates, RefreshState(s))
}
