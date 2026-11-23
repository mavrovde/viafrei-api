//go:build integration

package store

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
