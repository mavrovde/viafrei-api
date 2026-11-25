package energy

import (
	"strings"
	"time"
)

// The platform's judgement of a charge point's status (judgeStatus,
// judgePoints): each point's newest status row, judged against its status
// source's age limits and the source's run ledger, is fresh (counted), stale
// (shown with its time, never counted) or none.

// Verdict is one point as a place's line counts it: a fresh state, stale,
// or unknown (no status, or none that may be quoted).
type Verdict string

// The verdicts.
const (
	VerdictAvailable  Verdict = "available"
	VerdictOccupied   Verdict = "occupied"
	VerdictOutOfOrder Verdict = "out_of_order"
	VerdictUnknown    Verdict = "unknown"
	VerdictStale      Verdict = "stale"
)

// deltaTrustDays is how far back a change-only statement may be quoted at
// all: the platform's source-movement look-back (VALIDATOR_LOOKBACK_DAYS).
const deltaTrustDays = 30

// duplicateLookbackHours is how long the loader compares a snapshot's point
// with its newest row (DUPLICATE_LOOKBACK_HOURS): only a row that young was
// restated by a later snapshot that wrote nothing for it.
const duplicateLookbackHours = 48

// DeltaTrustHorizon is deltaTrustDays as a duration.
var DeltaTrustHorizon = time.Duration(deltaTrustDays) * 24 * time.Hour

var duplicateLookback = time.Duration(duplicateLookbackHours) * time.Hour

// CycleCheckedStatuses are the run statuses that mean a cycle really
// checked (CYCLE_CHECKED_STATUSES).
var CycleCheckedStatuses = []string{"ok", "unchanged", "degraded"}

// StatusRow is a point's newest stored status: the source, the three words
// and the three clocks (observed: when it was pulled; sourceTs: the
// publication's time; lastUpdated: when the point last changed), and the
// pull's exchange protocol.
type StatusRow struct {
	Source           string
	ObservedAt       time.Time
	SourceTs         *time.Time
	LastUpdated      *time.Time
	ExchangeProtocol *string
	Status           *string
	OperationStatus  *string
	OpeningStatus    *string
}

// SourceLiveness is what the run ledger says about a status source as a whole
// (STATUS_FEED_LIVENESS): the newest conclusive cycle, the validator and
// since when it holds, the newest change delivered and received in the
// status window, the newest complete restatement and its receipt, and the
// newest jump past unread publications with no complete snapshot since.
type SourceLiveness struct {
	Source             string
	LastConclusiveAt   *time.Time
	Validator          *string
	ValidatorSince     *time.Time
	MovedAt            *time.Time
	ReceivedAt         *time.Time
	RestatedAt         *time.Time
	RestatedReceivedAt *time.Time
	GapAt              *time.Time
}

// reading is one point's status as the answer may use it.
type reading struct {
	kind     Verdict // "" none, VerdictStale, or the fresh state
	reported Verdict
	asOf     time.Time
	backlog  *time.Time
}

var statusWords = map[string]Verdict{
	"available": VerdictAvailable, "occupied": VerdictOccupied, "charging": VerdictOccupied, "blocked": VerdictOccupied,
	"reserved": VerdictOccupied, "faulted": VerdictOutOfOrder, "inoperative": VerdictOutOfOrder, "outoforder": VerdictOutOfOrder,
	"outofservice": VerdictOutOfOrder, "notavailable": VerdictOutOfOrder, "removed": VerdictOutOfOrder, "planned": VerdictOutOfOrder,
	"unknown": VerdictUnknown, "preparing": VerdictOccupied, "suspendedev": VerdictOccupied, "suspendedevse": VerdictOccupied,
	"finishing": VerdictOccupied, "unavailable": VerdictOutOfOrder,
}

var (
	blockingOperation = map[string]bool{"notinoperation": true, "outoforder": true, "outofservice": true}
	blockingOpening   = map[string]bool{"closed": true, "temporarilyclosed": true, "notopen": true}
)

// lowerWord is a status word as the platform compares it: trimmed, lower
// case, without blanks, underscores or hyphens.
func lowerWord(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	s := strings.ToLower(strings.TrimSpace(*v))
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v', '_', '-':
			return -1
		}
		return r
	}, s)
	return s, true
}

// reportedState is what a row says, before anybody asks how old it is.
func reportedState(r StatusRow) Verdict {
	if w, ok := lowerWord(r.OperationStatus); ok && blockingOperation[w] {
		return VerdictOutOfOrder
	}
	if w, ok := lowerWord(r.OpeningStatus); ok && blockingOpening[w] {
		return VerdictOutOfOrder
	}
	w, ok := lowerWord(r.Status)
	if !ok {
		return VerdictUnknown
	}
	if v, known := statusWords[w]; known {
		return v
	}
	return VerdictUnknown
}

// isDelta tells a change-only pull's row.
func (r StatusRow) isDelta() bool {
	return r.ExchangeProtocol != nil && strings.ToLower(strings.TrimSpace(*r.ExchangeProtocol)) == "deltapull"
}

// statementAt is the instant a row states its status as of: a delta row's
// lastUpdated, any other row's publication time, our pull when the
// publisher sent none, never later than our pull.
func statementAt(r StatusRow) time.Time {
	stated := r.SourceTs
	if r.isDelta() {
		stated = r.LastUpdated
	}
	if stated == nil || !stated.Before(r.ObservedAt) {
		return r.ObservedAt
	}
	return *stated
}

// sourceSpeaksForNow: a conclusive cycle within the source's stale limit, and
// the source moved within its frozen limit (none: never judged frozen).
func sourceSpeaksForNow(w StatusSource, l SourceLiveness, now time.Time) bool {
	if l.LastConclusiveAt == nil || now.Sub(*l.LastConclusiveAt) > w.StaleAfter {
		return false
	}
	if w.FrozenAfter == nil || l.ValidatorSince == nil {
		return true
	}
	return now.Sub(*l.ValidatorSince) <= *w.FrozenAfter
}

// deliversBacklog: rows received inside the status window whose newest
// change is older than the status limit.
func deliversBacklog(w StatusSource, l SourceLiveness, now, readAt time.Time) bool {
	if l.ReceivedAt == nil || l.MovedAt == nil || readAt.Sub(*l.ReceivedAt) > w.StatementMaxAge {
		return false
	}
	return now.Sub(*l.MovedAt) > w.StatementMaxAge
}

func livenessOf(ls []SourceLiveness, source string) *SourceLiveness {
	for i := range ls {
		if ls[i].Source == source {
			return &ls[i]
		}
	}
	return nil
}

func windowOf(ws []StatusSource, source string) *StatusSource {
	for i := range ws {
		if ws[i].Key == source {
			return &ws[i]
		}
	}
	return nil
}

// judgeStatus is one point at now from its newest stored row (judgeStatus).
func judgeStatus(row *StatusRow, windows []StatusSource, now time.Time, liveness []SourceLiveness) reading {
	if row == nil {
		return reading{}
	}
	w := windowOf(windows, row.Source)
	if w == nil {
		return reading{}
	}
	reported := reportedState(*row)
	if reported == VerdictUnknown {
		return reading{}
	}
	pulledAge := now.Sub(row.ObservedAt)
	confirmedAt := statementAt(*row)
	stale := false
	var backlog *time.Time
	source := livenessOf(liveness, row.Source)
	gapped := source != nil && source.GapAt != nil && row.ObservedAt.Before(*source.GapAt)
	staleNow := reading{kind: VerdictStale, reported: reported, asOf: confirmedAt}
	if row.isDelta() {
		if pulledAge > DeltaTrustHorizon {
			return reading{}
		}
		if gapped {
			return staleNow
		}
		if source == nil || source.LastConclusiveAt == nil {
			stale = pulledAge > w.StaleAfter
		} else {
			if !sourceSpeaksForNow(*w, *source, now) {
				stale = true
			} else if deliversBacklog(*w, *source, now, now) {
				backlog = source.ReceivedAt
			}
			if source.MovedAt != nil && source.MovedAt.After(confirmedAt) {
				confirmedAt = *source.MovedAt
			}
		}
	} else {
		if gapped {
			return staleNow
		}
		if source != nil && source.RestatedAt != nil && source.RestatedReceivedAt != nil && source.LastConclusiveAt != nil &&
			source.RestatedReceivedAt.After(row.ObservedAt) && source.RestatedAt.After(confirmedAt) && pulledAge <= duplicateLookback {
			confirmedAt = *source.RestatedAt
			stale = now.Sub(*source.LastConclusiveAt) > w.StaleAfter
		} else {
			stale = pulledAge > w.StaleAfter
		}
	}
	if now.Sub(confirmedAt) > w.StatementMaxAge {
		stale = true
	}
	if !stale {
		return reading{kind: reported, asOf: confirmedAt}
	}
	return reading{kind: VerdictStale, reported: reported, asOf: confirmedAt, backlog: backlog}
}

// StaleStates counts what the stale points last reported, by state.
type StaleStates struct{ Available, Occupied, OutOfOrder int }

func (s StaleStates) add(o StaleStates) StaleStates {
	return StaleStates{s.Available + o.Available, s.Occupied + o.Occupied, s.OutOfOrder + o.OutOfOrder}
}

// JudgedPoints are a place record's points, judged (judgePoints).
type JudgedPoints struct {
	// States is one verdict per point, in the points' order.
	States []Verdict
	// StatusAsOf is the newest confirmation among the fresh points.
	StatusAsOf *time.Time
	// StaleAsOf is the newest confirmation among the stale points.
	StaleAsOf *time.Time
	// PointAsOf and PointStatedAt are, per point, when its status was last
	// confirmed and what its own newest row states; nil for none.
	PointAsOf, PointStatedAt []*time.Time
	// StaleBacklogReceivedAt is set when the newest stale status's source
	// delivers a backlog: its newest receipt.
	StaleBacklogReceivedAt *time.Time
	StaleStates            StaleStates
	// StatusSources are the status sources whose rows decided a state.
	StatusSources []string
}

func newer(a *time.Time, b time.Time) *time.Time {
	if a == nil || b.After(*a) {
		return &b
	}
	return a
}

// JudgePoints judges every point of one place record at now.
func JudgePoints(points []PlacePoint, windows []StatusSource, liveness []SourceLiveness, now time.Time) JudgedPoints {
	var j JudgedPoints
	for _, p := range points {
		sr := p.statusRow()
		r := judgeStatus(sr, windows, now, liveness)
		switch r.kind {
		case "":
			j.States = append(j.States, VerdictUnknown)
			j.PointAsOf = append(j.PointAsOf, nil)
			j.PointStatedAt = append(j.PointStatedAt, nil)
			continue
		case VerdictStale:
			j.States = append(j.States, VerdictStale)
		default:
			j.States = append(j.States, r.kind)
		}
		asOf, stated := r.asOf, statementAt(*sr)
		j.PointAsOf = append(j.PointAsOf, &asOf)
		j.PointStatedAt = append(j.PointStatedAt, &stated)
		if !containsString(j.StatusSources, sr.Source) {
			j.StatusSources = append(j.StatusSources, sr.Source)
		}
		if r.kind != VerdictStale {
			j.StatusAsOf = newer(j.StatusAsOf, r.asOf)
			continue
		}
		j.StaleStates = j.StaleStates.add(staleOf(r.reported))
		if j.StaleAsOf == nil || r.asOf.After(*j.StaleAsOf) {
			j.StaleBacklogReceivedAt = r.backlog
		} else if r.asOf.Equal(*j.StaleAsOf) && r.backlog != nil {
			j.StaleBacklogReceivedAt = newer(j.StaleBacklogReceivedAt, *r.backlog)
		}
		j.StaleAsOf = newer(j.StaleAsOf, r.asOf)
	}
	return j
}

func staleOf(v Verdict) StaleStates {
	switch v {
	case VerdictAvailable:
		return StaleStates{Available: 1}
	case VerdictOccupied:
		return StaleStates{Occupied: 1}
	default:
		return StaleStates{OutOfOrder: 1}
	}
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Tally counts verdicts.
type Tally struct{ Available, Occupied, OutOfOrder, Unknown, Stale, Total int }

// TallyOf counts states.
func TallyOf(states []Verdict) Tally {
	t := Tally{Total: len(states)}
	for _, s := range states {
		switch s {
		case VerdictAvailable:
			t.Available++
		case VerdictOccupied:
			t.Occupied++
		case VerdictOutOfOrder:
			t.OutOfOrder++
		case VerdictStale:
			t.Stale++
		default:
			t.Unknown++
		}
	}
	return t
}

// allOutOfOrder: every point out of order by a current status.
func allOutOfOrder(states []Verdict) bool {
	if len(states) == 0 {
		return false
	}
	for _, s := range states {
		if s != VerdictOutOfOrder {
			return false
		}
	}
	return true
}

// FreeNowOf is the platform's one rule for the only-available filter
// over judged states (freeNowOf).
func FreeNowOf(states []Verdict) FreeNow {
	t := TallyOf(states)
	switch {
	case t.Available > 0:
		return Free
	case t.Available+t.Occupied+t.OutOfOrder == 0 && t.Stale > 0:
		return Stale
	case t.Available+t.Occupied+t.OutOfOrder == 0:
		return NoStatus
	}
	return NotFree
}
