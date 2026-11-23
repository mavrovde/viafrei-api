// Package fakelive is a fake live refresher for tests and local runs: it
// stands in for the live refresher of the request protocol, LISTENing on
// live_refresh_requested, claiming the requests the API records
// (live.refresh_requests) with FOR NO KEY UPDATE SKIP LOCKED and settling them once
// (settled_at is null), all as the live refresher's own role,
// viafrei_live_ingestor, and settles each by a script instead of calling a
// provider. The API's role is never used here: it records and reads only.
//
// The script is JSON, one entry per source id of the registry the API reads:
//
//	{"sources": {"<source>": {
//	  "ticks": "<SQL run on refreshed>",
//	  "default": <outcome>,
//	  "subjects": {"<subject key>": <outcome>, ...}}}}
//
//	<outcome> = {"state": "refreshed" | "none_due" | "refused" | "failed" | "never",
//	             "reason": "...", "after_ms": <n>}
//
// The ticks read the request from the transaction's settings:
// current_setting('fakelive.subject_key'), 'fakelive.subject' (JSON text) and
// 'fakelive.at' (the settle instant, RFC 3339).
//
// never leaves the request pending (never claimed); after_ms settles it
// that long after the claim (slow). A source the script does not name is refused
// with the reason "no script". The fake's clock is the asker's: it claims at
// the request's created_at and settles after_ms later, so an API on a fixed
// clock reads deterministic times, and the ticks are dated at the settle.
package fakelive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
)

// Outcome is how a request settles.
type Outcome struct {
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	AfterMS int    `json:"after_ms,omitempty"`
}

// Never is the outcome that leaves a request pending.
const Never = "never"

var settled = []string{"refreshed", "none_due", "refused", "failed"}

// connect opens a session; a test of the package swaps it for one whose
// statements it can fail.
var connect = pgx.Connect

// SourceScript is one source's script.
type SourceScript struct {
	Ticks string `json:"ticks,omitempty"`
	// Partitions are the day-partitioned tables the ticks write: the day
	// partition of the settle instant is made where missing (a fixture
	// database holds only its own days), named <table>_YYYYMMDD as the
	// schema's ensure_daily_partitions names them.
	Partitions []string           `json:"partitions,omitempty"`
	Default    Outcome            `json:"default"`
	Subjects   map[string]Outcome `json:"subjects,omitempty"`
}

// Script is the fake's script.
type Script struct {
	Sources map[string]SourceScript `json:"sources"`
}

// ParseScript reads a script and checks it against the registry: every
// source it names is registered and every outcome is known.
func ParseScript(raw []byte, reg *livesources.Registry) (Script, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var sc Script
	if err := dec.Decode(&sc); err != nil {
		return Script{}, fmt.Errorf("fakelive: script: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(sc.Sources)) {
		if !slices.Contains(reg.Names(), name) {
			return Script{}, fmt.Errorf("fakelive: script: source %q is not in the registry", name)
		}
		s := sc.Sources[name]
		for _, o := range append([]Outcome{s.Default}, slices.Collect(maps.Values(s.Subjects))...) {
			if o.State != Never && !slices.Contains(settled, o.State) || o.AfterMS < 0 {
				return Script{}, fmt.Errorf("fakelive: script: source %q: no outcome %q after %d ms", name, o.State, o.AfterMS)
			}
		}
	}
	return sc, nil
}

// Load reads the registry and the script from their files.
func Load(registryPath, scriptPath string) (Script, error) {
	reg, err := livesources.Load(registryPath, "")
	if err != nil {
		return Script{}, err
	}
	raw, err := os.ReadFile(scriptPath) // #nosec G304 -- the operator's script file
	if err != nil {
		return Script{}, fmt.Errorf("fakelive: %w", err)
	}
	return ParseScript(raw, reg)
}

func (sc Script) outcome(source, key string) Outcome {
	s, ok := sc.Sources[source]
	if !ok {
		return Outcome{State: "refused", Reason: "no script"}
	}
	if o, ok := s.Subjects[key]; ok {
		return o
	}
	return s.Default
}

// Fake is a running fake live refresher.
type Fake struct {
	url    string
	script Script
	claims atomic.Int64
	cancel context.CancelFunc
	done   chan struct{}
	work   sync.WaitGroup
	errMu  sync.Mutex
	err    error
	// skipped are the requests the script leaves pending.
	skipped []string
}

// Start runs the fake against the database at url (an owner's URL: the
// fake sets the viafrei_live_ingestor role to claim and settle, and writes
// the ticks as the owner). It is listening when Start returns.
func Start(ctx context.Context, url string, script Script) (*Fake, error) {
	listener, err := connect(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fakelive: %w", err)
	}
	if _, err := listener.Exec(ctx, "listen live_refresh_requested"); err != nil {
		_ = listener.Close(ctx)
		return nil, fmt.Errorf("fakelive: %w", err)
	}
	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &Fake{url: url, script: script, cancel: cancel, done: make(chan struct{}), skipped: []string{}}
	go f.loop(run, listener)
	return f, nil
}

// Claims is how many requests the fake has claimed.
func (f *Fake) Claims() int64 { return f.claims.Load() }

// Stop stops the fake once the settles under way are done, and returns
// the first error it met, if any.
func (f *Fake) Stop() error {
	f.cancel()
	<-f.done
	f.work.Wait()
	f.errMu.Lock()
	defer f.errMu.Unlock()
	return f.err
}

func (f *Fake) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	f.errMu.Lock()
	defer f.errMu.Unlock()
	if f.err == nil {
		f.err = err
	}
}

// loop drains the pending requests, then waits for the next insert.
func (f *Fake) loop(ctx context.Context, listener *pgx.Conn) {
	defer close(f.done)
	defer func() { _ = listener.Close(context.WithoutCancel(ctx)) }()
	for {
		f.fail(f.drain(ctx))
		if _, err := listener.WaitForNotification(ctx); err != nil {
			f.fail(err)
			if ctx.Err() != nil {
				f.fail(f.heard(ctx, listener))
			}
			return
		}
	}
}

// heard is the loss of the listener a stop interrupted the wait on: the
// connection can have been lost before the stop, and only a read tells. The
// ping runs past the stop's cancel; a lost backend fails the read at once.
func (f *Fake) heard(ctx context.Context, listener *pgx.Conn) error {
	if err := listener.Ping(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("fakelive: the listener was lost: %w", err)
	}
	return nil
}

// claimed is a request the fake claimed.
type claimed struct {
	id, source, key string
	subject         []byte
	createdAt       time.Time
}

// drain claims every pending request but the skipped ones, one by one.
func (f *Fake) drain(ctx context.Context) error {
	conn, err := connect(ctx, f.url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for {
		c, o, ok, err := f.claim(ctx, conn)
		if err != nil || !ok {
			return err
		}
		f.claims.Add(1)
		f.work.Go(func() { f.fail(f.settle(ctx, c, o)) })
	}
}

// claim takes the oldest pending request: a never outcome is skipped and
// left pending, any other is claimed at its created_at. The row lock takes
// no key: an asker being linked to the request holds a key-share lock on it
// (live.refresh_askers' key check), and a key lock would skip the row with no
// insert left to wake the fake.
func (f *Fake) claim(ctx context.Context, conn *pgx.Conn) (claimed, Outcome, bool, error) {
	for {
		var c claimed
		var o Outcome
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "set local role viafrei_live_ingestor"); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `select id::text, source, subject_key, subject::text, created_at from live.refresh_requests
				where state = 'pending' and claimed_at is null and not id::text = any($1)
				order by created_at, id for no key update skip locked limit 1`, f.skipped).Scan(&c.id, &c.source, &c.key, &c.subject, &c.createdAt); err != nil {
				return err
			}
			if o = f.script.outcome(c.source, c.key); o.State == Never {
				return nil
			}
			// the claim of the protocol, at the asker's clock: the state stays pending
			_, err := tx.Exec(ctx, `update live.refresh_requests set claimed_at = created_at where id = $1::bigint`, c.id)
			return err
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return claimed{}, Outcome{}, false, nil
		case err != nil:
			return claimed{}, Outcome{}, false, err
		case o.State == Never:
			f.skipped = append(f.skipped, c.id)
			continue
		}
		return c, o, true, nil
	}
}

// settle waits the outcome's delay, writes the source's ticks on
// refreshed, and settles the request.
func (f *Fake) settle(ctx context.Context, c claimed, o Outcome) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(time.Duration(o.AfterMS) * time.Millisecond):
	}
	at := c.createdAt.Add(time.Duration(o.AfterMS) * time.Millisecond)
	conn, err := connect(ctx, f.url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if ticks := f.script.Sources[c.source].Ticks; o.State == "refreshed" && ticks != "" {
			for _, table := range f.script.Sources[c.source].Partitions {
				if err := dayPartition(ctx, tx, table, at); err != nil {
					return fmt.Errorf("fakelive: partition of %s: %w", table, err)
				}
			}
			// The ticks read the request from settings, so a script uses
			// what it needs and may hold several statements.
			if _, err := tx.Exec(ctx, `select set_config('fakelive.subject_key', $1, true), set_config('fakelive.subject', $2, true),
				set_config('fakelive.at', $3, true)`, c.key, string(c.subject), at.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, ticks); err != nil {
				return fmt.Errorf("fakelive: ticks of %s: %w", c.source, err)
			}
		}
		if _, err := tx.Exec(ctx, "set local role viafrei_live_ingestor"); err != nil {
			return err
		}
		var reason *string
		if o.Reason != "" {
			reason = &o.Reason
		}
		// the settle of the protocol, once, in the transaction of the ticks
		_, err := tx.Exec(ctx, `update live.refresh_requests set state = $2, settled_at = $3, reason = $4, reused = false
			where id = $1::bigint and settled_at is null`, c.id, o.State, at, reason)
		return err
	})
}

// dayPartition makes table's partition of the UTC day of at, unless one of
// its name is there.
func dayPartition(ctx context.Context, tx pgx.Tx, table string, at time.Time) error {
	y, m, d := at.UTC().Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	schema, name, ok := strings.Cut(table, ".")
	if !ok {
		return errors.New("a partitioned table is named schema.table")
	}
	part := pgx.Identifier{schema, name + "_" + day.Format("20060102")}.Sanitize() //nolint:misspell // the pgx method's name
	_, err := tx.Exec(ctx, fmt.Sprintf(`create table if not exists %s partition of %s for values from ('%s') to ('%s')`,
		part, pgx.Identifier{schema, name}.Sanitize(), day.Format(time.RFC3339), day.AddDate(0, 0, 1).Format(time.RFC3339))) //nolint:misspell // the pgx method's name
	return err
}
