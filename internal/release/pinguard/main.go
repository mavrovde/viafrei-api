// Command pinguard refuses a database.pin that no release may carry, and
// otherwise prints the image reference it pins.
//
//	go run ./internal/release/pinguard [-dir DIR]
//
// A pin may name a locally built viafrei-database image
// (<name>:<X.Y.Z>-local) while that release is not yet published: the tests
// and the local stack run it. Nothing built from such a pin may leave the
// machine, so CI pulls the image this command prints (and stops here, with
// the reason, on a local pin) and the release workflow runs it before
// anything is built. A reference that is not the published viafrei-database
// release, or whose digest is a placeholder (one hex digit repeated), is
// refused the same way.
//
// Exit 0 with the reference on stdout; 1 on a refusal; 2 when the pin cannot
// be read or on a usage error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// Published is the repository every released pin names.
const Published = "ghcr.io/mavrovde/viafrei-database:"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pinguard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "a directory of the module whose database.pin is read")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	abs, _ := filepath.Abs(*dir) // an unreadable working directory finds no module, and ReadPin says so
	pin, err := pgtest.ReadPin(abs)
	if err != nil {
		fmt.Fprintln(stderr, "pinguard:", err)
		return 2
	}
	if why := refusal(pin); why != "" {
		fmt.Fprintf(stderr, "pinguard: %s names %s: %s; pin the published viafrei-database release by its digest first\n", pgtest.PinFile, pin.Ref, why)
		return 1
	}
	fmt.Fprintln(stdout, pin.Ref)
	return 0
}

// refusal is why no release may carry pin, or empty when one may.
func refusal(pin pgtest.Pin) string {
	switch {
	case pin.Local:
		return "a locally built image, which only this machine holds"
	case !strings.HasPrefix(pin.Ref, Published):
		return "not the published viafrei-database release"
	}
	digest := pin.Ref[strings.LastIndex(pin.Ref, ":")+1:]
	if strings.Count(digest, digest[:1]) == len(digest) {
		return "a placeholder digest"
	}
	return ""
}
