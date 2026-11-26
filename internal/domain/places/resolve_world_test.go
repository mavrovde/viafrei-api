package places

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// testdata/resolve_world.json.gz is a synthetic world asked close to two
// thousand place texts, in both languages: the reads the store answered on
// it (by read and arguments) and what the platform's resolvePlace answered
// on the same rows, each a generated record, none written by hand. The
// world's rows are the store's testdata/resolve_world.sql.gz, which the
// store's integration test resolves the same texts over.
type world struct {
	Reads  map[string]json.RawMessage `json:"reads"`
	Expect []struct {
		Q    string     `json:"q"`
		Lang string     `json:"lang"`
		R    Resolution `json:"r"`
	} `json:"expect"`
}

func loadWorld(t *testing.T) world {
	t.Helper()
	f, err := os.Open("testdata/resolve_world.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var w world
	if err := json.NewDecoder(z).Decode(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

// replay answers each read from the world's record; failAt makes the
// read of that number (from 1) fail, and calls counts them.
type replay struct {
	got    map[string]json.RawMessage
	failAt int
	calls  int
	failed string
}

func (r *replay) get(key string, args []any, v any) error {
	r.calls++
	if r.calls == r.failAt {
		r.failed = key
		return errRead
	}
	a, _ := json.Marshal(args)
	if b, ok := r.got[key+"|"+string(a)]; ok {
		return json.Unmarshal(b, v)
	}
	return nil
}

func (r *replay) CandidateRows(_ context.Context, c CandidateRead) (v []GazetteerRow, err error) {
	return v, r.get("CandidateRows", []any{c}, &v)
}

func (r *replay) RoadRows(_ context.Context, road string, limit int) (v []GazetteerRow, err error) {
	return v, r.get("RoadRows", []any{road, limit}, &v)
}

func (r *replay) LandsNear(_ context.Context, lons, lats []float64, w float64, ids []string) (v map[int]string, err error) {
	return v, r.get("LandsNear", []any{lons, lats, w, ids}, &v)
}

func (r *replay) KreisNames(_ context.Context, keys []string) (v map[string]string, err error) {
	return v, r.get("KreisNames", []any{keys}, &v)
}

func (r *replay) ShownStops(_ context.Context, fold string, limit int) (v []GazetteerRow, err error) {
	return v, r.get("ShownStops", []any{fold, limit}, &v)
}

func (r *replay) JunctionsNamed(_ context.Context, core string, floor float64, limit int) (v []NamedJunction, err error) {
	return v, r.get("JunctionsNamed", []any{core, floor, limit}, &v)
}

func (r *replay) LCLNear(_ context.Context, lons, lats []float64) (v [][]LCLPoint, err error) {
	if err = r.get("LCLNear", []any{lons, lats}, &v); err == nil && v == nil {
		v = make([][]LCLPoint, len(lons))
	}
	return v, err
}

func (r *replay) PoiRows(_ context.Context, p PoiRead) (v []PoiHit, err error) {
	return v, r.get("PoiRows", []any{p}, &v)
}

func (r *replay) PlaceWords(_ context.Context, words []string) ([]string, bool, error) {
	var v []json.RawMessage
	if err := r.get("PlaceWords", []any{words}, &v); err != nil {
		return nil, false, err
	}
	var held []string
	var loaded bool
	if len(v) == 2 {
		_ = json.Unmarshal(v[0], &held)
		_ = json.Unmarshal(v[1], &loaded)
	}
	return held, loaded, nil
}

func (r *replay) AddressRows(_ context.Context, stmt string, args []any) (v []AddressHit, err error) {
	return v, r.get("AddressRows", []any{stmt, args}, &v)
}

func (r *replay) StreetRows(_ context.Context, stmt string, args []any) (v []StreetHit, err error) {
	return v, r.get("StreetRows", []any{stmt, args}, &v)
}

func (r *replay) IsLoaded(_ context.Context, what string) (v bool, err error) {
	return v, r.get("Loaded", []any{what}, &v)
}

// sameResolution compares two resolutions with an empty list and none alike.
func sameResolution(a, b Resolution) bool {
	for _, r := range []*Resolution{&a, &b} {
		if len(r.SourceIDs) == 0 {
			r.SourceIDs = nil
		}
		if len(r.Alternatives) == 0 {
			r.Alternatives = nil
		}
		if len(r.Options) == 0 {
			r.Options = nil
		}
	}
	return reflect.DeepEqual(a, b)
}

// One subtest a text, each a leaf within the 2 s bound (TST-012), where the
// whole world in one test took 16 s. Not t.Parallel: thousands of parallel
// leaves under -race took every CPU of the runner and starved other packages'
// leaves past the bound (#249); one after the other they hold one CPU.
func TestResolveTheWorldAsThePlatformDoes(t *testing.T) {
	w := loadWorld(t)
	for i, e := range w.Expect {
		t.Run(fmt.Sprintf("%04d-%s", i, e.Lang), func(t *testing.T) {
			got, err := Resolve(context.Background(), &replay{got: w.Reads}, e.Q, e.Lang)
			if err != nil || !sameResolution(got, e.R) {
				t.Errorf("%s %q:\n got  %+v %v\n want %+v", e.Lang, e.Q, got, err, e.R)
			}
		})
	}
}

// Every read a resolution makes, failing, fails the resolution, but the
// LCL read: a junction's road then stays unknown, as the platform reads it.
// One subtest a text, one leaf a failing read, each within the 2 s bound
// (TST-012), one after the other for the reason above (#249).
func TestResolveTheWorldWithEachReadFailing(t *testing.T) {
	w := loadWorld(t)
	for i, e := range w.Expect {
		t.Run(fmt.Sprintf("%04d-%s", i, e.Lang), func(t *testing.T) {
			count := &replay{got: w.Reads}
			if _, err := Resolve(context.Background(), count, e.Q, e.Lang); err != nil {
				t.Fatal(err)
			}
			// One leaf a failing read: a text makes up to 25 reads, and all
			// of them in one leaf passed the bound on a loaded runner.
			for k := 1; k <= count.calls; k++ {
				t.Run(fmt.Sprintf("read%02d", k), func(t *testing.T) {
					r := &replay{got: w.Reads, failAt: k}
					if _, err := Resolve(context.Background(), r, e.Q, e.Lang); err == nil && r.failed != "LCLNear" {
						t.Errorf("%q: read %d (%s) failing: no error", e.Q, k, r.failed)
					}
				})
			}
		})
	}
}
