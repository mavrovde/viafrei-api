package pgtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// PinFile is the name of the file, at the root of the module, that names
// the viafrei-database release the test databases are migrated by.
const PinFile = "database.pin"

// Pin is the viafrei-database release of database.pin.
type Pin struct {
	// Ref is the image, <image>:<X.Y.Z>@sha256:<digest>.
	Ref string
	// Version is the X.Y.Z of Ref.
	Version string
	// Head is the schema head the image's `head` must print as READY <Head>.
	Head int
}

var (
	pinRef  = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+:([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$`)
	pinHead = regexp.MustCompile(`^schema-head ([1-9][0-9]{0,5})$`)
)

// ParsePin reads the two lines of a pin, the image reference and then
// `schema-head N`, past comments and blank lines. Anything else is refused,
// never guessed.
func ParsePin(src string) (Pin, error) {
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		return Pin{}, fmt.Errorf("%s holds %d lines; it must hold the image reference, then 'schema-head N'", PinFile, len(lines))
	}
	ref := pinRef.FindStringSubmatch(lines[0])
	if ref == nil {
		return Pin{}, fmt.Errorf("%s: %q is not <image>:<X.Y.Z>@sha256:<digest>", PinFile, lines[0])
	}
	head := pinHead.FindStringSubmatch(lines[1])
	if head == nil {
		return Pin{}, fmt.Errorf("%s: %q is not 'schema-head N'", PinFile, lines[1])
	}
	n, _ := strconv.Atoi(head[1]) // six digits at most: always a number
	return Pin{Ref: lines[0], Version: ref[2], Head: n}, nil
}

// ReadPin reads database.pin from the root of the module that holds dir,
// the nearest directory upwards with a go.mod. dir is absolute, as
// os.Getwd returns it.
func ReadPin(dir string) (Pin, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			src, err := os.ReadFile(filepath.Join(d, PinFile)) // #nosec G304 -- the module's own pin
			if err != nil {
				return Pin{}, fmt.Errorf("read %s: %w", PinFile, err)
			}
			return ParsePin(string(src))
		}
		if filepath.Dir(d) == d {
			return Pin{}, errors.New("no go.mod above " + dir + ": no module, so no " + PinFile)
		}
	}
}

// VersionLine is what the pinned image's `version` must print.
func (p Pin) VersionLine() string {
	return fmt.Sprintf("viafrei-database %s schema-head %d", p.Version, p.Head)
}

// Ready is what the pinned image's `head` must print.
func (p Pin) Ready() string { return fmt.Sprintf("READY %d", p.Head) }
