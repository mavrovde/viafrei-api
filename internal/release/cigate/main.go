// Command cigate decides whether a commit may be tagged or published: every
// required CI check run on that commit must have completed with conclusion
// success. A check that is missing, still queued or running, or concluded
// anything else is a refusal, so nothing is released from a commit CI has not
// passed (REL-015).
//
//	gh api --paginate --slurp "repos/OWNER/REPO/commits/SHA/check-runs?per_page=100" |
//	    go run ./internal/release/cigate -sha SHA -require "Unit and handler tests" ...
//
// stdin holds the GitHub check-runs answer: one page object, or the array of
// pages that gh api --paginate --slurp prints. When a name ran more than once
// (a re-run), any run still in progress refuses and otherwise the newest
// completed run decides.
//
// Exit 0 when every required check passed, 1 on a refusal, 2 on a usage error
// or an answer that cannot be read (never a pass).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type checkRun struct {
	Name        string     `json:"name"`
	HeadSHA     string     `json:"head_sha"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	CompletedAt *time.Time `json:"completed_at"`
}

type page struct {
	TotalCount *int       `json:"total_count"`
	CheckRuns  []checkRun `json:"check_runs"`
}

type names []string

func (n *names) String() string     { return strings.Join(*n, ", ") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cigate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sha := fs.String("sha", "", "the full commit sha the check runs must belong to")
	var required names
	fs.Var(&required, "require", "a check run name that must have passed (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !fullSHA.MatchString(*sha) || len(required) == 0 || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cigate: -sha <40 hex> and at least one -require NAME are required")
		return 2
	}
	runs, err := readRuns(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "cigate:", err)
		return 2
	}
	refusals := judge(runs, *sha, required)
	if len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintf(stderr, "REFUSED %s: %s\n", (*sha)[:12], r)
		}
		return 1
	}
	fmt.Fprintf(stdout, "cigate: %s passed %s\n", (*sha)[:12], required.String())
	return 0
}

// readRuns accepts one page or an array of pages and refuses a truncated
// listing: fewer runs than the answer's own total_count.
func readRuns(r io.Reader) ([]checkRun, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the check runs: %w", err)
	}
	var pages []page
	if err := json.Unmarshal(data, &pages); err != nil {
		var one page
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, fmt.Errorf("the check-runs answer is not JSON: %w", err)
		}
		pages = []page{one}
	}
	if len(pages) == 0 || pages[0].TotalCount == nil {
		return nil, fmt.Errorf("the check-runs answer carries no total_count; it is not a check-runs listing")
	}
	var runs []checkRun
	for _, p := range pages {
		runs = append(runs, p.CheckRuns...)
	}
	if len(runs) < *pages[0].TotalCount {
		return nil, fmt.Errorf("the listing holds %d of %d check runs; a truncated listing is not evidence", len(runs), *pages[0].TotalCount)
	}
	return runs, nil
}

func judge(runs []checkRun, sha string, required []string) []string {
	var refusals []string
	for _, name := range required {
		var mine []checkRun
		for _, r := range runs {
			if r.Name == name && r.HeadSHA == sha {
				mine = append(mine, r)
			}
		}
		if len(mine) == 0 {
			refusals = append(refusals, fmt.Sprintf("%q has not run on this commit", name))
			continue
		}
		if slices.ContainsFunc(mine, func(r checkRun) bool { return r.Status != "completed" || r.CompletedAt == nil }) {
			refusals = append(refusals, fmt.Sprintf("%q is still %s; wait for it to complete", name, pending(mine)))
			continue
		}
		newest := slices.MaxFunc(mine, func(a, b checkRun) int { return a.CompletedAt.Compare(*b.CompletedAt) })
		if newest.Conclusion != "success" {
			refusals = append(refusals, fmt.Sprintf("%q concluded %q", name, newest.Conclusion))
		}
	}
	return refusals
}

func pending(runs []checkRun) string {
	for _, r := range runs {
		if r.Status != "completed" {
			return r.Status
		}
	}
	return "without a completion time"
}
