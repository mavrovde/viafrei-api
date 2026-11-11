package perf_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/perf"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

const classes = "# comment\n\nconditional\t5\t10\t30\nnear\t60\t200\t500\n"

func load(t *testing.T, c, o string) (perf.Budgets, error) {
	t.Helper()
	return perf.Load(strings.NewReader(c), strings.NewReader(o), 2*time.Second)
}

func TestLoadReadsBothFiles(t *testing.T) {
	b, err := load(t, classes, "# op\tclass\nlistChargingOptions\tnear\n")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := b.Of("listChargingOptions")
	if !ok || c != (perf.Class{Name: "near", P50: 60 * time.Millisecond, P95: 200 * time.Millisecond, P99: 500 * time.Millisecond}) {
		t.Fatalf("budget %+v, %v", c, ok)
	}
	if _, ok := b.Of("unknown"); ok {
		t.Fatal("an unknown operation has a budget")
	}
}

func TestLoadRefusesWhatAssertsNothing(t *testing.T) {
	for name, tc := range map[string]struct{ c, o, want string }{
		"short line":       {"near\t1\t2\n", "", "3 fields, want 4"},
		"not a number":     {"near\t1\tx\t3\n", "", `"x" is not a positive number`},
		"zero":             {"near\t0\t2\t3\n", "", "not a positive number"},
		"out of order":     {"near\t5\t2\t3\n", "", "want p50 ≤ p95 ≤ p99"},
		"at the deadline":  {"near\t5\t20\t2000\n", "", "want p50 ≤ p95 ≤ p99 < 2s"},
		"class twice":      {classes + "near\t1\t2\t3\n", "", "class near twice"},
		"no conditional":   {"near\t1\t2\t3\n", "", `no "conditional" class`},
		"unknown class":    {classes, "op\tfar\n", `unknown class "far"`},
		"operation twice":  {classes, "op\tnear\nop\tnear\n", "operation op twice"},
		"operation fields": {classes, "op\n", "1 fields, want 2"},
		"no operation":     {classes, "# nothing\n", "no operation read"},
	} {
		if _, err := load(t, tc.c, tc.o); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	if _, err := perf.Load(iotest.ErrReader(errors.New("unreadable")), strings.NewReader(""), 2*time.Second); err == nil {
		t.Error("an unreadable classes file was accepted")
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	var s []time.Duration
	for i := 100; i >= 1; i-- {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	for q, want := range map[float64]time.Duration{0.5: 50 * time.Millisecond, 0.95: 95 * time.Millisecond, 0.99: 99 * time.Millisecond, 1: 100 * time.Millisecond, 0: time.Millisecond} {
		if got := perf.Percentile(s, q); got != want {
			t.Errorf("p%v = %s, want %s", q*100, got, want)
		}
	}
	if s[0] != 100*time.Millisecond {
		t.Error("Percentile sorted the caller's samples")
	}
	if perf.Percentile(nil, 0.95) != 0 {
		t.Error("no samples have a percentile")
	}
}

func TestCheckNamesEveryMiss(t *testing.T) {
	c := perf.Class{Name: "near", P95: 200 * time.Millisecond, P99: 500 * time.Millisecond}
	if f := perf.Measure("op", nil).Check(c); len(f) != 1 || !strings.Contains(f[0], "no samples") {
		t.Fatalf("no samples: %v", f)
	}
	ok := perf.Measure("op", []time.Duration{time.Millisecond, 200 * time.Millisecond})
	if f := ok.Check(c); len(f) != 0 || ok.N != 2 || ok.P50 != time.Millisecond {
		t.Fatalf("within budget: %v %+v", f, ok)
	}
	slow := perf.Result{Name: "op", N: 1, P95: 201 * time.Millisecond, P99: 501 * time.Millisecond}
	if f := slow.Check(c); len(f) != 2 || !strings.Contains(f[0], "p95 201ms over the near budget of 200ms") {
		t.Fatalf("over budget: %v", f)
	}
	if !strings.Contains(ok.String(), "p95=200ms") {
		t.Fatalf("report line %q", ok.String())
	}
}

// TestEveryOperationHasABudget holds the budget files and the operations
// together: every operation has a class, each under the configured deadline, and
// most operations (more than half) answer within 200 ms at p95
// (Performance-and-Caching design targets).
func TestEveryOperationHasABudget(t *testing.T) {
	c, err := os.Open("../../test/perf/classes.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	o, err := os.Open("../../test/perf/operations.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := perf.Load(c, o, cfg.Deadline.Request)
	if err != nil {
		t.Fatal(err)
	}
	fast := 0
	for _, id := range httpapi.OperationIDs() {
		class, ok := b.Of(id)
		if !ok {
			t.Errorf("%s has no budget in test/perf/operations.tsv", id)
			continue
		}
		if class.P95 <= 200*time.Millisecond {
			fast++
		}
		delete(b.Operations, id)
	}
	for id := range b.Operations {
		t.Errorf("test/perf/operations.tsv budgets %s, which is not an operation", id)
	}
	if n := len(httpapi.OperationIDs()); fast*2 <= n {
		t.Errorf("%d of %d operations have a p95 budget within 200 ms; most must", fast, n)
	}
}
