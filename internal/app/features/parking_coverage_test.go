package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// parkSeq answers each parking read with the next of its answers and records
// the kind each read asked for.
type parkSeq struct {
	*fake
	reads [][]parking.Site
	errs  []error
	kinds []string
}

func (p *parkSeq) ParkingNear(_ context.Context, q parking.Query, _, _ []string, _ time.Time) ([]parking.Site, bool, *time.Time, error) {
	i := len(p.kinds)
	p.kinds = append(p.kinds, q.Kind)
	return p.reads[i], false, nil, p.errs[i]
}

func parkService(p *parkSeq) *features.Service {
	return features.New(features.Repositories{Register: p, Parking: p}, features.Settings{Language: "en"}, func() time.Time { return now })
}

// An empty answer credits the coverage of the kind asked, a gated family
// left out; with none of that kind, the coverage of any kind; a fault of
// a coverage read is the answer's error.
func TestParkingCoverageFallsBackToAnyKind(t *testing.T) {
	ctx := context.Background()
	k := key("commercial", allScopes, everything...)
	p := &parkSeq{fake: &fake{}, reads: [][]parking.Site{nil, {{ID: "g", Family: "p2"}}, {{ID: "a", Family: "p1"}}}, errs: make([]error, 3)}
	a, err := parkService(p).Parking(ctx, k, parking.Query{Kind: "lot"})
	if err != nil || len(a.Items) != 0 || !slices.Equal(names(a.Families), []string{"p1"}) || !slices.Equal(p.kinds, []string{"lot", "lot", ""}) {
		t.Fatalf("%v %v, kinds %q", names(a.Families), err, p.kinds)
	}
	p = &parkSeq{fake: &fake{}, reads: [][]parking.Site{nil, nil, nil}, errs: []error{nil, nil, errStore}}
	if _, err := parkService(p).Parking(ctx, k, parking.Query{Kind: "lot"}); !errors.Is(err, errStore) {
		t.Fatalf("coverage fault: %v", err)
	}
	p = &parkSeq{fake: &fake{}, reads: [][]parking.Site{nil, nil}, errs: []error{nil, errStore}}
	if _, err := parkService(p).Parking(ctx, k, parking.Query{}); !errors.Is(err, errStore) {
		t.Fatalf("coverage fault of any kind: %v", err)
	}
}

// A site of a family the read was not asked for is a gate violation.
func TestParkingRefusesASiteOutsideTheListedFamilies(t *testing.T) {
	p := &parkSeq{fake: &fake{}, reads: [][]parking.Site{{{ID: "x", Family: "e1"}}}, errs: make([]error, 1)}
	if _, err := parkService(p).Parking(context.Background(), key("commercial", allScopes, everything...), parking.Query{}); !errors.Is(err, features.ErrGateViolation) {
		t.Fatalf("%v", err)
	}
}

// A speed-camera answer listing none credits no register.
func TestSpeedCamerasCreditNoneWhenNoneIsListed(t *testing.T) {
	a, err := speedService(&speedFake{}).SpeedCameras(context.Background(), key("commercial", []access.Scope{"roads"}, "f1"), roads.SpeedQuery{Limit: 20})
	if err != nil || a.Meta.Families != nil {
		t.Fatalf("%+v %v", a.Meta, err)
	}
}
