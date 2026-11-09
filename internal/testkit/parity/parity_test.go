package parity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

const good = `{"format":"viafrei.parity/2","platform":"1.2.3","producer":"api","rules":"viafrei.parity.rules/4",
"fixture":{"profile":"tiny","seed":1,"sha256":"ab"},
"cases":[{"id":"c1","operation":"listX","counterpart":"x search","request":"/v1/x","key":{"useClass":"consumer","scopes":["x"],"sources":["s"],"accepted":null},
"now":"2026-03-30T00:00:00Z","facts":[{"name":"data[a].id","value":"a"}]}]}`

func lockFor(platform, path, body string) fstest.MapFS {
	return fstest.MapFS{
		parity.LockFile: {Data: []byte("# comment\n\n" + platform + "\t" + path + "\t" + parity.Checksum([]byte(body)) + "\n")},
		path:            {Data: []byte(body)},
	}
}

func TestLoadVerifiesTheChecksumBeforeUse(t *testing.T) {
	b, err := parity.Load(lockFor("1.2.3", "b.json", good), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Cases) != 1 || b.Cases[0].Key.Sources[0] != "s" || b.Fixture.Seed != 1 {
		t.Errorf("loaded %+v", b)
	}

	tampered := lockFor("1.2.3", "b.json", good)
	tampered["b.json"] = &fstest.MapFile{Data: []byte(strings.Replace(good, `"value":"a"`, `"value":"b"`, 1))}
	if _, err := parity.Load(tampered, "1.2.3"); !errors.Is(err, parity.ErrChecksum) || !strings.Contains(err.Error(), "b.json") {
		t.Errorf("a changed bundle was used: %v", err)
	}
}

func TestLoadFailsClosed(t *testing.T) {
	missing := lockFor("1.2.3", "b.json", good)
	delete(missing, "b.json")
	cases := []struct {
		name string
		fsys fstest.MapFS
		want error
	}{
		{"no lock file", fstest.MapFS{}, nil},
		{"another version pinned", lockFor("1.2.2", "b.json", good), parity.ErrNotPinned},
		{"malformed lock line", fstest.MapFS{parity.LockFile: {Data: []byte("1.2.3\tb.json\n")}}, parity.ErrMalformed},
		{"pinned file absent", missing, nil},
		{"empty bundle", lockFor("1.2.3", "b.json", strings.Replace(good, good[strings.Index(good, `"cases"`):], `"cases":[]}`, 1)), parity.ErrEmpty},
	}
	for _, c := range cases {
		_, err := parity.Load(c.fsys, "1.2.3")
		if err == nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestParseRefusesWhatItCannotCompare(t *testing.T) {
	cases := map[string]string{
		"not json":       `{`,
		"unknown member": strings.Replace(good, `"rules"`, `"extra":1,"rules"`, 1),
		"other format":   strings.Replace(good, "viafrei.parity/2", "viafrei.parity/1", 1),
		"other platform": strings.Replace(good, `"1.2.3"`, `"1.2.4"`, 1),
		"other rules":    strings.Replace(good, "rules/4", "rules/3", 1),
		"no facts":       strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[]`, 1),
		"no id":          strings.Replace(good, `"id":"c1"`, `"id":""`, 1),
		"no operation":   strings.Replace(good, `"operation":"listX"`, `"operation":""`, 1),
		"no request":     strings.Replace(good, `"request":"/v1/x"`, `"request":""`, 1),
		"no producer":    strings.Replace(good, `"producer":"api",`, ``, 1),
		"other producer": strings.Replace(good, `"producer":"api"`, `"producer":"platform"`, 1),
		"excluded fact":  strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},{"name":"meta.asOf","value":"2026-03-30T00:00:00Z"}]`, 1),
		"hasMore under a power": strings.Replace(strings.Replace(good, `"request":"/v1/x"`, `"request":"/v1/x?min_kw=50"`, 1),
			`[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},{"name":"meta.page.hasMore","value":"false"}]`, 1),
		"repeated fact": strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},{"name":"data[a].id","value":"b"}]`, 1),
		"duplicate id":  strings.Replace(good, `]}]}`, `]},`+good[strings.Index(good, `{"id"`):len(good)-2]+`]}`, 1),
	}
	for name, raw := range cases {
		if _, err := parity.Parse([]byte(raw), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseKeepsHasMoreWhereNoPowerIsAsked(t *testing.T) {
	raw := strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},{"name":"meta.page.hasMore","value":"false"}]`, 1)
	if _, err := parity.Parse([]byte(raw), "1.2.3"); err != nil {
		t.Errorf("hasMore refused in a case asking no power: %v", err)
	}
}

func TestInventoryNamesOperationsWithoutACase(t *testing.T) {
	b, err := parity.Parse([]byte(good), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"paths":{"/v1/x":{"get":{"operationId":"listX","x-mcp-counterpart":"x search"}},
"/v1/y":{"get":{"operationId":"listY","x-mcp-counterpart":"y search"}},
"/v1/z":{"get":{"operationId":"getZ"}}}}`
	missing, err := parity.Inventory([]byte(doc), b)
	if err != nil || !slices.Equal(missing, []string{"listY: no case"}) {
		t.Errorf("missing %v, %v", missing, err)
	}
	renamed := strings.Replace(doc, `"x-mcp-counterpart":"x search"`, `"x-mcp-counterpart":"x finder"`, 1)
	got, err := parity.Inventory([]byte(renamed), b)
	if err != nil || !slices.Equal(got, []string{`case c1: counterpart "x search", listX declares "x finder"`, "listY: no case"}) {
		t.Errorf("a case asking another counterpart: %v, %v", got, err)
	}
	if _, err := parity.Inventory([]byte(`[`), b); err == nil {
		t.Error("an unreadable document passed the inventory")
	}
}

const body = `{"data":[
 {"id":" CS-1 ","sourceFamily":"SRC-A","location":{"lat":48.4045249,"lon":7.659936},"power":22.50,"seen":"2026-03-29T23:11:57+02:00",
  "points":[{"id":"p2","free":true},{"id":"p1","free":null}],"tags":["b","a"],"empty":[],"none":{},"plain":[1,2]}],
"meta":{"requestId":"r","attribution":[{"family":"src-a","text":"T"}],"warnings":[{"code":"partial"}],"notes":{"condition":["Use only after accepting."]},"huge":1}}`

func TestProjectAppliesTheRules(t *testing.T) {
	got, err := parity.Project([]byte(body), parity.Default, "/v1/x", owners)
	if err != nil {
		t.Fatal(err)
	}
	want := []parity.Fact{
		{"data[cs-1].empty", "[]"},
		{"data[cs-1].id", "cs-1"},
		{"data[cs-1].location.lat", "48.40452"},
		{"data[cs-1].location.lon", "7.65994"},
		{"data[cs-1].none", "{}"},
		{"data[cs-1].plain[0]", "1"},
		{"data[cs-1].plain[1]", "2"},
		{"data[cs-1].points[p1].free", "null"},
		{"data[cs-1].points[p1].id", "p1"},
		{"data[cs-1].points[p2].free", "true"},
		{"data[cs-1].points[p2].id", "p2"},
		{"data[cs-1].power", "22.5"},
		{"data[cs-1].seen", "2026-03-29T21:11:57Z"},
		{"data[cs-1].sourceFamily", "src-a"},
		{"data[cs-1].tags", `["a","b"]`},
		{"meta.attribution[src-a].family", "src-a"},
		{"meta.notes.condition[src-a]", "1"},
	}
	if d := parity.Diff(want, got); len(d) > 0 {
		t.Errorf("projection differs:\n%s", strings.Join(d, "\n"))
	}
}

func TestProjectRefusesTwoValuesUnderOneName(t *testing.T) {
	// Two elements with one key, after normalisation: either value kept
	// alone would hide a difference in the other.
	_, err := parity.Project([]byte(`{"data":[{"id":"a","n":1},{"id":" A ","n":2}]}`), parity.Default, "/v1/x", nil)
	if !errors.Is(err, parity.ErrDuplicateFact) || !strings.Contains(err.Error(), "data[a].id") {
		t.Errorf("duplicate names projected: %v", err)
	}
}

func TestProjectStatesAListAsAJSONArray(t *testing.T) {
	// A value holding the old separator stays one value.
	got, err := parity.Project([]byte(`{"data":{"tags":["b | c","a"]}}`), parity.Default, "/v1/x", nil)
	if err != nil || len(got) != 1 || got[0].Value != `["a","b | c"]` {
		t.Errorf("%v, %v", got, err)
	}
}

func TestProjectKeepsAnOutOfRangeNumberVerbatim(t *testing.T) {
	got, err := parity.Project([]byte(`{"data":{"n":1e999}}`), parity.Default, "/v1/x", nil)
	if err != nil || len(got) != 1 || got[0].Value != "1e999" {
		t.Errorf("%v, %v", got, err)
	}
	if _, err := parity.Project([]byte(`[`), parity.Default, "/v1/x", nil); err == nil {
		t.Error("an unreadable body was projected")
	}
}

func TestAPlantedRuleChangeNamesTheFact(t *testing.T) {
	want, _ := parity.Project([]byte(body), parity.Default, "/v1/x", owners)
	planted := parity.Default
	planted.PositionDecimals = 4
	got, _ := parity.Project([]byte(body), planted, "/v1/x", owners)
	d := parity.Diff(want, got)
	if !slices.Equal(d, []string{`changed data[cs-1].location.lat: want "48.40452", got "48.4045"`, `changed data[cs-1].location.lon: want "7.65994", got "7.6599"`}) {
		t.Errorf("diff %q", d)
	}
}

func TestDiffStatesMissingExtraAndChanged(t *testing.T) {
	want := []parity.Fact{{"a", "1"}, {"b", "2"}, {"c", "3"}}
	got := []parity.Fact{{"b", "2"}, {"c", "4"}, {"d", "5"}}
	d := parity.Diff(want, got)
	if !slices.Equal(d, []string{`missing a: want "1"`, `changed c: want "3", got "4"`, `extra   d: got "5"`}) {
		t.Errorf("diff %q", d)
	}
	if d := parity.Diff(want, want); d != nil {
		t.Errorf("equal facts differ: %q", d)
	}
}

func TestExclusionsCoverANameAndWhatIsUnderItOnly(t *testing.T) {
	r := parity.Default
	const plain = "/v1/charging-options?at=1,2&limit=5"
	for _, name := range []string{
		"meta.asOf", "meta.freshness", "meta.freshness[src-a].ageS", "meta.warnings[partial].code",
		"data[cs-1].access", "data[cs-1].chargePoints[cp-1].current", "meta.attribution[src-a].text", "meta.attribution[src-a].shareAlikeOffer",
	} {
		if !r.Excludes(plain, name) {
			t.Errorf("%s is compared", name)
		}
	}
	for _, name := range []string{
		"meta.asOfX", "meta.page.limit", "meta.page.hasMore", "data[cs-1].accessible", "data[cs-1].chargePoints[cp-1].currentKw",
		"data[cs-1].chargePoints[cp-1].availability.state", "meta.attribution[src-a].family", "meta.notes.condition", "data.access",
	} {
		if r.Excludes(plain, name) {
			t.Errorf("%s is excluded", name)
		}
	}
	for _, e := range r.Excluded {
		if e.Pattern == "" || e.Reason == "" {
			t.Errorf("an exclusion without its pattern or reason: %+v", e)
		}
	}
	if (parity.Rules{}).Excludes(plain, "meta.asOf") {
		t.Error("a rule set with no exclusion excluded a fact")
	}
}

func TestHasMoreIsExcludedOnlyWhereAPowerIsAsked(t *testing.T) {
	r := parity.Default
	for request, excluded := range map[string]bool{
		"/v1/charging-options?at=1,2&min_kw=150": true,
		"/v1/charging-options?at=1,2&min_kw=x":   true, // a value that is no number is asked; the API refuses it
		"/v1/charging-options?at=1,2&min_kw=0":   false,
		"/v1/charging-options?at=1,2&min_kw=-3":  false,
		"/v1/charging-options?at=1,2":            false,
		"%zz":                                    false, // an unreadable request asks nothing
	} {
		if got := r.Excludes(request, "meta.page.hasMore"); got != excluded {
			t.Errorf("%s: hasMore excluded %v", request, got)
		}
	}
}

func TestANamedDifferenceIsReportedAsItselfAndOnlyInItsDirection(t *testing.T) {
	r := parity.Default
	const state = "data[cs-1].chargePoints[cp-1].availability.state"
	want := []parity.Fact{{state, "occupied"}, {"data[cs-1].id", "cs-1"}}
	diff, named := parity.Compare(want, []parity.Fact{{state, "reserved"}, {"data[cs-1].id", "cs-1"}}, r)
	if len(diff) != 0 || len(named) != 1 || named[0].API != "reserved" || named[0].Reason == "" {
		t.Errorf("the named difference: diff %q, named %+v", diff, named)
	}
	if u := r.Unseen(named); len(u) != 0 {
		t.Errorf("a shown difference is unseen: %+v", u)
	}
	if u := r.Unseen(nil); len(u) != len(r.Differences) {
		t.Errorf("no case shown, %d unseen", len(u))
	}
	// The other direction, another value, or a fact under another name is a plain difference.
	for name, got := range map[string][]parity.Fact{
		"reversed":     {{state, "occupied"}, {"data[cs-1].id", "cs-1"}},
		"other value":  {{state, "available"}, {"data[cs-1].id", "cs-1"}},
		"missing fact": {{"data[cs-1].id", "cs-1"}},
	} {
		w := want
		if name == "reversed" {
			w = []parity.Fact{{state, "reserved"}, {"data[cs-1].id", "cs-1"}}
		}
		if diff, named := parity.Compare(w, got, r); len(diff) != 1 || len(named) != 0 {
			t.Errorf("%s: diff %q, named %+v", name, diff, named)
		}
	}
	if diff, named := parity.Compare(want, want, r); diff != nil || named != nil {
		t.Errorf("equal facts: diff %q, named %+v", diff, named)
	}
	for _, d := range r.Differences {
		if d.Pattern == "" || d.Bundle == d.API || d.Reason == "" {
			t.Errorf("a difference without its pattern, two values or reason: %+v", d)
		}
	}
}
