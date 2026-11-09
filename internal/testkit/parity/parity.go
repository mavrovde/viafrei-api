// Package parity compares the answers of this API with the facts the MCP
// capability gives for the same question on the same fixture rows (TEST-009,
// FR-GEN-002). A bundle holds the questions and the expected facts; it is
// pinned per platform version in a lock file and verified by checksum before
// use. The projection rules turn a response body into facts both sides can
// state the same way. Nothing here touches the network.
package parity

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// Format names the bundle layout this package reads.
const Format = "viafrei.parity/2"

// The producers a bundle can name.
const (
	ProducerAPI = "api"
	ProducerMCP = "mcp"
)

// LockFile is the name of the lock file in a parity directory.
const LockFile = "lock.tsv"

// Bundle is one platform version's parity questions and facts.
type Bundle struct {
	Format   string `json:"format"`
	Platform string `json:"platform"`
	// Producer names the side that recorded the facts: ProducerMCP for
	// facts the MCP capability gave, ProducerAPI for facts this API recorded
	// itself. An api bundle is a regression lock, not parity evidence: the
	// API agrees with it by construction.
	Producer string  `json:"producer"`
	Rules    string  `json:"rules"`
	Fixture  Fixture `json:"fixture"`
	Cases    []Case  `json:"cases"`
}

// Fixture names the generated seed rows the facts were taken on.
type Fixture struct {
	Profile string `json:"profile"`
	Seed    uint64 `json:"seed"`
	// SHA256 is the checksum of the generated SQL, so a bundle cannot be
	// compared against rows it was not recorded on.
	SHA256 string `json:"sha256"`
}

// Case is one question and the facts the capability answered.
type Case struct {
	ID          string    `json:"id"`
	Operation   string    `json:"operation"`
	Counterpart string    `json:"counterpart"`
	Request     string    `json:"request"`
	Key         Key       `json:"key"`
	Now         time.Time `json:"now"`
	Facts       []Fact    `json:"facts"`
}

// Key is the caller the question is asked as.
type Key struct {
	UseClass string       `json:"useClass"`
	Scopes   []string     `json:"scopes"`
	Sources  []string     `json:"sources"`
	Accepted []Acceptance `json:"accepted"`
}

// Acceptance is one accepted version of a family's terms.
type Acceptance struct {
	Source  string `json:"source"`
	Version string `json:"version"`
}

// Fact is one named value of an answer.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Rules are the projection rules. Both sides state their facts by them.
type Rules struct {
	// ID names the rule set; a bundle records the one it was made with.
	ID string
	// PositionDecimals rounds every member named in Positions.
	PositionDecimals int
	Positions        []string
	// Keys are the members that name an array element, first present wins;
	// their values, and those of Identifiers, are normalised.
	Keys        []string
	Identifiers []string
	// Meta are the members of meta that are facts; the rest describe the
	// request, not the answer.
	Meta []string
}

// Default is the projection rule set of this format.
var Default = Rules{
	ID:               "viafrei.parity.rules/2",
	PositionDecimals: 5,
	Positions:        []string{"lat", "lon"},
	Keys:             []string{"id", "family", "code"},
	Identifiers:      []string{"sourceFamily"},
	Meta:             []string{"asOf", "attribution", "freshness", "notes", "page", "warnings"},
}

// Errors of loading a bundle.
var (
	ErrNotPinned = errors.New("no parity bundle is pinned for this platform version")
	ErrChecksum  = errors.New("parity bundle checksum does not match the lock file")
	ErrEmpty     = errors.New("parity bundle has no cases")
	ErrMalformed = errors.New("parity bundle is malformed")
)

// Load reads the bundle pinned for platform in the lock file of fsys, after
// verifying its checksum.
func Load(fsys fs.FS, platform string) (Bundle, error) {
	lock, err := fs.ReadFile(fsys, LockFile)
	if err != nil {
		return Bundle{}, err
	}
	path, sum, err := pinned(lock, platform)
	if err != nil {
		return Bundle{}, err
	}
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return Bundle{}, err
	}
	if got := Checksum(raw); got != sum {
		return Bundle{}, fmt.Errorf("%w: %s is %s, pinned %s", ErrChecksum, path, got, sum)
	}
	return Parse(raw, platform)
}

// Checksum is the hex SHA-256 of b.
func Checksum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func pinned(lock []byte, platform string) (string, string, error) {
	sc := bufio.NewScanner(bytes.NewReader(lock))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			return "", "", fmt.Errorf("%w: %s:%d wants platform, bundle and sha256", ErrMalformed, LockFile, n)
		}
		if f[0] == platform {
			return f[1], f[2], nil
		}
	}
	return "", "", fmt.Errorf("%w: %q", ErrNotPinned, platform)
}

// Parse decodes a bundle and checks it is one this package can compare.
func Parse(raw []byte, platform string) (Bundle, error) {
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	switch {
	case b.Format != Format:
		return Bundle{}, fmt.Errorf("%w: format %q, want %q", ErrMalformed, b.Format, Format)
	case b.Platform != platform:
		return Bundle{}, fmt.Errorf("%w: platform %q, pinned for %q", ErrMalformed, b.Platform, platform)
	case b.Rules != Default.ID:
		return Bundle{}, fmt.Errorf("%w: rules %q, want %q", ErrMalformed, b.Rules, Default.ID)
	case b.Producer != ProducerAPI && b.Producer != ProducerMCP:
		return Bundle{}, fmt.Errorf("%w: producer %q, want %q or %q", ErrMalformed, b.Producer, ProducerAPI, ProducerMCP)
	case len(b.Cases) == 0:
		return Bundle{}, ErrEmpty
	}
	seen := map[string]bool{}
	for _, c := range b.Cases {
		if c.ID == "" || seen[c.ID] || c.Operation == "" || c.Request == "" || len(c.Facts) == 0 {
			return Bundle{}, fmt.Errorf("%w: case %q needs a unique id, an operation, a request and facts", ErrMalformed, c.ID)
		}
		seen[c.ID] = true
		if n, ok := repeated(c.Facts); ok {
			return Bundle{}, fmt.Errorf("%w: case %q names the fact %q more than once", ErrMalformed, c.ID, n)
		}
	}
	return b, nil
}

// Inventory states every operation of an OpenAPI document that declares an
// MCP counterpart and has no case in b, and every case whose counterpart is
// not the one its operation declares; none means the bundle covers the
// document.
func Inventory(doc []byte, b Bundle) ([]string, error) {
	var d struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Counterpart string `json:"x-mcp-counterpart"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	declared := map[string]string{}
	for _, ops := range d.Paths {
		for _, op := range ops {
			if op.Counterpart != "" {
				declared[op.OperationID] = op.Counterpart
			}
		}
	}
	asked := map[string]bool{}
	var out []string
	for _, c := range b.Cases {
		asked[c.Operation] = true
		if want, ok := declared[c.Operation]; ok && c.Counterpart != want {
			out = append(out, fmt.Sprintf("case %s: counterpart %q, %s declares %q", c.ID, c.Counterpart, c.Operation, want))
		}
	}
	for op := range declared {
		if !asked[op] {
			out = append(out, op+": no case")
		}
	}
	sort.Strings(out)
	return out, nil
}

// ErrDuplicateFact means two values of an answer project onto one fact name,
// so one of them could not be compared.
var ErrDuplicateFact = errors.New("parity: two values project onto one fact name")

// Project turns a response body into its facts under r, sorted by name. Two
// values under one name are an error: a comparison keeping either would hide
// a difference in the other.
func Project(body []byte, r Rules) ([]Fact, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	p := projector{r: r}
	p.walk("data", "data", doc["data"])
	meta, _ := doc["meta"].(map[string]any)
	for _, m := range r.Meta {
		if v, ok := meta[m]; ok {
			p.walk("meta."+m, m, v)
		}
	}
	sort.Slice(p.out, func(i, j int) bool { return p.out[i].Name < p.out[j].Name })
	if n, ok := repeated(p.out); ok {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateFact, n)
	}
	return p.out, nil
}

// repeated returns a fact name that occurs more than once in fs.
func repeated(fs []Fact) (string, bool) {
	seen := make(map[string]bool, len(fs))
	for _, f := range fs {
		if seen[f.Name] {
			return f.Name, true
		}
		seen[f.Name] = true
	}
	return "", false
}

type projector struct {
	r   Rules
	out []Fact
}

func (p *projector) add(name, value string) { p.out = append(p.out, Fact{name, value}) }

func (p *projector) walk(path, member string, v any) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			p.add(path, "{}")
		}
		for k, e := range x {
			p.walk(path+"."+k, k, e)
		}
	case []any:
		p.array(path, member, x)
	case json.Number:
		p.add(path, p.number(member, x))
	case string:
		p.add(path, p.text(member, x))
	case bool:
		p.add(path, strconv.FormatBool(x))
	default:
		p.add(path, "null")
	}
}

func (p *projector) array(path, member string, xs []any) {
	if len(xs) == 0 {
		p.add(path, "[]")
		return
	}
	if strs, ok := stringList(xs); ok {
		// A list of values is a set: families, codes, notes. It is stated
		// as a sorted JSON array, so no value can be mistaken for a
		// separator.
		slices.Sort(strs)
		p.add(path, string(invariant.Must(json.Marshal(strs)))) // a list of strings always encodes
		return
	}
	for i, e := range xs {
		p.walk(path+"["+p.key(i, e)+"]", member, e)
	}
}

func stringList(xs []any) ([]string, bool) {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func (p *projector) key(i int, e any) string {
	if o, ok := e.(map[string]any); ok {
		for _, k := range p.r.Keys {
			if s, ok := o[k].(string); ok {
				return normalise(s)
			}
		}
	}
	return strconv.Itoa(i)
}

func (p *projector) number(member string, n json.Number) string {
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	if slices.Contains(p.r.Positions, member) {
		scale := math.Pow(10, float64(p.r.PositionDecimals))
		return strconv.FormatFloat(math.Round(f*scale)/scale, 'f', p.r.PositionDecimals, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (p *projector) text(member, s string) string {
	if slices.Contains(p.r.Keys, member) || slices.Contains(p.r.Identifiers, member) {
		return normalise(s)
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return s
}

func normalise(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// Diff states every difference between the expected and the projected facts,
// one line per fact, in name order; none means parity.
func Diff(want, got []Fact) []string {
	w, g := index(want), index(got)
	names := make([]string, 0, len(w)+len(g))
	for n := range w {
		names = append(names, n)
	}
	for n := range g {
		if _, ok := w[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		wv, inW := w[n]
		gv, inG := g[n]
		switch {
		case !inG:
			out = append(out, fmt.Sprintf("missing %s: want %q", n, wv))
		case !inW:
			out = append(out, fmt.Sprintf("extra   %s: got %q", n, gv))
		case wv != gv:
			out = append(out, fmt.Sprintf("changed %s: want %q, got %q", n, wv, gv))
		}
	}
	return out
}

func index(fs []Fact) map[string]string {
	m := make(map[string]string, len(fs))
	for _, f := range fs {
		m[f.Name] = f.Value
	}
	return m
}
