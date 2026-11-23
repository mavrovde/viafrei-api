//go:build integration

package store

// NoteParity counts a run of the named statement name beside its view
// statement made outside the store (the namedCases harness), with the rows
// compared, so a full run's check that every named statement ran counts it.
func NoteParity(name string, rows int) {
	parityMu.Lock()
	defer parityMu.Unlock()
	parityRuns[name]++
	parityRows[name] += rows
}
