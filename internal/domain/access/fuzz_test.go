package access_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// FuzzParse (TEST-006, TST-016): a presented key is attacker input. Any
// string either parses or is refused with ErrMalformed alone, never a crash;
// what parses has an id and a secret of the fixed lengths that are the
// input's own parts, and never parses for the other environment.
// Seed corpus: testdata/fuzz/FuzzParse (test/fuzz/targets.tsv).
func FuzzParse(f *testing.F) {
	k, err := access.Generate(bytes.NewReader(bytes.Repeat([]byte{7}, 64)), access.Live, prefixes)
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{k.Full, k.Full[:len(k.Full)-1] + "0", "vf_test_" + k.Full[len("vf_live_"):], "vf_live_", "", "vf_live_" + string(make([]byte, 60))} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id, secret, err := access.Parse(s, access.Live, prefixes)
		if err != nil {
			if !errors.Is(err, access.ErrMalformed) || id != "" || secret != "" {
				t.Fatalf("%q refused with %v but returned %q, %q", s, err, id, secret)
			}
			return
		}
		if len(id) != access.IDLength || len(secret) != access.SecretLength {
			t.Fatalf("%q parsed to parts of the wrong length: %q, %q", s, id, secret)
		}
		if body := prefixes.Live + id + "_" + secret; len(s) != len(body)+access.ChecksumLength || s[:len(body)] != body {
			t.Fatalf("%q parsed to %q, %q, which are not its own parts", s, id, secret)
		}
		if _, _, err := access.Parse(s, access.Test, prefixes); err == nil {
			t.Fatalf("%q parsed for both environments", s)
		}
	})
}

// FuzzParseAcceptance (TEST-006, TST-016): accept_terms is caller input. Any
// string parses or is refused, never a crash; what parses has a non-empty
// family and version per pair, each family once, and writes back to s.
func FuzzParseAcceptance(f *testing.F) {
	for _, s := range []string{"", "sid:v3", "sid:v3,open:1", "sid", "sid:v1,sid:v2", ",", "a:b:c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := access.ParseAcceptance(s)
		if err != nil {
			if got != nil {
				t.Fatalf("%q refused with %v but returned %v", s, err, got)
			}
			return
		}
		seen := map[string]bool{}
		parts := make([]string, len(got))
		for i, a := range got {
			if a.Source == "" || a.Version == "" || seen[a.Source] {
				t.Fatalf("%q parsed to %v", s, got)
			}
			seen[a.Source] = true
			parts[i] = access.TermsValue(access.Terms{Family: a.Source, Version: a.Version})
		}
		if strings.Join(parts, ",") != s {
			t.Fatalf("%q parsed to %v, which does not write back", s, got)
		}
	})
}
