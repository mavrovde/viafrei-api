// Command fixturegen writes a synthetic fixture set for the read contract
// (TST-014): seed.sql, which loads into a database built from db/contract, and
// manifest.json, which records the generator version, seed, profile, counts,
// edge cases, input licences and the checksum of seed.sql.
//
//	fixturegen -profile tiny|ci|benchmark -seed N -out DIR
//
// The data is fictional and generated from the seed alone; the command reads
// no network and no file. It exits 0 and prints the counts on success, 1 when
// the set has a coverage gap or cannot be written, and 2 on a usage error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "fixturegen: -out DIR is required and no positional arguments are accepted")
		return 2
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		fmt.Fprintln(stderr, "fixturegen:", err)
		return 1
	}
	m, err := write(*out, *profile, *seed)
	if err != nil {
		fmt.Fprintf(stderr, "fixturegen: %v (profile %s, seed %d)\n", err, *profile, *seed)
		if m == nil {
			return 2
		}
		return 1
	}
	fmt.Fprintln(stdout, m.Summary())
	return 0
}

func write(dir, profile string, seed uint64) (m *fixtures.Manifest, err error) {
	sqlPath := filepath.Join(dir, "seed.sql")
	f, err := os.Create(filepath.Clean(sqlPath))
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()
	m, err = fixtures.Generate(f, profile, seed, contract.Version())
	if err != nil {
		return m, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	return m, os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o600)
}
