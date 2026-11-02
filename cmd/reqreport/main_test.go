package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestCheckExitCodes(t *testing.T) {
	req.Covers(t, "TEST-002")
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cat := write("cat.tsv", "AAA-001\tMUST\tUnit\tComponent\tP\nAAA-002\tMUST\tUnit\tComponent\tP\n")
	ok := write("ok.txt", "AAA-001\n")
	missing := write("missing.txt", "AAA-001\nAAA-002\n")
	res := write("unit.json", `{"Action":"output","Package":"m/p","Test":"TestA","Output":"    a_test.go:9: REQ-ID: AAA-001\n"}`+"\n"+
		`{"Action":"pass","Package":"m/p","Test":"TestA","Elapsed":0.1}`+"\n")
	empty := write("empty.json", "")
	report := filepath.Join(dir, "report.md")
	wiki := t.TempDir()
	for name, body := range map[string]string{
		"Spec-Traceability-Matrix.md": "Source: [[A|Spec-A]]\n| AAA-001 | S | Unit | Component | x |\n",
		"Spec-A.md":                   "| AAA-001 | The API MUST answer. | Unit |\n",
	} {
		if err := os.WriteFile(filepath.Join(wiki, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		args []string
		want int
		out  string
	}{
		{"covered", []string{"check", "-catalogue", cat, "-scope", ok, "-results", "unit=" + res, "-report", report}, 0, "1 in scope, 1 tests from 1 result file(s), 1 requirement(s) covered: ok"},
		{"uncovered", []string{"check", "-catalogue", cat, "-scope", missing, "-results", "unit=" + res}, 1, "FAIL: in-scope requirement AAA-002 has no passing test"},
		{"no results", []string{"check", "-catalogue", cat, "-scope", ok}, 2, ""},
		{"empty results", []string{"check", "-catalogue", cat, "-scope", ok, "-results", "unit=" + empty}, 2, ""},
		{"bad results flag", []string{"check", "-catalogue", cat, "-scope", ok, "-results", res}, 2, ""},
		{"no command", nil, 2, ""},
		{"unknown command", []string{"lint"}, 2, ""},
		{"catalogue without wiki", []string{"catalogue"}, 2, ""},
		{"catalogue of an empty wiki", []string{"catalogue", "-wiki", dir}, 2, ""},
		{"catalogue of a wiki", []string{"catalogue", "-wiki", wiki}, 0, "AAA-001\tMUST\tUnit\tComponent\tSpec-A"},
		{"unknown flag", []string{"check", "-nope"}, 2, ""},
		{"missing catalogue", []string{"check", "-catalogue", filepath.Join(dir, "absent.tsv"), "-scope", ok, "-results", "unit=" + res}, 2, ""},
		{"malformed catalogue", []string{"check", "-catalogue", ok, "-scope", ok, "-results", "unit=" + res}, 2, ""},
		{"missing scope", []string{"check", "-catalogue", cat, "-scope", filepath.Join(dir, "absent.txt"), "-results", "unit=" + res}, 2, ""},
		{"malformed scope", []string{"check", "-catalogue", cat, "-scope", cat, "-results", "unit=" + res}, 2, ""},
		{"missing results", []string{"check", "-catalogue", cat, "-scope", ok, "-results", "unit=" + filepath.Join(dir, "absent.json")}, 2, ""},
		{"unwritable report", []string{"check", "-catalogue", cat, "-scope", ok, "-results", "unit=" + res, "-report", filepath.Join(dir, "no", "r.md")}, 2, ""},
		{"unwritable completion", []string{"check", "-catalogue", cat, "-scope", ok, "-results", "unit=" + res, "-completion", filepath.Join(dir, "no", "c.md")}, 2, ""},
	}
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		if got := run(c.args, &stdout, &stderr); got != c.want {
			t.Errorf("%s: exit %d, want %d (stdout %q, stderr %q)", c.name, got, c.want, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), c.out) {
			t.Errorf("%s: stdout %q lacks %q", c.name, stdout.String(), c.out)
		}
	}
	if b, err := os.ReadFile(report); err != nil || !strings.Contains(string(b), "| AAA-001 | Unit | 1 | yes |") {
		t.Errorf("report not written: %v %q", err, b)
	}
}
