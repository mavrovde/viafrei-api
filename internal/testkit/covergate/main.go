// Command covergate merges Go coverage profiles and fails below a statement
// threshold in any package (TST-001, the 100 % rule for every layer).
//
//	go run ./internal/testkit/covergate -min 100 -exclusions test/coverage-exclusions.txt \
//	    -o coverage.out coverage-unit.out coverage-integration.out coverage-boot.out
//
// A block counts as covered when any profile ran it, so a repository line
// reached only by an integration test and a main reached only by the boot
// smoke are both credited. The merged profile is what SonarCloud reads.
//
// Exclusions are a named file, one entry per line: a glob over the
// module-relative file path, whitespace, and the reason. An entry without a
// reason, or one that matches no measured file (stale), fails the gate, so the
// file can only ever list what it names on purpose. Only generated code may be
// excluded: every file an entry matches must carry the standard
// "// Code generated ... DO NOT EDIT." line, or the gate fails.
//
// Exit 0 when every package reaches the threshold, 1 on a package below it or
// a bad exclusion, 2 on a usage error, an unreadable file or a profile that
// measured nothing.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type block struct {
	stmts int
	count int64
}

type exclusion struct {
	glob, reason string
	files        map[string]bool
}

type gate struct {
	mode   string
	blocks map[string]*block // "file:pos" -> block
	order  []string
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covergate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minPct := fs.Float64("min", 100, "minimum statement coverage per package, in percent")
	exclPath := fs.String("exclusions", "", "exclusion file (required; it may list nothing)")
	gomod := fs.String("gomod", "go.mod", "go.mod whose module path is stripped from file names")
	out := fs.String("o", "", "write the merged profile here")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *exclPath == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "covergate: -exclusions FILE and at least one profile are required")
		return 2
	}
	module, err := modulePath(*gomod)
	if err != nil {
		fmt.Fprintln(stderr, "covergate:", err)
		return 2
	}
	excl, problems, err := readExclusions(*exclPath)
	if err != nil {
		fmt.Fprintln(stderr, "covergate:", err)
		return 2
	}
	g := &gate{blocks: map[string]*block{}}
	for _, p := range fs.Args() {
		if err := g.read(p); err != nil {
			fmt.Fprintln(stderr, "covergate:", err)
			return 2
		}
	}
	if *out != "" {
		if err := os.WriteFile(*out, g.profile(), 0o600); err != nil {
			fmt.Fprintln(stderr, "covergate:", err)
			return 2
		}
	}
	problems = append(problems, g.report(stdout, module, excl, *exclPath, *minPct)...)
	problems = append(problems, notGenerated(filepath.Dir(*gomod), excl)...)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, "FAIL", p)
		}
		return 1
	}
	return 0
}

func modulePath(gomod string) (string, error) {
	data, err := os.ReadFile(gomod) // #nosec G304 -- a path the caller names
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(m), nil
		}
	}
	return "", fmt.Errorf("%s names no module", gomod)
}

func readExclusions(p string) ([]*exclusion, []string, error) {
	data, err := os.ReadFile(p) // #nosec G304 -- a path the caller names
	if err != nil {
		return nil, nil, err
	}
	var excl []*exclusion
	var problems []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		glob, reason := fields[0], strings.Join(fields[1:], " ")
		if _, err := path.Match(glob, ""); err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: bad glob %q", p, i+1, glob))
			continue
		}
		if reason == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: exclusion %s carries no reason", p, i+1, glob))
			continue
		}
		excl = append(excl, &exclusion{glob: glob, reason: reason, files: map[string]bool{}})
	}
	return excl, problems, nil
}

// read merges one profile; a profile without a single block measured nothing.
func (g *gate) read(p string) error {
	f, err := os.Open(p) // #nosec G304 -- a path the caller names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if mode, ok := strings.CutPrefix(text, "mode: "); ok {
			if g.mode == "" {
				g.mode = mode
			}
			continue
		}
		if text == "" {
			continue
		}
		key, stmts, count, err := parseBlock(text)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", p, line, err)
		}
		b, seen := g.blocks[key]
		if !seen {
			b = &block{stmts: stmts}
			g.blocks[key] = b
			g.order = append(g.order, key)
		}
		b.count += count
		n++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	if n == 0 {
		return fmt.Errorf("%s holds no coverage block: a profile that measured nothing is not evidence", p)
	}
	return nil
}

// parseBlock reads "file.go:1.2,3.4 stmts count".
func parseBlock(text string) (string, int, int64, error) {
	fields := strings.Fields(text)
	if len(fields) != 3 || !strings.Contains(fields[0], ":") {
		return "", 0, 0, fmt.Errorf("not a coverage block: %q", text)
	}
	stmts, err1 := strconv.Atoi(fields[1])
	count, err2 := strconv.ParseInt(fields[2], 10, 64)
	if err := errors.Join(err1, err2); err != nil || stmts < 0 || count < 0 {
		return "", 0, 0, fmt.Errorf("not a coverage block: %q", text)
	}
	return fields[0], stmts, count, nil
}

func (g *gate) profile() []byte {
	var b strings.Builder
	b.WriteString("mode: " + g.mode + "\n")
	for _, k := range g.order {
		fmt.Fprintf(&b, "%s %d %d\n", k, g.blocks[k].stmts, g.blocks[k].count)
	}
	return []byte(b.String())
}

type tally struct{ covered, total int }

func (t tally) pct() float64 {
	if t.total == 0 {
		return 100
	}
	return 100 * float64(t.covered) / float64(t.total)
}

// report prints coverage per package and returns every package below min and
// every exclusion that matched nothing.
func (g *gate) report(w io.Writer, module string, excl []*exclusion, exclPath string, minPct float64) []string {
	pkgs := map[string]*tally{}
	var all tally
	for _, k := range g.order {
		file := strings.TrimPrefix(strings.TrimPrefix(k[:strings.LastIndex(k, ":")], module), "/")
		if e := matching(excl, file); e != nil {
			e.files[file] = true
			continue
		}
		pkg := path.Dir(file)
		if pkgs[pkg] == nil {
			pkgs[pkg] = &tally{}
		}
		b := g.blocks[k]
		pkgs[pkg].total += b.stmts
		all.total += b.stmts
		if b.count > 0 {
			pkgs[pkg].covered += b.stmts
			all.covered += b.stmts
		}
	}
	names := make([]string, 0, len(pkgs))
	for p := range pkgs {
		names = append(names, p)
	}
	slices.Sort(names)
	var problems []string
	fmt.Fprintf(w, "statement coverage per package (minimum %.1f%%):\n", minPct)
	for _, p := range names {
		t := pkgs[p]
		fmt.Fprintf(w, "  %6.1f%%  %5d/%-5d  %s\n", t.pct(), t.covered, t.total, p)
		if t.pct() < minPct {
			problems = append(problems, fmt.Sprintf("%s: %.1f%% of %d statements, below %.1f%%", p, t.pct(), t.total, minPct))
		}
	}
	fmt.Fprintf(w, "excluded by %s: %d entries\n", exclPath, len(excl))
	for _, e := range excl {
		fmt.Fprintf(w, "  %s (%d files): %s\n", e.glob, len(e.files), e.reason)
		if len(e.files) == 0 {
			problems = append(problems, fmt.Sprintf("%s: exclusion %s matches no measured file (stale)", exclPath, e.glob))
		}
	}
	fmt.Fprintf(w, "total %.1f%% of %d statements in %d packages\n", all.pct(), all.total, len(names))
	return problems
}

// generatedHeader is the line the Go toolchain recognises as marking a
// generated file (https://go.dev/s/generatedcode).
var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// notGenerated lists every excluded file that is not generated code: only a
// generator's output may leave the 100 % rule.
func notGenerated(root string, excl []*exclusion) []string {
	var problems []string
	for _, e := range excl {
		files := make([]string, 0, len(e.files))
		for f := range e.files {
			files = append(files, f)
		}
		slices.Sort(files)
		for _, f := range files {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f))) // #nosec G304 -- a file the measured profile names
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("exclusion %s: %s cannot be read, so it is not shown to be generated: %v", e.glob, f, err))
			case !generatedHeader.Match(src):
				problems = append(problems, fmt.Sprintf("exclusion %s: %s has no \"// Code generated ... DO NOT EDIT.\" line; only generated code may be excluded", e.glob, f))
			}
		}
	}
	return problems
}

func matching(excl []*exclusion, file string) *exclusion {
	for _, e := range excl {
		if ok, _ := path.Match(e.glob, file); ok {
			return e
		}
	}
	return nil
}
