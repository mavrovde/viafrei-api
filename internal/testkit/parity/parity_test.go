package parity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

const good = `{"format":"viafrei.parity/2","platform":"1.2.3","producer":"api","rules":"viafrei.parity.rules/2",
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
		"other rules":    strings.Replace(good, "rules/2", "rules/1", 1),
		"no facts":       strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[]`, 1),
		"no id":          strings.Replace(good, `"id":"c1"`, `"id":""`, 1),
		"no operation":   strings.Replace(good, `"operation":"listX"`, `"operation":""`, 1),
		"no request":     strings.Replace(good, `"request":"/v1/x"`, `"request":""`, 1),
		"no producer":    strings.Replace(good, `"producer":"api",`, ``, 1),
		"other producer": strings.Replace(good, `"producer":"api"`, `"producer":"platform"`, 1),
		"repeated fact":  strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},{"name":"data[a].id","value":"b"}]`, 1),
		"duplicate id":   strings.Replace(good, `]}]}`, `]},`+good[strings.Index(good, `{"id"`):len(good)-2]+`]}`, 1),
	}
	for name, raw := range cases {
		if _, err := parity.Parse([]byte(raw), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
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
	got, err := parity.Project([]byte(body), parity.Default)
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
		{"meta.attribution[src-a].text", "T"},
		{"meta.notes.condition", `["Use only after accepting."]`},
		{"meta.warnings[partial].code", "partial"},
	}
	if d := parity.Diff(want, got); len(d) > 0 {
		t.Errorf("projection differs:\n%s", strings.Join(d, "\n"))
	}
}

func TestProjectRefusesTwoValuesUnderOneName(t *testing.T) {
	// Two elements with one key, after normalisation: either value kept
	// alone would hide a difference in the other.
	_, err := parity.Project([]byte(`{"data":[{"id":"a","n":1},{"id":" A ","n":2}]}`), parity.Default)
	if !errors.Is(err, parity.ErrDuplicateFact) || !strings.Contains(err.Error(), "data[a].id") {
		t.Errorf("duplicate names projected: %v", err)
	}
}

func TestProjectStatesAListAsAJSONArray(t *testing.T) {
	// A value holding the old separator stays one value.
	got, err := parity.Project([]byte(`{"data":{"tags":["b | c","a"]}}`), parity.Default)
	if err != nil || len(got) != 1 || got[0].Value != `["a","b | c"]` {
		t.Errorf("%v, %v", got, err)
	}
}

func TestProjectKeepsAnOutOfRangeNumberVerbatim(t *testing.T) {
	got, err := parity.Project([]byte(`{"data":{"n":1e999}}`), parity.Default)
	if err != nil || len(got) != 1 || got[0].Value != "1e999" {
		t.Errorf("%v, %v", got, err)
	}
	if _, err := parity.Project([]byte(`[`), parity.Default); err == nil {
		t.Error("an unreadable body was projected")
	}
}

func TestAPlantedRuleChangeNamesTheFact(t *testing.T) {
	want, _ := parity.Project([]byte(body), parity.Default)
	planted := parity.Default
	planted.PositionDecimals = 4
	got, _ := parity.Project([]byte(body), planted)
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
