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
// times the seconds per mutant of its directory (test/mutation/cost.tsv, or
// defaultCost), plus unitOverhead for the run's start and its coverage run.
// The shards take the units by longest cost first, each to the lightest
// shard, so the heaviest shard stays near the mean.

const (
	// unitCap is the most a unit may cost before it is cut, under half the
	// shard budget of shard_test.go, so the shards pack evenly.
	unitCap = 240.0
	// unitOverhead is the start of a Gremlins run: module download and the
	// coverage run of the package, once per unit. 30 s makes the estimate of
	// the heaviest release shard (3 units of internal/domain/places) match
	// its local run times the cost file's factor.
	unitOverhead = 30.0
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

// readCost reads DIR, SECONDS PER MUTANT and HOW MEASURED per line.
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
		if err != nil || s <= 0 || strings.TrimSpace(r[2]) == "" || path.Clean(r[0]) != r[0] {
			return nil, fmt.Errorf("%s: cost line %q needs a clean directory, seconds above 0 and how they were measured", name, strings.Join(r, "\t"))
		}
		out[r[0]] = s
	}
	return out, nil
}

// piece is a part of a package: files with every mutator, or one file with
// some mutators.
type piece struct {
	files, mutators []string
	sites           int
}

// planUnits cuts the scope roots into the units of the full run, reading the
// sources under tree.
func planUnits(tree string, roots []root, cost map[string]float64) ([]unit, error) {
	var out []unit
	done := map[string]bool{}
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
			us, err := packageUnits(tree, dir, dirs[dir], roots, cost)
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
	for dir := range cost {
		if !done[dir] {
			return nil, fmt.Errorf("the cost of %s names no package of the scope (stale); remove it", dir)
		}
	}
	return out, nil
}

// packageUnits cuts one package directory.
func packageUnits(tree, dir string, names []string, roots []root, cost map[string]float64) ([]unit, error) {
	per := cost[dir]
	if per == 0 {
		per = defaultCost
	}
	var files, others []string
	count := map[string]map[string]int{}
	total := 0
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
		for _, n := range c {
			total += n
		}
	}
	if total == 0 {
		// Gremlins writes no report for a run without a mutant, and there is
		// nothing to mutate.
		return nil, nil
	}
	name := strings.ReplaceAll(dir, "/", "_")
	weigh := func(sites int) float64 { return unitOverhead + per*float64(sites) }
	if weigh(total) <= unitCap {
		return []unit{{name: name, dir: dir, files: files, others: others, sites: total, cost: weigh(total)}}, nil
	}
	// Each file whole when it fits, else its mutators in groups that fit.
	var whole, split []piece
	for _, f := range files {
		n := 0
		for _, m := range enabled {
			n += count[f][m]
		}
		if weigh(n) <= unitCap {
			whole = append(whole, piece{files: []string{f}, sites: n})
			continue
		}
		cur := piece{files: []string{f}}
		for _, m := range enabled {
			if cur.sites > 0 && weigh(cur.sites+count[f][m]) > unitCap {
				split = append(split, cur)
				cur = piece{files: []string{f}}
			}
			cur.mutators = append(cur.mutators, m)
			cur.sites += count[f][m]
		}
		split = append(split, cur)
	}
	// The whole files packed into groups, the heaviest first, each into the
	// first group it fits.
	slices.SortStableFunc(whole, func(a, b piece) int { return cmp.Compare(b.sites, a.sites) })
	var groups []piece
	for _, p := range whole {
		i := slices.IndexFunc(groups, func(g piece) bool { return weigh(g.sites+p.sites) <= unitCap })
		if i < 0 {
			groups = append(groups, piece{})
			i = len(groups) - 1
		}
		groups[i].files = append(groups[i].files, p.files...)
		groups[i].sites += p.sites
	}
	var out []unit
	for _, g := range append(groups, split...) {
		if g.sites == 0 {
			continue // files without a mutant, packed together
		}
		slices.Sort(g.files)
		u := unit{name: fmt.Sprintf("%s.%d", name, len(out)+1), dir: dir, files: g.files, mutators: g.mutators, sites: g.sites, cost: weigh(g.sites)}
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
		out = append(out, ms...)
	}
	return out, seen, nil
}
