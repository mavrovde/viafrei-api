// Command inventory exports what the API must serve, from its two
// consumers' releases, into the pinned files of test/inventory, and checks
// that those files are as new as the releases (the release guard).
//
//	go run ./internal/release/inventory mcp -version X.Y.Z -commit SHA -contract contract.json -catalogue catalogue.json
//	go run ./internal/release/inventory routes -version X.Y.Z -commit SHA FILE...
//	go run ./internal/release/inventory web -version X.Y.Z -commit SHA [-expand TEMPLATE=a,b,...]... DIR
//	go run ./internal/release/inventory age -dir test/inventory -platform X.Y.Z -web X.Y.Z
//
// mcp reads the MCP server's catalogue as the platform release generates it
// from the booted server (its tools with their arguments and input schemas,
// its resources) and writes one row per tool, per argument and per resource.
// Prompts are left out: they hold no data, they compose the tools.
//
// routes reads the source files of the platform's fragment service and
// writes one row per route it registers (method and path, its constants
// resolved).
//
// web reads the site's source tree of a release (the files below DIR, test
// files left out, comment lines skipped) and writes one row per distinct
// fragment path the site asks for, with the number of places it is asked
// from; a ${...} part becomes {}. -expand replaces a template whose {} the
// source fills from a fixed list by one row per value (the list is named in
// the file's header, so the expansion is part of the record).
//
// Every file starts with `# source <consumer> <version> <commit>`. age
// refuses (exit 1) when a file's version is older than the newest release of
// its consumer: export again first.
//
// Exit 0 on success; 1 on a refusal; 2 on a usage error or an input that
// cannot be read (never a pass).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// errRefused is a check that ran and said no.
var errRefused = errors.New("refused")

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "inventory: mcp, routes, web or age")
		return 2
	}
	cmds := map[string]func([]string, io.Writer, io.Writer) error{"mcp": mcpCmd, "routes": routesCmd, "web": webCmd, "age": ageCmd}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "inventory: unknown command %q\n", args[0])
		return 2
	}
	switch err := cmd(args[1:], stdout, stderr); {
	case errors.Is(err, errRefused):
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "inventory:", err)
		return 2
	}
	return 0
}

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// source parses the flags every export shares and returns the header line.
func source(fs *flag.FlagSet, consumer string, args []string, stderr io.Writer) (string, error) {
	fs.SetOutput(stderr)
	version := fs.String("version", "", "the consumer's release X.Y.Z")
	commit := fs.String("commit", "", "the commit of that release")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if !releaseVersion.MatchString(*version) || !regexp.MustCompile(`^[0-9a-f]{7,40}$`).MatchString(*commit) {
		return "", fmt.Errorf("%s needs -version X.Y.Z and -commit SHA", consumer)
	}
	return "# source " + consumer + " " + *version + " " + *commit, nil
}

func mcpCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	contract := fs.String("contract", "", "the generated contract.json (tools, arguments, input schemas)")
	catalogue := fs.String("catalogue", "", "the generated catalogue.json (resources)")
	head, err := source(fs, "mcp", args, stderr)
	if err != nil {
		return err
	}
	var c struct {
		Tools []struct {
			Name string `json:"name"`
			Args []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"args"`
			Schema json.RawMessage `json:"schema"`
		} `json:"tools"`
	}
	var cat struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	if err := readJSON(*contract, &c); err != nil {
		return err
	}
	if err := readJSON(*catalogue, &cat); err != nil {
		return err
	}
	if len(c.Tools) == 0 || len(cat.Resources) == 0 {
		return errors.New("the catalogue lists no tool or no resource")
	}
	rows := []string{head, "# kind\tentry\tdetail"}
	for _, t := range c.Tools {
		sum := sha256.Sum256(t.Schema)
		rows = append(rows, "tool\t"+t.Name+"\tschema sha256:"+hex.EncodeToString(sum[:]))
		for _, a := range t.Args {
			req := "optional"
			if a.Required {
				req = "required"
			}
			rows = append(rows, "argument\t"+t.Name+"."+a.Name+"\t"+a.Type+" "+req)
		}
	}
	for _, r := range cat.Resources {
		rows = append(rows, "resource\t"+r.URI+"\t-")
	}
	_, err = io.WriteString(stdout, strings.Join(rows, "\n")+"\n")
	return err
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) // #nosec G304 -- a file the maintainer names
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

var (
	constDecl = regexp.MustCompile("(?m)^export const ([A-Z][A-Z0-9_]*) = (\"[^\"]*\"|`[^`]*`);")
	routeCall = regexp.MustCompile("app\\.(get|post|put|patch|delete)\\(\\s*(\"[^\"]*\"|`[^`]*`|[A-Z][A-Z0-9_]*)\\s*,")
	interp    = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)
)

func routesCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	opaque := opaques{}
	fs.Var(opaque, "opaque", "PREFIX=FILE: FILE's routes are written as PREFIX-01, PREFIX-02, ... in their order, never by path (repeatable)")
	head, err := source(fs, "routes", args, stderr)
	if err != nil {
		return err
	}
	var srcs, prefixes []string
	for _, p := range fs.Args() {
		b, err := os.ReadFile(p) // #nosec G304 -- a file the maintainer names
		if err != nil {
			return err
		}
		srcs = append(srcs, string(b))
		prefixes = append(prefixes, opaque[p])
		delete(opaque, p)
	}
	for p := range opaque {
		return fmt.Errorf("-opaque names %s, which is not a FILE argument", p)
	}
	consts := map[string]string{}
	for _, s := range srcs {
		for _, m := range constDecl.FindAllStringSubmatch(s, -1) {
			consts[m[1]] = m[2][1 : len(m[2])-1]
		}
	}
	resolve := func(s string) (string, error) {
		var missing error
		out := interp.ReplaceAllStringFunc(s, func(x string) string {
			v, ok := consts[x[2:len(x)-1]]
			if !ok {
				missing = fmt.Errorf("route %q names an unknown constant %s", s, x)
			}
			return v
		})
		return out, missing
	}
	seen := map[string]bool{}
	rows := []string{head, "# method\tpath"}
	for i, s := range srcs {
		n := 0
		for _, m := range routeCall.FindAllStringSubmatch(s, -1) {
			p := m[2]
			if c, ok := consts[p]; ok {
				p = c
			} else if p[0] == '"' || p[0] == '`' {
				p = p[1 : len(p)-1]
			} else {
				return fmt.Errorf("a route names an unknown constant %s", p)
			}
			p, err := resolve(p)
			if err != nil {
				return err
			}
			row := strings.ToUpper(m[1]) + "\t" + p
			if seen[row] {
				continue
			}
			seen[row] = true
			if prefixes[i] != "" { // the file's routes by number, never by path
				n++
				row = fmt.Sprintf("%s\t%s-%02d", strings.ToUpper(m[1]), prefixes[i], n)
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 2 {
		return errors.New("no route read")
	}
	_, err = io.WriteString(stdout, strings.Join(rows, "\n")+"\n")
	return err
}

// opaques is the repeatable -opaque flag: each file's prefix.
type opaques map[string]string

func (o opaques) String() string { return fmt.Sprint(map[string]string(o)) }

var opaquePrefix = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (o opaques) Set(s string) error {
	prefix, file, ok := strings.Cut(s, "=")
	if !ok || !opaquePrefix.MatchString(prefix) || file == "" {
		return fmt.Errorf("-opaque %q: want PREFIX=FILE, PREFIX in lower-case letters, digits and hyphens", s)
	}
	o[file] = prefix
	return nil
}

// expansions is the repeatable -expand flag.
type expansions map[string][]string

func (e expansions) String() string { return fmt.Sprint(map[string][]string(e)) }

func (e expansions) Set(s string) error {
	tmpl, vals, ok := strings.Cut(s, "=")
	if !ok || !strings.Contains(tmpl, "{}") || vals == "" {
		return fmt.Errorf("-expand %q: want TEMPLATE=a,b, a {} in TEMPLATE (the first is replaced)", s)
	}
	e[tmpl] = strings.Split(vals, ",")
	return nil
}

const fragmentRoot = "/internal/"

// fragmentPaths returns the fragment paths a line of code asks for.
func fragmentPaths(line string) []string {
	var out []string
	for i := strings.Index(line, fragmentRoot); i >= 0; {
		var b strings.Builder
		j := i
	scan:
		for j < len(line) {
			switch c := line[j]; {
			case c == '$' && j+1 < len(line) && line[j+1] == '{':
				depth := 0
				for j < len(line) {
					if line[j] == '{' {
						depth++
					} else if line[j] == '}' {
						depth--
						if depth == 0 {
							break
						}
					}
					j++
				}
				b.WriteString("{}")
				j = min(j+1, len(line)) // past the }, or the end of an unclosed one
			case c == '`' || c == '"' || c == '\'' || c == '?' || c == ' ' || c == ')' || c == ',':
				break scan
			default:
				b.WriteByte(c)
				j++
			}
		}
		out = append(out, strings.TrimSuffix(b.String(), "/"))
		next := strings.Index(line[j:], fragmentRoot)
		if next < 0 {
			break
		}
		i = j + next
	}
	return out
}

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "*") || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*")
}

func webCmd(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("web", flag.ContinueOnError)
	expand := expansions{}
	set.Var(expand, "expand", "TEMPLATE=a,b: one row per value for a template the source fills from a fixed list (repeatable)")
	head, err := source(set, "web", args, stderr)
	if err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("web needs one source directory")
	}
	calls := map[string]int{}
	err = filepath.WalkDir(set.Arg(0), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(p)
		if (ext != ".ts" && ext != ".astro") || strings.Contains(p, ".test.") {
			return nil
		}
		b, err := os.ReadFile(p) // #nosec G304 G122 -- below the directory the maintainer names, read once
		if err != nil {
			return err
		}
		for _, l := range strings.Split(string(b), "\n") {
			if isComment(l) {
				continue
			}
			for _, f := range fragmentPaths(l) {
				calls[f]++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return errors.New("no fragment path read")
	}
	for tmpl, vals := range expand {
		n, ok := calls[tmpl]
		if !ok {
			return fmt.Errorf("-expand %s: the source asks for no such path", tmpl)
		}
		delete(calls, tmpl)
		for _, v := range vals {
			calls[strings.Replace(tmpl, "{}", v, 1)] += n
		}
	}
	paths := make([]string, 0, len(calls))
	for p := range calls {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	rows := []string{head}
	tmpls := make([]string, 0, len(expand))
	for t := range expand {
		tmpls = append(tmpls, t)
	}
	sort.Strings(tmpls)
	for _, t := range tmpls {
		rows = append(rows, "# expanded "+t+" = "+strings.Join(expand[t], ","))
	}
	rows = append(rows, "# path\tplaces asked from")
	for _, p := range paths {
		rows = append(rows, p+"\t"+strconv.Itoa(calls[p]))
	}
	_, err = io.WriteString(stdout, strings.Join(rows, "\n")+"\n")
	return err
}

// Inventory files and the consumer whose release each records.
var Files = map[string]string{"mcp.tsv": "platform", "routes.tsv": "platform", "web.tsv": "web"}

// Header reads the `# source <consumer> <version> <commit>` line of an
// inventory file.
func Header(src string) (consumer, version, commit string, err error) {
	first, _, _ := strings.Cut(src, "\n")
	f := strings.Fields(first)
	if len(f) != 5 || f[0] != "#" || f[1] != "source" || !releaseVersion.MatchString(f[3]) {
		return "", "", "", fmt.Errorf("the first line %q is not '# source <consumer> <X.Y.Z> <commit>'", first)
	}
	return f[2], f[3], f[4], nil
}

// older reports whether release a is older than release b (both X.Y.Z).
func older(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func ageCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("age", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "test/inventory", "the inventory directory")
	newest := map[string]*string{
		"platform": fs.String("platform", "", "the platform's newest release X.Y.Z"),
		"web":      fs.String("web", "", "the site's newest release X.Y.Z"),
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	for k, v := range newest {
		if !releaseVersion.MatchString(*v) {
			return fmt.Errorf("age needs -%s X.Y.Z", k)
		}
	}
	names := make([]string, 0, len(Files))
	for n := range Files {
		names = append(names, n)
	}
	sort.Strings(names)
	stale := false
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(*dir, n)) // #nosec G304 -- the inventory's own files
		if err != nil {
			return err
		}
		_, version, _, err := Header(string(b))
		if err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
		want := *newest[Files[n]]
		if older(version, want) {
			stale = true
			fmt.Fprintf(stderr, "inventory: %s records %s %s, older than its newest release %s; export it again\n", n, Files[n], version, want)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s %s\n", n, Files[n], version)
	}
	if stale {
		return errRefused
	}
	return nil
}
