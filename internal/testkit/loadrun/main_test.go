package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// dockerScript answers the runner's docker compose calls like the test
// stack at rest.
const dockerScript = `#!/bin/sh
case "$*" in
*"state <> 'idle'"*) echo 0 ;;
*pg_stat_activity*) echo "2|0" ;;
*"ps -q api"*) echo c1 ;;
*"top c1"*) printf 'PID RSS\n7 2048\n' ;;
esac
`

type cli struct {
	dir, allow, budgets, docker, report string
	api, proxy                          *httptest.Server
	env                                 map[string]string
}

func newCLI(t *testing.T, api http.HandlerFunc) *cli {
	t.Helper()
	p := tiny
	p.soak = 400 * time.Millisecond // a forked docker is slower than the in-process fake
	profiles["test"] = p
	t.Cleanup(func() { delete(profiles, "test") })
	c := &cli{dir: t.TempDir(), env: map[string]string{"LOADRUN_KEY": "vf_test_warm", "LOADRUN_COLD_KEY": "vf_test_cold"}}
	c.allow = c.write(t, "allow.txt", "# the loopback\n\n127.0.0.1\n")
	c.budgets = c.write(t, "budgets.tsv", "# class\tp50\tp95\tp99\nlookup\t40\t120\t300\nnear\t60\t200\t500\ncomposed\t300\t800\t1500\n")
	c.docker = c.write(t, "docker", dockerScript)
	if err := os.Chmod(c.docker, 0o700); err != nil {
		t.Fatal(err)
	}
	c.report = filepath.Join(c.dir, "report.md")
	c.api = httptest.NewServer(api)
	t.Cleanup(c.api.Close)
	c.proxy = httptest.NewServer(&toxi{})
	t.Cleanup(c.proxy.Close)
	return c
}

func (c *cli) write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(c.dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (c *cli) args(extra ...string) []string {
	return append([]string{
		"-api", c.api.URL, "-proxy", c.proxy.URL + "/", "-allow", c.allow, "-budgets", c.budgets,
		"-profile", "test", "-scenarios", "soak", "-report", c.report, "-place", "pl-1",
		"-docker", c.docker, "-compose-files", "a.yaml", "-cached-api", c.api.URL,
	}, extra...)
}

func (c *cli) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return c.env[k] }, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunPassesAndWritesTheReport(t *testing.T) {
	req.Covers(t, "TST-007")
	c := newCLI(t, healthyAPI)
	code, out, errs := c.run(c.args()...)
	if code != 0 || !strings.Contains(out, "loadrun: soak:") || !strings.Contains(out, "1 scenario(s), 0 failed") {
		b, _ := os.ReadFile(c.report)
		t.Fatalf("exit %d\n%s\n%s\n%s", code, out, errs, b)
	}
	b, err := os.ReadFile(c.report)
	if err != nil || !strings.Contains(string(b), "| soak |") {
		t.Errorf("report: %v\n%s", err, b)
	}
	for _, k := range []string{"vf_test_warm", "vf_test_cold"} {
		if strings.Contains(out+errs+string(b), k) {
			t.Errorf("a key was printed")
		}
	}
}

func TestRunFailsOnAFailedAssertion(t *testing.T) {
	req.Covers(t, "TST-007")
	c := newCLI(t, brokenAPI)
	if code, out, _ := c.run(c.args()...); code != 1 || !strings.Contains(out, "1 failed") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestRunRefusesBadUsageAndForeignTargets(t *testing.T) {
	req.Covers(t, "TST-007")
	c := newCLI(t, healthyAPI)
	file := func(name, body string) string { return c.write(t, name, body) }
	cases := map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"unknown flag":        {args: []string{"-nonesuch"}, want: "flag provided but not defined"},
		"no profile":          {args: c.args("-profile", ""), want: "-profile must be"},
		"extra argument":      {args: append(c.args(), "x"), want: "unexpected arguments"},
		"no place":            {args: c.args("-place", ""), want: "-place is required"},
		"no pool":             {args: c.args("-pool-max", "0"), want: "-pool-max"},
		"no tolerance":        {args: c.args("-regression-tolerance", "0"), want: "-regression-tolerance"},
		"huge tolerance":      {args: c.args("-regression-tolerance", "11"), want: "-regression-tolerance"},
		"negative floor":      {args: c.args("-regression-floor", "-1ms"), want: "-regression-floor"},
		"report not .md":      {args: c.args("-report", filepath.Join(c.dir, "r.txt")), want: "-report must end in .md"},
		"no keys":             {args: c.args(), env: map[string]string{}, want: "two different keys"},
		"one key twice":       {args: c.args(), env: map[string]string{"LOADRUN_KEY": "k", "LOADRUN_COLD_KEY": "k"}, want: "two different keys"},
		"no allow-list":       {args: c.args("-allow", filepath.Join(c.dir, "absent")), want: "absent"},
		"empty allow-list":    {args: c.args("-allow", file("empty", "# nothing\n")), want: "allows no host"},
		"tls target":          {args: c.args("-api", "https://127.0.0.1:1"), want: "an http URL"},
		"foreign host":        {args: c.args("-api", "http://api.example.test"), want: "not on the test-environment allow-list"},
		"credentials in URL":  {args: c.args("-proxy", "http://u:p@127.0.0.1:1"), want: "an http URL"},
		"unparsable URL":      {args: c.args("-api", "http://[::1"), want: "refused target"},
		"no budgets":          {args: c.args("-budgets", filepath.Join(c.dir, "absent")), want: "absent"},
		"short budget line":   {args: c.args("-budgets", file("b1", "near\t1\t2\n")), want: "want class, p50, p95, p99"},
		"bad budget":          {args: c.args("-budgets", file("b2", "near\t1\t0\t3\n")), want: "not a positive number"},
		"missing class":       {args: c.args("-budgets", file("b3", "near\t1\t2\t3\n")), want: `no budget for class "lookup"`},
		"unknown scenario":    {args: c.args("-scenarios", "soak,nonesuch"), want: "unknown scenario"},
		"unwritable report":   {args: c.args("-report", filepath.Join(c.dir, "absent", "r.md")), want: "r.md"},
		"no breaker failures": {args: c.args("-breaker-failures", "0"), want: "-breaker-failures"},
		"no breaker probes":   {args: c.args("-breaker-probes", "0"), want: "-breaker-probes"},
		"no breaker open_for": {args: c.args("-breaker-open-for", "0s"), want: "-breaker-open-for"},
		"no stale allowance":  {args: c.args("-stale-if-error", "0s"), want: "-stale-if-error must be positive"},
		"no max-age":          {args: c.args("-max-age-energy", "0s"), want: "-versions-ttl must be positive"},
		"no versions ttl":     {args: c.args("-versions-ttl", "0s"), want: "-versions-ttl must be positive"},
		"no parking max-age":  {args: c.args("-max-age-parking", "0s"), want: "-versions-ttl must be positive"},
		"allowance too short": {args: c.args("-stale-if-error", "3s", "-versions-ttl", "2s"), want: "at least twice the longest"},
		"foreign cached host": {args: c.args("-cached-api", "http://api.example.test"), want: "not on the test-environment allow-list"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			saved := c.env
			if tc.env != nil {
				c.env = tc.env
			}
			defer func() { c.env = saved }()
			code, _, errs := c.run(tc.args...)
			if code != 2 || !strings.Contains(errs, tc.want) {
				t.Errorf("exit %d, stderr %q, want %q", code, errs, tc.want)
			}
		})
	}
}

func TestShippedAllowListIsTheLoopbackOnly(t *testing.T) {
	req.Covers(t, "TST-007")
	hosts, err := readAllowList("../../../test/load/allow-list.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h != "localhost" && h != "127.0.0.1" && h != "::1" {
			t.Errorf("the shipped allow-list names %q, which is not the loopback", h)
		}
	}
	if _, err := readBudgets("../../../test/load/budgets.tsv"); err != nil {
		t.Errorf("the shipped budgets: %v", err)
	}
}
