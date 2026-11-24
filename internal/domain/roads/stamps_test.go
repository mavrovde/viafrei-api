package roads_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func fam(name string, asOf *time.Time) provenance.Family {
	return provenance.Family{Source: access.Source{Family: name}, DataAsOf: asOf}
}

// A source's Stand is its newest confirmation, never older than its data's
// instant; a source with neither has none.
func TestStampOfIsTheNewestCheck(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	t0 := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	iv, zero := 60.0, 0.0
	for _, c := range []struct {
		name     string
		f        provenance.Family
		c        *ampel.Confirmation
		ok       bool
		checked  time.Time
		interval bool
	}{
		{"confirmed after the change", fam("a", &t0), &ampel.Confirmation{At: &t1, IntervalS: &iv}, true, t1, true},
		{"confirmed at the change", fam("a", &t0), &ampel.Confirmation{At: &t0}, true, t0, false},
		{"changed after the confirmation", fam("a", &t1), &ampel.Confirmation{At: &t0, IntervalS: &iv}, true, t1, true},
		{"never confirmed", fam("a", &t0), &ampel.Confirmation{IntervalS: &zero}, true, t0, false},
		{"confirmed, no data", fam("a", nil), &ampel.Confirmation{At: &t1}, true, t1, false},
		{"no freshness row", fam("a", &t0), nil, true, t0, false},
		{"nothing", fam("a", nil), nil, false, time.Time{}, false},
	} {
		s, ok := roads.StampOf(c.f, c.c)
		if ok != c.ok || !s.CheckedAt.Equal(c.checked) || (s.IntervalS != nil) != c.interval || (ok && s.UnchangedSince != c.f.DataAsOf) {
			t.Errorf("%s: %+v %v", c.name, s, ok)
		}
	}
	got := roads.Stamps([]provenance.Family{fam("a", &t0), fam("b", nil), fam("c", nil)}, map[string]ampel.Confirmation{"c": {At: &t1}})
	if len(got) != 2 || got[0].Family != "a" || got[1].Family != "c" || !got[1].CheckedAt.Equal(t1) {
		t.Errorf("stamps %+v", got)
	}
}

// Possibly out of date past three of the source's own cycles, the limit
// itself still current; never without a stated cycle.
func TestAStampLapsesPastThreeCycles(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	t0 := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	iv := 60.0
	s := roads.Stamp{CheckedAt: t0, IntervalS: &iv}
	at := s.LapsesAt()
	if at == nil || !at.Equal(t0.Add(3*time.Minute+time.Millisecond)) {
		t.Fatalf("lapses at %v", at)
	}
	if s.PossiblyStale(t0.Add(3*time.Minute)) || !s.PossiblyStale(*at) {
		t.Error("the boundary")
	}
	if u := (roads.Stamp{CheckedAt: t0}); u.PossiblyStale(t0.Add(1000*time.Hour)) || u.LapsesAt() != nil {
		t.Error("a stamp with no cycle was judged")
	}
}

// A paused source's confirmation never lapses for the lamp, but its Stand
// still turns possibly out of date: the answer changes then.
func TestChangesAtWhenAStampLapses(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	iv := 60.0
	p := roads.Page{
		Sources:       []provenance.Family{{Source: access.Source{Family: "a"}, NeverStale: true}},
		Confirmations: map[string]ampel.Confirmation{"a": {At: &now, IntervalS: &iv, Paused: true}},
	}
	if got := p.ChangesAt(now); got == nil || !got.Equal(now.Add(3*time.Minute+time.Millisecond)) {
		t.Errorf("changes at %v", got)
	}
}
