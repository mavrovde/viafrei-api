package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// viewsPending are the files whose statements still read a contract view,
// until each is ported to the platform's tables; the list only shrinks, and
// a file that reads none any more fails until it leaves the list.
var viewsPending = map[string]bool{
	"address.go": true, "bays.go": true, "catalogue.go": true, "coverage.go": true, "disruption.go": true,
	"features.go": true, "freshness.go": true, "hydrogen.go": true,
	"location.go": true, "lookup.go": true, "lorry.go": true, "national.go": true, "nearby.go": true,
	"poi.go": true, "register.go": true, "roads.go": true, "roadshape.go": true, "sharing.go": true,
	"speed.go": true, "state.go": true, "store.go": true, "trainboard.go": true, "transit.go": true,
}

var contractRead = regexp.MustCompile(`(?i)\bcontract\.[a-z_]`)

// TestStoreReadsNoContractView: no string the store sends names a contract
// view, outside the files still pending.
func TestStoreReadsNoContractView(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 20 {
		t.Fatalf("read the store's files: %v %d", err, len(files))
	}
	reads := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && contractRead.MatchString(lit.Value) {
				reads[f] = true
			}
			return true
		})
	}
	for _, f := range slices.Sorted(maps.Keys(reads)) {
		if !viewsPending[f] {
			t.Errorf("%s reads a contract view", f)
		}
	}
	for _, f := range slices.Sorted(maps.Keys(viewsPending)) {
		if !reads[f] {
			t.Errorf("%s reads no contract view: take it off viewsPending", f)
		}
	}
}
