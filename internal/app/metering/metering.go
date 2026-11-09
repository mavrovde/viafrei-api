// Package metering counts usage per key, hour and operation in memory and
// writes it to the access store in one batched write per interval and once
// more on shutdown (RATE-014). There is no storage write per request. A crash
// loses at most the counts of one interval: the ones not yet written.
package metering

import (
	"context"
	"sync"
	"time"
)

// Refusal is a reason a gate shed a keyed request.
type Refusal string

// The refusals a usage record counts apart.
const (
	RateLimited       Refusal = "rate_limited"
	QuotaExceeded     Refusal = "quota_exceeded"
	Overloaded        Refusal = "overloaded"
	DeadlineExhausted Refusal = "deadline_exhausted"
)

// Event is one keyed request as metering sees it.
type Event struct {
	Key       string
	Operation string
	// NotModified is set for a 304 answer.
	NotModified bool
	// Refused is empty unless a gate shed the request.
	Refused Refusal
	// Storage is the time the request's reads held storage.
	Storage time.Duration
}

// Counts are the counters of one (key, hour, operation).
type Counts struct {
	Requests          int64
	NotModified       int64
	RateLimited       int64
	QuotaExceeded     int64
	Overloaded        int64
	DeadlineExhausted int64
	Storage           time.Duration
}

func (c *Counts) add(o Counts) {
	c.Requests += o.Requests
	c.NotModified += o.NotModified
	c.RateLimited += o.RateLimited
	c.QuotaExceeded += o.QuotaExceeded
	c.Overloaded += o.Overloaded
	c.DeadlineExhausted += o.DeadlineExhausted
	c.Storage += o.Storage
}

// Row is one usage record: the counts of a key's operation in one hour,
// the hour named by its start in UTC.
type Row struct {
	Key       string
	Hour      time.Time
	Operation string
	Counts
}

// Writer adds rows to the stored usage: a row that exists is added to.
type Writer interface {
	AddUsage(ctx context.Context, rows []Row) error
}

// Settings configure a meter.
type Settings struct {
	FlushInterval time.Duration
	WriteTimeout  time.Duration
	// MaxCounters bounds the counters held between flushes.
	MaxCounters int
}

type counterKey struct {
	key, operation string
	hour           time.Time
}

// Meter holds the counts not written yet.
type Meter struct {
	s   Settings
	w   Writer
	now func() time.Time

	mu       sync.Mutex
	counters map[counterKey]*Counts
	dropped  int64
}

// New returns a meter writing to w; now is the clock (time.Now when nil).
func New(s Settings, w Writer, now func() time.Time) *Meter {
	if now == nil {
		now = time.Now
	}
	return &Meter{s: s, w: w, now: now, counters: map[counterKey]*Counts{}}
}

// Record counts one event in the hour of now.
func (m *Meter) Record(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := counterKey{key: e.Key, operation: e.Operation, hour: m.now().UTC().Truncate(time.Hour)}
	c, ok := m.counters[k]
	if !ok {
		if len(m.counters) >= m.s.MaxCounters {
			m.dropped++
			return
		}
		c = &Counts{}
		m.counters[k] = c
	}
	c.Requests++
	if e.NotModified {
		c.NotModified++
	}
	switch e.Refused {
	case RateLimited:
		c.RateLimited++
	case QuotaExceeded:
		c.QuotaExceeded++
	case Overloaded:
		c.Overloaded++
	case DeadlineExhausted:
		c.DeadlineExhausted++
	}
	c.Storage += e.Storage
}

// Report is the outcome of one flush.
type Report struct {
	// Rows is the number of usage records written.
	Rows int
	// Dropped is the number of events not counted since the last flush
	// because the counters were full.
	Dropped int64
	Err     error
}

// Flush writes every held counter in one batch, within the write timeout. A
// failed write keeps the counts for the next flush.
func (m *Meter) Flush(ctx context.Context) Report {
	m.mu.Lock()
	held := m.counters
	m.counters = map[counterKey]*Counts{}
	r := Report{Dropped: m.dropped}
	m.dropped = 0
	m.mu.Unlock()
	if len(held) == 0 {
		return r
	}
	rows := make([]Row, 0, len(held))
	for k, c := range held {
		rows = append(rows, Row{Key: k.key, Hour: k.hour, Operation: k.operation, Counts: *c})
	}
	ctx, cancel := context.WithTimeout(ctx, m.s.WriteTimeout)
	defer cancel()
	if err := m.w.AddUsage(ctx, rows); err != nil {
		m.mu.Lock()
		for k, c := range held {
			if mine, ok := m.counters[k]; ok {
				mine.add(*c)
			} else {
				m.counters[k] = c
			}
		}
		m.mu.Unlock()
		r.Err = err
		return r
	}
	r.Rows = len(rows)
	return r
}

// Run flushes every interval until ctx ends, then once more with a context
// of its own, so the counts of the last interval are written on shutdown.
// Each flush's report goes to report.
func (m *Meter) Run(ctx context.Context, report func(Report)) {
	t := time.NewTicker(m.s.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			report(m.Flush(context.WithoutCancel(ctx)))
			return
		case <-t.C:
			report(m.Flush(ctx))
		}
	}
}
