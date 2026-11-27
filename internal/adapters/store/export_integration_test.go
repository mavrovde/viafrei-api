//go:build integration

package store

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// HoldJunctionRead calls hold before each road's junction read of the
// background road read (WarmRoads, KeepRoads), so a test can hold that read
// part-way.
func (s *Store) HoldJunctionRead(hold func()) { s.landsRead = hold }

// WithReferenceVersions keeps the reference fingerprints of the versions read
// for ttl while their probe stays (db.reference_versions_ttl).
func (s *Store) WithReferenceVersions(ttl time.Duration) *Store { s.refs.ttl = ttl; return s }

// PunctualityOfArea is the punctuality of the Land of the area $1 alone.
func PunctualityOfArea() string { return punctualityOfArea }

// OpaqueRead is a statement opaque rewrote: as sent, as written, and the
// types it declares.
type OpaqueRead struct {
	Sent, SQL string
	Types     []string
}

// OpaqueReads lists every statement opaque rewrote when the package loaded.
func OpaqueReads() []OpaqueRead {
	opaqueMu.Lock()
	defer opaqueMu.Unlock()
	out := make([]OpaqueRead, 0, len(opaqueReads))
	for sent, r := range opaqueReads {
		out = append(out, OpaqueRead{Sent: sent, SQL: r.SQL, Types: r.Types})
	}
	return out
}

// NamedRead is a named statement of the store: its name, text and types.
type NamedRead struct {
	Name, SQL string
	Types     []string
}

// NamedReads lists every named statement of the store, by name.
func NamedReads() []NamedRead {
	out := []NamedRead{}
	for _, s := range namedList() {
		out = append(out, NamedRead(s))
	}
	return out
}

// RunNamed runs sql as a named statement of the store called name, on tx.
func RunNamed(ctx context.Context, tx pgx.Tx, name, sql string) error {
	rows, err := run(ctx, tx, stmt{Name: name, SQL: sql}, nil...)
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	return err
}

// ParityMiss is a named statement that answered other rows than the view
// statement it replaced, with the same arguments, in the same transaction.
type ParityMiss struct {
	Name, Plan string
	Args       []any
	Got, Want  []string
}

var (
	parityMu     sync.Mutex
	parityMisses []ParityMiss
	parityRuns   = map[string]int{}
	parityRows   = map[string]int{}
)

// ParityResults are the misses, the runs and the rows compared per statement of every named
// statement the tests ran, each beside its frozen view statement.
func ParityResults() ([]ParityMiss, map[string]int, map[string]int) {
	parityMu.Lock()
	defer parityMu.Unlock()
	return slices.Clone(parityMisses), maps.Clone(parityRuns), maps.Clone(parityRows)
}

// parityCheck is a named statement a test ran, with its arguments and the
// key's own-audience setting, kept to be compared with its view statement
// after the tests (ReplayParity), outside the test's request budget.
type parityCheck struct {
	name, sql, legacy string
	args              []any
	own               string
	// url is the request connection's connection string: the database (a
	// test's private copy among them) and the role the read ran as, on a
	// dialer of its own (not a test's faulty one).
	url string
}

var (
	parityChecks []parityCheck
	parityKeys   = map[string]bool{}
)

// testKey carries, in a test's context, the test the reads under it belong to.
type testKey struct{}

// WithTest is ctx carrying t: a named statement read under it is compared
// with its view statement in a cleanup of t, registered at the read, so
// after the test's body but before the cleanups registered earlier (the
// rows the test planted before the read are still there; t.Cleanup is
// last in, first out).
func WithTest(ctx context.Context, t testing.TB) context.Context {
	return context.WithValue(ctx, testKey{}, t)
}

// batchKey carries, in a test's context, the batch its reads' checks join.
type batchKey struct{}

// parityBatch is the checks of reads under one WithBatch context.
type parityBatch struct {
	mu     sync.Mutex
	checks []parityCheck
}

// batchReplays is how many of a batch's checks replay side by side, each on
// a connection of its own.
const batchReplays = 4

// WithBatch is ctx carrying t, for a test that reads many statements after
// planting its rows: the checks of the reads under it are compared side by
// side, batchReplays at a time, in one cleanup of t registered now, so
// before the cleanups registered earlier (the rows planted before this
// call are still there). A read under it must come after every row it
// reads is planted and before t plants rows of another database.
func WithBatch(ctx context.Context, t testing.TB) context.Context {
	b := &parityBatch{}
	t.Cleanup(func() {
		b.mu.Lock()
		checks := slices.Clone(b.checks)
		b.mu.Unlock()
		next := make(chan parityCheck)
		var wg sync.WaitGroup
		for range batchReplays {
			wg.Go(func() {
				for c := range next {
					replayInTest(ctx, t, c)
				}
			})
		}
		for _, c := range checks {
			next <- c
		}
		close(next)
		wg.Wait()
	})
	return context.WithValue(WithTest(ctx, t), batchKey{}, b)
}

// replayBound is the ceiling on one check's replay: three reads, outside
// the request's budget.
const replayBound = time.Minute

// replayInTest compares c in t's cleanup, under a bound of its own (the
// request's context has ended by then): a miss fails t.
func replayInTest(ctx context.Context, t testing.TB, c parityCheck) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replayBound)
	defer cancel()
	conn, err := pgx.Connect(ctx, c.url)
	if err != nil {
		t.Errorf("parity %s: connect: %v", c.name, err)
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := replay(ctx, conn, c, func(m ParityMiss) {
		t.Errorf("parity %s (%s) %v:\n got %q\nwant %q", m.Name, m.Plan, m.Args, m.Got, m.Want)
	}); err != nil {
		t.Errorf("parity %s: the replay failed: %v", c.name, err)
	}
}

// parity turns on the parity of the named statements against the frozen
// view statements they replaced (-args -parity): a verification set of its
// own, as the benchmark set and internal/perf are, run by CI as a step of
// its own until the views are dropped. Without it no read is compared and
// no test pays for the comparison.
var parity = flag.Bool("parity", false, "compare every named statement with the frozen view statement it replaced")

// noPlatformRead are the named statements the platform has no statement
// of, so none to compare with, each with its reason.
var noPlatformRead = map[string]string{
	"api_register_probe":         "the API's own probe of the row versions its register cache is read from; it answers no question",
	"api_charging_feed_register": "the platform holds the charging feed register as code, not as a table it reads",
	"api_stop_stations":          "the platform's route lists a station page's stops from the page, not the station pages around a stop; its rows are asserted on their own (TestStopStationIsTheStationThatListsIt)",
	"api_state_events_english":   "the platform names a Land's events in German only; the English names are the API's own (lang=en)",
	"api_location_land_area":     "the platform judges the Land and abroad from gazetteer points and an address scan; the Land areas' read is the API's own (#299), its rows asserted on their own (TestTheLandAndAbroadAreReadFromTheLandAreas)",
}

// NoPlatformRead is why a named statement has no platform statement to
// compare with; empty when it has one.
func NoPlatformRead(name string) string { return noPlatformRead[name] }

// ParityOn reports whether the run compares the named statements with the
// frozen view statements (-parity); the flags are parsed.
func ParityOn() bool { return *parity }

// EnableParity keeps every named statement a test runs with its arguments,
// to run beside its view statement (ReplayParity, or the test's cleanup):
// the comparison takes three reads, which inside the request would spend
// its budget.
func EnableParity() {
	beside = func(ctx context.Context, tx pgx.Tx, s stmt, args []any) {
		legacy, ok := legacyReads[s.Name]
		if !ok && noPlatformRead[s.Name] != "" {
			return
		}
		if !ok {
			parityMu.Lock()
			defer parityMu.Unlock()
			parityMisses = append(parityMisses, ParityMiss{Name: s.Name, Plan: "no frozen view statement", Args: args})
			return
		}
		// The key's setting, as the read runs under it: one read of a
		// setting, the only work the hook does in the request.
		var own string
		if err := tx.QueryRow(ctx, "select coalesce(current_setting('viafrei_api.own_key', true), '')").Scan(&own); err != nil {
			return // the request's budget ran out, or a failure the test injected: the test's own run reports it
		}
		key := fmt.Sprintf("%s\x1f%s\x1f%#v", s.Name, own, args)
		c := parityCheck{name: s.Name, sql: s.SQL, legacy: legacy, args: slices.Clone(args), own: own}
		t, inTest := ctx.Value(testKey{}).(testing.TB)
		if inTest {
			key = t.Name() + "\x1f" + key
		}
		parityMu.Lock()
		defer parityMu.Unlock()
		parityRuns[s.Name]++
		if parityKeys[key] {
			return
		}
		parityKeys[key] = true
		if inTest {
			c.url = tx.Conn().Config().ConnString()
			if b, ok := ctx.Value(batchKey{}).(*parityBatch); ok {
				b.mu.Lock()
				b.checks = append(b.checks, c)
				b.mu.Unlock()
				return
			}
			// Registered now, so it runs before the cleanups of the rows
			// planted before this read.
			t.Cleanup(func() { replayInTest(ctx, t, c) })
			return
		}
		// A read outside a test's context: compared after the tests.
		parityChecks = append(parityChecks, c)
	}
}

// ReplayParity runs each kept named statement beside its view statement, on
// a connection of its own to url, each in a read-only transaction under the
// key's setting it ran with: the view statement, then the named one under
// the custom plan and under the generic one. A named statement that fails
// where the view statement answered is a miss; a view statement that fails
// (arguments a test meant to fail) is skipped.
func ReplayParity(ctx context.Context, url string) error {
	parityMu.Lock()
	checks := slices.Clone(parityChecks)
	parityMu.Unlock()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for _, c := range checks {
		if err := replay(ctx, conn, c, nil); err != nil {
			return err
		}
	}
	return nil
}

// ReplayedAfter counts, by statement, the checks read outside a test's
// context, so compared only after the tests, against the base seed.
func ReplayedAfter() map[string]int {
	parityMu.Lock()
	defer parityMu.Unlock()
	out := map[string]int{}
	for _, c := range parityChecks {
		out[c.name]++
	}
	return out
}

// replay compares one kept check, each miss also to onMiss when set; it
// fails only when ctx does.
func replay(ctx context.Context, conn *pgx.Conn, c parityCheck, onMiss func(ParityMiss)) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	miss := func(plan string, got, want []string) {
		m := ParityMiss{Name: c.name, Plan: plan, Args: c.args, Got: got, Want: want}
		if onMiss != nil {
			onMiss(m)
		}
		parityMu.Lock()
		defer parityMu.Unlock()
		parityMisses = append(parityMisses, m)
	}
	if _, err := tx.Exec(ctx, "select set_config('viafrei_api.own_key', $1, true)", c.own); err != nil {
		return err
	}
	// The replay runs outside the request's budget, under replayBound: the
	// role's per-read statement timeout would cancel the frozen statement
	// whenever the machine is loaded (four parity shards side by side).
	if _, err := tx.Exec(ctx, "select set_config('statement_timeout', $1, true)", strconv.FormatInt(replayBound.Milliseconds(), 10)); err != nil {
		return err
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	want, frozenErr := textRows(ctx, sp, c.legacy, c.args)
	if frozenErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := sp.Rollback(ctx); err != nil {
			return err
		}
		// The frozen statement failed: only a named statement that fails
		// as well (arguments a test meant to fail) leaves nothing to
		// compare; one that answers is a miss, never a silent skip (a
		// frozen statement left behind by a new parameter).
		if _, err := conn.Prepare(ctx, c.name, c.sql); err != nil {
			return ctx.Err()
		}
		got, err := textRows(ctx, tx, c.name, c.args)
		if err != nil {
			return ctx.Err()
		}
		miss("frozen statement failed: "+frozenErr.Error(), got, nil)
		return nil
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	if _, err := conn.Prepare(ctx, c.name, c.sql); err != nil {
		miss("prepare: "+err.Error(), nil, want)
		return ctx.Err()
	}
	for _, plan := range []string{"force_custom_plan", "force_generic_plan"} {
		if _, err := tx.Exec(ctx, "set local plan_cache_mode = "+plan); err != nil {
			return err
		}
		got, err := textRows(ctx, tx, c.name, c.args)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The view statement answered: a named statement that fails is a miss.
			miss(plan+": "+err.Error(), nil, want)
			return nil
		}
		// Order is each caller's (its own tests hold it): rows compare as a set.
		if !slices.Equal(sorted(got), sorted(want)) {
			miss(plan, got, want)
		}
	}
	parityMu.Lock()
	parityRows[c.name] += len(want)
	parityMu.Unlock()
	return nil
}

// ReplayOne compares one named statement with a frozen one as the parity
// replay does, on a connection of its own to url, and returns its misses
// without keeping them among the run's (a test of the replay itself).
func ReplayOne(ctx context.Context, url, name, sql, legacy string, args []any) ([]ParityMiss, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var got []ParityMiss
	parityMu.Lock()
	n, rows := len(parityMisses), parityRows[name]
	parityMu.Unlock()
	err = replay(ctx, conn, parityCheck{name: name, sql: sql, legacy: legacy, args: args, own: "off"}, func(m ParityMiss) { got = append(got, m) })
	parityMu.Lock()
	parityMisses, parityRows[name] = parityMisses[:n], rows
	parityMu.Unlock()
	return got, err
}

// RowText is a row's values as text, each apart from the next by a unit
// separator, so two rows compare equal only value by value.
func RowText(v []any) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, "\x1f")
}

// textRows reads every row of sql with args as text.
func textRows(ctx context.Context, tx pgx.Tx, sql string, args []any) ([]string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		v, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, RowText(v))
	}
	return out, rows.Err()
}

func sorted(rows []string) []string { s := slices.Clone(rows); slices.Sort(s); return s }

// CutRegisterProbe runs the register's probe (api_register_probe) in a read
// and then waits in that read until ctx ends, as a read the deadline cuts
// after its statement started.
func CutRegisterProbe(ctx context.Context, s *Store) error {
	_, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (struct{}, error) {
		var probe string
		if err := scanOne(run(ctx, tx, registerProbeRead))(&probe); err != nil {
			return struct{}{}, err
		}
		<-ctx.Done()
		return struct{}{}, ctx.Err()
	})
	return err
}

// SleepPastTheBudget runs the register's probe (api_register_probe) in a
// read and then a sleep of sleep, which the read's statement timeout cuts
// when sleep is past the read's budget.
func SleepPastTheBudget(ctx context.Context, s *Store, sleep time.Duration) error {
	_, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (struct{}, error) {
		var probe string
		if err := scanOne(run(ctx, tx, registerProbeRead))(&probe); err != nil {
			return struct{}{}, err
		}
		_, err := tx.Exec(ctx, "select pg_sleep($1)", sleep.Seconds())
		return struct{}{}, err
	})
	return err
}
