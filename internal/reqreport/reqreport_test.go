package reqreport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const catalogueTSV = "# header comment\nid\tkeyword\tmethod\tlevel\tpage\n" +
	"AAA-001\tMUST\tUnit\tComponent\tSpec-A\n" +
	"AAA-002\tMUST\tHandler\tComponent integration\tSpec-A\n" +
	"AAA-003\tSHOULD\tUnit\tComponent\tSpec-A\n" +
	"AAA-004\tMUST\tReview\tStatic\tSpec-A\n"

func catalogue(t *testing.T) map[string]Requirement {
	t.Helper()
	c, err := ReadCatalogue(strings.NewReader(catalogueTSV))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// line builds one `go test -json` event.
func line(action, test, output string, elapsed float64) string {
	b := `{"Action":"` + action + `","Package":"example.com/m/internal/p"`
	if test != "" {
		b += `,"Test":"` + test + `"`
	}
	if output != "" {
		b += `,"Output":"` + output + `"`
	}
	if action == "pass" || action == "fail" || action == "skip" {
		b += `,"Elapsed":` + strconv.FormatFloat(elapsed, 'f', -1, 64)
	}
	return b + "}\n"
}

func results(t *testing.T, stream string) []TestResult {
	t.Helper()
	ts, err := ReadResults("unit", strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func hasFinding(r *Report, part string) bool {
	for _, f := range r.Findings {
		if strings.Contains(f, part) {
			return true
		}
	}
	return false
}

func TestCoveredScopePasses(t *testing.T) {
	req.Covers(t, "TEST-002")
	ts := results(t, line("run", "TestA", "", 0)+
		line("output", "TestA", "    a_test.go:9: REQ-ID: AAA-001\\n", 0)+
		line("output", "TestA", "    a_test.go:9: REQ-ID: AAA-002\\n", 0)+
		line("pass", "TestA", "", 0.01)+
		line("pass", "", "", 0.02))
	r := Check(catalogue(t), []string{"AAA-001", "AAA-002"}, 1, ts)
	if r.Failed() {
		t.Fatalf("a covered scope failed: %v", r.Findings)
	}
	if len(r.Covering["AAA-001"]) != 1 || len(r.Covering["AAA-002"]) != 1 {
		t.Fatalf("covering = %v", r.Covering)
	}
}

func TestInScopeWithoutPassingTestFails(t *testing.T) {
	req.Covers(t, "TEST-002")
	ts := results(t, line("output", "TestA", "REQ-ID: AAA-001\\n", 0)+
		line("fail", "TestA", "", 0.01)+
		line("output", "TestB", "REQ-ID: AAA-001\\n", 0)+
		line("skip", "TestB", "", 0))
	r := Check(catalogue(t), []string{"AAA-001", "AAA-002"}, 1, ts)
	for _, want := range []string{"AAA-001 has no passing test", "AAA-002 has no passing test", "failed: unit internal/p.TestA"} {
		if !hasFinding(r, want) {
			t.Errorf("no finding %q in %v", want, r.Findings)
		}
	}
}

func TestUnknownIDFails(t *testing.T) {
	req.Covers(t, "TEST-001")
	ts := results(t, line("output", "TestA", "REQ-ID: AAA-001 ZZZ-999\\n", 0)+line("pass", "TestA", "", 0))
	r := Check(catalogue(t), []string{"AAA-001"}, 1, ts)
	if !hasFinding(r, "unknown requirement ZZZ-999") {
		t.Fatalf("an ID outside the catalogue passed: %v", r.Findings)
	}
}

func TestScopeHoldsOnlyAutomatedMustRows(t *testing.T) {
	req.Covers(t, "TEST-002")
	ts := results(t, line("output", "TestA", "REQ-ID: AAA-003 AAA-004\\n", 0)+line("pass", "TestA", "", 0))
	r := Check(catalogue(t), []string{"AAA-003", "AAA-004", "BBB-001"}, 1, ts)
	for _, want := range []string{"scope lists AAA-003", "scope lists AAA-004", "BBB-001, which the catalogue does not hold"} {
		if !hasFinding(r, want) {
			t.Errorf("no finding %q in %v", want, r.Findings)
		}
	}
}

func TestOnlyLeafTestsAreBounded(t *testing.T) {
	req.Covers(t, "TST-012")
	// The parent's elapsed time is the sum of its subtests; only a leaf
	// over the bound is a finding.
	ts := results(t, line("pass", "TestP/a", "", 1.5)+
		line("pass", "TestP/b", "", 1.5)+
		line("pass", "TestP", "", 3.0)+
		line("pass", "TestSlow", "", 2.5))
	r := Check(catalogue(t), []string{"AAA-001"}, 1, ts)
	if !hasFinding(r, "unit internal/p.TestSlow took 2.5s") {
		t.Errorf("a 2.5 s leaf passed: %v", r.Findings)
	}
	if hasFinding(r, "TestP took") {
		t.Errorf("a parent was held to the leaf bound: %v", r.Findings)
	}
}

func TestEmptyInputsAreErrors(t *testing.T) {
	req.Covers(t, "TEST-002")
	if _, err := ReadCatalogue(strings.NewReader("# only a comment\nid\tkeyword\tmethod\tlevel\tpage\n")); err == nil {
		t.Error("an empty catalogue was read")
	}
	if _, err := ReadCatalogue(strings.NewReader("AAA-001\tMUST\tUnit\tComponent\tP\nAAA-001\tMUST\tUnit\tComponent\tP\n")); err == nil {
		t.Error("a duplicate ID was read")
	}
	if _, err := ReadCatalogue(strings.NewReader("AAA-001\tMUST\n")); err == nil {
		t.Error("a short row was read")
	}
	if _, err := ReadScope(strings.NewReader("# nothing\n\n")); err == nil {
		t.Error("an empty scope was read")
	}
	if _, err := ReadScope(strings.NewReader("aaa-1\n")); err == nil {
		t.Error("a malformed scope ID was read")
	}
	if _, err := ReadResults("unit", strings.NewReader(line("start", "", "", 0))); err == nil {
		t.Error("a result stream with no test was read")
	}
	if _, err := ReadResults("unit", strings.NewReader("ok  \tpkg\t0.1s\n")); err == nil {
		t.Error("plain text was read as test JSON")
	}
}

func TestFromWikiReadsMatrixAndKeywords(t *testing.T) {
	req.Covers(t, "TEST-001")
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(matrixPage, "# Matrix\n\nSource: [[Spec A|Spec-A]]\n\n| ID | Section | Method | Test level |\n|---|---|---|---|\n"+
		"| AAA-001 | S | Unit | Component | x | y | pending |\n| AAA-002 | S | Review | Static | x | y | pending |\n"+
		"Source: [[Spec B|Spec-B]]\n| BBB-001 | S | Lint | Static | x | y | pending |\n")
	write("Spec-A.md", "| AAA-001 | The API MUST answer; it MAY cache. | Unit |\n| AAA-002 | It SHOULD log. | Review |\n")
	write("Spec-B.md", "| BBB-001 | Plain prose without a keyword. | Lint |\n")
	reqs, err := FromWiki(dir)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteCatalogue(&buf, reqs); err != nil {
		t.Fatal(err)
	}
	cat, err := ReadCatalogue(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Requirement{
		"AAA-001": {ID: "AAA-001", Keyword: "MUST", Method: "Unit", Level: "Component", Page: "Spec-A"},
		"AAA-002": {ID: "AAA-002", Keyword: "SHOULD", Method: "Review", Level: "Static", Page: "Spec-A"},
		"BBB-001": {ID: "BBB-001", Keyword: "-", Method: "Lint", Level: "Static", Page: "Spec-B"},
	}
	if len(cat) != len(want) {
		t.Fatalf("read %d rows, want %d: %v", len(cat), len(want), cat)
	}
	for id, w := range want {
		if cat[id] != w {
			t.Errorf("%s = %+v, want %+v", id, cat[id], w)
		}
	}
	if !cat["AAA-001"].Automated() || cat["AAA-002"].Automated() {
		t.Error("Automated() does not follow the method")
	}
}

func TestReportsCarryTheRun(t *testing.T) {
	req.Covers(t, "TEST-002")
	ts := results(t, line("output", "TestA", "REQ-ID: AAA-001\\n", 0)+line("pass", "TestA", "", 0.25))
	r := Check(catalogue(t), []string{"AAA-001", "AAA-002"}, 1, ts)
	m := Meta{Version: "9.9.9", Commit: "abc123", Generated: "2026-11-02T00:00:00Z"}
	var rep, comp bytes.Buffer
	if err := WriteRequirementReport(&rep, r, m); err != nil {
		t.Fatal(err)
	}
	if err := WriteCompletionDraft(&comp, r, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Requirement report: 9.9.9", "`abc123`", "| AAA-001 | Unit | 1 | yes |", "| AAA-002 | Handler | 0 | **no** |", "`internal/p.TestA` | unit | pass | 0.25 s |"} {
		if !strings.Contains(rep.String(), want) {
			t.Errorf("requirement report lacks %q:\n%s", want, rep.String())
		}
	}
	for _, want := range []string{"## 1. Summary", "Verdict from the automated gates: not ready", "| unit | 1 | 1 | 0 | 0 | 0 |", "1 of 2", "## 10. Approval"} {
		if !strings.Contains(comp.String(), want) {
			t.Errorf("completion draft lacks %q:\n%s", want, comp.String())
		}
	}
}

func TestFromWikiRefusesABrokenWiki(t *testing.T) {
	req.Covers(t, "TEST-001")
	const row = "| AAA-001 | S | Unit | Component | x |\n"
	for want, pages := range map[string]map[string]string{
		"no such file":               {},
		"a row with 2 cells":         {matrixPage: "Source: [[A|Spec-A]]\n| AAA-001 | S |\n"},
		"no requirement row read":    {matrixPage: "# Matrix without rows\n"},
		"AAA-001 has no Source line": {matrixPage: row},
		"Spec-A.md":                  {matrixPage: "Source: [[A|Spec-A]]\n" + row},
	} {
		dir := t.TempDir()
		for name, body := range pages {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := FromWiki(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FromWiki = %v, want an error about %q", err, want)
		}
	}
}

func TestUnreadableStreamsAreErrors(t *testing.T) {
	req.Covers(t, "TEST-002")
	if _, err := ReadCatalogue(iotest.ErrReader(errors.New("catalogue gone"))); err == nil || !strings.Contains(err.Error(), "catalogue gone") {
		t.Errorf("ReadCatalogue = %v", err)
	}
	if _, err := ReadScope(iotest.ErrReader(errors.New("scope gone"))); err == nil || !strings.Contains(err.Error(), "scope gone") {
		t.Errorf("ReadScope = %v", err)
	}
	if _, err := ReadResults("unit", iotest.ErrReader(errors.New("results gone"))); err == nil || !strings.Contains(err.Error(), "results gone") {
		t.Errorf("ReadResults = %v", err)
	}
}

// TestACleanRunReportsNoneAndSortsTests: blank lines in a stream are skipped,
// tests print in key order whatever order they ran in, a command package
// keeps its cmd/ prefix, and a clean run's completion draft says "none".
func TestACleanRunReportsNoneAndSortsTests(t *testing.T) {
	req.Covers(t, "TEST-002")
	cmd := `{"Action":"output","Package":"example.com/m/cmd/tool","Test":"TestZ","Output":"REQ-ID: AAA-001\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/m/cmd/tool","Test":"TestZ","Elapsed":0.1}` + "\n"
	ts := results(t, line("output", "TestB", "REQ-ID: AAA-001\\n", 0)+"\n"+line("pass", "TestB", "", 0.1)+cmd+
		line("output", "TestA", "REQ-ID: AAA-001\\n", 0)+line("pass", "TestA", "", 0.1))
	r := Check(catalogue(t), []string{"AAA-001"}, 1, ts)
	var rep, comp bytes.Buffer
	if err := WriteRequirementReport(&rep, r, Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCompletionDraft(&comp, r, Meta{}); err != nil {
		t.Fatal(err)
	}
	a, b, z := strings.Index(rep.String(), "internal/p.TestA"), strings.Index(rep.String(), "internal/p.TestB"), strings.Index(rep.String(), "`cmd/tool.TestZ`")
	if a < 0 || z < 0 || z >= a || a >= b {
		t.Errorf("tests not in key order (cmd/tool.TestZ %d, TestA %d, TestB %d):\n%s", z, a, b, rep.String())
	}
	if !strings.Contains(comp.String(), "Rows without a passing test, with reason: none") ||
		!strings.Contains(comp.String(), "Findings of the automated gates: none") {
		t.Errorf("a clean run's draft does not say none:\n%s", comp.String())
	}
}

func TestScopeReadsIDsOnceWithComments(t *testing.T) {
	req.Covers(t, "TEST-002")
	got, err := ReadScope(strings.NewReader("# milestone\nAAA-001 # why\n\nAAA-002\nAAA-001\n"))
	if err != nil || strings.Join(got, ",") != "AAA-001,AAA-002" {
		t.Fatalf("ReadScope = %v, %v", got, err)
	}
	if k := (TestResult{Level: "unit", Package: "example.com/m", Name: "TestRoot"}).Key(); k != "unit example.com/m.TestRoot" {
		t.Errorf("a root package's key = %q", k)
	}
}
