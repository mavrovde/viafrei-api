//go:build integration

package store

import (
	"context"

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
