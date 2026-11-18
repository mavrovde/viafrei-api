package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

type lorryRepo struct {
	*fake
	rules []roads.LorryRule
	err   error
	got   []string
}

func (l *lorryRepo) LorryRules(_ context.Context, _ roads.LorryQuery, fams []string) ([]roads.LorryRule, error) {
	l.got = fams
	return l.rules, l.err
}

// LorryRules reads the roads families the key may read and credits the
// families of the rules it lists; a store fault is the answer's error.
func TestLorryRulesCreditsTheListedFamilies(t *testing.T) {
	l := &lorryRepo{fake: &fake{}, rules: []roads.LorryRule{{ID: "1", Family: "rd"}}}
	s := features.New(features.Repositories{Register: l, Lorry: l}, features.Settings{Language: "en"}, func() time.Time { return now })
	k := key("commercial", []access.Scope{"roads"}, "rd")
	got, err := s.LorryRules(context.Background(), k, roads.LorryQuery{Vehicle: "lorry"})
	if err != nil || len(got.Rules) != 1 || !slices.Equal(l.got, []string{"rd"}) || !slices.Equal(names(got.Meta.Families), []string{"rd"}) {
		t.Fatalf("%+v %v, read %v", got, err, l.got)
	}
	l.err = errStore
	if _, err := s.LorryRules(context.Background(), k, roads.LorryQuery{}); !errors.Is(err, errStore) {
		t.Fatalf("store: %v", err)
	}
}

// Sources lists the families the key may read, of one domain or of all;
// a domain the key may not read leaves the answer partial; a key that may
// read nothing gets none; register and provenance faults are errors.
func TestSourcesListsWhatTheKeyMayRead(t *testing.T) {
	ctx := context.Background()
	f := &fake{}
	s := service(f, nil)
	k := key("commercial", []access.Scope{"energy", "places"}, everything...)
	fams, meta, err := s.Sources(ctx, k, "energy")
	if err != nil || !slices.Equal(names(fams), []string{"e1"}) || !meta.Partial || !meta.At.Equal(now) {
		t.Fatalf("energy: %v %+v %v", names(fams), meta, err)
	}
	fams, meta, err = s.Sources(ctx, k, "")
	if err != nil || !slices.Contains(names(fams), "pl") || !meta.Partial {
		t.Fatalf("all: %v %+v %v", names(fams), meta, err)
	}
	if fams, meta, err = s.Sources(ctx, key("commercial", nil, "e1"), "parking"); err != nil || fams != nil || !meta.Partial {
		t.Fatalf("none: %v %+v %v", fams, meta, err)
	}
	if fams, meta, err = s.Sources(ctx, k, "no-such-domain"); err != nil || fams != nil || meta.Partial {
		t.Fatalf("no domain: %v %+v %v", fams, meta, err)
	}
	f.provErr = errStore
	if _, _, err := s.Sources(ctx, k, "energy"); !errors.Is(err, errStore) {
		t.Fatalf("provenance: %v", err)
	}
	f.regErr = errStore
	if _, _, err := s.Sources(ctx, k, "energy"); !errors.Is(err, errStore) {
		t.Fatalf("register: %v", err)
	}
}
