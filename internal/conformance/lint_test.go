package conformance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// root is the repository root, two levels above this package.
var root = filepath.Join("..", "..")

// goFiles returns every non-test, non-generated Go file under the given
// directories, relative to the repository root.
func goFiles(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 10 {
		t.Fatalf("only %d Go files found; the walk did not read the tree", len(out))
	}
	return out
}

var durationUnits = map[string]bool{
	"Nanosecond": true, "Microsecond": true, "Millisecond": true,
	"Second": true, "Minute": true, "Hour": true,
}

func isTimeUnit(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time" && durationUnits[sel.Sel.Name]
}

func isNumber(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && (lit.Kind == token.INT || lit.Kind == token.FLOAT)
}

// literalDurations reports every N*time.Unit and time.Duration(N) in src.
func literalDurations(fset *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.MUL && ((isNumber(x.X) && isTimeUnit(x.Y)) || (isTimeUnit(x.X) && isNumber(x.Y))) {
				found = append(found, fset.Position(x.Pos()).String())
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 1 && isNumber(x.Args[0]) {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" && sel.Sel.Name == "Duration" {
					found = append(found, fset.Position(x.Pos()).String())
				}
			}
		}
		return true
	})
	return found
}

// TestNoLiteralTunables holds CFG-001: a timeout, budget or interval is a
// configuration key, never a constant in the code. A bare unit (time.Second
// as a rounding step) is not a tunable and is allowed.
func TestNoLiteralTunables(t *testing.T) {
	req.Covers(t, "CFG-001")
	fset := token.NewFileSet()
	for _, path := range goFiles(t, "cmd", "internal") {
		// The configuration package declares the defaults; the fixture
		// generator's durations are the shape of synthetic data (validity
		// windows, delays), not tunables of the running service.
		if p := filepath.ToSlash(path); strings.Contains(p, "internal/runtime/config/") || strings.Contains(p, "internal/fixtures/") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pos := range literalDurations(fset, file) {
			t.Errorf("%s: a literal duration; declare a configuration key instead", pos)
		}
	}
}

func TestLiteralLintCanSayNo(t *testing.T) {
	req.Covers(t, "CFG-001")
	src := "package p\nimport \"time\"\nvar a = 3 * time.Second\nvar b = time.Millisecond * 250\nvar c = time.Duration(5)\nvar ok = time.Second\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := literalDurations(fset, file); len(got) != 3 {
		t.Fatalf("the lint found %d of the 3 planted literals: %v", len(got), got)
	}
}

// TestNoRetryLoops holds the read path to one attempt per request: a retry
// inside a 2 s deadline only spends the caller's budget twice. No retry or
// backoff package may be imported by production code.
func TestNoRetryLoops(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	fset := token.NewFileSet()
	for _, path := range goFiles(t, "cmd", "internal") {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			low := strings.ToLower(p)
			if strings.Contains(low, "retry") || strings.Contains(low, "backoff") {
				t.Errorf("%s imports %s", path, p)
			}
		}
	}
}
