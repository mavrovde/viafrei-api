//go:build integration

package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestStateWarningsCountAsTheLandPage(t *testing.T) {
	st := seeded(t)
	fams := families(t, st, "weather")
	plantCells(t)
	var fam string
	owner(t, `select source_ref from contract.weather_warning where warning_id = 'dwd:900001'`, &fam)
	at := time.Date(2103, 1, 1, 1, 0, 0, 0, time.UTC)
	got, err := st.StateWarnings(bounded(t), "09", fams, at)
	// the Gemeinde cell's three warnings in force and the coast cell's one;
	// not the Kreis cell's, not the one starting later
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	w := got[0]
	if w.Event == nil || *w.Event != "STURMBÖEN" || w.Level != 3 || w.Notices != 4 || w.Cells != 2 || w.Until == nil ||
		!w.Until.Equal(time.Date(2103, 1, 2, 0, 0, 0, 0, time.UTC)) || !slices.Equal(w.Families, []string{fam}) {
		t.Errorf("counted %+v", w)
	}
	for name, c := range map[string]struct {
		land string
		at   time.Time
		fams []string
	}{
		"another Land":   {"08", at, fams},
		"before them":    {"09", at.Add(-2 * time.Hour), fams},
		"after them":     {"09", at.Add(48 * time.Hour), fams},
		"not its family": {"09", at, []string{"no-such-family"}},
	} {
		if got, err := st.StateWarnings(bounded(t), c.land, c.fams, c.at); err != nil || len(got) != 0 {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
}

// plantMessage plants one message carried by both alert families: the
// secondary's row first by id, the primary's naming another Land too.
func plantMessage(t *testing.T) {
	t.Helper()
	n := plant(t, `insert into contract.service_alert (alert_id, header, active_periods, line_names, district_codes, state_codes, placed_by, dedupe_key,
  source_ref, recorded_at, changed_at)
values ('delfi_rt_sa:pm-1', 'Gleisarbeiten', '[]', '{U 3}', '{09162,08111}', '{09,08}', 'stop', 'pm-key', 'delfi_rt_sa', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z'),
       ('delfi_siri_sx:pm-0', 'Gleisarbeiten (SX)', '[]', '{U 6}', '{09162}', '{09}', 'stop', 'pm-key', 'delfi_siri_sx', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z')`)
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	// the read contract takes no delete and no other test of the package
	// reads a message, so the rows stay, as the planted departures do
}

func TestStateAlertsReadAsTheLandPage(t *testing.T) {
	st := seeded(t)
	fams := families(t, st, "transit")
	for _, f := range []string{"delfi_rt_sa", "delfi_siri_sx", "delfi_sa_alpha"} {
		if !slices.Contains(fams, f) {
			t.Fatalf("%s is no transit family: %v", f, fams)
		}
	}
	plantMessage(t)
	// the fixture's first day: its route and stop alerts in force, its
	// network alert naming no stop
	at := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	rows, err := st.StateAlerts(bounded(t), "09", fams, at)
	if err != nil {
		t.Fatal(err)
	}
	got := transit.StateAlertsOf(rows, "09")
	var heads []string
	for _, a := range got {
		heads = append(heads, a.Header)
	}
	if !slices.Equal(heads, []string{"Gleisarbeiten", "Bauarbeiten auf der Linie RE 1", "Weichenstörung"}) {
		t.Fatalf("messages %v", heads)
	}
	if m := got[0]; !slices.Equal(m.Lines, []string{"U 3", "U 6"}) || !slices.Equal(m.CarriedBy, []string{"delfi_rt_sa", "delfi_siri_sx"}) {
		t.Errorf("the message carried twice: %+v", m)
	}
	// the message reaches Land 08 through its primary row only
	if rows, err := st.StateAlerts(bounded(t), "08", fams, at); err != nil || len(transit.StateAlertsOf(rows, "08")) == 0 ||
		transit.StateAlertsOf(rows, "08")[0].Header != "Gleisarbeiten" {
		t.Errorf("Land 08: %+v %v", rows, err)
	}
	// without the primary family the secondary's row is its own message
	sec := slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == "delfi_rt_sa" })
	if rows, err := st.StateAlerts(bounded(t), "09", sec, at); err != nil ||
		!slices.ContainsFunc(transit.StateAlertsOf(rows, "09"), func(a transit.StateAlert) bool { return a.Header == "Gleisarbeiten (SX)" }) {
		t.Errorf("without the primary: %+v %v", rows, err)
	}
	// two days later the stop alert's period is over
	if rows, err := st.StateAlerts(bounded(t), "09", []string{"delfi_sa_alpha"}, at.Add(48*time.Hour)); err != nil || len(rows) != 1 || rows[0].Header != "Bauarbeiten auf der Linie RE 1" {
		t.Errorf("later: %+v %v", rows, err)
	}
}
