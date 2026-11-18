// Package livepin reads the live pin (File): the release of the live
// refresher this API's on-demand refreshes are proven with (make live-e2e)
// and released beside. The live refresher claims and settles the refresh
// requests the API records; the two meet only in the database, so the pin
// also names the viafrei-database release the live refresher reads and
// writes, which must be the one database.pin names.
//
// Two lines, past comments and blank lines:
//
//	<Published><X.Y.Z>@sha256:<digest>
//	database <X.Y.Z>
//
// Before that release exists, the first line is Unreleased; no release may
// carry it (Refusal).
package livepin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// File is the name of the pin, at the root of the module, and Published
// the repository every released pin names: the live refresher's own
// published names.
const (
	File      = "live-ingestor.pin"
	Published = "ghcr.io/mavrovde/viafrei-live-ingestor:"
)

// Unreleased is the first line of a pin whose release does not exist yet.
const Unreleased = Published + "unreleased"

// Pin is the live refresher release of the live pin.
type Pin struct {
	// Ref is <image>:<X.Y.Z>@sha256:<digest>, or Unreleased.
	Ref string
	// Version is the X.Y.Z of Ref; empty while unreleased.
	Version string
	// Database is the viafrei-database release the live refresher reads
	// and writes.
	Database string
}

var (
	ref      = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+:([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$`)
	database = regexp.MustCompile(`^database ([0-9]+\.[0-9]+\.[0-9]+)$`)
)

// Parse reads the two lines of a pin. Anything else is refused, never
// guessed.
func Parse(src string) (Pin, error) {
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		return Pin{}, fmt.Errorf("%s holds %d lines; it must hold the image reference, then 'database X.Y.Z'", File, len(lines))
	}
	p := Pin{Ref: lines[0]}
	if m := ref.FindStringSubmatch(lines[0]); m != nil {
		p.Version = m[2]
	} else if lines[0] != Unreleased {
		return Pin{}, fmt.Errorf("%s: %q is not <image>:<X.Y.Z>@sha256:<digest> nor %s", File, lines[0], Unreleased)
	}
	m := database.FindStringSubmatch(lines[1])
	if m == nil {
		return Pin{}, fmt.Errorf("%s: %q is not 'database X.Y.Z'", File, lines[1])
	}
	p.Database = m[1]
	return p, nil
}

// Read reads the pin from the root of the module that holds dir, the nearest
// directory upwards with a go.mod. dir is absolute.
func Read(dir string) (Pin, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			src, err := os.ReadFile(filepath.Join(d, File)) // #nosec G304 -- the module's own pin
			if err != nil {
				return Pin{}, fmt.Errorf("read %s: %w", File, err)
			}
			return Parse(string(src))
		}
		if filepath.Dir(d) == d {
			return Pin{}, errors.New("no go.mod above " + dir + ": no module, so no " + File)
		}
	}
}

// Refusal is why no release may carry p beside a database.pin of release
// databaseVersion, or empty when one may.
func Refusal(p Pin, databaseVersion string) string {
	switch {
	case p.Ref == Unreleased:
		return "unreleased: the live refresher's release does not exist yet"
	case !strings.HasPrefix(p.Ref, Published):
		return "not the published live refresher release (" + Published + "...)"
	case p.Database != databaseVersion:
		return fmt.Sprintf("a live refresher of viafrei-database %s, while database.pin names %s", p.Database, databaseVersion)
	}
	digest := p.Ref[strings.LastIndex(p.Ref, ":")+1:]
	if strings.Count(digest, digest[:1]) == len(digest) {
		return "a placeholder digest"
	}
	return ""
}
