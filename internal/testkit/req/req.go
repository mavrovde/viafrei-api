// Package req carries requirement IDs on tests (TEST-001).
//
// A test declares the requirements it verifies by calling Covers first:
//
//	func TestReadyzFailsWithoutStorage(t *testing.T) {
//		req.Covers(t, "FR-SVC-004")
//		...
//	}
//
// Covers writes one marker line per ID to the test's output. `go test -json`
// records that line under the test's name, and cmd/reqreport maps it, with
// the test's result, to the requirement catalogue.
package req

import (
	"regexp"
	"testing"
)

// Marker starts every line Covers writes.
const Marker = "REQ-ID:"

var idPattern = regexp.MustCompile(`^[A-Z]+(-[A-Z]+)*-[0-9]{3}$`)

// Covers records that the calling test verifies the given requirement IDs.
// A malformed ID fails the test at once.
func Covers(t testing.TB, ids ...string) {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("req.Covers needs at least one requirement ID")
	}
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			t.Fatalf("req.Covers: %q is not a requirement ID", id)
		}
		t.Log(Marker, id)
	}
}
