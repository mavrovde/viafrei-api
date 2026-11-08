package service_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/service"
)

// FuzzParseContractVersion (TEST-006, TST-016): the read-contract version a
// database publishes is input to the service. Any string either parses or is
// refused, never a crash; what parses renders back to a version that parses
// to the same value (round trip), and a version always accepts itself.
// Seed corpus: testdata/fuzz/FuzzParseContractVersion (test/fuzz/targets.tsv).
func FuzzParseContractVersion(f *testing.F) {
	for _, s := range []string{"0.1", " 2.10 ", "1.0", "", "1", "1.x", "-1.0", "1.2.3", "+1.2", "99999999999999999999.1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := service.ParseContractVersion(s)
		if err != nil {
			if v != (service.ContractVersion{}) {
				t.Fatalf("%q refused but returned %v", s, v)
			}
			return
		}
		if v.Major < 0 || v.Minor < 0 {
			t.Fatalf("%q parsed to a negative version %v", s, v)
		}
		back, err := service.ParseContractVersion(v.String())
		if err != nil || back != v {
			t.Fatalf("%q parsed to %v, which renders %q and parses back to %v, %v", s, v, v.String(), back, err)
		}
		if !v.Accepts(v) {
			t.Fatalf("%v does not accept itself", v)
		}
	})
}
