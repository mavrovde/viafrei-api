package service_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/service"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestParseContractVersion(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	good := map[string]service.ContractVersion{"0.1": {0, 1}, " 2.10 ": {2, 10}, "1.0": {1, 0}}
	for in, want := range good {
		got, err := service.ParseContractVersion(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v %v", in, got, err)
		}
		if got.String() != want.String() {
			t.Errorf("%q renders %q", in, got.String())
		}
	}
	for _, bad := range []string{"", "1", "1.x", "a.1", "-1.0", "1.-2", "1.2.3"} {
		if _, err := service.ParseContractVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestContractCompatibility(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	want := service.ContractVersion{Major: 1, Minor: 2}
	cases := map[service.ContractVersion]bool{
		{1, 2}: true, {1, 3}: true, {1, 1}: false, {2, 2}: false, {0, 9}: false,
	}
	for got, ok := range cases {
		if want.Accepts(got) != ok {
			t.Errorf("1.2 accepts %s = %v, want %v", got, !ok, ok)
		}
	}
}
