package conformance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The fuzz inventory (TEST-006, TST-016): test/fuzz/targets.tsv names every
// parser of the service and the native fuzz target that covers it, and
// these rules keep it true. A parser the Test Strategy names has a line; a
// built parser has a target with a committed seed corpus; and every
// parser-shaped function of a package the service is built from is listed,
// so a parser added without a target fails here.

const fuzzInventory = "test/fuzz/targets.tsv"

// namedParsers are the parsers the Test Strategy and issue #16 name.
var namedParsers = []string{"configuration", "cursor", "key", "polyline", "location", "fields", "lang", "query-body"}

// parserShaped matches the names of functions that read untrusted input.
var parserShaped = regexp.MustCompile(`^(?i:parse|decode|unmarshal|load)`)

var plannedBy = regexp.MustCompile(`^#[1-9][0-9]*$`)

type fuzzLine struct {
	parser, pkg, target, issue string
	funcs                      []string
}

// fuzzPackage is what the inventory needs to know of one package.
type fuzzPackage struct {
	funcs   map[string]bool // functions and methods of non-test files
	targets map[string]bool // func FuzzX(*testing.F) of test files
	corpus  map[string]int  // target -> committed seed files in Go's corpus format
}

func readFuzzInventory(src []byte) ([]fuzzLine, []string) {
	var lines []fuzzLine
	var bad []string
	for _, l := range dataLines(src) {
		f := strings.Split(l, "\t")
		if len(f) != 5 {
			bad = append(bad, "inventory line does not have five tab-separated fields: "+l)
			continue
		}
		fl := fuzzLine{parser: f[0], pkg: f[1], target: f[3], issue: f[4]}
		if f[2] != "-" {
			fl.funcs = strings.Split(f[2], ",")
		}
		lines = append(lines, fl)
	}
	return lines, bad
}

// fuzzInventoryFindings judges the inventory against the packages it names
// (pkgs, by directory) and the service's own packages (service).
func fuzzInventoryFindings(lines []fuzzLine, pkgs map[string]fuzzPackage, service []string) []string {
	var out []string
	listed := map[string]bool{}
	byParser := map[string]bool{}
	for _, l := range lines {
		byParser[l.parser] = true
		if l.target == "-" {
			if l.pkg != "-" || len(l.funcs) > 0 || !plannedBy.MatchString(l.issue) {
				out = append(out, l.parser+": a planned parser names no package, functions or target, and names the issue that builds it")
			}
			continue
		}
		p, ok := pkgs[l.pkg]
		switch {
		case !ok:
			out = append(out, l.parser+": package "+l.pkg+" was not read")
			continue
		case l.issue != "-":
			out = append(out, l.parser+": has a target and still names a planning issue")
		case len(l.funcs) == 0:
			out = append(out, l.parser+": names no function")
		}
		if !p.targets[l.target] {
			out = append(out, l.parser+": "+l.pkg+" has no fuzz target "+l.target)
		}
		if p.corpus[l.target] < 3 {
			out = append(out, l.parser+": "+l.pkg+"/testdata/fuzz/"+l.target+" holds fewer than three seed files")
		}
		for _, fn := range l.funcs {
			if !p.funcs[fn] {
				out = append(out, l.parser+": "+l.pkg+" has no function "+fn)
			}
			listed[l.pkg+"."+fn] = true
		}
	}
	for _, name := range namedParsers {
		if !byParser[name] {
			out = append(out, name+": a parser the Test Strategy names has no inventory line")
		}
	}
	for _, dir := range service {
		for fn := range pkgs[dir].funcs {
			if parserShaped.MatchString(fn) && !listed[dir+"."+fn] {
				out = append(out, dir+"."+fn+": a parser without a fuzz target; list it in "+fuzzInventory)
			}
		}
	}
	sort.Strings(out)
	return out
}

// servicePackages lists the module directories cmd/viafrei-api is built
// from, relative to the repository root.
func servicePackages(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{if .Module.Main}}{{.Dir}}{{end}}{{end}}", "./cmd/viafrei-api")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range strings.Fields(string(b)) {
		rel, err := filepath.Rel(abs, d)
		if err != nil {
			t.Fatal(err)
		}
		if rel != filepath.Join("cmd", "viafrei-api") {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	if len(out) < 5 {
		t.Fatalf("go list named %d service packages; the walk did not read the module", len(out))
	}
	return out
}

func readFuzzPackage(t *testing.T, dir string) fuzzPackage {
	t.Helper()
	p := fuzzPackage{funcs: map[string]bool{}, targets: map[string]bool{}, corpus: map[string]int{}}
	files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		test := strings.HasSuffix(path, "_test.go")
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if !test {
				p.funcs[fd.Name.Name] = true
			} else if strings.HasPrefix(fd.Name.Name, "Fuzz") && fd.Type.Params.NumFields() == 1 {
				p.targets[fd.Name.Name] = true
			}
		}
	}
	for target := range p.targets {
		seeds, _ := filepath.Glob(filepath.Join(root, dir, "testdata", "fuzz", target, "*"))
		for _, s := range seeds {
			b, err := os.ReadFile(s)
			if err == nil && strings.HasPrefix(string(b), "go test fuzz v1\n") {
				p.corpus[target]++
			}
		}
	}
	return p
}

func TestFuzzInventory(t *testing.T) {
	req.Covers(t, "TEST-006", "TST-016")
	src, err := os.ReadFile(filepath.Join(root, fuzzInventory))
	if err != nil {
		t.Fatal(err)
	}
	lines, bad := readFuzzInventory(src)
	for _, b := range bad {
		t.Error(b)
	}
	service := servicePackages(t)
	pkgs := map[string]fuzzPackage{}
	dirs := slices.Clone(service)
	for _, l := range lines {
		if l.pkg != "-" {
			dirs = append(dirs, l.pkg)
		}
	}
	for _, d := range dirs {
		if _, done := pkgs[d]; !done {
			pkgs[d] = readFuzzPackage(t, d)
		}
	}
	active := 0
	for _, l := range lines {
		if l.target != "-" {
			active++
		}
	}
	if active < 3 {
		t.Fatalf("the inventory names %d fuzz targets; a check over too few proves nothing", active)
	}
	for _, f := range fuzzInventoryFindings(lines, pkgs, service) {
		t.Error(f)
	}
}

// fuzzTargetFindings (TST-010): a fuzzing job reaches the code under test
// (scripts/fuzz.sh, go test only) or the compose stack it starts
// (scripts/compose-smoke.sh, which refuses a target that is not the
// loopback), and nothing else: no URL in a fuzzing workflow, no network
// client in the fuzz script, and every run step is one of those two.
func fuzzTargetFindings(files map[string]string) []string {
	var out []string
	for _, name := range []string{"fuzz-nightly.yml", "fuzz.sh"} {
		if strings.Contains(files[name], "http://") || strings.Contains(files[name], "https://") {
			out = append(out, name+" names a URL; fuzzing reaches the code under test or the CI-started stack only")
		}
	}
	for _, client := range []string{"curl", "wget", "nc "} {
		if strings.Contains(files["fuzz.sh"], client) {
			out = append(out, "fuzz.sh calls "+strings.TrimSpace(client))
		}
	}
	if !strings.Contains(files["compose-smoke.sh"], loopbackGuard) {
		out = append(out, "compose-smoke.sh no longer refuses a target that is not the loopback")
	}
	runs := regexp.MustCompile(`(?m)^\s+run: (.+)$`).FindAllStringSubmatch(files["fuzz-nightly.yml"], -1)
	if len(runs) == 0 {
		out = append(out, "fuzz-nightly.yml has no run step")
	}
	for _, m := range runs {
		if !strings.HasPrefix(m[1], "scripts/fuzz.sh ") && !strings.HasPrefix(m[1], "make compose-test ") && !strings.HasPrefix(m[1], "cat fuzz-report.md ") {
			out = append(out, "fuzz-nightly.yml runs "+m[1]+", which is neither the fuzz script nor the compose smoke")
		}
	}
	if !strings.Contains(files["ci.yml"], "run: scripts/fuzz.sh 20s fuzz-report.md") {
		out = append(out, "ci.yml has no pull-request budget of the fuzz targets")
	}
	if !strings.Contains(files["fuzz-nightly.yml"], `SMOKE_MAX_RESPONSE_TIME: "2.05"`) {
		out = append(out, "fuzz-nightly.yml does not hold the schema-driven run to 2 s plus 50 ms")
	}
	sort.Strings(out)
	return out
}

const loopbackGuard = `[[ $API_URL =~ ^http://(localhost|127\.0\.0\.1|\[::1\]):[0-9]+$ ]] ||`

func readFuzzJobFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for name, path := range map[string]string{
		"fuzz-nightly.yml": ".github/workflows/fuzz-nightly.yml",
		"ci.yml":           ".github/workflows/ci.yml",
		"fuzz.sh":          "scripts/fuzz.sh",
		"compose-smoke.sh": "scripts/compose-smoke.sh",
	} {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	return files
}

func TestFuzzingTargetsOnlyTheCodeOrTheCIStack(t *testing.T) {
	req.Covers(t, "TST-010", "TST-016")
	files := readFuzzJobFiles(t)
	for _, f := range fuzzTargetFindings(files) {
		t.Error(f)
	}
	plants := map[string][2]string{
		"names a URL":              {"fuzz-nightly.yml", "\n# https://example.invalid\n"},
		"fuzz.sh calls curl":       {"fuzz.sh", "\ncurl -s x\n"},
		"neither the fuzz script":  {"fuzz-nightly.yml", "\n        run: ./bin/viafrei-api serve\n"},
		"no longer refuses":        {"compose-smoke.sh", ""},
		"no pull-request budget":   {"ci.yml", ""},
		"does not hold the schema": {"fuzz-nightly.yml", ""},
	}
	for want, p := range plants {
		planted := map[string]string{}
		for k, v := range files {
			planted[k] = v
		}
		if p[1] == "" {
			planted[p[0]] = strings.NewReplacer(loopbackGuard, "", "run: scripts/fuzz.sh 20s fuzz-report.md", "", `SMOKE_MAX_RESPONSE_TIME: "2.05"`, "").Replace(planted[p[0]])
		} else {
			planted[p[0]] += p[1]
		}
		got := fuzzTargetFindings(planted)
		if !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("planted %q, the rules said %v", want, got)
		}
	}
}

func TestFuzzInventoryCanSayNo(t *testing.T) {
	req.Covers(t, "TEST-006", "TST-016")
	good := func() ([]fuzzLine, map[string]fuzzPackage) {
		lines := []fuzzLine{{parser: "configuration", pkg: "p", funcs: []string{"Load"}, target: "FuzzLoad", issue: "-"}}
		for _, n := range namedParsers[1:] {
			lines = append(lines, fuzzLine{parser: n, pkg: "-", target: "-", issue: "#9"})
		}
		pkgs := map[string]fuzzPackage{"p": {funcs: map[string]bool{"Load": true, "helper": true}, targets: map[string]bool{"FuzzLoad": true}, corpus: map[string]int{"FuzzLoad": 3}}}
		return lines, pkgs
	}
	lines, pkgs := good()
	if f := fuzzInventoryFindings(lines, pkgs, []string{"p"}); len(f) != 0 {
		t.Fatalf("the good inventory has findings: %v", f)
	}
	plants := map[string]func([]fuzzLine, map[string]fuzzPackage) []fuzzLine{
		"a parser without a fuzz target": func(l []fuzzLine, p map[string]fuzzPackage) []fuzzLine {
			p["p"].funcs["DecodeCursor"] = true
			return l
		},
		"has no fuzz target FuzzLoad": func(l []fuzzLine, p map[string]fuzzPackage) []fuzzLine {
			delete(p["p"].targets, "FuzzLoad")
			return l
		},
		"fewer than three seed files": func(l []fuzzLine, p map[string]fuzzPackage) []fuzzLine {
			p["p"].corpus["FuzzLoad"] = 2
			return l
		},
		"has no function Load": func(l []fuzzLine, p map[string]fuzzPackage) []fuzzLine {
			delete(p["p"].funcs, "Load")
			return l
		},
		"names the issue that builds it": func(l []fuzzLine, _ map[string]fuzzPackage) []fuzzLine {
			l[1].issue = "-"
			return l
		},
		"has no inventory line": func(l []fuzzLine, _ map[string]fuzzPackage) []fuzzLine {
			return l[:len(l)-1]
		},
		"still names a planning issue": func(l []fuzzLine, _ map[string]fuzzPackage) []fuzzLine {
			l[0].issue = "#16"
			return l
		},
		"names no function": func(l []fuzzLine, _ map[string]fuzzPackage) []fuzzLine {
			l[0].funcs = nil
			return l
		},
		"was not read": func(l []fuzzLine, _ map[string]fuzzPackage) []fuzzLine {
			l[0].pkg = "q"
			return l
		},
	}
	for want, plant := range plants {
		lines, pkgs := good()
		got := fuzzInventoryFindings(plant(lines, pkgs), pkgs, []string{"p"})
		if !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("planted %q, the rules said %v", want, got)
		}
	}
	if _, bad := readFuzzInventory([]byte("a\tb\tc\n")); len(bad) != 1 {
		t.Errorf("a short line was read: %v", bad)
	}
}
