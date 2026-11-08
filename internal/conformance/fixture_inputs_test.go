package conformance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// fixtureImports is what the generator's production code may import: the
// standard library's pure packages, the read-contract version and the
// project's own helpers. No network, database, process or embedded-file
// package is on it, so the generator can read no network resource and no
// production data (TST-014); only the command may touch the file system, to
// write its output and read the checksums it is asked to verify.
var fixtureImports = map[string][]string{
	"internal/fixtures": {
		"bufio", "crypto/sha256", "errors", "fmt", "hash", "io", "math", "math/rand/v2",
		"slices", "strconv", "strings", "time", "unicode", "unicode/utf8",
	},
	"cmd/fixturegen": {
		"encoding/json", "errors", "flag", "fmt", "io", "os", "path/filepath", "slices", "strings",
		"github.com/mavrovde/viafrei-api/db/contract",
		"github.com/mavrovde/viafrei-api/internal/fixtures",
		"github.com/mavrovde/viafrei-api/internal/invariant",
	},
}

// importFindings lists the imports of file that are not on allowed.
func importFindings(fset *token.FileSet, file *ast.File, allowed []string) []string {
	var out []string
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !slices.Contains(allowed, p) {
			out = append(out, fset.Position(imp.Pos()).String()+": imports "+p)
		}
	}
	return out
}

func TestFixtureGeneratorReadsNothingReal(t *testing.T) {
	req.Covers(t, "TST-014")
	fset := token.NewFileSet()
	for dir, allowed := range fixtureImports {
		files := 0
		for _, path := range goFiles(t, "cmd", "internal") {
			if filepath.ToSlash(filepath.Dir(path)) != filepath.ToSlash(filepath.Join(root, dir)) {
				continue
			}
			files++
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range importFindings(fset, file, allowed) {
				t.Error(f)
			}
		}
		if files == 0 {
			t.Fatalf("no production file found in %s; the lint read nothing", dir)
		}
	}
}

func TestFixtureImportLintCanSayNo(t *testing.T) {
	req.Covers(t, "TST-014")
	src := "package p\nimport (\n\t\"fmt\"\n\t\"net/http\"\n\t\"github.com/jackc/pgx/v5\"\n)\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	got := importFindings(fset, file, fixtureImports["internal/fixtures"])
	if len(got) != 2 || !strings.Contains(got[0], "net/http") || !strings.Contains(got[1], "pgx") {
		t.Fatalf("findings %v, want net/http and pgx", got)
	}
}
