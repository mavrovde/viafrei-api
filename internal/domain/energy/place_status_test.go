package energy

import (
	"reflect"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var judgeNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) *time.Time {
	t := judgeNow.Add(-d)
	return &t
}

func dur(d time.Duration) *time.Duration { return &d }

// sw is a status source of a 15-minute stale limit and an hour's
// statement age.
var sw = StatusSource{Key: "s", StaleAfter: 15 * time.Minute, StatementMaxAge: time.Hour}

func TestStatusWords(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	if _, ok := lowerWord(nil); ok {
		t.Error("lowerWord(nil) is ok")
	}
	if w, _ := lowerWord(sp(" Out_Of-Order ")); w != "outoforder" {
		t.Errorf("lowerWord = %q", w)
	}
	for name, c := range map[string]struct {
		row  StatusRow
		want Verdict
	}{
		"operation blocks": {StatusRow{OperationStatus: sp("NOT_IN_OPERATION"), Status: sp("available")}, VerdictOutOfOrder},
		"opening blocks":   {StatusRow{OperationStatus: sp("operational"), OpeningStatus: sp("temporarily closed"), Status: sp("available")}, VerdictOutOfOrder},
		"no status":        {StatusRow{OpeningStatus: sp("open")}, VerdictUnknown},
		"unknown word":     {StatusRow{Status: sp("sleeping")}, VerdictUnknown},
		"charging":         {StatusRow{Status: sp("CHARGING")}, VerdictOccupied},
		"available":        {StatusRow{Status: sp("Available")}, VerdictAvailable},
	} {
		if got := reportedState(c.row); got != c.want {
			t.Errorf("%s: reportedState = %q", name, got)
		}
	}
	obs := *ago(10 * time.Minute)
	for name, c := range map[string]struct {
		row  StatusRow
		want time.Time
	}{
		"no times":         {StatusRow{ObservedAt: obs}, obs},
		"publication time": {StatusRow{ObservedAt: obs, SourceTs: ago(20 * time.Minute)}, *ago(20 * time.Minute)},
		"after the pull":   {StatusRow{ObservedAt: obs, SourceTs: ago(time.Minute)}, obs},
		"delta":            {StatusRow{ObservedAt: obs, SourceTs: ago(20 * time.Minute), LastUpdated: ago(30 * time.Minute), ExchangeProtocol: sp(" DeltaPull ")}, *ago(30 * time.Minute)},
	} {
		if got := statementAt(c.row); !got.Equal(c.want) {
			t.Errorf("%s: statementAt = %v", name, got)
		}
	}
}

func TestSourceLiveness(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	frozen := sw
	frozen.FrozenAfter = dur(2 * time.Hour)
	for name, c := range map[string]struct {
		w    StatusSource
		l    SourceLiveness
		want bool
	}{
		"no cycle":        {sw, SourceLiveness{}, false},
		"old cycle":       {sw, SourceLiveness{LastConclusiveAt: ago(time.Hour)}, false},
		"no frozen limit": {sw, SourceLiveness{LastConclusiveAt: ago(time.Minute), ValidatorSince: ago(9 * time.Hour)}, true},
		"no validator":    {frozen, SourceLiveness{LastConclusiveAt: ago(time.Minute)}, true},
		"moving":          {frozen, SourceLiveness{LastConclusiveAt: ago(time.Minute), ValidatorSince: ago(time.Hour)}, true},
		"frozen":          {frozen, SourceLiveness{LastConclusiveAt: ago(time.Minute), ValidatorSince: ago(3 * time.Hour)}, false},
	} {
		if got := sourceSpeaksForNow(c.w, c.l, judgeNow); got != c.want {
			t.Errorf("%s: sourceSpeaksForNow = %v", name, got)
		}
	}
	for name, c := range map[string]struct {
		l    SourceLiveness
		want bool
	}{
		"nothing received":  {SourceLiveness{MovedAt: ago(2 * time.Hour)}, false},
		"received long ago": {SourceLiveness{ReceivedAt: ago(2 * time.Hour), MovedAt: ago(3 * time.Hour)}, false},
		"recent change":     {SourceLiveness{ReceivedAt: ago(time.Minute), MovedAt: ago(time.Minute)}, false},
		"backlog":           {SourceLiveness{ReceivedAt: ago(time.Minute), MovedAt: ago(2 * time.Hour)}, true},
	} {
		if got := deliversBacklog(sw, c.l, judgeNow, judgeNow); got != c.want {
			t.Errorf("%s: deliversBacklog = %v", name, got)
		}
	}
}

func TestJudgeStatus(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	delta := sp("deltaPull")
	row := func(observed time.Duration, proto *string) *StatusRow {
		return &StatusRow{Source: "s", ObservedAt: *ago(observed), Status: sp("available"), ExchangeProtocol: proto}
	}
	withStated := func(r *StatusRow, stated time.Duration) *StatusRow {
		r.LastUpdated, r.SourceTs = ago(stated), ago(stated)
		return r
	}
	live := func(l SourceLiveness) []SourceLiveness { l.Source = "s"; return []SourceLiveness{l} }
	fresh := func(asOf time.Duration) reading { return reading{kind: VerdictAvailable, asOf: *ago(asOf)} }
	stale := func(asOf time.Duration) reading {
		return reading{kind: VerdictStale, reported: VerdictAvailable, asOf: *ago(asOf)}
	}
	for name, c := range map[string]struct {
		row      *StatusRow
		windows  []StatusSource
		liveness []SourceLiveness
		want     reading
	}{
		"no row":       {nil, []StatusSource{sw}, nil, reading{}},
		"no sw":        {row(time.Minute, nil), []StatusSource{{Key: "other"}}, nil, reading{}},
		"unknown word": {&StatusRow{Source: "s", ObservedAt: judgeNow, Status: sp("?")}, []StatusSource{sw}, nil, reading{}},
		"fresh":        {row(time.Minute, nil), []StatusSource{sw}, nil, fresh(time.Minute)},
		"old pull":     {row(20*time.Minute, nil), []StatusSource{sw}, nil, stale(20 * time.Minute)},
		"old statement": {withStated(row(time.Minute, nil), 2*time.Hour), []StatusSource{sw}, nil,
			stale(2 * time.Hour)},
		"gap, snapshot": {row(time.Minute, nil), []StatusSource{sw}, live(SourceLiveness{GapAt: ago(0)}), stale(time.Minute)},
		"restated": {row(10*time.Minute, nil), []StatusSource{sw},
			live(SourceLiveness{RestatedAt: ago(2 * time.Minute), RestatedReceivedAt: ago(time.Minute), LastConclusiveAt: ago(time.Minute)}),
			fresh(2 * time.Minute)},
		"restated, cycle old": {row(10*time.Minute, nil), []StatusSource{sw},
			live(SourceLiveness{RestatedAt: ago(2 * time.Minute), RestatedReceivedAt: ago(time.Minute), LastConclusiveAt: ago(20 * time.Minute)}),
			stale(2 * time.Minute)},
		"delta beyond trust": {row(31*24*time.Hour, delta), []StatusSource{sw}, nil, reading{}},
		"delta, gap":         {row(time.Minute, delta), []StatusSource{sw}, live(SourceLiveness{GapAt: ago(0)}), stale(time.Minute)},
		"delta, no ledger":   {row(20*time.Minute, delta), []StatusSource{sw}, nil, stale(20 * time.Minute)},
		"delta, silent source": {row(time.Minute, delta), []StatusSource{sw}, live(SourceLiveness{LastConclusiveAt: ago(time.Hour)}),
			stale(time.Minute)},
		"delta, confirmed by the source's move": {withStated(row(20*time.Minute, delta), 50*time.Minute), []StatusSource{sw},
			live(SourceLiveness{LastConclusiveAt: ago(time.Minute), MovedAt: ago(30 * time.Minute)}), fresh(30 * time.Minute)},
		"delta, backlog": {withStated(row(time.Minute, delta), 2*time.Hour), []StatusSource{sw},
			live(SourceLiveness{LastConclusiveAt: ago(time.Minute), MovedAt: ago(90 * time.Minute), ReceivedAt: ago(time.Minute)}),
			reading{kind: VerdictStale, reported: VerdictAvailable, asOf: *ago(90 * time.Minute), backlog: ago(time.Minute)}},
	} {
		if got := judgeStatus(c.row, c.windows, judgeNow, c.liveness); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: judgeStatus = %+v, want %+v", name, got, c.want)
		}
	}
}

func TestJudgePointsAndTallies(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	s, other := sp("s"), sp("t")
	point := func(source *string, status string, observed time.Duration) PlacePoint {
		return PlacePoint{StatusSource: source, Status: sp(status), ObservedAt: ago(observed)}
	}
	windows := []StatusSource{sw, {Key: "t", StaleAfter: 15 * time.Minute, StatementMaxAge: time.Hour}}
	backlogged := func(observed time.Duration) PlacePoint {
		p := point(other, "occupied", observed)
		p.ExchangeProtocol, p.LastUpdated = sp("deltapull"), ago(2*time.Hour)
		return p
	}
	liveness := []SourceLiveness{{Source: "t", LastConclusiveAt: ago(time.Minute), MovedAt: ago(90 * time.Minute), ReceivedAt: ago(2 * time.Minute)}}
	j := JudgePoints([]PlacePoint{
		{PointKey: "none"},
		point(s, "available", time.Minute),
		point(s, "faulted", 3*time.Minute),
		point(s, "available", 30*time.Minute),
		backlogged(time.Minute),
		backlogged(2 * time.Minute),
		point(s, "occupied", 40*time.Minute),
	}, windows, liveness, judgeNow)
	wantStates := []Verdict{VerdictUnknown, VerdictAvailable, VerdictOutOfOrder, VerdictStale, VerdictStale, VerdictStale, VerdictStale}
	if !reflect.DeepEqual(j.States, wantStates) {
		t.Errorf("States = %v", j.States)
	}
	if !j.StatusAsOf.Equal(*ago(time.Minute)) || !j.StaleAsOf.Equal(*ago(30 * time.Minute)) {
		t.Errorf("StatusAsOf %v, StaleAsOf %v", j.StatusAsOf, j.StaleAsOf)
	}
	if j.StaleBacklogReceivedAt != nil {
		t.Errorf("a newer stale status without a backlog keeps one: %v", j.StaleBacklogReceivedAt)
	}
	if j.StaleStates != (StaleStates{Available: 1, Occupied: 3}) || !reflect.DeepEqual(j.StatusSources, []string{"s", "t"}) {
		t.Errorf("StaleStates %+v, StatusSources %v", j.StaleStates, j.StatusSources)
	}
	if j.PointAsOf[0] != nil || j.PointStatedAt[0] != nil || !j.PointAsOf[4].Equal(*ago(90 * time.Minute)) || !j.PointStatedAt[4].Equal(*ago(2 * time.Hour)) {
		t.Errorf("PointAsOf %v, PointStatedAt %v", j.PointAsOf, j.PointStatedAt)
	}
	// Two stale points of one time, both of a backlogged source: its receipt
	// is kept.
	b := JudgePoints([]PlacePoint{backlogged(time.Minute), backlogged(time.Minute)}, windows, liveness, judgeNow)
	if b.StaleBacklogReceivedAt == nil || !b.StaleBacklogReceivedAt.Equal(*ago(2 * time.Minute)) {
		t.Errorf("StaleBacklogReceivedAt = %v", b.StaleBacklogReceivedAt)
	}
	if staleOf(VerdictOutOfOrder) != (StaleStates{OutOfOrder: 1}) {
		t.Error("staleOf(out of order)")
	}
	if got := TallyOf([]Verdict{VerdictAvailable, VerdictOccupied, VerdictOutOfOrder, VerdictStale, VerdictUnknown}); got != (Tally{1, 1, 1, 1, 1, 5}) {
		t.Errorf("TallyOf = %+v", got)
	}
	for _, c := range []struct {
		states []Verdict
		all    bool
		free   FreeNow
	}{
		{nil, false, NoStatus},
		{[]Verdict{VerdictOutOfOrder, VerdictOutOfOrder}, true, NotFree},
		{[]Verdict{VerdictOutOfOrder, VerdictAvailable}, false, Free},
		{[]Verdict{VerdictStale, VerdictUnknown}, false, Stale},
		{[]Verdict{VerdictOccupied, VerdictStale}, false, NotFree},
	} {
		if got := allOutOfOrder(c.states); got != c.all {
			t.Errorf("allOutOfOrder(%v) = %v", c.states, got)
		}
		if got := FreeNowOf(c.states); got != c.free {
			t.Errorf("FreeNowOf(%v) = %v", c.states, got)
		}
	}
}
