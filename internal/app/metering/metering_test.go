package metering_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// writer records the batches; a set err refuses them.
type writer struct {
	mu      sync.Mutex
	batches [][]metering.Row
	err     error
	ctxErr  error
}

func (w *writer) AddUsage(ctx context.Context, rows []metering.Row) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		w.ctxErr = errors.New("a write without a deadline")
	}
	if w.err != nil {
		return w.err
	}
	w.batches = append(w.batches, slices.Clone(rows))
	return nil
}

func (w *writer) rows() []metering.Row {
	w.mu.Lock()
	defer w.mu.Unlock()
	var all []metering.Row
	for _, b := range w.batches {
		all = append(all, b...)
	}
	slices.SortFunc(all, func(a, b metering.Row) int {
		return strings.Compare(a.Key+a.Operation+a.Hour.String(), b.Key+b.Operation+b.Hour.String())
	})
	return all
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

var t0 = time.Date(2026, 5, 1, 9, 59, 0, 0, time.FixedZone("CEST", 2*3600))

var settings = metering.Settings{FlushInterval: time.Hour, WriteTimeout: time.Second, MaxCounters: 3}

func TestCountsArePerKeyHourAndOperation(t *testing.T) {
	req.Covers(t, "RATE-014")
	clk := &clock{t: t0}
	w := &writer{}
	m := metering.New(settings, w, clk.now)
	m.Record(metering.Event{Key: "a", Operation: "getKey", Storage: 3 * time.Millisecond})
	m.Record(metering.Event{Key: "a", Operation: "getKey", NotModified: true, Storage: 2 * time.Millisecond})
	for _, r := range []metering.Refusal{metering.RateLimited, metering.QuotaExceeded, metering.Overloaded, metering.DeadlineExhausted} {
		m.Record(metering.Event{Key: "a", Operation: "getKey", Refused: r})
	}
	m.Record(metering.Event{Key: "b", Operation: "getKey"})
	clk.t = t0.Add(2 * time.Minute) // the next hour
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	rep := m.Flush(context.Background())
	if rep.Rows != 3 || rep.Err != nil || rep.Dropped != 0 || w.ctxErr != nil {
		t.Fatalf("report %+v %v", rep, w.ctxErr)
	}
	rows := w.rows()
	// The hour is its start in UTC.
	first := metering.Row{Key: "a", Operation: "getKey", Hour: time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC), Counts: metering.Counts{
		Requests: 6, NotModified: 1, RateLimited: 1, QuotaExceeded: 1, Overloaded: 1, DeadlineExhausted: 1, Storage: 5 * time.Millisecond}}
	if rows[0] != first {
		t.Fatalf("first hour %+v", rows[0])
	}
	if rows[1].Key != "a" || rows[1].Hour != time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC) || rows[1].Requests != 1 {
		t.Fatalf("second hour %+v", rows[1])
	}
	if rows[2].Key != "b" || rows[2].Requests != 1 {
		t.Fatalf("other key %+v", rows[2])
	}
	// A flush with nothing held writes nothing.
	if rep := m.Flush(context.Background()); rep.Rows != 0 || len(w.batches) != 1 {
		t.Fatalf("an empty flush: %+v", rep)
	}
}

func TestAFailedWriteKeepsTheCounts(t *testing.T) {
	req.Covers(t, "RATE-014")
	clk := &clock{t: t0}
	w := &writer{err: errors.New("store down")}
	m := metering.New(settings, w, clk.now)
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	if rep := m.Flush(context.Background()); rep.Err == nil || rep.Rows != 0 {
		t.Fatalf("a failed write: %+v", rep)
	}
	// Counts recorded meanwhile are merged with the kept ones.
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	m.Record(metering.Event{Key: "b", Operation: "getKey"})
	w.err = nil
	if rep := m.Flush(context.Background()); rep.Rows != 2 {
		t.Fatalf("the retry: %+v", rep)
	}
	if rows := w.rows(); rows[0].Requests != 2 || rows[1].Requests != 1 {
		t.Fatalf("counts after the retry: %+v", rows)
	}
}

func TestAFailedWriteIsMergedIntoCountsRecordedMeanwhile(t *testing.T) {
	req.Covers(t, "RATE-014")
	clk := &clock{t: t0}
	w := &blockingWriter{started: make(chan struct{}), fail: make(chan struct{})}
	m := metering.New(settings, w, clk.now)
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	done := make(chan metering.Report)
	go func() { done <- m.Flush(context.Background()) }()
	<-w.started
	// Recorded while the write is in flight, into a fresh counter.
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	close(w.fail)
	<-done
	w.ok = true
	if rep := m.Flush(context.Background()); rep.Rows != 1 || w.last[0].Requests != 2 {
		t.Fatalf("merge: %+v %+v", rep, w.last)
	}
}

// blockingWriter fails its first write once fail is closed, then accepts.
type blockingWriter struct {
	started chan struct{}
	fail    chan struct{}
	ok      bool
	last    []metering.Row
}

func (w *blockingWriter) AddUsage(_ context.Context, rows []metering.Row) error {
	if w.ok {
		w.last = rows
		return nil
	}
	close(w.started)
	<-w.fail
	return errors.New("store down")
}

func TestCountersAreBounded(t *testing.T) {
	req.Covers(t, "RATE-014")
	w := &writer{}
	m := metering.New(settings, w, nil)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		m.Record(metering.Event{Key: k, Operation: "getKey"})
	}
	// A held counter still counts when the table is full.
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	rep := m.Flush(context.Background())
	if rep.Rows != 3 || rep.Dropped != 2 {
		t.Fatalf("bounded: %+v", rep)
	}
	if rep := m.Flush(context.Background()); rep.Dropped != 0 {
		t.Fatal("the drop count is not reset by a flush")
	}
}

func TestRunFlushesOnEveryTickAndOnceMoreAtTheEnd(t *testing.T) {
	req.Covers(t, "RATE-014")
	w := &writer{}
	s := settings
	s.FlushInterval = 10 * time.Millisecond
	m := metering.New(s, w, nil)
	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan metering.Report, 100)
	done := make(chan struct{})
	go func() { m.Run(ctx, func(r metering.Report) { reports <- r }); close(done) }()
	m.Record(metering.Event{Key: "a", Operation: "getKey"})
	deadline := time.After(time.Second)
	for len(w.rows()) == 0 {
		select {
		case <-deadline:
			t.Fatal("no flush within 1 s")
		case <-reports:
		}
	}
	// Recorded after the last tick and before the end: written by the
	// final flush, with a context that is not the cancelled one.
	m.Record(metering.Event{Key: "b", Operation: "getKey"})
	cancel()
	<-done
	if rows := w.rows(); len(rows) != 2 || rows[1].Key != "b" {
		t.Fatalf("the final flush: %+v", rows)
	}
}
