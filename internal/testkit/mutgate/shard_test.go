package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The count the full run of .github/workflows/mutation.yml is cut into
// (its matrix shard: [1, ..., 20] and -shard "$SHARD/20").
const releaseShards = 20

// shardBudget is the most estimated work a release shard may carry, in
// seconds of a ubuntu-24.04 runner: with about a minute of checkout, Go
// setup and the Gremlins build, a shard job stays near 10 minutes, well
// inside its timeout-minutes: 15.
const shardBudget = 540.0

func TestParseShardTakesKOfNAndNothingElse(t *testing.T) {
	for raw, want := range map[string]shard{"1/4": {1, 4}, "4/4": {4, 4}, "1/1": {1, 1}, "12/12": {12, 12}} {
		if got, ok := parseShard(raw); !ok || got != want {
			t.Errorf("%q: %v %v", raw, got, ok)
		}
	}
	for _, bad := range []string{"0/4", "5/4", "1/0", "-1/4", "1/4/2", "1.5/4", "01/4", "a/4", "1", "", " 1/4", "99999999999999999999/99999999999999999999"} {
		if got, ok := parseShard(bad); ok {
			t.Errorf("%q parsed as %v", bad, got)
		}
	}
}

// spreadFindings checks a deal of units to shards: every unit in exactly
// one shard, none empty, and the heaviest shard at most the lightest plus
// the costliest unit (what dealing the costliest first to the lightest
// shard guarantees).
func spreadFindings(units []unit, shards [][]unit) []string {
	var out []string
	seen := map[string]int{}
	lo, hi, top := -1.0, 0.0, 0.0
	for k, s := range shards {
		if len(s) == 0 {
			out = append(out, fmt.Sprintf("shard %d/%d is empty", k+1, len(shards)))
		}
		load := 0.0
		for _, u := range s {
			seen[u.name]++
			load += u.cost
		}
		if lo < 0 || load < lo {
			lo = load
		}
		hi = max(hi, load)
	}
	for _, u := range units {
		top = max(top, u.cost)
		if seen[u.name] != 1 {
			out = append(out, fmt.Sprintf("unit %s is in %d shard(s), not one", u.name, seen[u.name]))
		}
	}
	if len(seen) != len(units) {
		out = append(out, fmt.Sprintf("the shards hold %d units, the plan %d", len(seen), len(units)))
	}
	if hi > lo+top {
		out = append(out, fmt.Sprintf("the shards cost from %.0f to %.0f, more apart than the costliest unit (%.0f)", lo, hi, top))
	}
	return out
}

func names(us []unit) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.name)
	}
	return out
}

func TestSpreadDealsEveryUnitOnceAndEvenly(t *testing.T) {
	req.Covers(t, "TST-002")
	var units []unit
	for i, c := range []float64{50, 10, 300, 20, 20, 120, 80, 5, 240, 60, 60, 30} {
		units = append(units, unit{name: fmt.Sprintf("u%d", i), cost: c})
	}
	for _, count := range []int{1, 2, 3, 4, 7, 12} {
		if f := spreadFindings(units, spread(units, count)); len(f) > 0 {
			t.Errorf("%d shards: %v", count, f)
		}
	}
	// Costliest first, each to the lightest shard, the lower one on a tie.
	got := spread([]unit{{name: "a", cost: 1}, {name: "b", cost: 3}, {name: "c", cost: 2}, {name: "d", cost: 2}}, 2)
	if !slices.Equal(names(got[0]), []string{"b", "a"}) || !slices.Equal(names(got[1]), []string{"c", "d"}) {
		t.Errorf("deal: %v %v", names(got[0]), names(got[1]))
	}
	// The check catches what a broken deal does.
	for planted, bad := range map[string]func(d [][]unit) [][]unit{
		"a unit dropped": func(d [][]unit) [][]unit { return [][]unit{d[0][1:], d[1], d[2]} },
		"a unit twice":   func(d [][]unit) [][]unit { return [][]unit{d[0], append(d[1], d[0][0]), d[2]} },
		"an empty shard": func(d [][]unit) [][]unit { return [][]unit{append(d[0], d[1]...), nil, d[2]} },
		"a lopsided one": func(d [][]unit) [][]unit {
			return [][]unit{append(append(d[0], d[1][1:]...), d[2][1:]...), d[1][:1], d[2][:1]}
		},
	} {
		if len(spreadFindings(units, bad(spread(units, 3)))) == 0 {
			t.Errorf("%s: planted and not reported", planted)
		}
	}
}

func TestCountSitesIsGremlinsTokenTable(t *testing.T) {
	dir := files(t, map[string]string{"s.go": `package s

func S(a, b int, p, q bool) int {
	for i := 0; i < a; i++ { // LSS: boundary and negation; INC
		if a == b || p && q { // EQL; LOR; LAND
			break // loop control
		}
		if a >= -b { // GEQ: boundary and negation; unary SUB: negatives and arithmetic
			continue // loop control
		}
		a += b * 2 // ADD_ASSIGN: invert-assignments, off; MUL
		a = a&b | a<<1 // bitwise, off
		b--             // DEC
	}
	if !p { // NOT: no mutator
		goto end // GOTO: no mutator
	}
end:
	return a/b + a%b - 1 // QUO, REM, ADD; binary SUB: negatives and arithmetic
}
`, "bad.go": "package s\nfunc {"})
	got, err := countSites(filepath.Join(dir, "s.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"ARITHMETIC_BASE": 6, "CONDITIONALS_BOUNDARY": 2, "CONDITIONALS_NEGATION": 3,
		"INCREMENT_DECREMENT": 2, "INVERT_LOGICAL": 2, "INVERT_LOOPCTRL": 2, "INVERT_NEGATIVES": 2,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sites %v, want %v", got, want)
	}
	for _, m := range enabled {
		if !slices.Contains(mutators, m) {
			t.Errorf("enabled mutator %s is no Gremlins mutator", m)
		}
	}
	if _, err := countSites(filepath.Join(dir, "bad.go")); err == nil {
		t.Error("a source that does not parse was counted")
	}
}

func excludes(args []string, file string) bool {
	return slices.Contains(args, "^"+strings.ReplaceAll(file, ".", `\.`)+"$")
}

// partitionFindings checks that the units hold every enabled mutator of
// every in-scope file of the tree exactly once, that a unit's Gremlins
// arguments exclude every other Go file of its directory and every
// subdirectory, and that a unit's mutators are the ones its run leaves on.
func partitionFindings(t *testing.T, tree string, roots []root, units []unit) []string {
	t.Helper()
	var out []string
	held := map[string]int{}
	for _, u := range units {
		args := u.args()
		if len(args) < 2 || args[0] != "-E" || args[1] != "/" {
			out = append(out, u.name+": subdirectories are not excluded")
		}
		ents, err := os.ReadDir(filepath.Join(tree, filepath.FromSlash(u.dir)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			f := e.Name()
			own := slices.Contains(u.files, f)
			if e.IsDir() || !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") || own != excludes(args, f) {
				continue
			}
			out = append(out, fmt.Sprintf("%s: %s is its own %v and excluded %v", u.name, f, own, excludes(args, f)))
		}
		for _, f := range u.files {
			for _, m := range enabled {
				on := !slices.Contains(args, "--"+strings.ToLower(strings.ReplaceAll(m, "_", "-"))+"=false")
				if on != u.holds(mutant{file: u.dir + "/" + f, mutator: m}) {
					out = append(out, fmt.Sprintf("%s: %s %s is run %v and held %v", u.name, f, m, on, !on))
				}
				if on {
					held[u.dir+"/"+f+" "+m]++
				}
			}
		}
	}
	err := filepath.WalkDir(tree, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(tree, p)
		rel = filepath.ToSlash(rel)
		if err != nil || d.IsDir() || !inScope(roots, rel) {
			return err
		}
		c, err := countSites(p)
		for _, m := range enabled {
			if c[m] > 0 && held[rel+" "+m] != 1 {
				out = append(out, fmt.Sprintf("%s %s (%d mutants) is in %d unit(s), not one", rel, m, c[m], held[rel+" "+m]))
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// big is a source of 2*n CONDITIONALS_BOUNDARY, 3*n CONDITIONALS_NEGATION
// and n INVERT_LOGICAL mutants.
func big(pkg string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nfunc F(a, b int) int {\n", pkg)
	for range n {
		b.WriteString("\tif a < b && a == 1 {\n\t\ta = 2\n\t}\n\tif a > b {\n\t\tb = 3\n\t}\n")
	}
	b.WriteString("\treturn a\n}\n")
	return b.String()
}

func TestUnitsCutACostlyPackageByFilesThenMutators(t *testing.T) {
	req.Covers(t, "TST-002")
	// At 10 s a mutant a unit holds at most 21 mutants (unitCap): p's
	// huge.go (24) is cut by mutator, large.go (18) is whole, the small
	// ones share a unit, empty.go has no mutant, other.go is out of scope;
	// p/sub and q are one unit each; e's only whole file has no mutant.
	dir := files(t, map[string]string{
		"p/huge.go":     big("p", 4),
		"p/large.go":    big("p", 3),
		"p/s1.go":       big("p", 1),
		"p/s2.go":       big("p", 1),
		"p/empty.go":    "package p\n",
		"p/other.go":    big("p", 1),
		"p/x_test.go":   big("p", 9),
		"p/sub/sub.go":  big("sub", 1),
		"q/q.go":        big("q", 1),
		"q/testdata/r":  "not Go",
		"e/huge.go":     big("e", 4),
		"e/empty.go":    "package e\n",
		"z/only_out.go": big("z", 1),
	})
	roots := []root{{dir: "p", files: []string{"huge.go", "large.go", "s1.go", "s2.go", "empty.go", "sub/sub.go"}}, {dir: "q"}, {dir: "e"}}
	cost := map[string]float64{"p": 10, "e": 10}
	units, err := planUnits(dir, roots, cost)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, u := range units {
		lines = append(lines, fmt.Sprintf("%s %s %v %v %d %.0f", u.name, u.dir, u.files, u.mutators, u.sites, u.cost))
	}
	want := []string{
		"p.1 p [empty.go large.go] [] 18 210",
		"p.2 p [s1.go s2.go] [] 12 150",
		"p.3 p [huge.go] [ARITHMETIC_BASE CONDITIONALS_BOUNDARY CONDITIONALS_NEGATION INCREMENT_DECREMENT] 20 230",
		"p.4 p [huge.go] [INVERT_LOGICAL INVERT_LOOPCTRL INVERT_NEGATIVES] 4 70",
		"p_sub p/sub [sub.go] [] 6 34",
		"q q [q.go] [] 6 34",
		"e.1 e [huge.go] [ARITHMETIC_BASE CONDITIONALS_BOUNDARY CONDITIONALS_NEGATION INCREMENT_DECREMENT] 20 230",
		"e.2 e [huge.go] [INVERT_LOGICAL INVERT_LOOPCTRL INVERT_NEGATIVES] 4 70",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("units:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if f := partitionFindings(t, dir, roots, units); len(f) > 0 {
		t.Errorf("%v", f)
	}
	if got := units[1].line(); got != "p.2\tp\t-E\t/\t-E\t^empty\\.go$\t-E\t^huge\\.go$\t-E\t^large\\.go$\t-E\t^other\\.go$" {
		t.Errorf("plan line %q", got)
	}
	if got := strings.Join(units[3].args(), " "); !strings.HasSuffix(got, " --arithmetic-base=false --conditionals-boundary=false --conditionals-negation=false --increment-decrement=false") {
		t.Errorf("a mutator group's arguments: %q", got)
	}
	// The check catches what a broken cut does.
	for planted, bad := range map[string]func([]unit) []unit{
		"a unit dropped":       func(us []unit) []unit { return us[1:] },
		"a mutator twice":      func(us []unit) []unit { us[3].mutators = append(us[3].mutators, "CONDITIONALS_BOUNDARY"); return us },
		"an own file excluded": func(us []unit) []unit { us[0].others = append(us[0].others, "large.go"); return us },
		"another file mutated": func(us []unit) []unit { us[0].others = us[0].others[1:]; return us },
		"a file in two units":  func(us []unit) []unit { us[5].files = append(us[5].files, "q.go"); return append(us, us[5]) },
		"a group's file lost":  func(us []unit) []unit { us[2].files = nil; return us },
	} {
		us, err := planUnits(dir, roots, cost)
		if err != nil {
			t.Fatal(err)
		}
		if len(partitionFindings(t, dir, roots, bad(us))) == 0 {
			t.Errorf("%s: planted and not reported", planted)
		}
	}
	// A cheap package is one unit, whatever its size.
	if one, err := planUnits(dir, roots[:1], nil); err != nil || !slices.Equal(names(one), []string{"p", "p_sub"}) {
		t.Errorf("a cheap package: %v %v", names(one), err)
	}
}

func TestUnitsRefuseWhatCannotBePlanned(t *testing.T) {
	dir := files(t, map[string]string{
		"a/b/x.go":  big("b", 1),
		"a_b/y.go":  big("a_b", 1),
		"bad/x.go":  "package bad\nfunc {",
		"none/x.go": "package none\n",
	})
	for name, c := range map[string]struct {
		roots []root
		cost  map[string]float64
		want  string
	}{
		"a missing root":       {[]root{{dir: "gone"}}, nil, "scope root gone"},
		"an unparsable source": {[]root{{dir: "bad"}}, nil, "x.go"},
		"no mutant":            {[]root{{dir: "none"}}, nil, "scope root none has no in-scope mutant"},
		"no in-scope file":     {[]root{{dir: "a", files: []string{"nope.go"}}}, nil, "scope root a has no in-scope mutant"},
		"a root in another":    {[]root{{dir: "a"}, {dir: "a/b"}}, nil, "scope root a/b has no in-scope mutant"},
		"two unit names":       {[]root{{dir: "a"}, {dir: "a_b"}}, nil, "two units are named a_b"},
		"a stale cost":         {[]root{{dir: "a"}}, map[string]float64{"gone": 1}, "the cost of gone names no package"},
	} {
		if us, err := planUnits(dir, c.roots, c.cost); err == nil || !strings.Contains(err.Error(), c.want) || us != nil {
			t.Errorf("%s: %v %v", name, names(us), err)
		}
	}
	for body, want := range map[string]string{
		"a/b\t-1\tmeasured\n": "seconds above 0",
		"a/b\tx\tmeasured\n":  "seconds above 0",
		"a/b\t1\t \n":         "how they were measured",
		"a/../b\t1\tm\n":      "a clean directory",
		"a/b\t1\n":            "want 3 tab-separated fields",
	} {
		p := filepath.Join(dir, "cost.tsv")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCost(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("cost %q: %v", body, err)
		}
	}
	if c, err := readCost(""); err != nil || len(c) != 0 {
		t.Errorf("no cost file: %v %v", c, err)
	}
}

func TestTheReleaseShardsOfTheRealScope(t *testing.T) {
	req.Covers(t, "TST-002")
	tree := filepath.Join("..", "..", "..")
	roots, err := readScope(filepath.Join(tree, "test", "mutation", "scope.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	cost, err := readCost(filepath.Join(tree, "test", "mutation", "cost.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	units, err := planUnits(tree, roots, cost)
	if err != nil {
		t.Fatal(err)
	}
	if f := partitionFindings(t, tree, roots, units); len(f) > 0 {
		t.Errorf("the units of the real scope: %v", f)
	}
	shards := spread(units, releaseShards)
	if f := spreadFindings(units, shards); len(f) > 0 {
		t.Errorf("the release shards: %v", f)
	}
	for k, s := range shards {
		load := 0.0
		for _, u := range s {
			load += u.cost
		}
		if load > shardBudget {
			t.Errorf("shard %d/%d is estimated at %.0f s, above the budget of %.0f s: raise the shard count of mutation.yml and releaseShards", k+1, releaseShards, load, shardBudget)
		}
	}
	for _, r := range roots {
		if !slices.ContainsFunc(units, func(u unit) bool { return inScope([]root{r}, path.Join(u.dir, u.files[0])) }) {
			t.Errorf("scope root %s has no unit", r.dir)
		}
	}
}

func TestTheScriptEnablesTheMutatorsTheUnitsCount(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "mutation.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Gremlins' defaults (ARITHMETIC_BASE, CONDITIONALS_BOUNDARY,
	// CONDITIONALS_NEGATION, INCREMENT_DECREMENT, INVERT_NEGATIVES) and the
	// two the script switches on.
	if !strings.Contains(string(b), `"${gremlins[@]}" unleash --silent --invert-logical --invert-loopctrl --timeout-coefficient 30 "$@"`) {
		t.Error("scripts/mutation.sh does not run Gremlins with the mutators of enabled (units.go)")
	}
	if len(enabled) != 7 {
		t.Errorf("enabled lists %d mutators, the script runs 7", len(enabled))
	}
}

func TestPlanPrintsAShardOfTheUnitsInFullMode(t *testing.T) {
	req.Covers(t, "TST-002")
	dir := files(t, base())
	for k, want := range map[int]string{1: "d\td\t-E\t/\n", 2: "h\th\t-E\t/\t-E\t^server\\.go$\n"} {
		code, out, errs := runGate(dir, "-mode", "full", "-min", "70", "-plan", "-shard", fmt.Sprintf("%d/2", k))
		if code != 0 || out != want {
			t.Errorf("shard %d/2: exit %d %q %s", k, code, out, errs)
		}
	}
	if code, out, _ := runGate(dir, "-mode", "full", "-min", "70", "-plan", "-shard", "1/1"); code != 0 || out != "d\td\t-E\t/\nh\th\t-E\t/\t-E\t^server\\.go$\n" {
		t.Errorf("shard 1/1: exit %d %q", code, out)
	}
	bad := files(t, with(map[string]string{"cost.tsv": "d\t0\tnone\n", "stale.tsv": "x\t1\tm\n"}))
	for name, c := range map[string]struct {
		dir  string
		args []string
		want string
	}{
		"an empty shard":     {dir, []string{"-mode", "full", "-min", "70", "-plan", "-shard", "3/3"}, "selects no unit"},
		"a bad shard":        {dir, []string{"-mode", "full", "-min", "70", "-plan", "-shard", "0/4"}, "-shard must be K/N"},
		"an empty value":     {dir, []string{"-mode", "full", "-min", "70", "-plan", "-shard", ""}, "-shard must be K/N"},
		"a shard in pr mode": {dir, []string{"-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan", "-shard", "1/2"}, "-shard needs full mode and -plan"},
		"a shard of a gate":  {dir, []string{"-mode", "full", "-min", "70", "-shard", "1/2", "d=d.json", "h=h.json"}, "-shard needs full mode and -plan"},
		"no shard":           {dir, []string{"-mode", "full", "-min", "70", "-plan"}, "full mode with -shard K/N"},
		"a bad cost":         {bad, []string{"-mode", "full", "-min", "70", "-cost", "cost.tsv", "-plan", "-shard", "1/1"}, "seconds above 0"},
		"a stale cost":       {bad, []string{"-mode", "full", "-min", "70", "-cost", "stale.tsv", "-plan", "-shard", "1/1"}, "the cost of x names no package"},
		"a missing tree":     {dir, []string{"-mode", "full", "-min", "70", "-tree", filepath.Join(dir, "gone"), "-plan", "-shard", "1/1"}, "scope root d"},
	} {
		code, out, errs := runGate(c.dir, c.args...)
		if code != 2 || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %q %q", name, code, out, errs)
		}
	}
}

func TestAUnitsReportHoldsOnlyItsOwnMutants(t *testing.T) {
	req.Covers(t, "TST-002")
	dir := files(t, with(map[string]string{
		"server.json": gremlins("server.go", mutation("ARITHMETIC_BASE", "LIVED", 3, 1)),
		"sub.json":    `{"files":[{"file_name":"sub/b.go","mutations":[` + mutation("ARITHMETIC_BASE", "KILLED", 1, 1) + `]}]}`,
	}))
	for name, report := range map[string]string{"an excluded file": "h=server.json", "a subdirectory": "d=sub.json"} {
		code, _, errs := runGate(dir, "-mode", "full", "-min", "70", "d=d.json", "h=h.json", report)
		if code != 2 || !strings.Contains(errs, "which is not its own") {
			t.Errorf("%s: exit %d %s", name, code, errs)
		}
	}
	u := unit{name: "p.2", dir: "p", files: []string{"a.go"}, mutators: []string{"INVERT_LOGICAL"}}
	if !u.holds(mutant{file: "p/a.go", mutator: "INVERT_LOGICAL"}) || u.holds(mutant{file: "p/a.go", mutator: "ARITHMETIC_BASE"}) || u.holds(mutant{file: "p/b.go", mutator: "INVERT_LOGICAL"}) {
		t.Error("a mutator group holds a mutant of another group or file")
	}
}
