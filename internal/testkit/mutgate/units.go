package main

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The full run's units. A scope root is cut into one unit per package
// directory under it, and a package whose estimated cost passes unitCap into
// groups of its files; a file that alone passes unitCap is cut by mutator
// into groups of mutators (one mutator of one file is the smallest unit,
// whatever it costs). A unit is one Gremlins run: on its directory, with
// -E excluding every other file of it and every subdirectory (each its own
// unit), and with the mutators of other groups switched off. So the units
// are disjoint in (file, mutator) and together every in-scope mutant: each
// is mutated exactly once.
//
// The cost of a unit is an estimate in seconds of a ubuntu-24.04 runner (4
// workers): its mutation sites, counted the way Gremlins v0.6.0 finds them,
// each times the seconds per mutant of its mutator in its file, its file or
// its directory (test/mutation/cost.tsv, the most specific row, else
// defaultCost), plus unitOverhead for the run's start and its coverage run.
// The seconds per mutant differ by file and mutator, not only by package: a
// mutant that lives runs every test of its package, one killed stops at
// the first failing test (-failfast), and one that never ends costs
// Gremlins' timeout (the coverage run times --timeout-coefficient). So the
// rows come from the unit times of a real release run.
// The shards take the units by longest cost first, each to the lightest
// shard, so the heaviest shard stays near the mean.

const (
	// unitCap is the most a unit may cost before it is cut, under half the
	// shard budget of shard_test.go, so the shards pack evenly. One mutator
	// of one file is never cut, whatever it costs.
	unitCap = 240.0
	// unitOverhead is the start of a Gremlins run: module download and the
	// coverage run of the package, once per unit. Run 45751491495 measured 9
	// to 28 s of a places unit outside Gremlins' elapsed time, and about 1 s
	// of a small package's.
	unitOverhead = 20.0
	// defaultCost is the seconds per mutant of a directory without a row in
	// the cost file.
	defaultCost = 0.6
)

// enabled is the mutator set of scripts/mutation.sh: Gremlins' defaults plus
// --invert-logical and --invert-loopctrl.
var enabled = []string{
	"ARITHMETIC_BASE", "CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION",
	"INCREMENT_DECREMENT", "INVERT_LOGICAL", "INVERT_LOOPCTRL", "INVERT_NEGATIVES",
}

// gremlinsVersion is the Gremlins release whose token table sites copies;
// scripts/mutation.sh runs the same one (shard_test.go pins the two).
const gremlinsVersion = "v0.6.0"

// sites is Gremlins v0.6.0's TokenMutantType, cut to the enabled mutators:
// the token of an assignment, binary, branch, inc/dec or unary node and the
// mutants it yields.
var sites = map[token.Token][]string{
	token.ADD: {"ARITHMETIC_BASE"}, token.MUL: {"ARITHMETIC_BASE"}, token.QUO: {"ARITHMETIC_BASE"}, token.REM: {"ARITHMETIC_BASE"},
	token.SUB:   {"INVERT_NEGATIVES", "ARITHMETIC_BASE"},
	token.BREAK: {"INVERT_LOOPCTRL"}, token.CONTINUE: {"INVERT_LOOPCTRL"},
	token.INC: {"INCREMENT_DECREMENT"}, token.DEC: {"INCREMENT_DECREMENT"},
	token.EQL: {"CONDITIONALS_NEGATION"}, token.NEQ: {"CONDITIONALS_NEGATION"},
	token.GEQ: {"CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION"}, token.GTR: {"CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION"},
	token.LEQ: {"CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION"}, token.LSS: {"CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION"},
	token.LAND: {"INVERT_LOGICAL"}, token.LOR: {"INVERT_LOGICAL"},
}

// countSites returns the mutants Gremlins makes of a Go source, per mutator.
func countSites(name string) (map[string]int, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		var tok token.Token
		switch n := n.(type) {
		case *ast.AssignStmt:
			tok = n.Tok
		case *ast.BinaryExpr:
			tok = n.Op
		case *ast.BranchStmt:
			tok = n.Tok
		case *ast.IncDecStmt:
			tok = n.Tok
		case *ast.UnaryExpr:
			tok = n.Op
		}
		for _, m := range sites[tok] {
			out[m]++
		}
		return true
	})
	return out, nil
}

// unit is one Gremlins run of the full mode.
type unit struct {
	name, dir string
	files     []string // the file names of dir it mutates
	others    []string // the other non-test Go files of dir, excluded
	mutators  []string // nil: every enabled mutator
	sites     int
	want      map[string]int // "file mutator" -> the mutants countSites finds
	cost      float64
}

// args are the Gremlins arguments of the unit beyond the common ones.
func (u unit) args() []string {
	out := []string{"-E", "/"}
	for _, f := range u.others {
		out = append(out, "-E", "^"+regexp.QuoteMeta(f)+"$")
	}
	if u.mutators != nil {
		for _, m := range enabled {
			if !slices.Contains(u.mutators, m) {
				out = append(out, "--"+strings.ToLower(strings.ReplaceAll(m, "_", "-"))+"=false")
			}
		}
	}
	return out
}

// line is the plan line of the unit: name, directory and arguments,
// tab-separated.
func (u unit) line() string {
	return strings.Join(append([]string{u.name, u.dir}, u.args()...), "\t")
}

// holds reports whether the unit mutates m: a mutant of another file or of
// another mutator group in its report means its run was not the planned one.
func (u unit) holds(m mutant) bool {
	return slices.Contains(u.files, strings.TrimPrefix(m.file, u.dir+"/")) && (u.mutators == nil || slices.Contains(u.mutators, m.mutator))
}

// readCost reads KEY, SECONDS PER MUTANT and HOW MEASURED per line. A key
// is a package directory, a file of one (dir/file.go) or one mutator of a
// file (dir/file.go:MUTATOR); rate takes the most specific.
func readCost(name string) (map[string]float64, error) {
	out := map[string]float64{}
	if name == "" {
		return out, nil
	}
	rows, err := lines(name, 3)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		s, err := strconv.ParseFloat(r[1], 64)
		p, m, pair := strings.Cut(r[0], ":")
		if err != nil || s <= 0 || strings.TrimSpace(r[2]) == "" || path.Clean(p) != p || pair && (!strings.HasSuffix(p, ".go") || !slices.Contains(enabled, m)) {
			return nil, fmt.Errorf("%s: cost line %q needs a clean directory, file or file:MUTATOR of an enabled mutator, seconds above 0 and how they were measured", name, strings.Join(r, "\t"))
		}
		out[r[0]] = s
	}
	return out, nil
}

// rate is the seconds per mutant of mutator m of file f of dir: the row of
// the pair, else of the file, else of the directory, else defaultCost.
func rate(cost map[string]float64, dir, f, m string) float64 {
	for _, k := range []string{dir + "/" + f + ":" + m, dir + "/" + f, dir} {
		if s, ok := cost[k]; ok {
			return s
		}
	}
	return defaultCost
}

// piece is a part of a package: files with every mutator, or one file with
// some mutators.
type piece struct {
	files, mutators []string
	sites           int
	work            float64 // the seconds of its mutants, without unitOverhead
}

// planUnits cuts the scope roots into the units of the full run, reading the
// sources under tree.
func planUnits(tree string, roots []root, cost map[string]float64) ([]unit, error) {
	var out []unit
	done := map[string]bool{} // the package directories, and the cost keys they make valid
	for _, r := range roots {
		dirs := map[string][]string{}
		err := filepath.WalkDir(filepath.Join(tree, r.dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(tree, p)
			rel = filepath.ToSlash(rel)
			if !d.IsDir() && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
				dirs[path.Dir(rel)] = append(dirs[path.Dir(rel)], path.Base(rel))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scope root %s: %w", r.dir, err)
		}
		before := len(out)
		for _, dir := range slices.Sorted(maps.Keys(dirs)) {
			if done[dir] {
				continue
			}
			done[dir] = true
			us, err := packageUnits(tree, dir, dirs[dir], roots, cost, done)
			if err != nil {
				return nil, err
			}
			out = append(out, us...)
		}
		if len(out) == before {
			return nil, fmt.Errorf("scope root %s has no in-scope mutant: a root with nothing to mutate is never a pass", r.dir)
		}
	}
	names := map[string]bool{}
	for _, u := range out {
		if names[u.name] {
			return nil, fmt.Errorf("two units are named %s", u.name)
		}
		names[u.name] = true
	}
	for k := range cost {
		if !done[k] {
			return nil, fmt.Errorf("the cost of %s names no package, in-scope file or mutant of one in the scope (stale); remove it", k)
		}
	}
	return out, nil
}

// packageUnits cuts one package directory.
// The cost keys of its in-scope files and of their mutants go into known.
func packageUnits(tree, dir string, names []string, roots []root, cost map[string]float64, known map[string]bool) ([]unit, error) {
	var files, others []string
	count := map[string]map[string]int{}
	work := map[string]float64{} // file -> seconds, and "file mutator" -> seconds
	total, all := 0, 0.0
	for _, f := range slices.Sorted(slices.Values(names)) {
		if !inScope(roots, dir+"/"+f) {
			others = append(others, f)
			continue
		}
		c, err := countSites(filepath.Join(tree, filepath.FromSlash(dir), f))
		if err != nil {
			return nil, err
		}
		files, count[f] = append(files, f), c
		known[dir+"/"+f] = true
		for _, m := range enabled {
			if c[m] > 0 {
				known[dir+"/"+f+":"+m] = true
				w := rate(cost, dir, f, m) * float64(c[m])
				work[f+" "+m], work[f], total, all = w, work[f]+w, total+c[m], all+w
			}
		}
	}
	if total == 0 {
		// Gremlins writes no report for a run without a mutant, and there is
		// nothing to mutate.
		return nil, nil
	}
	name := strings.ReplaceAll(dir, "/", "_")
	weigh := func(w float64) float64 { return unitOverhead + w }
	if weigh(all) <= unitCap {
		return []unit{{name: name, dir: dir, files: files, others: others, sites: total, cost: weigh(all), want: wants(count, files, nil)}}, nil
	}
	// Each file whole when it fits, else its mutators in groups that fit.
	var whole, split []piece
	for _, f := range files {
		n := 0
		for _, m := range enabled {
			n += count[f][m]
		}
		if weigh(work[f]) <= unitCap {
			whole = append(whole, piece{files: []string{f}, sites: n, work: work[f]})
			continue
		}
		cur := piece{files: []string{f}}
		for _, m := range enabled {
			if cur.sites > 0 && weigh(cur.work+work[f+" "+m]) > unitCap {
				split = append(split, cur)
				cur = piece{files: []string{f}}
			}
			cur.mutators = append(cur.mutators, m)
			cur.sites += count[f][m]
			cur.work += work[f+" "+m]
		}
		split = append(split, cur)
	}
	// The whole files packed into groups, the heaviest first, each into the
	// first group it fits.
	slices.SortStableFunc(whole, func(a, b piece) int { return cmp.Compare(b.work, a.work) })
	var groups []piece
	for _, p := range whole {
		i := slices.IndexFunc(groups, func(g piece) bool { return weigh(g.work+p.work) <= unitCap })
		if i < 0 {
			groups = append(groups, piece{})
			i = len(groups) - 1
		}
		groups[i].files = append(groups[i].files, p.files...)
		groups[i].sites += p.sites
		groups[i].work += p.work
	}
	var out []unit
	for _, g := range append(groups, split...) {
		if g.sites == 0 {
			continue // files without a mutant, packed together
		}
		slices.Sort(g.files)
		u := unit{name: fmt.Sprintf("%s.%d", name, len(out)+1), dir: dir, files: g.files, mutators: g.mutators, sites: g.sites, cost: weigh(g.work), want: wants(count, g.files, g.mutators)}
		for _, f := range files {
			if !slices.Contains(g.files, f) {
				u.others = append(u.others, f)
			}
		}
		u.others = append(u.others, others...)
		slices.Sort(u.others)
		out = append(out, u)
	}
	return out, nil
}

// wants are the mutants per "file mutator" of the files with the mutators
// (nil: every enabled one).
func wants(count map[string]map[string]int, files, ms []string) map[string]int {
	if ms == nil {
		ms = enabled
	}
	out := map[string]int{}
	for _, f := range files {
		for _, m := range ms {
			if count[f][m] > 0 {
				out[f+" "+m] = count[f][m]
			}
		}
	}
	return out
}

// drift compares the mutants of a unit's report, every status counted, with
// the ones countSites found: a difference means the copy of Gremlins' token
// table no longer is Gremlins', and the cut may have lost a mutant.
func drift(u unit, ms []mutant) error {
	got := map[string]int{}
	for _, m := range ms {
		got[strings.TrimPrefix(m.file, u.dir+"/")+" "+m.mutator]++
	}
	keys := slices.Sorted(maps.Keys(got))
	for k := range maps.Keys(u.want) {
		if _, ok := got[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		if got[k] != u.want[k] {
			file, m, _ := strings.Cut(k, " ")
			return fmt.Errorf("unit %s: the token table drifted from Gremlins %s: %s/%s %s has %d mutant(s) in the report and %d counted", u.name, gremlinsVersion, u.dir, file, m, got[k], u.want[k])
		}
	}
	return nil
}

// spread deals the units to count shards, the costliest first, each to the
// shard with the least cost so far (the lower index on a tie).
func spread(units []unit, count int) [][]unit {
	order := slices.Clone(units)
	slices.SortStableFunc(order, func(a, b unit) int { return cmp.Compare(b.cost, a.cost) })
	out := make([][]unit, count)
	load := make([]float64, count)
	for _, u := range order {
		i := 0
		for k := range load {
			if load[k] < load[i] {
				i = k
			}
		}
		out[i] = append(out[i], u)
		load[i] += u.cost
	}
	return out
}

// readUnitReports reads the UNIT=REPORT arguments of a full-mode gate.
func readUnitReports(args []string, units []unit) ([]mutant, map[string]bool, error) {
	seen := map[string]bool{}
	var out []mutant
	for _, a := range args {
		name, file, ok := strings.Cut(a, "=")
		if !ok || name == "" || file == "" {
			return nil, nil, fmt.Errorf("argument %q is not UNIT=REPORT", a)
		}
		i := slices.IndexFunc(units, func(u unit) bool { return u.name == name })
		if i < 0 {
			return nil, nil, fmt.Errorf("%s is not a unit of the full run (mutgate -plan -shard 1/1 lists them)", name)
		}
		seen[name] = true
		ms, err := readReport(units[i].dir, file)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range ms {
			if !units[i].holds(m) {
				return nil, nil, fmt.Errorf("%s: the report of unit %s holds %s %s, which is not its own: its run was not the planned one", file, name, m.location(), m.mutator)
			}
		}
		if err := drift(units[i], ms); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", file, err)
		}
		out = append(out, ms...)
	}
	return out, seen, nil
}
