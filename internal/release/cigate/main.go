// Command cigate decides whether a commit may be tagged or published: every
// required CI check run on that commit must have completed with conclusion
// success. A check that is missing, still queued or running, or concluded
// anything else is a refusal, so nothing is released from a commit CI has not
// passed (REL-015).
//
//	gh api --paginate --slurp "repos/OWNER/REPO/actions/runs?head_sha=SHA&per_page=100" > runs.json
//	gh api --paginate --slurp "repos/OWNER/REPO/commits/SHA/check-runs?per_page=100" |
//	    go run ./internal/release/cigate -sha SHA -workflow .github/workflows/ci.yml \
//	        -workflow-runs runs.json -require "Unit and handler tests" ...
//
// stdin holds the GitHub check-runs answer, -workflow-runs the workflow-runs
// answer for the same sha: each one page object, or the array of pages that
// gh api --paginate --slurp prints.
//
// Only a check run that GitHub Actions created (app slug github-actions) in
// the check suite of a run of -workflow on this sha counts. Any app or
// workflow that holds checks: write can create a check run with any name and
// conclusion, so a same-named run from elsewhere is never evidence. When a
// name ran more than once (a re-run), any run still in progress refuses and
// otherwise the newest completed run decides.
//
// Exit 0 when every required check passed, 1 on a refusal, 2 on a usage error
// or an answer that cannot be read (never a pass).
package main

import (
	"bytes"
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

// actionsApp is the slug of the GitHub App behind every check run a workflow
// job creates.
const actionsApp = "github-actions"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type checkRun struct {
	Name        string     `json:"name"`
	HeadSHA     string     `json:"head_sha"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	CompletedAt *time.Time `json:"completed_at"`
	App         struct {
		Slug string `json:"slug"`
	} `json:"app"`
	CheckSuite struct {
		ID int64 `json:"id"`
	} `json:"check_suite"`
}

type workflowRun struct {
	CheckSuiteID int64  `json:"check_suite_id"`
	Path         string `json:"path"`
	HeadSHA      string `json:"head_sha"`
}

// page is one page of a GitHub listing: check runs or workflow runs.
type page struct {
	TotalCount   *int          `json:"total_count"`
	CheckRuns    []checkRun    `json:"check_runs"`
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type names []string

func (n *names) String() string     { return strings.Join(*n, ", ") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cigate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sha := fs.String("sha", "", "the full commit sha the check runs must belong to")
	workflow := fs.String("workflow", "", "the workflow file whose runs may carry the checks (.github/workflows/ci.yml)")
	runsFile := fs.String("workflow-runs", "", "file holding the workflow-runs answer for -sha")
	var required names
	fs.Var(&required, "require", "a check run name that must have passed (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !fullSHA.MatchString(*sha) || *workflow == "" || *runsFile == "" || len(required) == 0 || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cigate: -sha <40 hex>, -workflow PATH, -workflow-runs FILE and at least one -require NAME are required")
		return 2
	}
	suites, err := readSuites(*runsFile, *sha, *workflow)
	if err != nil {
		fmt.Fprintln(stderr, "cigate:", err)
		return 2
	}
	pages, err := readPages(stdin, "check runs", func(p page) int { return len(p.CheckRuns) })
	if err != nil {
		fmt.Fprintln(stderr, "cigate:", err)
		return 2
	}
	var checks []checkRun
	for _, p := range pages {
		checks = append(checks, p.CheckRuns...)
	}
	refusals := judge(checks, suites, *sha, *workflow, required)
	if len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintf(stderr, "REFUSED %s: %s\n", (*sha)[:12], r)
		}
		return 1
	}
	fmt.Fprintf(stdout, "cigate: %s passed %s in %s\n", (*sha)[:12], required.String(), *workflow)
	return 0
}

// readSuites returns the check suites of the runs of workflow on sha.
func readSuites(path, sha, workflow string) (map[int64]bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- a path the caller names
	if err != nil {
		return nil, fmt.Errorf("reading the workflow runs: %w", err)
	}
	pages, err := readPages(bytes.NewReader(data), "workflow runs", func(p page) int { return len(p.WorkflowRuns) })
	if err != nil {
		return nil, err
	}
	suites := map[int64]bool{}
	for _, p := range pages {
		for _, r := range p.WorkflowRuns {
			if r.Path == workflow && r.HeadSHA == sha {
				suites[r.CheckSuiteID] = true
			}
		}
	}
	return suites, nil
}

// readPages accepts one page or an array of pages and refuses a truncated
// listing: fewer items (counted by count) than the answer's own total_count.
func readPages(r io.Reader, what string, count func(page) int) ([]page, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the %s: %w", what, err)
	}
	var pages []page
	if err := json.Unmarshal(data, &pages); err != nil {
		var one page
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, fmt.Errorf("the %s answer is not JSON: %w", what, err)
		}
		pages = []page{one}
	}
	if len(pages) == 0 || pages[0].TotalCount == nil {
		return nil, fmt.Errorf("the %s answer carries no total_count; it is not a %s listing", what, what)
	}
	n := 0
	for _, p := range pages {
		n += count(p)
	}
	if n < *pages[0].TotalCount {
		return nil, fmt.Errorf("the listing holds %d of %d %s; a truncated listing is not evidence", n, *pages[0].TotalCount, what)
	}
	return pages, nil
}

func judge(checks []checkRun, suites map[int64]bool, sha, workflow string, required []string) []string {
	var refusals []string
	for _, name := range required {
		var mine []checkRun
		foreign := 0
		for _, c := range checks {
			if c.Name != name || c.HeadSHA != sha {
				continue
			}
			if c.App.Slug == actionsApp && suites[c.CheckSuite.ID] {
				mine = append(mine, c)
			} else {
				foreign++
			}
		}
		if r := verdict(name, mine, foreign, workflow); r != "" {
			refusals = append(refusals, r)
		}
	}
	return refusals
}

// verdict judges the trusted runs of one required name; "" is a pass.
func verdict(name string, mine []checkRun, foreign int, workflow string) string {
	if len(mine) == 0 && foreign > 0 {
		return fmt.Sprintf("%q has not run in %s on this commit; %d same-named run(s) from another app or workflow are not evidence", name, workflow, foreign)
	}
	if len(mine) == 0 {
		return fmt.Sprintf("%q has not run on this commit", name)
	}
	if slices.ContainsFunc(mine, func(r checkRun) bool { return r.Status != "completed" || r.CompletedAt == nil }) {
		return fmt.Sprintf("%q is still %s; wait for it to complete", name, pending(mine))
	}
	newest := slices.MaxFunc(mine, func(a, b checkRun) int { return a.CompletedAt.Compare(*b.CompletedAt) })
	if newest.Conclusion != "success" {
		return fmt.Sprintf("%q concluded %q", name, newest.Conclusion)
	}
	return ""
}

func pending(runs []checkRun) string {
	for _, r := range runs {
		if r.Status != "completed" {
			return r.Status
		}
	}
	return "without a completion time"
}
