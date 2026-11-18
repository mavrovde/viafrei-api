package rules_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

func TestVerdictOfJudgesAsThePlatform(t *testing.T) {
	zone := func(key, role, fam string, inside bool, d int) rules.Zone {
		return rules.Zone{Key: key, Role: role, Family: fam, Inside: inside, DistanceM: d}
	}
	for name, c := range map[string]struct {
		rows                         []rules.Zone
		kind, zone, transfer, exempt string
	}{
		"inside, with its own publisher's released area and exempt road": {
			rows: []rules.Zone{
				zone("t-other", "transfer", "b", true, 0), zone("z", "zone", "a", true, 0), zone("t", "transfer", "a", true, 0),
				zone("t2", "transfer", "a", true, 0), zone("x-far", "transfer", "a", false, 40), zone("e", "exempt_road", "a", false, 20), zone("e2", "exempt_road", "a", false, 30),
			},
			kind: "inside", zone: "z", transfer: "t", exempt: "e",
		},
		"a released area it is inside": {
			rows: []rules.Zone{zone("z", "zone", "a", false, 900), zone("t-out", "transfer", "a", false, 10), zone("t", "transfer", "a", true, 0)},
			kind: "transfer", transfer: "t",
		},
		"outside, the nearest zone": {
			rows: []rules.Zone{zone("far", "zone", "a", false, 900), zone("near", "zone", "b", false, 300), zone("e", "exempt_road", "a", false, 5), zone("mid", "zone", "a", false, 600)},
			kind: "outside", zone: "near",
		},
		"none within reach": {kind: "unknown"},
	} {
		v := rules.VerdictOf(c.rows)
		key := func(z *rules.Zone) string {
			if z == nil {
				return ""
			}
			return z.Key
		}
		if v.Kind != c.kind || key(v.Zone) != c.zone || key(v.Transfer) != c.transfer || key(v.ExemptRoad) != c.exempt {
			t.Errorf("%s: %s zone %q transfer %q exempt %q", name, v.Kind, key(v.Zone), key(v.Transfer), key(v.ExemptRoad))
		}
	}
}
