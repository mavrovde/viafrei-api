//go:build integration

package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

// HoldJunctionRead calls hold before each road's junction read of the
// background road read (WarmRoads, KeepRoads), so a test can hold that read
// part-way.
func (s *Store) HoldJunctionRead(hold func()) { s.landsRead = hold }

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
}

var (
	parityChecks []parityCheck
	parityKeys   = map[string]bool{}
)

// Every named statement a test runs is kept with its arguments, to run
// beside its view statement after the tests (ReplayParity): the comparison
// takes three reads, which inside the request would spend its budget.
func init() {
	beside = func(ctx context.Context, tx pgx.Tx, s stmt, args []any) {
		legacy, ok := legacyReads[s.Name]
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
		parityMu.Lock()
		defer parityMu.Unlock()
		if !parityKeys[key] {
			parityKeys[key] = true
			parityChecks = append(parityChecks, parityCheck{name: s.Name, sql: s.SQL, legacy: legacy, args: slices.Clone(args), own: own})
		}
		parityRuns[s.Name]++
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
		if err := replay(ctx, conn, c); err != nil {
			return err
		}
	}
	return nil
}

// replay compares one kept check; it fails only when ctx does.
func replay(ctx context.Context, conn *pgx.Conn, c parityCheck) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	miss := func(plan string, got, want []string) {
		parityMu.Lock()
		defer parityMu.Unlock()
		parityMisses = append(parityMisses, ParityMiss{Name: c.name, Plan: plan, Args: c.args, Got: got, Want: want})
	}
	if _, err := tx.Exec(ctx, "select set_config('viafrei_api.own_key', $1, true)", c.own); err != nil {
		return err
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	want, err := textRows(ctx, sp, c.legacy, c.args)
	if err != nil {
		return ctx.Err() // the view statement fails with these arguments too: nothing to compare
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
