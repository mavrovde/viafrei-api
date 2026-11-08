package access

import (
	"errors"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestParseRefusesANonBase62PartWithAValidChecksum: the checksum is no
// substitute for the alphabet check. A key whose id or secret carries a
// character outside base62 is refused even when its checksum was computed
// over exactly that text, so each part is checked on its own.
func TestParseRefusesANonBase62PartWithAValidChecksum(t *testing.T) {
	req.Covers(t, "SEC-003")
	p := Prefixes{Live: "vf_live_", Test: "vf_test_"}
	id, secret := strings.Repeat("A", IDLength), strings.Repeat("B", SecretLength)
	for name, parts := range map[string][2]string{
		"id":     {"AAAA-AAAAA", secret},
		"secret": {id, "BBBB-" + secret[5:]},
	} {
		body := p.Live + parts[0] + "_" + parts[1]
		if _, _, err := Parse(body+checksum(body), Live, p); !errors.Is(err, ErrMalformed) {
			t.Errorf("a non-base62 %s with a matching checksum: %v, want ErrMalformed", name, err)
		}
	}
	body := p.Live + id + "_" + secret
	if _, _, err := Parse(body+checksum(body), Live, p); err != nil {
		t.Fatalf("the well-formed control is refused: %v", err)
	}
}
