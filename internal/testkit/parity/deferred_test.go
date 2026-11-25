package parity_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

func TestDeferredDifferences(t *testing.T) {
	file := func(s string) fstest.MapFS {
		return fstest.MapFS{parity.DeferredFile: {Data: []byte(s)}}
	}
	ds, err := parity.LoadDeferred(file("# judged\n\nC\top\textra\tdata[*].extra\tthe API states it\nD\top\t*\tdata.url\tthe recording's language\n"))
	if err != nil || len(ds) != 2 || ds[1].Kind != "*" {
		t.Fatalf("%+v %v", ds, err)
	}
	for line, want := range map[string]int{
		`extra   data[p:1].extra: got "[]"`:    0,
		`extra   data[p:1].extra.n: got "1"`:   0,
		`missing data[p:1].extra: want "1"`:    -1,
		`changed data.url: want "a", got "b"`:  1,
		`changed data.urls: want "a", got "b"`: -1,
	} {
		if got := parity.Defers(ds, "op", line); got != want {
			t.Errorf("%s: %d, want %d", line, got, want)
		}
	}
	if parity.Defers(ds, "other", `changed data.url: want "a", got "b"`) != -1 {
		t.Error("another operation")
	}
	if ds, err := parity.LoadDeferred(fstest.MapFS{}); ds != nil || err != nil {
		t.Errorf("absent: %v %v", ds, err)
	}
	if _, err := parity.LoadDeferred(failingFS{}); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("unreadable: %v", err)
	}
	if ds[0].BlindSpot() || !(parity.Deferral{Reason: parity.BlindSpotPrefix + "x"}).BlindSpot() {
		t.Error("blind spot")
	}
	for in, want := range map[string]string{
		"C\top\textra\tx\n":        "want class",
		"C\t\textra\tx\twhy\n":     "want class",
		"A\top\textra\tx\twhy\n":   "class A difference is fixed",
		"C\top\tdropped\tx\twhy\n": `kind "dropped"`,
	} {
		if _, err := parity.LoadDeferred(file(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// failingFS refuses every open with something other than absence.
type failingFS struct{}

func (failingFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
