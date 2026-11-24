package store

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// contractRead is any text naming a relation of schema contract, in any form:
// a whole name, a name built into a format string (contract.%s) or a prefix
// a name is appended to. contractKey is the one configuration key of that
// spelling, which names no relation.
var (
	contractRead = regexp.MustCompile(`(?i)\b` + `contract\.`)
	contractKey  = regexp.MustCompile(`(?i)\b` + `contract\.expected\b`)
)

// contractFrozen are the files outside the store's frozen legacy reads that
// name the read contract's relations on purpose: the fixture writer, whose
// statements are the seed format of the database image's test seed (which
// writes the platform's tables), not a read of this service; and this
// file, whose cases spell the forms the check refuses.
var contractFrozen = map[string]bool{
	"internal/adapters/store/contract_free_test.go": true,
	"internal/fixtures/writer.go":                   true,
}

// frozenLegacy reports whether path is one of the store's frozen legacy reads
// or the parity harness that compares each named statement with them, both
// removed with the views.
func frozenLegacy(path string) bool {
	dir, base := filepath.Split(path)
	return dir == "internal/adapters/store/" && strings.HasSuffix(base, "_integration_test.go") &&
		(strings.HasPrefix(base, "legacy_") || strings.HasPrefix(base, "parity_"))
}

// contractPending are the test files that still read a contract view to set
// up or check their case, until each reads the platform's tables; the list
// only shrinks, and a file that reads none any more fails until it leaves
// the list. A file that is not a test is never pending.
var contractPending = map[string]bool{
	"internal/adapters/accessstore/accessstore_integration_test.go": true,
	"internal/adapters/accessstore/refresh_integration_test.go":     true,
	"internal/adapters/store/catalogue_integration_test.go":         true,
	"internal/adapters/store/commit_integration_test.go":            true,
	"internal/adapters/store/datapaths_integration_test.go":         true,
	"internal/adapters/store/disruption_integration_test.go":        true,
	"internal/adapters/store/fault_integration_test.go":             true,
	"internal/adapters/store/features_integration_test.go":          true,
	"internal/adapters/store/feeding_integration_test.go":           true,
	"internal/adapters/store/location_c_integration_test.go":        true,
	"internal/adapters/store/named_test.go":                         true,
	"internal/adapters/store/objects_integration_test.go":           true,
	"internal/adapters/store/places_integration_test.go":            true,
	"internal/adapters/store/register_integration_test.go":          true,
	"internal/adapters/store/roads_integration_test.go":             true,
	"internal/adapters/store/roadshape_integration_test.go":         true,
	"internal/adapters/store/state_integration_test.go":             true,
	"internal/adapters/store/stations_area_integration_test.go":     true,
	"internal/adapters/store/store_integration_test.go":             true,
	"internal/adapters/store/transit_integration_test.go":           true,
	"internal/adapters/store/weather_cell_integration_test.go":      true,
	"internal/adapters/store/weather_integration_test.go":           true,
	"internal/conformance/scope_lint_test.go":                       true,
	"internal/fixtures/generate_test.go":                            true,
	"internal/fixtures/load_integration_test.go":                    true,
	"internal/perf/perf_integration_test.go":                        true,
	"internal/testkit/baseseed/baseseed_test.go":                    true,
	"internal/testkit/parity/parity_integration_test.go":            true,
	"internal/testkit/pgtest/pgtest_integration_test.go":            true,
}

// namesContract reports whether a string literal names a contract relation.
func namesContract(lit string) bool {
	return contractRead.MatchString(contractKey.ReplaceAllString(lit, ""))
}

// contractReaders are the repository's Go files (relative, slash-separated)
// with a string literal naming a contract relation.
func contractReaders(t *testing.T, root string) map[string]bool {
	t.Helper()
	reads := map[string]bool{}
	fset := token.NewFileSet()
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		n++
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// A literal naming a contract relation holds the word, in any
		// case: a file without it is not parsed.
		if !bytes.Contains(bytes.ToLower(src), []byte("contract")) {
			return nil
		}
		file, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		ast.Inspect(file, func(x ast.Node) bool {
			if lit, ok := x.(*ast.BasicLit); ok && lit.Kind == token.STRING && namesContract(lit.Value) {
				reads[rel] = true
			}
			return true
		})
		return nil
	})
	if err != nil || n < 100 {
		t.Fatalf("read the repository's files: %v %d", err, n)
	}
	return reads
}

// TestRepositoryReadsNoContractView: no string anywhere in the repository
// names a contract relation, in any form, outside the store's frozen legacy
// reads, the fixture writer and the test files still pending.
func TestRepositoryReadsNoContractView(t *testing.T) {
	reads := contractReaders(t, filepath.Join("..", "..", ".."))
	for _, f := range slices.Sorted(maps.Keys(reads)) {
		switch {
		case frozenLegacy(f) || contractFrozen[f]:
		case contractPending[f] && strings.HasSuffix(f, "_test.go"):
		default:
			t.Errorf("%s names a contract view", f)
		}
	}
	for _, f := range slices.Sorted(maps.Keys(contractPending)) {
		if !reads[f] {
			t.Errorf("%s names no contract view: take it off contractPending", f)
		}
	}
	for _, f := range slices.Sorted(maps.Keys(contractFrozen)) {
		if !reads[f] {
			t.Errorf("%s names no contract view: take it off contractFrozen", f)
		}
	}
}

// TestContractReadCatchesEveryForm: the match finds a name whole, built into
// a format string, or as a prefix, in any case; only the configuration key
// and names that merely start with the word pass.
func TestContractReadCatchesEveryForm(t *testing.T) {
	for lit, want := range map[string]bool{
		`"select 1 from contract.place"`:        true,
		"`from contract.%s`":                    true,
		`"insert into contract."`:               true,
		`"FROM CONTRACT.Fuel_Station"`:          true,
		`"contract.expected"`:                   false,
		`"contract.expected or contract.place"`: true,
		`"from contract_source_rules k"`:        false,
		`"dbcontract.ContractVersion"`:          false,
	} {
		if got := namesContract(lit); got != want {
			t.Errorf("%s: %v, want %v", lit, got, want)
		}
	}
	if !frozenLegacy("internal/adapters/store/legacy_c_integration_test.go") || frozenLegacy("internal/adapters/store/legacy.go") ||
		frozenLegacy("internal/fixtures/legacy_x_integration_test.go") {
		t.Error("frozenLegacy")
	}
}

// rawAllowed are the store's statements sent as text, not named, by file:
// each sets the transaction up and reads no relation of the platform (the
// admission slot's advisory lock, the statement budget); and the one call
// that runs every named statement by its name.
var rawAllowed = map[string][]string{
	"budget.go": {"slotSQL"},
	"store.go":  {`"select set_config('statement_timeout', $1, true)"`},
	"named.go":  {"s.Name"},
}

// rawCalls are the calls of Query, QueryRow, Exec and QueryFunc in files,
// each as "file: the text of its SQL argument", and of SendBatch.
func rawCalls(t *testing.T, files map[string]string) []string {
	t.Helper()
	var out []string
	fset := token.NewFileSet()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		src := files[name]
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(x ast.Node) bool {
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			switch sel.Sel.Name {
			case "Query", "QueryRow", "Exec", "QueryFunc":
				arg := call.Args[1]
				out = append(out, name+": "+src[fset.Position(arg.Pos()).Offset:fset.Position(arg.End()).Offset])
			case "SendBatch":
				out = append(out, name+": a batch")
			}
			return true
		})
	}
	return out
}

// storeSources are the store's Go files that are not tests, by name.
func storeSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 20 {
		t.Fatalf("read the store's files: %v %d", err, len(files))
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

// unnamed are the calls of rawCalls that rawAllowed does not name.
func unnamed(calls []string) []string {
	var out []string
	for _, c := range calls {
		file, arg, _ := strings.Cut(c, ": ")
		if !slices.Contains(rawAllowed[file], arg) {
			out = append(out, c)
		}
	}
	return out
}

// TestStoreSendsOnlyNamedStatements: the store sends every statement that
// reads the platform as a named statement (run); a call of Query, QueryRow,
// Exec or QueryFunc with any other text fails, as does a batch.
func TestStoreSendsOnlyNamedStatements(t *testing.T) {
	calls := rawCalls(t, storeSources(t))
	if len(calls) < len(rawAllowed) {
		t.Fatalf("found only %v", calls)
	}
	for _, c := range unnamed(calls) {
		t.Errorf("a statement sent as text, not named: %s", c)
	}
	// The check turns red on what it guards against: the families' read sent
	// as text (feeding.go before it was named), a statement built from a
	// format string, an Exec of a literal and a batch; run's own call passes.
	planted := map[string]string{
		"x.go": `package store
func a() { rows, _ := tx.Query(ctx, feedingRead) }
func b() { tx.QueryRow(ctx, fmt.Sprintf("select 1 from contract.%s", v)) }
func c() { tx.Exec(ctx, "select 1") }
func d() { tx.SendBatch(ctx, b) }`,
		"named.go": `package store
func e() { tx.Query(ctx, s.Name, args...) }`,
	}
	if got := unnamed(rawCalls(t, planted)); len(got) != 4 {
		t.Errorf("the planted calls: %q", got)
	}
}
