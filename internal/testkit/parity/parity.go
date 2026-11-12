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
	"net/url"
	"regexp"
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
	Producer string `json:"producer"`
	// Commit is the platform commit the MCP side answered from (40 hex
	// digits). It is required of an mcp bundle, because a platform version
	// alone does not name the code that answered, and absent from an api
	// bundle, which no platform code answered.
	Commit  string  `json:"commit,omitempty"`
	Rules   string  `json:"rules"`
	Fixture Fixture `json:"fixture"`
	Cases   []Case  `json:"cases"`
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
	// Excluded are the facts left out of the comparison, each with its
	// reason (docs/parity.md). Project drops them and Parse refuses a bundle
	// that states one, so neither side can compare them by accident.
	Excluded []Exclusion
	// OwnedNotes are the note kinds of meta.notes whose text is not
	// compared: each is stated per family that owns a note of it, as
	// meta.notes.<kind>[<family>] = how many distinct notes it owns, and as
	// meta.notes.<kind> = [] when the answer states none (docs/parity.md).
	// The count and the family are compared; the text is not.
	OwnedNotes []string
}

// Owners maps a note's text to the families whose notes carry it. The API's
// answer states its notes as texts only; the harness reads who owns each
// from the fixture rows.
type Owners map[string][]string

// Exclusion is one fact, or family of facts, left out of the comparison.
// Pattern is a fact name in which [*] stands for any element key; it covers
// the name itself and every fact under it. When Asked names a request
// parameter, the exclusion holds only in a case that asks it (Asks).
type Exclusion struct {
	Pattern string
	Asked   string
	Reason  string
}

// Excludes reports whether the fact name falls under one of r's exclusions
// in a case asking request.
func (r Rules) Excludes(request, name string) bool {
	for _, e := range r.Excluded {
		if exclusionPattern(e.Pattern).MatchString(name) && (e.Asked == "" || Asks(request, e.Asked)) {
			return true
		}
	}
	return false
}

// Asks reports whether request asks the parameter: it is present and its
// value is not a number of 0 or less, which filters nothing.
func Asks(request, param string) bool {
	u, err := url.Parse(request)
	if err != nil {
		return false
	}
	v, ok := u.Query()[param]
	if !ok {
		return false
	}
	n, err := strconv.ParseFloat(v[0], 64)
	return err != nil || n > 0
}

func exclusionPattern(p string) *regexp.Regexp {
	q := strings.ReplaceAll(regexp.QuoteMeta(p), `\[\*\]`, `\[[^\]]*\]`)
	return regexp.MustCompile(`^` + q + `(?:$|[.\[])`)
}

// Default is the projection rule set of this format.
var Default = Rules{
	ID:               "viafrei.parity.rules/4",
	PositionDecimals: 5,
	Positions:        []string{"lat", "lon"},
	Keys:             []string{"id", "family", "code"},
	Identifiers:      []string{"sourceFamily"},
	Meta:             []string{"asOf", "attribution", "freshness", "notes", "page", "warnings"},
	Excluded: []Exclusion{
		{"data[*].access", "", "the MCP capability has no access concept: no charging source of the platform publishes who may use a site"},
		{"data[*].chargePoints[*].current", "", "the MCP capability states no current per point"},
		{"meta.freshness", "", "API-only: the per-family freshness block of the read contract (ENV-003)"},
		{"meta.warnings", "", "API-only: the warnings follow from API keys and licence classes, which the MCP capability has not got"},
		{"meta.page.hasMore", "min_kw", "the MCP capability states no count of the sites a power filter keeps, so it cannot say whether there are more"},
		{"meta.asOf", "", "each side defines it differently: the API the oldest family dataAsOf (ENV-004), MCP the newest current status or its clock"},
		{"meta.attribution[*].domain", "", attributionReason},
		{"meta.attribution[*].licence", "", attributionReason},
		{"meta.attribution[*].licenceUrl", "", attributionReason},
		{"meta.attribution[*].publisher", "", attributionReason},
		{"meta.attribution[*].shareAlike", "", attributionReason},
		{"meta.attribution[*].shareAlikeOffer", "", attributionReason},
		{"meta.attribution[*].termsUrl", "", attributionReason},
		{"meta.attribution[*].text", "", attributionReason},
	},
	// The fixture's condition notes are synthetic register texts and the
	// platform states its own register's (attributionReason): which family
	// a condition belongs to and how many it has are compared, the text is not.
	OwnedNotes: []string{"condition"},
}

// attributionReason is why only the credited family of an attribution entry
// is compared.
const attributionReason = "the fixture's families carry synthetic register texts, the platform credits its own register rows for the sources the families are stored as; which families are credited is compared"

// Errors of loading a bundle.
var (
	ErrNotPinned = errors.New("no parity bundle is pinned for this platform version")
	ErrChecksum  = errors.New("parity bundle checksum does not match the lock file")
	ErrEmpty     = errors.New("parity bundle has no cases")
	ErrMalformed = errors.New("parity bundle is malformed")
)

// ErrUnownedNote means an answer states a note no family owns, so it could
// not be stated by its family and would not be compared.
var ErrUnownedNote = errors.New("parity: a note no family owns")

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
	case b.Producer == ProducerMCP && !commitPattern.MatchString(b.Commit):
		return Bundle{}, fmt.Errorf("%w: an mcp bundle names the platform commit it answered from, 40 hex digits; got %q", ErrMalformed, b.Commit)
	case b.Producer == ProducerAPI && b.Commit != "":
		return Bundle{}, fmt.Errorf("%w: an api bundle was answered by no platform commit, got %q", ErrMalformed, b.Commit)
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
		for _, f := range c.Facts {
			if Default.Excludes(c.Request, f.Name) {
				return Bundle{}, fmt.Errorf("%w: case %q states %q, which %s excludes from the comparison", ErrMalformed, c.ID, f.Name, Default.ID)
			}
			if !Default.ownedNoteFact(f) {
				return Bundle{}, fmt.Errorf("%w: case %q states %q as %q; %s states a note of that kind as [] or as a count per family, never its text", ErrMalformed, c.ID, f.Name, f.Value, Default.ID)
			}
		}
		if k, ok := Default.bothWays(c.Facts); ok {
			return Bundle{}, fmt.Errorf("%w: case %q states meta.notes.%s both as [] and per family", ErrMalformed, c.ID, k)
		}
	}
	return b, nil
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

var countPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// ownedNoteFact reports whether f, if it names a note kind of r.OwnedNotes,
// is stated the way r states that kind: [] for none, or a positive count
// under one family. Any other fact is not one and is accepted.
func (r Rules) ownedNoteFact(f Fact) bool {
	for _, k := range r.OwnedNotes {
		base := "meta.notes." + k
		switch {
		case f.Name == base:
			return f.Value == "[]"
		case strings.HasPrefix(f.Name, base+"["):
			fam, ok := strings.CutSuffix(strings.TrimPrefix(f.Name, base+"["), "]")
			return ok && fam != "" && fam == normalise(fam) && !strings.ContainsAny(fam, "[]") && countPattern.MatchString(f.Value)
		}
	}
	return true
}

// bothWays returns a note kind of r.OwnedNotes that facts state both as []
// (none) and as a count under a family (some): one of the two is false.
func (r Rules) bothWays(facts []Fact) (string, bool) {
	for _, k := range r.OwnedNotes {
		base := "meta.notes." + k
		none := slices.ContainsFunc(facts, func(f Fact) bool { return f.Name == base })
		some := slices.ContainsFunc(facts, func(f Fact) bool { return strings.HasPrefix(f.Name, base+"[") })
		if none && some {
			return k, true
		}
	}
	return "", false
}

// Question is a case without its answer: what both sides are asked.
type Question struct {
	ID          string    `json:"id"`
	Operation   string    `json:"operation"`
	Counterpart string    `json:"counterpart"`
	Request     string    `json:"request"`
	Key         Key       `json:"key"`
	Now         time.Time `json:"now"`
}

// Questions are the parity questions and the fixture rows they are asked on.
// Both producers read them from one file (test/parity/questions.json): the
// API's recorder and the MCP side's producer.
type Questions struct {
	Fixture Fixture    `json:"fixture"`
	Cases   []Question `json:"cases"`
}

// QuestionsFile is the name of the questions file in a parity directory.
const QuestionsFile = "questions.json"

// LoadQuestions reads the questions file of fsys; it fails closed on an
// empty or malformed file.
func LoadQuestions(fsys fs.FS) (Questions, error) {
	raw, err := fs.ReadFile(fsys, QuestionsFile)
	if err != nil {
		return Questions{}, err
	}
	var q Questions
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		return Questions{}, fmt.Errorf("%w: %s: %w", ErrMalformed, QuestionsFile, err)
	}
	if len(q.Cases) == 0 {
		return Questions{}, fmt.Errorf("%w: %s", ErrEmpty, QuestionsFile)
	}
	seen := map[string]bool{}
	for _, c := range q.Cases {
		if c.ID == "" || seen[c.ID] || c.Operation == "" || c.Request == "" || c.Now.IsZero() {
			return Questions{}, fmt.Errorf("%w: %s: question %q needs a unique id, an operation, a request and a now", ErrMalformed, QuestionsFile, c.ID)
		}
		seen[c.ID] = true
	}
	return q, nil
}

// QuestionOf is the question a case answers.
func QuestionOf(c Case) Question {
	return Question{ID: c.ID, Operation: c.Operation, Counterpart: c.Counterpart, Request: c.Request, Key: c.Key, Now: c.Now}
}

// Answers states every way in which b does not answer exactly q: a question
// it leaves out, one it adds, one it asks differently, or other fixture rows.
// None means the bundle answers the questions.
func Answers(b Bundle, q Questions) []string {
	var out []string
	if fx := b.Fixture; fx != q.Fixture {
		out = append(out, fmt.Sprintf("fixture: the bundle was answered on %+v, the questions are asked on %+v", fx, q.Fixture))
	}
	asked := map[string]Question{}
	for _, c := range q.Cases {
		asked[c.ID] = c
	}
	answered := map[string]bool{}
	for _, c := range b.Cases {
		answered[c.ID] = true
		want, ok := asked[c.ID]
		switch {
		case !ok:
			out = append(out, "case "+c.ID+": not a question")
		case !sameQuestion(QuestionOf(c), want):
			out = append(out, "case "+c.ID+": asked differently from the question")
		}
	}
	for _, c := range q.Cases {
		if !answered[c.ID] {
			out = append(out, "question "+c.ID+": no case answers it")
		}
	}
	sort.Strings(out)
	return out
}

func sameQuestion(a, b Question) bool {
	ja := invariant.Must(json.Marshal(a)) // a question always encodes
	jb := invariant.Must(json.Marshal(b))
	return bytes.Equal(ja, jb)
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

// Project turns the response body of a case asking request into its facts
// under r, sorted by name. Two values under one name are an error: a
// comparison keeping either would hide a difference in the other. A note of
// a kind in r.OwnedNotes is stated by the families owners names for its text;
// a note owners names no family for is an error (ErrUnownedNote).
func Project(body []byte, r Rules, request string, owners Owners) ([]Fact, error) {
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
		v, ok := meta[m]
		if !ok {
			continue
		}
		if notes, isMap := v.(map[string]any); m == "notes" && isMap {
			if err := p.notes(notes, owners); err != nil {
				return nil, err
			}
			continue
		}
		p.walk("meta."+m, m, v)
	}
	sort.Slice(p.out, func(i, j int) bool { return p.out[i].Name < p.out[j].Name })
	if n, ok := repeated(p.out); ok {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateFact, n)
	}
	kept := p.out[:0]
	for _, f := range p.out {
		if !r.Excludes(request, f.Name) {
			kept = append(kept, f)
		}
	}
	return kept, nil
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

// notes states meta.notes: an owned kind per family as a count, every other
// member as walk states it.
func (p *projector) notes(notes map[string]any, owners Owners) error {
	if len(notes) == 0 {
		p.add("meta.notes", "{}")
	}
	for k, v := range notes {
		path := "meta.notes." + k
		if !slices.Contains(p.r.OwnedNotes, k) {
			p.walk(path, k, v)
			continue
		}
		xs, isList := v.([]any)
		texts, ok := stringList(xs)
		if !isList || !ok {
			return fmt.Errorf("%w: %s is not a list of texts", ErrMalformed, path)
		}
		count := map[string]map[string]bool{}
		for _, text := range texts {
			fams := owners[text]
			if len(fams) == 0 {
				return fmt.Errorf("%w: %s, %d character(s)", ErrUnownedNote, path, len(text))
			}
			for _, f := range fams {
				f = normalise(f)
				if count[f] == nil {
					count[f] = map[string]bool{}
				}
				count[f][text] = true
			}
		}
		if len(count) == 0 {
			p.add(path, "[]")
		}
		for f, ts := range count {
			p.add(path+"["+f+"]", strconv.Itoa(len(ts)))
		}
	}
	return nil
}

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
