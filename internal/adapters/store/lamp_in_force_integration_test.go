//go:build integration

package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// lampInForceSeed: four closures of the road operator's newest sweep on
// A98: one marked future (announced for next week), one whose validity
// windows are all next week, one of a ramp, and one of the carriageway in
// force now.
var lampInForceSeed = []string{
	`select vf_test.ensure_day('public.autobahn_events', now())`,
	`insert into public.autobahn_roads (road) values ('A98') on conflict do nothing`,
	`insert into public.autobahn_events (observed_at, road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, geom, extent, raw, raw_hash, source)
	 select now() - interval '1 minute', 'A98', 'closure', x, t, s, null, null, 'SRID=4326;POINT(7.7 47.6)'::geography, '47.6,7.7,47.61,7.7',
	        jsonb_build_object('description', d, 'isBlocked', 'false', 'future', f), decode(md5(x), 'hex'), 'autobahn'
	   from (values
	     ('f1', 'A98 | angekündigt', 'Lörrach -> Weil', 'true',
	      jsonb_build_array('A98: Lörrach -> Weil am Rhein, zwischen AS Lörrach-Mitte und AS Haltingen', 'Vollsperrung')),
	     ('w1', 'A98 | nachts', 'Lörrach -> Weil', 'false',
	      jsonb_build_array('Die Baustelle ist zu folgenden Zeiträumen gültig:',
	        to_char((now() + interval '7 days') at time zone 'Europe/Berlin', 'DD.MM.YY') || ' 18:00 bis zum ' || to_char((now() + interval '8 days') at time zone 'Europe/Berlin', 'DD.MM.YY') || ' 06:00 Uhr.',
	        '', 'A98: Lörrach -> Weil am Rhein, zwischen AS Lörrach-Mitte und AS Haltingen')),
	     ('r1', 'A98 | Auffahrt', null, 'false',
	      jsonb_build_array('Auffahrt auf die A98: AS Lörrach-Mitte (aus Richtung Lörrach)')),
	     ('c1', 'A98 | Vollsperrung', 'Lörrach -> Weil', 'false',
	      jsonb_build_array('A98: Lörrach -> Weil am Rhein, zwischen AS Lörrach-Mitte und AS Haltingen', 'Vollsperrung'))
	   ) v(x, t, s, f, d)`,
}

// TestLampCountsOnlyClosuresInForce: the lamp's view of a road holds an
// Autobahn closure only while it is in force: neither the one marked future
// nor the one whose validity windows lie next week gives a reason; the ramp
// closure is amber, the carriageway closure in force red (#304).
func TestLampCountsOnlyClosuresInForce(t *testing.T) {
	st, _ := private(t, lampInForceSeed...)
	now := time.Now()
	p, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A98", Direction: roads.DirectionBoth,
		Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 10}, onRoads([]string{"autobahn"}), now)
	if err != nil {
		t.Fatal(err)
	}
	fresh := []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}}
	var heads []string
	var all, ramp []ampel.Signal
	for _, m := range p.Messages {
		heads = append(heads, m.Headline)
		s, ok := m.Signal()
		if !ok {
			continue
		}
		all = append(all, s)
		if m.Headline == "A98 | Auffahrt" {
			ramp = append(ramp, s)
		}
	}
	slices.Sort(heads)
	if want := []string{"A98 | Auffahrt", "A98 | Vollsperrung"}; !slices.Equal(heads, want) {
		t.Errorf("the lamp reads %v, want %v", heads, want)
	}
	if l := ampel.Of(all, fresh).Lamp; l != ampel.LampRed {
		t.Errorf("with the carriageway closure in force: %s, want red", l)
	}
	if l := ampel.Of(ramp, fresh).Lamp; l != ampel.LampAmber {
		t.Errorf("the ramp closure alone: %s, want amber", l)
	}
}
