package parity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

// owners names the family of the projection body's one condition note.
var owners = parity.Owners{"Use only after accepting.": {" SRC-A "}}

func notes(t *testing.T, notes string, o parity.Owners) []parity.Fact {
	t.Helper()
	got, err := parity.Project([]byte(`{"meta":{"notes":`+notes+`}}`), parity.Default, "/v1/x", o)
	if err != nil {
		t.Fatal(err)
	}
	// The body has no data member; only the notes are under test.
	return slices.DeleteFunc(got, func(f parity.Fact) bool { return f.Name == "data" })
}

func TestProjectStatesConditionNotesByFamilyAndCountNotText(t *testing.T) {
	o := parity.Owners{"a1": {"fam-a"}, "a2": {"fam-a"}, "b1": {"fam-b"}, "shared": {"fam-a", "fam-c"}, "P": {"fam-p"}}
	got := notes(t, `{"condition":["a1","b1","a2","shared","a1"],"purpose":["P"]}`, o)
	want := []parity.Fact{
		{"meta.notes.condition[fam-a]", "3"},
		{"meta.notes.condition[fam-b]", "1"},
		{"meta.notes.condition[fam-c]", "1"},
		{"meta.notes.purpose[fam-p]", "1"},
	}
	if d := parity.Diff(want, got); len(d) > 0 {
		t.Errorf("notes projected:\n%s", strings.Join(d, "\n"))
	}
	if d := parity.Diff([]parity.Fact{{"meta.notes.condition", "[]"}, {"meta.notes.purpose", "[]"}}, notes(t, `{"condition":[],"purpose":[]}`, nil)); len(d) > 0 {
		t.Errorf("no notes: %q", d)
	}
	if d := parity.Diff([]parity.Fact{{"meta.notes", "{}"}}, notes(t, `{}`, nil)); len(d) > 0 {
		t.Errorf("an empty notes member: %q", d)
	}
}

func TestNoteTextIsNotComparedButFamilyAndCountAre(t *testing.T) {
	// The bundle states the MCP side's note by its family; the texts on the
	// two sides differ, and that alone is allowed.
	want := notes(t, `{"condition":["the platform's own text"]}`, parity.Owners{"the platform's own text": {"fam-a"}})
	o := parity.Owners{"synthetic": {"fam-a"}, "second": {"fam-a"}, "other": {"fam-b"}}
	if d := parity.Diff(want, notes(t, `{"condition":["synthetic"]}`, o)); len(d) > 0 {
		t.Errorf("another text of the same family's one note differs: %q", d)
	}
	cases := map[string]struct {
		notes string
		diff  []string
	}{
		"a missing note":  {`{"condition":[]}`, []string{`extra   meta.notes.condition: got "[]"`, `missing meta.notes.condition[fam-a]: want "1"`}},
		"an extra note":   {`{"condition":["synthetic","second"]}`, []string{`changed meta.notes.condition[fam-a]: want "1", got "2"`}},
		"another family":  {`{"condition":["other"]}`, []string{`missing meta.notes.condition[fam-a]: want "1"`, `extra   meta.notes.condition[fam-b]: got "1"`}},
		"a second family": {`{"condition":["synthetic","other"]}`, []string{`extra   meta.notes.condition[fam-b]: got "1"`}},
	}
	for name, c := range cases {
		if d := parity.Diff(want, notes(t, c.notes, o)); !slices.Equal(d, c.diff) {
			t.Errorf("%s: diff %q, want %q", name, d, c.diff)
		}
	}
}

func TestProjectRefusesANoteNoFamilyOwns(t *testing.T) {
	_, err := parity.Project([]byte(`{"meta":{"notes":{"condition":["secret text"]}}}`), parity.Default, "/v1/x", parity.Owners{"other": {"f"}})
	if !errors.Is(err, parity.ErrUnownedNote) || strings.Contains(err.Error(), "secret") {
		t.Errorf("an unowned note: %v", err)
	}
	for _, v := range []string{`"text"`, `null`, `[1]`} {
		if _, err := parity.Project([]byte(`{"meta":{"notes":{"condition":`+v+`}}}`), parity.Default, "/v1/x", nil); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("condition notes %s: %v", v, err)
		}
	}
}

func TestParseStatesConditionNotesOnlyByFamily(t *testing.T) {
	with := func(fact string) string {
		return strings.Replace(good, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},`+fact+`]`, 1)
	}
	for _, ok := range []string{
		`{"name":"meta.notes.condition","value":"[]"}`,
		`{"name":"meta.notes.condition[fam-a]","value":"2"}`,
		`{"name":"meta.notes.purpose[fam-p]","value":"1"}`,
		`{"name":"meta.notes.purpose","value":"[]"}`,
	} {
		if _, err := parity.Parse([]byte(with(ok)), "1.2.3"); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`{"name":"meta.notes.condition","value":"[\"a text\"]"}`,
		`{"name":"meta.notes.purpose","value":"[\"P\"]"}`,
		`{"name":"meta.notes.condition[fam-a]","value":"0"}`,
		`{"name":"meta.notes.condition[fam-a]","value":"a text"}`,
		`{"name":"meta.notes.condition[FAM-A]","value":"1"}`,
		`{"name":"meta.notes.condition[]","value":"1"}`,
		`{"name":"meta.notes.condition[fam-a].text","value":"1"}`,
		`{"name":"meta.notes.condition[a][b]","value":"1"}`,
		`{"name":"meta.notes.condition","value":"[]"},{"name":"meta.notes.condition[fam-a]","value":"1"}`,
	} {
		if _, err := parity.Parse([]byte(with(bad)), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// TestARules4BundleIsReadOnlyWithoutAPurposeNote: rules/4 compared purpose
// notes by text; a bundle of it is read under rules/5 only when it states
// none, which the two rule sets state alike.
func TestARules4BundleIsReadOnlyWithoutAPurposeNote(t *testing.T) {
	old := strings.Replace(good, "viafrei.parity.rules/5", "viafrei.parity.rules/4", 1)
	with := func(fact string) string {
		return strings.Replace(old, `[{"name":"data[a].id","value":"a"}]`, `[{"name":"data[a].id","value":"a"},`+fact+`]`, 1)
	}
	if _, err := parity.Parse([]byte(with(`{"name":"meta.notes.purpose","value":"[]"}`)), "1.2.3"); err != nil {
		t.Errorf("a rules/4 bundle with no purpose note: %v", err)
	}
	for _, bad := range []string{`{"name":"meta.notes.purpose","value":"[\"P\"]"}`, `{"name":"meta.notes.purpose[fam-p]","value":"1"}`} {
		if _, err := parity.Parse([]byte(with(bad)), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("a rules/4 bundle stating %s: %v", bad, err)
		}
	}
	if _, err := parity.Parse([]byte(strings.Replace(good, "viafrei.parity.rules/5", "viafrei.parity.rules/3", 1)), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
		t.Errorf("a rules/3 bundle: %v", err)
	}
}

// TestASharedNoteIsTheCreditedFamilys: a text two families own is stated as
// the note of the family the answer credits.
func TestASharedNoteIsTheCreditedFamilys(t *testing.T) {
	o := parity.Owners{"shared": {"fam-a", "fam-b"}}
	body := `{"meta":{"attribution":[{"family":"fam-a"}],"notes":{"purpose":["shared"],"condition":[]}}}`
	got, err := parity.Project([]byte(body), parity.Default, "/v1/x", o)
	if err != nil {
		t.Fatal(err)
	}
	want := []parity.Fact{{"data", "null"}, {"meta.attribution[fam-a].family", "fam-a"}, {"meta.notes.condition", "[]"}, {"meta.notes.purpose[fam-a]", "1"}}
	if d := parity.Diff(want, got); len(d) > 0 {
		t.Errorf("a shared note: %q", d)
	}
	if _, err := parity.Project([]byte(`{"meta":{"attribution":[{"family":"fam-c"}],"notes":{"purpose":["shared"]}}}`), parity.Default, "/v1/x", o); !errors.Is(err, parity.ErrUnownedNote) {
		t.Errorf("a note no credited family owns: %v", err)
	}
}
