// Command platformsync keeps the API's version in step with the ViaFrei
// platform's release (REL-001, docs/RELEASING.md). It has two steps, run by
// .github/workflows/platform-sync.yml and usable by hand:
//
//	go run ./internal/release/platformsync read -timeout D [-source npm|github] [-base URL]
//	go run ./internal/release/platformsync apply -version X.Y.Z [-start] [-dir .] [-message FILE]
//
// read prints the platform's current release, read from a public source
// only: the dist-tag latest of the npm package viafrei (default), or the
// latest release of the repository mavrovde/viafrei-mcp. Both carry the
// platform's version. Nothing else is read, and nothing is written. Any
// answer that cannot be read as one release version X.Y.Z is a refusal, never
// a guess.
//
// apply writes that version to internal/buildinfo/platform-version.txt, the
// file /v1/meta reports as platformVersion, and writes the commit message of
// the sync to -message: a deps: subject (the CHANGELOG line) and a
// Release-As footer, which tells release-please to cut exactly that version.
// The release pull request then bumps version.txt and CHANGELOG.md in one
// commit, and the tag job tags that commit. Before the first sync the file
// says unsynced and apply changes nothing unless -start is given, so the
// scheduled run is idle until a maintainer starts the sync. A version older
// than the current platform version or the current API version is refused:
// the sync never goes back.
//
// apply prints one line: "changed X.Y.Z", "unchanged X.Y.Z" or
// "not-started X.Y.Z". Exit 0 on those, 1 on a refusal, 2 on a usage error or
// a source or file that cannot be read (never a pass).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/buildinfo"
)

const (
	platformFile = "internal/buildinfo/platform-version.txt"
	versionFile  = "version.txt"
	npmPackage   = "viafrei"
	githubRepo   = "mavrovde/viafrei-mcp"
	maxBody      = 16 << 20
)

var defaultBase = map[string]string{"npm": "https://registry.npmjs.org", "github": "https://api.github.com"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: platformsync read|apply [flags]")
		return 2
	}
	switch args[0] {
	case "read":
		return readCmd(args[1:], stdout, stderr)
	case "apply":
		return applyCmd(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q; known: read, apply\n", args[0])
	return 2
}

func readCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "npm", "public source of the platform version: npm or github")
	base := fs.String("base", "", "base URL of the source (default: the public registry or API)")
	timeout := fs.Duration("timeout", 0, "deadline of the one request (required; the workflow names it, CFG-001)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "platformsync: -timeout must name a positive deadline")
		return 2
	}
	if _, ok := defaultBase[*source]; !ok {
		fmt.Fprintf(stderr, "platformsync: unknown source %q; known: npm, github\n", *source)
		return 2
	}
	if *base == "" {
		*base = defaultBase[*source]
	}
	v, err := read(&http.Client{Timeout: *timeout}, *source, strings.TrimRight(*base, "/"))
	if err != nil {
		fmt.Fprintf(stderr, "platformsync: cannot read the platform version from %s: %v\n", *source, err)
		return 2
	}
	fmt.Fprintln(stdout, v)
	return 0
}

// read asks one public source for the platform's current release.
func read(c *http.Client, source, base string) (string, error) {
	url, accept := base+"/"+npmPackage, "application/vnd.npm.install-v1+json"
	if source == "github" {
		url, accept = base+"/repos/"+githubRepo+"/releases/latest", "application/vnd.github+json"
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxBody {
		return "", errors.New("answer larger than 16 MiB")
	}
	v, err := versionOf(source, body)
	if err != nil {
		return "", err
	}
	if _, err := parse(v); err != nil {
		return "", err
	}
	return v, nil
}

// versionOf takes the version out of a source's answer.
func versionOf(source string, body []byte) (string, error) {
	if source == "npm" {
		var doc struct {
			DistTags map[string]string `json:"dist-tags"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return "", fmt.Errorf("answer is not JSON: %w", err)
		}
		return doc.DistTags["latest"], nil
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("answer is not JSON: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		return "", errors.New("the latest release is a draft or a pre-release")
	}
	v, ok := strings.CutPrefix(rel.TagName, "v")
	if !ok {
		return "", fmt.Errorf("tag %q is not vX.Y.Z", rel.TagName)
	}
	return v, nil
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// parse reads a release version X.Y.Z; each part has at most nine digits, so
// it always fits an int.
func parse(s string) ([3]int, error) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, fmt.Errorf("version %q is not X.Y.Z", s)
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, nil
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func applyCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "platform release X.Y.Z to keep in step with (required)")
	start := fs.Bool("start", false, "make the first sync (the file says unsynced)")
	dir := fs.String("dir", ".", "repository root")
	message := fs.String("message", "", "file to write the commit message of the sync to (required when the version changes)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	want, err := parse(*version)
	if err != nil {
		fmt.Fprintf(stderr, "platformsync: -version: %v\n", err)
		return 2
	}
	status, err := apply(*dir, *version, want, *start, *message)
	var r refusal
	switch {
	case errors.As(err, &r):
		fmt.Fprintf(stderr, "platformsync: refused: %v\n", err)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "platformsync: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "%s %s\n", status, *version)
	return 0
}

type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

// commitMessage is the sync commit: the subject is the CHANGELOG line, the
// footer tells release-please which version to cut.
func commitMessage(version string) string {
	return fmt.Sprintf("deps: keep in step with ViaFrei platform %s\n\n"+
		"The API's release follows the platform's (docs/RELEASING.md): /v1/meta\n"+
		"reports %s as platformVersion, and release-please cuts the same version.\n\n"+
		"Release-As: %s\n", version, version, version)
}

func apply(dir, version string, want [3]int, start bool, message string) (string, error) {
	pf := filepath.Join(dir, platformFile)
	raw, err := os.ReadFile(filepath.Clean(pf))
	if err != nil {
		return "", err
	}
	current, err := buildinfo.ParsePlatform(string(raw))
	if err != nil {
		return "", fmt.Errorf("%s: %w", platformFile, err)
	}
	switch {
	case current == "" && !start:
		return "not-started", nil
	case current == version:
		return "unchanged", nil
	case current != "":
		if cur, _ := parse(current); less(want, cur) {
			return "", refusal{fmt.Sprintf("platform %s is older than the synced %s; the sync never goes back", version, current)}
		}
	}
	rawAPI, err := os.ReadFile(filepath.Clean(filepath.Join(dir, versionFile)))
	if err != nil {
		return "", err
	}
	apiVersion := strings.TrimSpace(string(rawAPI))
	api, err := parse(apiVersion)
	if err != nil {
		return "", fmt.Errorf("%s: %w", versionFile, err)
	}
	if less(want, api) {
		return "", refusal{fmt.Sprintf("platform %s is older than the API's %s; release-please cannot cut a lower version", version, apiVersion)}
	}
	if message == "" {
		return "", errors.New("-message is required when the version changes")
	}
	if err := os.WriteFile(message, []byte(commitMessage(version)), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(pf, []byte(version+"\n"), 0o644); err != nil { //nolint:gosec // a tracked source file the build reads
		return "", err
	}
	return "changed", nil
}
