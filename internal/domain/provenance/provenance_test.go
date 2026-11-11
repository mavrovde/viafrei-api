package provenance_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var now = time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)

func at(d time.Duration) *time.Time {
	t := now.Add(-d)
	return &t
}

func TestMapNoteKind(t *testing.T) {
	req.Covers(t, "ENV-051")
	for raw, want := range map[string]provenance.NoteKind{
		"purpose": provenance.Purpose, "condition": provenance.Condition, "x_unmapped": provenance.Condition, "": provenance.Condition,
	} {
		if got := provenance.MapNoteKind(raw); got != want {
			t.Errorf("MapNoteKind(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAssess(t *testing.T) {
	req.Covers(t, "ENV-003", "FR-ENERGY-004")
	berlin := time.FixedZone("CET", 3600)
	local := now.Add(-90 * time.Second).In(berlin)
	ahead := now.Add(time.Minute)
	cases := []struct {
		name     string
		asOf     *time.Time
		age      *int64
		stale    bool
		wantAsOf *time.Time
	}{
		{"fresh", &local, ptr(int64(90)), false, ptr(now.Add(-90 * time.Second))},
		{"at the threshold", at(300 * time.Second), ptr(int64(300)), false, at(300 * time.Second)},
		{"one second over", at(301 * time.Second), ptr(int64(301)), true, at(301 * time.Second)},
		{"sub-second ages truncate", at(300*time.Second + 999*time.Millisecond), ptr(int64(300)), false, at(300*time.Second + 999*time.Millisecond)},
		{"dated after now", &ahead, ptr(int64(0)), false, &ahead},
		{"never published", nil, nil, true, nil},
	}
	for _, c := range cases {
		f := provenance.Family{Source: access.Source{Family: "f"}, StaleAfter: 300 * time.Second, DataAsOf: c.asOf}
		got := f.Assess(now)
		if got.Family != "f" || got.StaleAfterS != 300 || got.Stale != c.stale || !reflect.DeepEqual(got.AgeS, c.age) || !reflect.DeepEqual(got.DataAsOf, c.wantAsOf) {
			t.Errorf("%s: %+v (age %v, asOf %v)", c.name, got, deref(got.AgeS), got.DataAsOf)
		}
		if got.DataAsOf != nil && got.DataAsOf.Location() != time.UTC {
			t.Errorf("%s: dataAsOf is not UTC", c.name)
		}
	}
}

func TestAsOfIsTheOldest(t *testing.T) {
	req.Covers(t, "ENV-004")
	a, b, c := at(10*time.Second), at(30*time.Second), at(20*time.Second)
	if got := provenance.AsOf([]provenance.Freshness{{DataAsOf: a}, {DataAsOf: b}, {DataAsOf: c}}); got != b {
		t.Errorf("AsOf = %v, want the oldest %v", got, b)
	}
	if got := provenance.AsOf([]provenance.Freshness{{DataAsOf: b}, {DataAsOf: a}}); got != b {
		t.Errorf("AsOf = %v, want the oldest first entry %v", got, b)
	}
	if got := provenance.AsOf([]provenance.Freshness{{DataAsOf: a}, {}}); got != nil {
		t.Errorf("AsOf with an unknown instant = %v, want nil", got)
	}
	if got := provenance.AsOf(nil); got != nil {
		t.Errorf("AsOf of nothing = %v", got)
	}
}

func TestNotesAreDistinctAndOrdered(t *testing.T) {
	req.Covers(t, "ENV-003")
	fams := []provenance.Family{
		{Notes: []provenance.Note{{provenance.Purpose, "p1"}, {provenance.Condition, "c1"}, {provenance.Purpose, "p1"}}},
		{Notes: []provenance.Note{{provenance.Condition, "c2"}, {provenance.Condition, "c1"}, {provenance.Purpose, "p2"}}},
	}
	if got := provenance.Notes(fams, provenance.Purpose); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Errorf("purpose %v", got)
	}
	if got := provenance.Notes(fams, provenance.Condition); !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Errorf("condition %v", got)
	}
	if got := provenance.Notes(nil, provenance.Condition); got == nil || len(got) != 0 {
		t.Errorf("no notes = %#v, want an empty list", got)
	}
}

func TestAnyStale(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004")
	if provenance.AnyStale([]provenance.Freshness{{}, {}}) || provenance.AnyStale(nil) {
		t.Error("fresh entries reported stale")
	}
	if !provenance.AnyStale([]provenance.Freshness{{}, {Stale: true}}) {
		t.Error("a stale entry not reported")
	}
}

func ptr[T any](v T) *T { return &v }

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestWithoutDropsOneKindAndLeavesTheFamily(t *testing.T) {
	c := provenance.Note{Kind: provenance.Condition, Text: "c"}
	p := provenance.Note{Kind: provenance.Purpose, Text: "p"}
	f := provenance.Family{Source: access.Source{Family: "x"}, Notes: []provenance.Note{c, p, c}}
	got := f.Without(provenance.Condition)
	if got.Source.Family != "x" || len(got.Notes) != 1 || got.Notes[0] != p {
		t.Errorf("without conditions: %+v", got)
	}
	if len(f.Notes) != 3 || f.Notes[0] != c {
		t.Errorf("the original was changed: %+v", f.Notes)
	}
	if got := f.Without(provenance.Purpose); len(got.Notes) != 2 || got.Notes[0] != c || got.Notes[1] != c {
		t.Errorf("without purposes: %+v", got)
	}
}
