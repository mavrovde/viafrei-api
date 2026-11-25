//go:build integration

package store_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// shard splits the parity set across jobs that run side by side (-args
// -parity -parity.shard=k/n), so none of them nears the test time cap: shard
// k runs every n-th top-level test of the package, by name, and its part of
// the cases of the tests that run in every shard (everyShard): every n-th
// question of the synthetic world, every n-th statement of the parity
// cases. Each shard writes what it compared to
// -parity.ran; paritygather judges the shards together whether every
// named statement ran and compared rows, which no shard alone can.
var (
	shardFlag = flag.String("parity.shard", "", "k/n: run the k-th of n shards of the package's tests")
	ranPath   = flag.String("parity.ran", "", "write the statements this run compared, as JSON, to this file")
)

// everyShard are the tests that run in every shard, each over its part of
// its cases: the synthetic world's questions, and the statements the
// parity cases name.
var everyShard = []string{"TestResolveTheWorldOverTheStatements", "TestNamedStatementsAnswerAsTheViews"}

// shardK and shardN are the shard this run is, 1 of 1 when unsharded.
var shardK, shardN = 1, 1

// errShard is a -parity.shard the run cannot honour.
var errShard = errors.New("-parity.shard wants k/n with 1 <= k <= n, and no -run beside it")

// setShard reads -parity.shard and narrows -test.run to the shard's tests,
// found in the package's test files in dir.
func setShard(dir string) error {
	if *shardFlag == "" {
		return nil
	}
	ks, ns, ok := strings.Cut(*shardFlag, "/")
	k, kerr := strconv.Atoi(ks)
	n, nerr := strconv.Atoi(ns)
	if !ok || kerr != nil || nerr != nil || k < 1 || k > n || flag.Lookup("test.run").Value.String() != "" {
		return fmt.Errorf("%w: %q", errShard, *shardFlag)
	}
	names, err := topLevelTests(dir)
	if err != nil {
		return err
	}
	run := slices.Clone(everyShard)
	i := 0
	for _, name := range names {
		if slices.Contains(everyShard, name) {
			continue
		}
		if i%n == k-1 {
			run = append(run, name)
		}
		i++
	}
	shardK, shardN = k, n
	return flag.Set("test.run", "^("+strings.Join(run, "|")+")$")
}

// inShard reports whether the i-th case of a test in everyShard is this
// shard's.
func inShard(i int) bool { return i%shardN == shardK-1 }

// topLevelTests are the names, sorted, of the Test functions of the test
// files in dir that an integration build compiles.
func topLevelTests(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	var out []string
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if !built(file) {
			continue
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && isTest(fn) {
				out = append(out, fn.Name.Name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// built reports whether an integration build compiles file.
func built(file *ast.File) bool {
	for _, g := range file.Comments {
		if g.Pos() >= file.Package {
			break
		}
		for _, c := range g.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			x, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			return x.Eval(func(tag string) bool { return tag == "integration" || tag == runtime.GOOS || tag == runtime.GOARCH })
		}
	}
	return true
}

// isTest reports whether fn is a test function: TestXxx(t *testing.T).
func isTest(fn *ast.FuncDecl) bool {
	name, ok := strings.CutPrefix(fn.Name.Name, "Test")
	if !ok || name == "Main" || (name != "" && strings.ToUpper(name[:1]) != name[:1]) {
		return false
	}
	p := fn.Type.Params.List
	if len(p) != 1 {
		return false
	}
	star, ok := p[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	s, ok := star.X.(*ast.SelectorExpr)
	return ok && s.Sel.Name == "T"
}

// ranStatement is one named statement's line of a -parity.ran file.
type ranStatement struct {
	Name string `json:"name"`
	Runs int    `json:"runs"`
	Rows int    `json:"rows"`
	// Accept is why the full set accepts the statement unrun ("unrun") or
	// with no row compared ("norows"), with the reason; empty when it
	// accepts neither.
	Accept string `json:"accept,omitempty"`
}

// writeRan writes, when -parity.ran names a file, each named statement's
// runs and rows compared and what the full set accepts of it.
func writeRan(runs, rows map[string]int, reads []string, accept func(string) string) error {
	if *ranPath == "" {
		return nil
	}
	out := struct {
		Shard      string         `json:"shard"`
		Statements []ranStatement `json:"statements"`
	}{Shard: fmt.Sprintf("%d/%d", shardK, shardN)}
	for _, name := range reads {
		out.Statements = append(out.Statements, ranStatement{Name: name, Runs: runs[name], Rows: rows[name], Accept: accept(name)})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*ranPath, append(b, '\n'), 0o600)
}
