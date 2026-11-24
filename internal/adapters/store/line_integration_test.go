//go:build integration

package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// A line asked is looked up in the static timetable of the families read: by
// its name folded as the alerts' (case and white space aside), and with no
// line of those families at all nothing could be told.
func TestALineIsLookedUpInTheTimetable(t *testing.T) {
	st := seeded(t)
	if n := plant(t, `insert into vf_seed.transit_line (line_id, line_name, transport_mode, source_ref)
values ('71:ln-1', 'ZQ 45', 'rail', 'ln_a'), ('71:ln-2', 'zq45', 'bus', 'ln_a')`); n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	check := func(line string, fams ...string) transit.LineCheck {
		t.Helper()
		d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Line: transit.FoldLine(line), Limit: 6, Now: time.Now()}, fams)
		if err != nil || d.Line == nil {
			t.Fatalf("%s: %+v %v", line, d, err)
		}
		return *d.Line
	}
	if l := check("zQ 4 5", "ln_a", "delfi_static"); !l.Checked || !l.InTimetable || !slices.Equal(l.Families, []string{"ln_a"}) {
		t.Errorf("known: %+v", l)
	}
	if l := check("ZQ 46", "ln_a"); !l.Checked || l.InTimetable || len(l.Families) != 0 {
		t.Errorf("unknown: %+v", l)
	}
	if l := check("ZQ45", "delfi_static"); !l.Checked || l.InTimetable {
		t.Errorf("another timetable's line: %+v", l)
	}
	if l := check("ZQ45", "ln_none"); l.Checked || l.InTimetable {
		t.Errorf("no timetable to ask: %+v", l)
	}
	if d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: "09998", Limit: 6, Now: time.Now()}, []string{"ln_a"}); err != nil || d.Line != nil {
		t.Errorf("no line asked: %+v %v", d.Line, err)
	}
}
