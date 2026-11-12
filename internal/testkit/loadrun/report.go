package main

import (
	"fmt"
	"strings"
	"time"
)

// renderReport writes the Markdown report: one table row per scenario with
// its counts, then every scenario's assertions, notes and what it does not
// assert yet.
func renderReport(prof string, outs []*outcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Load and fault scenarios, %s budget\n\n", prof)
	b.WriteString("| Scenario | Requests | 2xx | 4xx | 5xx | No answer | p95 | Slowest | Verdict |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
	failed := 0
	for _, o := range outs {
		var c [6]int
		for _, r := range o.results {
			c[min(r.status/100, 5)]++
		}
		all := took(o.results, anyResult)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %s | %s | %s |\n", o.name, len(o.results), c[2], c[4], c[5], c[0],
			percentile(all, 0.95).Round(time.Millisecond), percentile(all, 1).Round(time.Millisecond), o.verdict())
		if !o.passed() {
			failed++
		}
	}
	fmt.Fprintf(&b, "\n%d scenario(s), %d failed.\n", len(outs), failed)
	for _, o := range outs {
		fmt.Fprintf(&b, "\n### %s\n\nFault: %s. Expected: %s.\n\n", o.name, o.fault, o.expect)
		for _, c := range o.checks {
			mark := "pass"
			if !c.ok {
				mark = "**FAIL**"
			}
			fmt.Fprintf(&b, "- %s: %s (%s)\n", mark, c.name, c.detail)
		}
		for _, n := range o.notes {
			fmt.Fprintf(&b, "- note: %s\n", n)
		}
		for _, p := range o.pending {
			fmt.Fprintf(&b, "- not asserted yet: %s\n", p)
		}
	}
	return b.String()
}
