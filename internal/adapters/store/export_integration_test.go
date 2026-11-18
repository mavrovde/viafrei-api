//go:build integration

package store

// HoldJunctionRead calls hold before each road's junction read of the
// background road read (WarmRoads, KeepRoads), so a test can hold that read
// part-way.
func (s *Store) HoldJunctionRead(hold func()) { s.landsRead = hold }
