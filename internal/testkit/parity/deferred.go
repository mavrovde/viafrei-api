package parity

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// DeferredFile lists, beside the bundles, the differences of an MCP-side
// bundle that are judged and deferred: one line per fact with its class,
// never a whole case. Acceptance by the answer text: a class A difference
// (a fact the answer alters, drops or falsifies) is never deferred; B is the
// same meaning in another shape, C a correct extra the API states and
// credits, D a gap of the fixture or of the producer's recording.
const DeferredFile = "deferred.tsv"

// DeferClasses are the classes a difference may be deferred under.
var DeferClasses = []string{"B", "C", "D"}

// Deferral is one line of DeferredFile: in a case of Operation, a
// difference of Kind (missing, changed, extra, or * for any) on a fact
// Pattern names (a [*] stands for any key, and a pattern names the facts
// under it too) is deferred as Class, for Reason.
type Deferral struct {
	Class, Operation, Kind, Pattern, Reason string
}

// BlindSpotPrefix opens the reason of a deferral whose facts the producer
// never projects: the kit cannot judge them at all, and its report counts
// them apart from the judged extras.
const BlindSpotPrefix = "blind spot: "

// BlindSpot reports whether d defers facts the producer never projects.
func (d Deferral) BlindSpot() bool { return strings.HasPrefix(d.Reason, BlindSpotPrefix) }

// LoadDeferred reads DeferredFile from fsys: tab-separated class,
// operation, kind, pattern and reason; blank lines and lines starting with #
// are skipped. A file that does not exist defers nothing; any other
// failure to open it is an error.
func LoadDeferred(fsys fs.FS) ([]Deferral, error) {
	f, err := fsys.Open(DeferredFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Deferral
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 5 || p[1] == "" || p[3] == "" || p[4] == "" {
			return nil, fmt.Errorf("parity: %s:%d: want class, operation, kind, pattern and reason", DeferredFile, n)
		}
		if !slices.Contains(DeferClasses, p[0]) {
			return nil, fmt.Errorf("parity: %s:%d: class %q is not one a difference may be deferred under (%s); a class A difference is fixed", DeferredFile, n, p[0], strings.Join(DeferClasses, ", "))
		}
		if !slices.Contains([]string{"missing", "changed", "extra", "*"}, p[2]) {
			return nil, fmt.Errorf("parity: %s:%d: kind %q", DeferredFile, n, p[2])
		}
		out = append(out, Deferral{Class: p[0], Operation: p[1], Kind: p[2], Pattern: p[3], Reason: p[4]})
	}
	return out, sc.Err()
}

// Defers returns the index of the first deferral that covers the
// difference line Diff wrote in a case of operation, or -1.
func Defers(ds []Deferral, operation, line string) int {
	kind, rest, _ := strings.Cut(line, " ")
	name, _, _ := strings.Cut(strings.TrimLeft(rest, " "), ": ")
	for i, d := range ds {
		if d.Operation == operation && (d.Kind == "*" || d.Kind == kind) && exclusionPattern(d.Pattern).MatchString(name) {
			return i
		}
	}
	return -1
}
