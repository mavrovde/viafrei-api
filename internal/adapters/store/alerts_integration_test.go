//go:build integration

package store_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The region's alerts are read as the platform's alert read decides on them:
// only of a publication alive now, a secondary row only when delivered again
// within the last day, and a secondary row sharing a primary row's dedupe key
// or message number counted as its duplicate and left out.
func TestRegionAlertsKeepOnlyLivePublicationsAndDropDuplicates(t *testing.T) {
	st := seeded(t)
	for _, src := range []struct {
		key, ago string
		cadence  int
	}{{"sa_live", "1 minute", 300}, {"sx_live", "10 minutes", 3600}, {"sa_dead", "2 hours", 300}} {
		plant(t, `insert into vf_seed.source (source_ref, licence_code, attribution_text, freshness_target_s)
values ('`+src.key+`', 'CC BY-SA 4.0', 'Test', `+strconv.Itoa(src.cadence)+`)`)
		plant(t, `insert into vf_seed.source_freshness (source_ref, data_as_of, confirmed_at, confirm_interval_s)
values ('`+src.key+`', now() - interval '`+src.ago+`', now() - interval '`+src.ago+`', `+strconv.Itoa(src.cadence)+`)`)
	}
	n := plant(t, `insert into vf_seed.service_alert (alert_id, header, district_codes, state_codes, placed_by, dedupe_key, message_ref, primary_source, seen_at, source_ref)
values ('sa_live:a1', 'Bauarbeiten', '{09998}', '{09}', 'stop', 'k1', null, true, now(), 'sa_live'),
       ('sa_live:a2', 'Umleitung', '{09998}', '{09}', 'stop', 'k2', 'm-2', true, now(), 'sa_live'),
       ('sx_live:s1', 'Bauarbeiten', '{09998}', '{09}', 'stop', 'k1', null, false, now(), 'sx_live'),
       ('sx_live:s2', 'Umleitung (SX)', '{09998}', '{09}', 'stop', 'k9', 'm-2', false, now(), 'sx_live'),
       ('sx_live:s3', 'Aufzug', '{09998}', '{09}', 'stop', 'k3', null, false, now(), 'sx_live'),
       ('sx_live:s4', 'Alt', '{09998}', '{09}', 'stop', 'k4', null, false, now() - interval '2 days', 'sx_live'),
       ('sa_dead:d1', 'Tot', '{09998}', '{09}', 'stop', 'k5', null, true, now(), 'sa_dead')`)
	if n != 7 {
		t.Fatalf("planted %d of 7", n)
	}
	fams := []string{"sa_live", "sx_live", "sa_dead"}
	q := transit.DisruptionQuery{Area: "09998", Limit: 2, Now: time.Now()}
	d, err := st.Disruptions(bounded(t), q, fams)
	ids := func(as []transit.Alert) []string {
		var out []string
		for _, a := range as {
			out = append(out, a.ID)
		}
		slices.Sort(out)
		return out
	}
	// Expected from the platform's rules, not from the code under test:
	// a1, a2, s3 in force and unique; s1 and s2 duplicates; s4 not repeated
	// for a day; d1 of a dead publication.
	if err != nil || !d.AlertsChecked || d.AlertsTotal != 3 || d.AlertsDuplicates != 2 || len(d.Alerts) != 2 ||
		!slices.Equal(d.AlertFamilies, []string{"sa_live", "sx_live"}) {
		t.Fatalf("read %+v %v", d, err)
	}
	// The Land's coverage: every stop-placed alert seen within 14 days, of
	// every family asked, a dead publication's too; a Land none placed one
	// in is not covered and was never seen.
	if c := d.Coverage; c == nil || !c.Covered || c.LookbackDays != 14 || len(c.Lands) != 1 || c.Lands[0].Land != "09" ||
		c.Lands[0].AlertsSeen != 7 || c.Lands[0].LastSeenAt == nil {
		t.Errorf("coverage %+v", d.Coverage)
	}
	if d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: "01", Limit: 2, Now: time.Now()}, fams); err != nil || d.Coverage == nil ||
		d.Coverage.Covered || d.Coverage.Lands[0].Land != "01" || d.Coverage.Lands[0].AlertsSeen != 0 || d.Coverage.Lands[0].LastSeenAt != nil {
		t.Errorf("an uncovered Land: %+v %v", d.Coverage, err)
	}
	q.Limit = 10
	if d, err := st.Disruptions(bounded(t), q, fams); err != nil || !slices.Equal(ids(d.Alerts), []string{"sa_live:a1", "sa_live:a2", "sx_live:s3"}) {
		t.Errorf("listed %v %v", ids(d.Alerts), err)
	}
	// Only the dead publication: nothing is listed and nothing was checked.
	if d, err := st.Disruptions(bounded(t), q, []string{"sa_dead"}); err != nil || d.AlertsChecked || d.AlertsTotal != 0 || len(d.Alerts) != 0 {
		t.Errorf("dead: %+v %v", d, err)
	}
	// The secondary alone: its rows are no duplicates without the primary read.
	if d, err := st.Disruptions(bounded(t), q, []string{"sx_live"}); err != nil || d.AlertsDuplicates != 0 || d.AlertsTotal != 3 {
		t.Errorf("secondary alone: %+v %v", d, err)
	}
}
