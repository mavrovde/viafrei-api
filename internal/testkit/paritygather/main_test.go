package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ran.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	one = `{"shard": "1/2", "statements": [
	  {"name": "a", "runs": 2, "rows": 0}, {"name": "b", "runs": 0, "rows": 0, "accept": "unrun: none"},
	  {"name": "c", "runs": 1, "rows": 0, "accept": "norows: none"}, {"name": "d", "runs": 0, "rows": 0}]}`
	two = `{"shard": "2/2", "statements": [
	  {"name": "a", "runs": 1, "rows": 3}, {"name": "b", "runs": 0, "rows": 0, "accept": "unrun: none"},
	  {"name": "c", "runs": 0, "rows": 0, "accept": "norows: none"}, {"name": "d", "runs": 4, "rows": 1}]}`
)

// The shards together prove a statement no shard alone does: a ran in
// both and compared rows in the second; d ran only in the second.
func TestTheShardsProveTogether(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{write(t, one), write(t, two)}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"parity: a ran 3 times beside its view statement, 3 rows compared", "parity: c ran 1 times", "2 shards, 4 statements, 0 not proven"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "parity: b ") {
		t.Errorf("an accepted unrun statement reported: %s", out.String())
	}
}

// A statement no shard ran, or none compared a row of, fails the set.
func TestAStatementNoShardProves(t *testing.T) {
	never := strings.Replace(two, `"name": "d", "runs": 4, "rows": 1`, `"name": "d", "runs": 0, "rows": 0`, 1)
	empty := strings.Replace(two, `"name": "d", "runs": 4, "rows": 1`, `"name": "d", "runs": 4, "rows": 0`, 1)
	for want, second := range map[string]string{"d never ran": never, "d compared 0 rows": empty} {
		var out, errb bytes.Buffer
		if code := run([]string{write(t, one), write(t, second)}, &out, &errb); code != 1 || !strings.Contains(errb.String(), want) {
			t.Errorf("%s: exit %d, %s", want, code, errb.String())
		}
	}
}

// Files that are not the shards of one set, or none, or unreadable, are a
// usage error, never a pass.
func TestShardsThatAreNotOneSet(t *testing.T) {
	cases := map[string][]string{
		"no file":       nil,
		"missing file":  {filepath.Join(t.TempDir(), "none.json")},
		"not JSON":      {write(t, "{")},
		"a shard twice": {write(t, one), write(t, one)},
		"too few":       {write(t, one)},
		"bad name":      {write(t, strings.Replace(one, `"1/2"`, `"x/2"`, 1)), write(t, two)},
		"out of range":  {write(t, strings.Replace(one, `"1/2"`, `"3/2"`, 1)), write(t, two)},
		"other lists":   {write(t, one), write(t, strings.Replace(two, `"name": "d"`, `"name": "e"`, 1))},
		"nothing":       {write(t, `{"shard": "1/1", "statements": []}`)},
	}
	for name, args := range cases {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || !strings.HasPrefix(errb.String(), prog+":") {
			t.Errorf("%s: exit %d, %q", name, code, errb.String())
		}
	}
}
