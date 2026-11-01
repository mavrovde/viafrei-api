package conformance_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

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

// forbiddenScope holds SHA-256 hashes of lower-case words that name the data
// collection side of the platform, hosting or private infrastructure. This
// repository serves reads only (scope, PRD); the words themselves are kept
// out of the tree, so only their hashes appear here.
var forbiddenScope = map[string]bool{
	"6eb7f2ea8ffbb37f44d41bdc3382d193c3de752f89d5bafe7b85afc93a65c32b": true,
	"b293b32367c388b66309c0fe9c7459f0ab75fe0642a60299c82a160a76baf757": true,
	"48a0af47591dd636c67a923a222271828864f0a263f6598b3868df4b00882af4": true,
	"04642363b830cd2cff53888faf05566f2fe34a0092f8a06450f23fe1b2757a1b": true,
	"dca267c008dd8ae938ce3697004692c080095675c46630d19c4c6b3f7dc06a37": true,
	"f1db105323d3fbb55fb6148b4f5557ba6469a88890ec73832b837b3ef2ca9f88": true,
	"04e7bc25ef31626a449c2539cd832f7f9ddf57c077835ff1cab739f60e602027": true,
	"0302751d9f8ef9773296afe237bcbfa206cd88c58980da46a3d2d9081d33deec": true,
	"645e3597571c5c8bc2bd6ced66a307541af8f63eaad00ff658326400259435b5": true,
	"294aa8d75483b8331e3ba6a7f24aea15202747f36de65197e7bc6194880b2558": true,
	"2ee1a9146997e91d8dd7da6fb378d3ad659712e8316af6f0d664eaadfaeda1a4": true,
	"05daaa94f532cca5571f5a8d93699d73e507e89455c85bf20b424fc82b21b6a6": true,
	"a02ba163f3a02db22fdd14b310119f1a4c2f9e4a773a573f757904dd5433d4dd": true,
	"e9fa55bbed9da9ad0cd8d5c5ec05df0ccacc25f5efdbc9291e6fccb82d850081": true,
	"71c6f2f5e422a76ab2803802b7ffe0b3fab91f51ccad5b1dbbba22e9c262d376": true,
	"0c109d842b690e15c0e172e4fc6f4ddf187285e6ea0ce76f4a5acaffc8560d2c": true,
	"798c5ec4c7e25dd48d4bba31f381bfbda3b32fc380d824ae9ac39e2c32a7f64e": true,
	"f01487c439e606bdae585a23cdeaf5c8e1c22acd32ebf69335443e19400c07d8": true,
	"7c09b62405a2c0917f8b7440599ce947d2dc40bc8ece294d8be99fd808b593f0": true,
	"157b2ff0261bd5b49cc8358609389fad7448c0a2083dff9878c8ebd8a0c55bc0": true,
}

func scopeHits(text string) []string {
	var hits []string
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		sum := sha256.Sum256([]byte(w))
		if forbiddenScope[hex.EncodeToString(sum[:])] {
			hits = append(hits, w)
		}
	}
	return hits
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "bin": true, "dist": true, ".idea": true, ".vscode": true}

// TestNoCollectionScope holds the scope boundary: nothing in the repository
// describes how data is collected, scheduled or hosted.
func TestNoCollectionScope(t *testing.T) {
	req.Covers(t, "CON-004")
	read := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.sum" || strings.HasSuffix(d.Name(), ".png") {
			return nil
		}
		f, err := os.Open(path) // #nosec G304 -- walking the repository
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			for _, w := range scopeHits(sc.Text()) {
				t.Errorf("%s:%d: out-of-scope word (%d letters)", path, line, len(w))
			}
		}
		read++
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if read < 20 {
		t.Fatalf("only %d files read; the walk did not read the tree", read)
	}
}

func TestScopeLintCanSayNo(t *testing.T) {
	req.Covers(t, "CON-004")
	// The planted word is assembled at run time so it never appears whole.
	planted := "the " + strings.Join([]string{"in", "gest"}, "") + " worker"
	if len(scopeHits(planted)) != 1 || len(scopeHits("the read path")) != 0 {
		t.Fatal("the scope lint cannot tell a forbidden word from an allowed one")
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
