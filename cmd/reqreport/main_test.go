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
	res := write("unit.json", `{"Action":"output","Package":"m/p","Test":"TestA","Output":"REQ-ID: AAA-001\n"}`+"\n"+
		`{"Action":"pass","Package":"m/p","Test":"TestA","Elapsed":0.1}`+"\n")
	empty := write("empty.json", "")
	report := filepath.Join(dir, "report.md")

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
