//go:build integration

package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
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

// Every named statement a test runs runs first beside its view statement,
// in a savepoint of the test's transaction, under the custom plan and the
// generic one.
func init() {
	beside = func(ctx context.Context, tx pgx.Tx, s stmt, args []any) {
		legacy, ok := legacyReads[s.Name]
		sp, err := tx.Begin(ctx)
		if err != nil {
			return
		}
		defer func() { _ = sp.Rollback(context.WithoutCancel(ctx)) }()
		miss := func(plan string, got, want []string) {
			parityMu.Lock()
			defer parityMu.Unlock()
			parityMisses = append(parityMisses, ParityMiss{Name: s.Name, Plan: plan, Args: args, Got: got, Want: want})
		}
		if !ok {
			miss("no frozen view statement", nil, nil)
			return
		}
		want, err := textRows(ctx, sp, legacy, args)
		if err != nil {
			return // the view statement failed as the test meant the named one to, or the budget ran out
		}
		if _, err := sp.Conn().Prepare(ctx, s.Name, s.SQL); err != nil {
			miss("prepare: "+err.Error(), nil, want)
			return
		}
		for _, plan := range []string{"force_custom_plan", "force_generic_plan"} {
			if _, err := sp.Exec(ctx, "set local plan_cache_mode = "+plan); err != nil {
				return
			}
			got, err := textRows(ctx, sp, s.Name, args)
			if err != nil {
				return // a failure the test injected, or its budget: the test's own run reports it
			}
			// Order is each caller's (its own tests hold it): rows compare as a set.
			if !slices.Equal(sorted(got), sorted(want)) {
				miss(plan, got, want)
			}
		}
		parityMu.Lock()
		parityRuns[s.Name]++
		parityRows[s.Name] += len(want)
		parityMu.Unlock()
	}
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
		out = append(out, fmt.Sprint(v...))
	}
	return out, rows.Err()
}

func sorted(rows []string) []string { s := slices.Clone(rows); slices.Sort(s); return s }
