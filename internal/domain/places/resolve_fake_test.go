package places

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// fakeReads answer the resolver's reads from rows a test writes: candidates
// by the fold asked (the literal one, else the expanded one), POIs by
// statement and name, addresses and streets by statement; a read listed in
// fail errs, and one listed in failOn errs the time its number names.
type fakeReads struct {
	cands     map[string][]GazetteerRow
	roads     map[string][]GazetteerRow
	lands     map[int]string
	kreise    map[string]string
	stops     map[string][]GazetteerRow
	junctions map[string][]NamedJunction
	lcl       []LCLPoint
	lclAt     map[float64][]LCLPoint
	pois      map[string][]PoiHit
	words     []string
	addresses map[string][]AddressHit
	streets   map[string][]StreetHit
	loaded    map[string]bool
	fail      map[string]bool
	failOn    map[string]int
	asked     []string
}

var errRead = errors.New("read failed")

func countOf(asked []string, what string) int {
	n := 0
	for _, a := range asked {
		if a == what {
			n++
		}
	}
	return n
}

func (f *fakeReads) err(what string) error {
	f.asked = append(f.asked, what)
	if f.fail[what] || (f.failOn[what] > 0 && f.failOn[what] == countOf(f.asked, what)) {
		return errRead
	}
	return nil
}

func (f *fakeReads) CandidateRows(_ context.Context, c CandidateRead) ([]GazetteerRow, error) {
	if err := f.err("candidates:" + c.Folded); err != nil {
		return nil, err
	}
	rows, ok := f.cands[c.Folded]
	if !ok {
		rows = f.cands[c.Expanded]
	}
	var out []GazetteerRow
	for _, r := range rows {
		if (c.OnlyKind == nil || *c.OnlyKind == r.Kind) && (c.OnlySource == nil || *c.OnlySource == r.SourceID) {
			out = append(out, r)
		}
	}
	return out[:min(len(out), c.Limit)], nil
}

func (f *fakeReads) RoadRows(_ context.Context, road string, limit int) ([]GazetteerRow, error) {
	if err := f.err("road:" + road); err != nil {
		return nil, err
	}
	rows := f.roads[road]
	return rows[:min(len(rows), limit)], nil
}

func (f *fakeReads) LandsNear(_ context.Context, lons, _ []float64, _ float64, _ []string) (map[int]string, error) {
	if err := f.err("lands"); err != nil {
		return nil, err
	}
	out := map[int]string{}
	for i := range lons {
		if l, ok := f.lands[i+1]; ok {
			out[i+1] = l
		}
	}
	return out, nil
}

func (f *fakeReads) KreisNames(_ context.Context, keys []string) (map[string]string, error) {
	if err := f.err("kreise"); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		if n, ok := f.kreise[k]; ok {
			out[k] = n
		}
	}
	return out, nil
}

func (f *fakeReads) ShownStops(_ context.Context, fold string, limit int) ([]GazetteerRow, error) {
	if err := f.err("stops:" + fold); err != nil {
		return nil, err
	}
	rows := f.stops[fold]
	return rows[:min(len(rows), limit)], nil
}

func (f *fakeReads) JunctionsNamed(_ context.Context, core string, _ float64, limit int) ([]NamedJunction, error) {
	if err := f.err("junctions:" + core); err != nil {
		return nil, err
	}
	rows := f.junctions[core]
	return rows[:min(len(rows), limit)], nil
}

func (f *fakeReads) LCLNear(_ context.Context, lons, _ []float64) ([][]LCLPoint, error) {
	if err := f.err("lcl"); err != nil {
		return nil, err
	}
	out := make([][]LCLPoint, len(lons))
	for i := range out {
		out[i] = f.lcl
		if at, ok := f.lclAt[lons[i]]; ok {
			out[i] = at
		}
	}
	return out, nil
}

// poiKey is a POI read's key: its statement and its first text argument.
func poiKey(r PoiRead) string {
	k := r.Statement
	for _, a := range r.Args {
		if s, ok := a.(string); ok {
			return k + ":" + s
		}
	}
	return k
}

func (f *fakeReads) PoiRows(_ context.Context, r PoiRead) ([]PoiHit, error) {
	k := poiKey(r)
	if err := f.err("poi:" + k); err != nil {
		return nil, err
	}
	if rows, ok := f.pois[k]; ok {
		return slices.Clone(rows), nil
	}
	return slices.Clone(f.pois[r.Statement]), nil
}

func (f *fakeReads) PlaceWords(_ context.Context, words []string) ([]string, bool, error) {
	if err := f.err("words:" + strings.Join(words, " ")); err != nil {
		return nil, false, err
	}
	var held []string
	for _, w := range words {
		if slices.Contains(f.words, w) {
			held = append(held, w)
		}
	}
	return held, len(f.words) > 0, nil
}

func (f *fakeReads) AddressRows(_ context.Context, stmt string, _ []any) ([]AddressHit, error) {
	if err := f.err("address:" + stmt); err != nil {
		return nil, err
	}
	return slices.Clone(f.addresses[stmt]), nil
}

func (f *fakeReads) StreetRows(_ context.Context, stmt string, _ []any) ([]StreetHit, error) {
	if err := f.err("street:" + stmt); err != nil {
		return nil, err
	}
	return slices.Clone(f.streets[stmt]), nil
}

func (f *fakeReads) IsLoaded(_ context.Context, what string) (bool, error) {
	if err := f.err("loaded:" + what); err != nil {
		return false, err
	}
	return f.loaded[what], nil
}

// row is a gazetteer row of a test.
func row(id, kind, name string, lat, lon float64, pop int, source string, score float64) GazetteerRow {
	r := GazetteerRow{ID: id, Kind: kind, Name: name, NameNorm: normalizeName(name), SourceID: source, ExternalID: id, Lat: &lat, Lon: &lon, Score: score}
	if pop > 0 {
		r.Population = &pop
	}
	return r
}

func withAdmin(r GazetteerRow, code string) GazetteerRow {
	r.AdminCode = &code
	return r
}

func withEn(r GazetteerRow, en string) GazetteerRow {
	r.NameEn = &en
	return r
}

func resolveWith(f *fakeReads, q, lang string) (Resolution, error) {
	return Resolve(context.Background(), f, q, lang)
}
