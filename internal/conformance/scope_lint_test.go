package conformance_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The scope lint (LIC-010, DEV-044): nothing in the repository describes how
// data is obtained, no platform-internal storage name appears anywhere, and no
// public field of the OpenAPI document is named after a read-contract column.
// Its inputs live in test/scope/; this file and those lists are the only files
// it does not read, because they hold the words it looks for.

const scopeDir = "test/scope"

// scopeSelf is this file, relative to the repository root. It plants the
// words the lint refuses, so it is not scanned.
const scopeSelf = "internal/conformance/scope_lint_test.go"

type vocabEntry struct {
	re             *regexp.Regexp
	prefix, reason string // prefix: the literal start of the pattern, a cheap filter before the expression runs
}

type allowEntry struct {
	rule, path, token, reason string
	used                      bool
}

type scopeFinding struct {
	rule, path string
	line       int
	token      string // empty for the denylist rule: the name is never printed
	length     int
}

func (f scopeFinding) String() string {
	if f.rule == "denylist" {
		return fmt.Sprintf("%s:%d: a platform-internal storage name (%d characters); rename it with the domain vocabulary", f.path, f.line, f.length)
	}
	return fmt.Sprintf("%s:%d: %s rule: %q", f.path, f.line, f.rule, f.token)
}

// dataLines returns the non-empty lines of a list that are not comments.
func dataLines(src []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func parseVocabulary(src []byte) ([]vocabEntry, error) {
	var out []vocabEntry
	for _, l := range dataLines(src) {
		pattern, reason, ok := strings.Cut(l, "\t")
		if !ok || strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("vocabulary line %q has no reason", l)
		}
		plain, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("vocabulary pattern %q: %w", pattern, err)
		}
		prefix, _ := plain.LiteralPrefix()
		if prefix == "" || prefix != strings.ToLower(prefix) {
			return nil, fmt.Errorf("vocabulary pattern %q must start with lower-case literal text", pattern)
		}
		out = append(out, vocabEntry{re: regexp.MustCompile(`(?i)\b(?:` + pattern + `)`), prefix: prefix, reason: reason})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the vocabulary is empty")
	}
	return out, nil
}

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parseDenylist(src []byte) (map[string]bool, error) {
	out := map[string]bool{}
	for _, l := range dataLines(src) {
		if !hexHash.MatchString(l) {
			return nil, fmt.Errorf("denylist line %q is not a lower-case SHA-256", l)
		}
		out[l] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the denylist is empty")
	}
	return out, nil
}

func parseAllow(src []byte) ([]*allowEntry, error) {
	var out []*allowEntry
	for _, l := range dataLines(src) {
		f := strings.Split(l, "\t")
		if len(f) != 4 || strings.TrimSpace(f[3]) == "" {
			return nil, fmt.Errorf("allow line %q needs rule, path, word and reason", l)
		}
		switch f[0] {
		case "vocabulary", "denylist", "column":
		default:
			return nil, fmt.Errorf("allow line %q names an unknown rule", l)
		}
		out = append(out, &allowEntry{rule: f[0], path: f[1], token: f[2], reason: f[3]})
	}
	return out, nil
}

func nameHash(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(name)))
	return hex.EncodeToString(sum[:])
}

func isWordByte(b byte, first bool) bool {
	letter := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	return letter || (!first && ((b >= '0' && b <= '9') || b == '_' || b == '-'))
}

// words returns the words of a line: an ASCII letter followed by letters,
// digits, underscores and hyphens.
func words(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		if !isWordByte(line[i], true) {
			i++
			continue
		}
		j := i + 1
		for j < len(line) && isWordByte(line[j], false) {
			j++
		}
		out = append(out, line[i:j])
		i = j
	}
	return out
}

// denyCache remembers the verdict for each word already hashed, so a word
// that recurs across the tree is hashed once.
type denyCache struct {
	deny map[string]bool
	seen map[string]bool
}

func (c *denyCache) denied(word string) bool {
	low := strings.ToLower(word)
	v, ok := c.seen[low]
	if !ok {
		v = c.deny[nameHash(low)]
		c.seen[low] = v
	}
	return v
}

// scanText applies the vocabulary and the denylist to one file. A word is
// checked as written (lower case, a hyphen read as an underscore) and each of
// its hyphen-separated parts.
func scanText(path string, src []byte, vocab []vocabEntry, deny *denyCache) []scopeFinding {
	var out []scopeFinding
	for i, line := range strings.Split(string(src), "\n") {
		lower := strings.ToLower(line)
		for _, v := range vocab {
			if !strings.Contains(lower, v.prefix) {
				continue
			}
			for _, m := range v.re.FindAllString(line, -1) {
				out = append(out, scopeFinding{rule: "vocabulary", path: path, line: i + 1, token: strings.ToLower(m)})
			}
		}
		for _, w := range words(line) {
			candidates := []string{strings.ReplaceAll(w, "-", "_")}
			if strings.Contains(w, "-") {
				candidates = append(candidates, strings.Split(w, "-")...)
			}
			for _, c := range candidates {
				if c != "" && deny.denied(c) {
					out = append(out, scopeFinding{rule: "denylist", path: path, line: i + 1, length: len(c), token: strings.ToLower(c)})
					break
				}
			}
		}
	}
	return out
}

// contractNames returns the compound (snake_case) relation and column names
// of the read contract (schema contract), from the column list
// (test/contract/columns.tsv: relation, column, type; # comments). A single
// English word (name, domain) is domain vocabulary and cannot leak a storage
// name.
func contractNames(tsv string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(tsv, "\n") {
		f := strings.Split(l, "\t")
		if strings.HasPrefix(l, "#") || len(f) < 2 {
			continue
		}
		rel, ok := strings.CutPrefix(f[0], "contract.")
		if !ok {
			continue // the access store's names are the key console's own words
		}
		out[strings.ToLower(rel)] = true
		out[strings.ToLower(f[1])] = true
	}
	for n := range out {
		if !strings.Contains(n, "_") {
			delete(out, n)
		}
	}
	return out
}

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func snakeCase(s string) string {
	return strings.ToLower(camelBoundary.ReplaceAllString(s, "${1}_${2}"))
}

// publicNames returns every property and parameter name of an OpenAPI
// document, wherever it is declared.
func publicNames(doc []byte) ([]string, error) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			if props, ok := x["properties"].(map[string]any); ok {
				for k := range props {
					seen[k] = true
				}
			}
			if params, ok := x["parameters"].([]any); ok {
				for _, p := range params {
					if pm, ok := p.(map[string]any); ok {
						if name, ok := pm["name"].(string); ok {
							seen[name] = true
						}
					}
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("the document declares no property or parameter")
	}
	return out, nil
}

func scanPublicNames(path string, names []string, columns map[string]bool) []scopeFinding {
	var out []scopeFinding
	for _, n := range names {
		if columns[snakeCase(n)] {
			out = append(out, scopeFinding{rule: "column", path: path, token: n})
		}
	}
	return out
}

// applyAllow drops the findings an allow entry covers and marks the entry used.
// A denylist entry whose word is "-" covers every denylist finding of the
// one file it names (a denylist finding prints no word): each file of the
// store that reads the platform's tables directly (owner decision
// 2026-11-23), and of its test seed, whose table and column names are the
// platform's. A file of its own, never a directory, so a new file is linted.
func applyAllow(findings []scopeFinding, allow []*allowEntry) []scopeFinding {
	var out []scopeFinding
next:
	for _, f := range findings {
		for _, a := range allow {
			whole := a.rule == "denylist" && a.token == "-"
			if a.rule == f.rule && a.path == f.path && (whole || strings.EqualFold(a.token, f.token)) {
				a.used = true
				continue next
			}
		}
		out = append(out, f)
	}
	return out
}

// trackedFiles lists the files git tracks, relative to the repository root.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v; the scope lint cannot list the tree", err)
	}
	var out []string
	for _, p := range strings.Split(string(raw), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) < 50 {
		t.Fatalf("git ls-files listed %d files; the scope lint did not read the tree", len(out))
	}
	return out
}

func readScopeList(t *testing.T, name string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, scopeDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestScopeLint(t *testing.T) {
	req.Covers(t, "LIC-010", "DEV-044")
	vocab, err := parseVocabulary(readScopeList(t, "vocabulary.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	deny, err := parseDenylist(readScopeList(t, "denylist.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	allow, err := parseAllow(readScopeList(t, "allow.tsv"))
	if err != nil {
		t.Fatal(err)
	}

	cache := &denyCache{deny: deny, seen: map[string]bool{}}
	var findings []scopeFinding
	scanned, lines := 0, 0
	for _, p := range trackedFiles(t) {
		if p == scopeSelf || p == "go.sum" || strings.HasPrefix(p, scopeDir+"/") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(src, 0) >= 0 {
			continue // binary
		}
		scanned++
		lines += bytes.Count(src, []byte("\n"))
		findings = append(findings, scanText(p, src, vocab, cache)...)
	}

	list, err := os.ReadFile(filepath.Join(root, "test", "contract", "columns.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	columns := contractNames(string(list))
	if len(columns) < 20 {
		t.Fatalf("only %d compound names read from test/contract/columns.tsv; the parse did not read it", len(columns))
	}
	doc, err := os.ReadFile(filepath.Join(root, "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := publicNames(doc)
	if err != nil {
		t.Fatal(err)
	}
	findings = append(findings, scanPublicNames("api/openapi.json", names, columns)...)

	for _, f := range applyAllow(findings, allow) {
		t.Error(f)
	}
	for _, a := range allow {
		if !a.used {
			t.Errorf("allow entry %s %s %q matches no finding; remove it", a.rule, a.path, a.token)
		}
	}
	t.Logf("scope lint read %d files (%d lines), %d vocabulary patterns, %d hashed names, %d public names against %d contract names, %d allow entries",
		scanned, lines, len(vocab), len(deny), len(names), len(columns), len(allow))
}

func TestScopeLintCanSayNo(t *testing.T) {
	req.Covers(t, "LIC-010", "DEV-044")
	vocab, err := parseVocabulary([]byte("crawl\tnames the walk\nfeeds?\\b\tthe route\n"))
	if err != nil {
		t.Fatal(err)
	}
	deny, err := parseDenylist([]byte("# planted\n" + nameHash("planted_internal_name") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("a clean line\nthe Crawler job reads feeds\nselect * from Planted_Internal_Name\npath /planted-internal-name/x\nfeedback is fine\n")
	got := scanText("x.md", src, vocab, &denyCache{deny: deny, seen: map[string]bool{}})
	var rendered []string
	for _, f := range got {
		rendered = append(rendered, f.String())
	}
	want := []string{
		`x.md:2: vocabulary rule: "crawl"`,
		`x.md:2: vocabulary rule: "feeds"`,
		"x.md:3: a platform-internal storage name (21 characters); rename it with the domain vocabulary",
		"x.md:4: a platform-internal storage name (21 characters); rename it with the domain vocabulary",
	}
	if strings.Join(rendered, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(rendered, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range rendered {
		if strings.Contains(strings.ToLower(r), "planted_internal") {
			t.Fatalf("a denylist finding printed the name: %s", r)
		}
	}

	columns := contractNames("# a comment_line\ncontract.thing\tdisplay_name\ttext\ncontract.thing\tname\ttext\ncontract.other_view\tid\ttext\naccess.api_key\tkey_id\ttext\nshort\n")
	if !columns["display_name"] || !columns["other_view"] || columns["name"] || columns["comment_line"] || len(columns) != 2 {
		t.Fatalf("contract names: %v", columns)
	}
	names, err := publicNames([]byte(`{"paths":{"/x":{"get":{"parameters":[{"name":"display_name"}]}}},"components":{"schemas":{"A":{"properties":{"displayName":{},"label":{"items":{"properties":{"otherView":{}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cols := scanPublicNames("doc", names, columns)
	if len(cols) != 3 {
		t.Fatalf("column findings %v, want displayName, display_name and otherView", cols)
	}

	allow, err := parseAllow([]byte("column\tdoc\tdisplayName\treason\nvocabulary\tx.md\tnever\treason\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rest := applyAllow(cols, allow); len(rest) != 2 || !allow[0].used || allow[1].used {
		t.Fatalf("allow: rest %v, used %v %v", rest, allow[0].used, allow[1].used)
	}

	// A denylist entry of word "-" covers the storage names of the one file
	// it names, and no other rule's, and no other file: a directory is no
	// path of a file, so it covers nothing.
	stored := []scopeFinding{{rule: "denylist", path: "a/b/x.go", line: 1, length: 9}, {rule: "denylist", path: "a/bc.go", line: 1, length: 9}, {rule: "vocabulary", path: "a/b/x.go", token: "never"}}
	file, err := parseAllow([]byte("denylist\ta/b/x.go\t-\treason\ndenylist\ta/\t-\treason\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rest := applyAllow(stored, file); len(rest) != 2 || rest[0].path != "a/bc.go" || rest[1].rule != "vocabulary" || !file[0].used || file[1].used {
		t.Fatalf("file allow: rest %v, used %v %v", rest, file[0].used, file[1].used)
	}
}

func TestScopeListsFailClosed(t *testing.T) {
	req.Covers(t, "LIC-010", "DEV-044")
	for name, err := range map[string]error{
		"empty vocabulary":   second(parseVocabulary([]byte("# only a comment\n"))),
		"reasonless pattern": second(parseVocabulary([]byte("crawl\n"))),
		"bad pattern":        second(parseVocabulary([]byte("(\treason\n"))),
		"no literal start":   second(parseVocabulary([]byte("[a-z]+\treason\n"))),
		"upper-case start":   second(parseVocabulary([]byte("Crawl\treason\n"))),
		"empty denylist":     second(parseDenylist([]byte("\n"))),
		"plain name listed":  second(parseDenylist([]byte("some_name\n"))),
		"allow short":        second(parseAllow([]byte("column\tdoc\tx\n"))),
		"allow unknown rule": second(parseAllow([]byte("other\tdoc\tx\treason\n"))),
		"no public names":    second(publicNames([]byte(`{"paths":{}}`))),
		"unparsable doc":     second(publicNames([]byte(`{`))),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }
