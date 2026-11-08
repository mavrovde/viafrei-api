// Package buildinfo carries the version facts of a build: the API version and
// commit injected at build time with
// -ldflags "-X github.com/mavrovde/viafrei-api/internal/buildinfo.version=...",
// and the ViaFrei platform version this build is kept in step with, read
// from platform-version.txt beside this file, which only the version sync
// writes (docs/RELEASING.md).
package buildinfo

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

// Set by the linker. The defaults describe a development build.
var (
	version = "0.2.0-dev"
	commit  = "unknown"
	date    = ""
)

// Unsynced is the content of platform-version.txt before the first version
// sync: the API is not yet kept in step with a platform release.
const Unsynced = "unsynced"

//go:embed platform-version.txt
var platformFile string

// platform is the parsed platform version, empty while unsynced. A malformed
// file stops the process at start-up instead of reporting a wrong version.
var platform = mustPlatform(platformFile)

var release = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParsePlatform reads the content of platform-version.txt: Unsynced, or one
// release version X.Y.Z (no pre-release, no build metadata). It returns the
// version, or "" for Unsynced.
func ParsePlatform(s string) (string, error) {
	v := strings.TrimSpace(s)
	switch {
	case v == Unsynced:
		return "", nil
	case release.MatchString(v):
		return v, nil
	}
	return "", fmt.Errorf("platform version %q is neither %s nor X.Y.Z", v, Unsynced)
}

func mustPlatform(s string) string {
	v, err := ParsePlatform(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Info is what a build reports about itself.
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date,omitempty"`
	Platform string `json:"platform,omitempty"`
}

// Get returns the build's facts.
func Get() Info { return Info{Version: version, Commit: commit, Date: date, Platform: platform} }
