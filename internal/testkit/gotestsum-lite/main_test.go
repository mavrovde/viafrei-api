package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestSummaryVerdicts(t *testing.T) {
	req.Covers(t, "TST-011")
	const (
		ok    = `{"Action":"pass","Package":"p","Test":"TestA"}`
		tfail = `{"Action":"output","Package":"p","Test":"TestB","Output":"boom\n"}` + "\n" + `{"Action":"fail","Package":"p","Test":"TestB"}`
		pfail = `{"Action":"output","Package":"q","Output":"build failed\n"}` + "\n" + `{"Action":"fail","Package":"q"}`
		skip  = `{"Action":"skip","Package":"p","Test":"TestC"}`
	)
	for _, c := range []struct {
		name, in, want string
		code           int
	}{
		{"a passing run", ok + "\n\n" + skip, "1 passed, 0 failed, 1 skipped", 0},
		{"a failing test", ok + "\n" + tfail, "--- FAIL p TestB\nboom", 1},
		{"a package that did not build", ok + "\n" + pfail, "--- FAIL package q\nbuild failed", 1},
		{"nothing ran", "", "a run that executed nothing is not a pass", 1},
		{"only skips", skip, "a run that executed nothing is not a pass", 1},
		{"a line that is not JSON", ok + "\nFAIL plain text", "not test JSON: FAIL plain text", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			code := run(strings.NewReader(c.in), &out)
			if code != c.code || !strings.Contains(out.String(), c.want) {
				t.Errorf("exit %d (want %d), output %q, want it to contain %q", code, c.code, out.String(), c.want)
			}
		})
	}
}

func TestAnUnreadableStreamFails(t *testing.T) {
	req.Covers(t, "TST-011")
	var out bytes.Buffer
	if code := run(iotest.ErrReader(errors.New("pipe closed")), &out); code != 1 || !strings.Contains(out.String(), "reading the test stream: pipe closed") {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}
