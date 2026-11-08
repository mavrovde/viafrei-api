// Command fixturegen writes a synthetic fixture set for the read contract
// (TST-014): seed.sql, which loads into a database built from db/contract, and
// manifest.json, which records the generator version, seed, profile, counts,
// edge cases, input licences and the checksum of seed.sql.
//
//	fixturegen -profile tiny|ci|benchmark -seed N -out DIR
//	fixturegen -verify FILE
//
// -verify regenerates every set listed in FILE (lines of profile, seed,
// generator version and checksum, tab-separated; # starts a comment) and
// compares each checksum with the recorded one. The recorded checksums were
// taken on another machine and architecture, so a match on this one shows
// that the output depends on the seed alone (TST-014). It exits 0 when every
// set matches, 1 on a mismatch or a row recorded for another generator
// version, and 2 when the file cannot be read or lists no set.
//
// The data is fictional and generated from the seed alone; the command reads
// no network and no file. It exits 0 and prints the counts on success, 1 when
// the set has a coverage gap or cannot be written, and 2 on a usage error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/invariant"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fixturegen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "tiny", "profile: "+strings.Join(fixtures.Profiles(), ", "))
	seed := fs.Uint64("seed", 1, "seed; the same seed, profile and generator version give the same bytes")
	out := fs.String("out", "", "output directory (created if missing)")
	verifyFile := fs.String("verify", "", "file of recorded checksums to verify instead of writing a set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *verifyFile != "" && fs.NArg() == 0 {
		return verify(*verifyFile, stdout, stderr)
	}
	if *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "fixturegen: -out DIR is required and no positional arguments are accepted")
		return 2
	}
	if !slices.Contains(fixtures.Profiles(), *profile) {
		fmt.Fprintf(stderr, "fixturegen: unknown profile %q (known: %s)\n", *profile, strings.Join(fixtures.Profiles(), ", "))
		return 2
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		fmt.Fprintln(stderr, "fixturegen:", err)
		return 1
	}
	m, err := write(*out, *profile, *seed)
	if err != nil {
		fmt.Fprintf(stderr, "fixturegen: %v (profile %s, seed %d)\n", err, *profile, *seed)
		return 1
	}
	fmt.Fprintln(stdout, m.Summary())
	return 0
}

// golden is one recorded set.
type golden struct {
	profile, version, checksum string
	seed                       uint64
	line                       int
}

func readGolden(path string) ([]golden, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var out []golden
	for i, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		var seed uint64
		if len(f) != 4 || !slices.Contains(fixtures.Profiles(), f[0]) || !strings.HasPrefix(f[3], "sha256:") {
			return nil, fmt.Errorf("line %d: want profile, seed, generator version and sha256 checksum", i+1)
		}
		if _, err := fmt.Sscan(f[1], &seed); err != nil {
			return nil, fmt.Errorf("line %d: seed %q: %w", i+1, f[1], err)
		}
		out = append(out, golden{profile: f[0], seed: seed, version: f[2], checksum: f[3], line: i + 1})
	}
	if len(out) == 0 {
		return nil, errors.New("no recorded set")
	}
	return out, nil
}

func verify(path string, stdout, stderr io.Writer) int {
	rows, err := readGolden(path)
	if err != nil {
		fmt.Fprintf(stderr, "fixturegen: -verify %s: %v\n", path, err)
		return 2
	}
	failed := 0
	for _, g := range rows {
		if g.version != fixtures.GeneratorVersion {
			fmt.Fprintf(stderr, "fixturegen: line %d records generator %s, this is %s: record the checksums again\n", g.line, g.version, fixtures.GeneratorVersion)
			failed++
			continue
		}
		m, err := fixtures.Generate(io.Discard, g.profile, g.seed, contract.Version())
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "fixturegen: %s seed %d: %v\n", g.profile, g.seed, err)
			failed++
		case m.Checksum != g.checksum:
			fmt.Fprintf(stderr, "fixturegen: %s seed %d: %s, recorded %s\n", g.profile, g.seed, m.Checksum, g.checksum)
			failed++
		default:
			fmt.Fprintf(stdout, "%s seed %d: %s matches\n", g.profile, g.seed, m.Checksum)
		}
	}
	fmt.Fprintf(stdout, "fixturegen: verified %d of %d recorded sets (generator %s)\n", len(rows)-failed, len(rows), fixtures.GeneratorVersion)
	if failed > 0 {
		return 1
	}
	return 0
}

func write(dir, profile string, seed uint64) (*fixtures.Manifest, error) {
	f, err := os.Create(filepath.Clean(filepath.Join(dir, "seed.sql")))
	if err != nil {
		return nil, err
	}
	m, err := fixtures.Generate(f, profile, seed, contract.Version())
	if err = errors.Join(err, f.Close()); err != nil {
		return m, err
	}
	data := invariant.Must(json.MarshalIndent(m, "", "  "))
	return m, os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o600)
}
