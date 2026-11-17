//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// The bays are read as the platform's bay read decides on them: a sensor's
// state counts for three hours after its newest message, a sensor-less site
// is never a reading, the counts cover the whole radius, and only the asked
// kind is read.
func TestBaysReadAsThePlatformsBayRead(t *testing.T) {
	st := seeded(t)
	n := plant(t, `insert into contract.street_bay (bay_id, bay_kind, street, house_number, postcode, city, location, state, state_at, spaces, reserved_when, source_ref)
values ('dortmund:fresh', 'disabled', 'Kuhstraße', '12', '44137', 'Testdorf', 'SRID=4326;POINT(7.0000 50.0000)', 'free', now() - interval '1 hour', null, null, 'dortmund_parksensoren_bays'),
       ('dortmund:old', 'disabled', 'Kuhstraße', '14', '44137', 'Testdorf', 'SRID=4326;POINT(7.0010 50.0000)', 'occupied', now() - interval '4 hours', null, null, 'dortmund_parksensoren_bays'),
       ('dortmund:none', 'disabled', null, null, null, null, 'SRID=4326;POINT(7.0020 50.0000)', null, null, null, null, 'dortmund_parksensoren_bays'),
       ('dortmund:site', 'disabled', 'Markt', null, null, 'Testdorf', 'SRID=4326;POINT(7.0030 50.0000)', null, null, 3, 'werktags', 'dortmund_parksensoren_bays'),
       ('dortmund:ev', 'ev_charging', null, null, null, null, 'SRID=4326;POINT(7.0001 50.0000)', 'free', now(), null, null, 'dortmund_parksensoren_bays'),
       ('dortmund:far', 'disabled', null, null, null, null, 'SRID=4326;POINT(7.5000 50.0000)', 'free', now(), null, null, 'dortmund_parksensoren_bays')`)
	if n != 6 {
		t.Fatalf("planted %d of 6", n)
	}
	fams := []string{"dortmund_parksensoren_bays"}
	q := parking.BayQuery{At: geo.Point{Lat: 50, Lon: 7}, RadiusM: 2000, Kind: "disabled", Limit: 2, Now: time.Now()}
	b, err := st.Bays(bounded(t), q, fams)
	// Expected from the platform's rules: four disabled bays in the radius,
	// three with a sensor; only t:fresh reads free (t:old is past three
	// hours); t:site stands for 3 spaces.
	want := parking.BayCounts{Total: 4, Sensors: 3, Free: 1, Occupied: 0, StaticSites: 1, StaticSpaces: 3}
	if err != nil || b.Counts != want || len(b.Items) != 2 || b.Items[0].ID != "dortmund:fresh" || b.Items[1].ID != "dortmund:old" ||
		b.Items[0].State == nil || *b.Items[0].State != "free" || b.Items[1].State != nil || b.Items[1].MessageAt == nil {
		t.Fatalf("read %+v %v", b, err)
	}
	q.Limit = 10
	if b, err = st.Bays(bounded(t), q, fams); err != nil || len(b.Items) != 4 || b.Items[3].ID != "dortmund:site" || b.Items[3].Sensor() ||
		*b.Items[3].Spaces != 3 || b.Items[3].State != nil {
		t.Errorf("all %+v %v", b.Items, err)
	}
	q.OnlyFree = true
	if b, err = st.Bays(bounded(t), q, fams); err != nil || len(b.Items) != 1 || b.Items[0].ID != "dortmund:fresh" {
		t.Errorf("free only %+v %v", b.Items, err)
	}
	q.OnlyFree, q.Kind = false, "ev_charging"
	if b, err = st.Bays(bounded(t), q, fams); err != nil || len(b.Items) != 1 || b.Items[0].ID != "dortmund:ev" {
		t.Errorf("ev %+v %v", b.Items, err)
	}
	if b, err = st.Bays(bounded(t), q, []string{"other"}); err != nil || len(b.Items) != 0 {
		t.Errorf("another family %+v %v", b.Items, err)
	}
}
