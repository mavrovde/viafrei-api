// Package perf holds the latency budgets of the service and the arithmetic
// the benchmark leg asserts them with (NFR-PERF-003). The budgets live in
// test/perf: classes.tsv sets p50, p95 and p99 per endpoint class, and
// operations.tsv puts every operation in a class.
package perf

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Conditional is the class of every 304 answer.
const Conditional = "conditional"

// Class is one endpoint class's latency budget.
type Class struct {
	Name          string
	P50, P95, P99 time.Duration
}

// Budgets are the classes and the class of every operation.
type Budgets struct {
	Classes    map[string]Class
	Operations map[string]string // operation id to class name
}

// Of returns the budget of an operation.
func (b Budgets) Of(operation string) (Class, bool) {
	c, ok := b.Classes[b.Operations[operation]]
	return c, ok
}

// Load reads the two budget files. It refuses a malformed line, a class out
// of order (p50 ≤ p95 ≤ p99 < deadline, the configured hard ceiling of every
// request, deadline.request, NFR-PERF-001), a duplicate, an operation of an
// unknown class, a class other than conditional that no operation uses, and
// files without the conditional class or any operation: a budget read from
// nothing, or one nothing is measured against, asserts nothing.
func Load(classes, operations io.Reader, deadline time.Duration) (Budgets, error) {
	b := Budgets{Classes: map[string]Class{}, Operations: map[string]string{}}
	err := rows(classes, 4, func(f []string) error {
		var ms [3]int
		for i := range ms {
			n, err := strconv.Atoi(f[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("class %s: %q is not a positive number of milliseconds", f[0], f[i+1])
			}
			ms[i] = n
		}
		c := Class{Name: f[0], P50: millis(ms[0]), P95: millis(ms[1]), P99: millis(ms[2])}
		if c.P50 > c.P95 || c.P95 > c.P99 || c.P99 >= deadline {
			return fmt.Errorf("class %s: want p50 ≤ p95 ≤ p99 < %s", c.Name, deadline)
		}
		if _, dup := b.Classes[c.Name]; dup {
			return fmt.Errorf("class %s twice", c.Name)
		}
		b.Classes[c.Name] = c
		return nil
	})
	if err != nil {
		return Budgets{}, fmt.Errorf("classes: %w", err)
	}
	if _, ok := b.Classes[Conditional]; !ok {
		return Budgets{}, fmt.Errorf("classes: no %q class", Conditional)
	}
	err = rows(operations, 2, func(f []string) error {
		if _, ok := b.Classes[f[1]]; !ok {
			return fmt.Errorf("operation %s: unknown class %q", f[0], f[1])
		}
		if _, dup := b.Operations[f[0]]; dup {
			return fmt.Errorf("operation %s twice", f[0])
		}
		b.Operations[f[0]] = f[1]
		return nil
	})
	if err != nil {
		return Budgets{}, fmt.Errorf("operations: %w", err)
	}
	if len(b.Operations) == 0 {
		return Budgets{}, fmt.Errorf("operations: no operation read")
	}
	// A class no operation uses is a budget nothing is measured against;
	// the conditional class is every operation's 304.
	used := map[string]bool{Conditional: true}
	for _, c := range b.Operations {
		used[c] = true
	}
	var unused []string
	for name := range b.Classes {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	if len(unused) > 0 {
		slices.Sort(unused)
		return Budgets{}, fmt.Errorf("classes: no operation uses %s", strings.Join(unused, ", "))
	}
	return b, nil
}

func millis(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// rows calls fn with the tab-separated fields of every line that is neither
// empty nor a comment, refusing a line with another number of fields.
func rows(r io.Reader, fields int, fn func([]string) error) error {
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != fields {
			return fmt.Errorf("line %d: %d fields, want %d", n, len(f), fields)
		}
		if err := fn(f); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	return s.Err()
}

// Percentile is the nearest-rank percentile q (0 < q ≤ 1) of samples, read
// from the full sample set, never from an average.
func Percentile(samples []time.Duration, q float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	rank := int(math.Ceil(q * float64(len(s))))
	return s[max(rank, 1)-1]
}

// Result is the measured latency of one operation or class.
type Result struct {
	Name          string
	N             int
	P50, P95, P99 time.Duration
}

// Measure reads the percentiles of samples.
func Measure(name string, samples []time.Duration) Result {
	return Result{Name: name, N: len(samples),
		P50: Percentile(samples, 0.50), P95: Percentile(samples, 0.95), P99: Percentile(samples, 0.99)}
}

// Check lists every way r misses c: no samples at all, or a p95 or p99 over
// its budget. p50 is a target to report, not a gate.
func (r Result) Check(c Class) []string {
	if r.N == 0 {
		return []string{r.Name + ": no samples; a budget over nothing is not met"}
	}
	var out []string
	if r.P95 > c.P95 {
		out = append(out, fmt.Sprintf("%s: p95 %s over the %s budget of %s", r.Name, r.P95, c.Name, c.P95))
	}
	if r.P99 > c.P99 {
		out = append(out, fmt.Sprintf("%s: p99 %s over the %s budget of %s", r.Name, r.P99, c.Name, c.P99))
	}
	return out
}

// String is one line of the benchmark report.
func (r Result) String() string {
	return fmt.Sprintf("%-22s n=%-4d p50=%-10s p95=%-10s p99=%s", r.Name, r.N, r.P50, r.P95, r.P99)
}
