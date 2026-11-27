// Package buildinfo carries the version facts of a build: the API version and
// commit injected at build time with
// -ldflags "-X github.com/mavrovde/viafrei-api/internal/buildinfo.version=...".
// The API's release version is also the platform version /v1/meta reports
// (docs/RELEASING.md).
package buildinfo

// Set by the linker. The defaults describe a development build.
var (
	version = "0.2.0-dev"
	commit  = "unknown"
	date    = ""
)

// Info is what a build reports about itself.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date,omitempty"`
}

// Get returns the build's facts.
func Get() Info { return Info{Version: version, Commit: commit, Date: date} }
