package places

import (
	"regexp"
	"slices"
	"strings"
)

// The place resolver's qualified readings: "<part>, <town>" and "<town>
// <part>", the area a town spans and the places inside it (place.ts
// contextAreaOf, qualifiedReadings, resolveInside, readQualified,
// adjectiveTown, withinArea).

// contextArea is the area a context names: its leading row, the keys of
// its municipalities, its reach and its sources.
type contextArea struct {
	best      located
	keys      map[string]bool
	radiusKm  float64
	sourceIDs []string
}

// qualified is one reading of a qualified text: the part and its context.
type qualified struct{ part, context string }

// commaTown is a reading whose context is a town, with the town's area.
type commaTown struct {
	reading qualified
	area    contextArea
}

func contextAreaOf(found []GazetteerRow, context string) *contextArea {
	var areas, containers, rural []located
	for _, c := range locate(found) {
		if c.Score >= 1-TransliterationPenalty && areaKinds[c.Kind] {
			areas = append(areas, c)
		}
	}
	if namesContainer(context) {
		for _, c := range areas {
			if containerKinds[c.Kind] {
				containers = append(containers, c)
			}
		}
	}
	rural = containers
	if namesLandkreis(context) {
		rural = nil
		for _, c := range containers {
			if !isCityKreis(c.GazetteerRow, found) {
				rural = append(rural, c)
			}
		}
	}
	exact := areas
	switch {
	case len(rural) > 0:
		exact = rural
	case len(containers) > 0:
		exact = containers
	}
	if len(exact) == 0 {
		return nil
	}
	folded := foldViews(exact)
	best := folded[0]
	if len(folded) > 1 && !answersAlone(best, folded[1:]) {
		return nil
	}
	if best.Kind == "city" && pop(best) <= 0 {
		return nil
	}
	keys := map[string]bool{}
	var views []located
	for _, r := range exact {
		self := r.ID == best.ID
		if !self && r.NameNorm != best.NameNorm {
			continue
		}
		if belongsToIsland(r, best) {
			key := municipalityKey(r)
			if self || key != "" {
				views = append(views, r)
			}
			if key != "" {
				keys[key] = true
			}
			continue
		}
		samePopulation := r.Population != nil && *r.Population > 0 && best.Population != nil && *r.Population == *best.Population
		if !self && !samePopulation && kmOf(best, r) > samePlaceKm {
			continue
		}
		key := municipalityKey(r)
		if key == "" {
			key = containerKey(r)
		}
		if self || key != "" {
			views = append(views, r)
		}
		if key != "" {
			keys[key] = true
		}
	}
	ids := make([]string, len(views))
	for i, v := range views {
		ids[i] = v.SourceID
	}
	return &contextArea{best: best, keys: keys, radiusKm: radiusOf(best), sourceIDs: credit(ids...)}
}

func sameAreaKeys(a, b *contextArea) bool {
	if len(a.keys) == 0 || len(a.keys) != len(b.keys) {
		return false
	}
	for k := range a.keys {
		if !b.keys[k] {
			return false
		}
	}
	return true
}

func insideArea(c located, area *contextArea, keyedInside []located) bool {
	if area.best.Kind == "island" {
		return kmOf(c, area.best) <= IslandReachKm
	}
	key := municipalityKey(c)
	if key == "" {
		key = containerKey(c)
	}
	if key != "" && len(area.keys) > 0 {
		for k := range area.keys {
			if len(key) >= len(k) && strings.HasPrefix(key, k) {
				return true
			}
		}
		return false
	}
	if kmOf(c, area.best) <= area.radiusKm {
		return true
	}
	return slices.ContainsFunc(keyedInside, func(k located) bool { return kmOf(c, k) <= samePlaceKm })
}

const maxReadingWords = 4

var readingSplit = regexp.MustCompile(`[\s\-–]+`)

// qualifiedReadings are the readings of a text: either side of its first
// comma, else each split of two to four words, context first then last.
func qualifiedReadings(query string) []qualified {
	if comma := strings.Index(query, ","); comma >= 0 {
		left, right := strings.TrimSpace(query[:comma]), strings.TrimSpace(query[comma+1:])
		if left == "" || right == "" {
			return nil
		}
		return []qualified{{part: left, context: right}, {part: right, context: left}}
	}
	var ws []string
	for _, w := range readingSplit.Split(strings.TrimSpace(query), -1) {
		if w != "" {
			ws = append(ws, w)
		}
	}
	if len(ws) < 2 || len(ws) > maxReadingWords {
		return nil
	}
	var out []qualified
	for i := 1; i < len(ws); i++ {
		out = append(out, qualified{context: strings.Join(ws[:i], " "), part: strings.Join(ws[i:], " ")})
	}
	for i := len(ws) - 1; i >= 1; i-- {
		out = append(out, qualified{context: strings.Join(ws[i:], " "), part: strings.Join(ws[:i], " ")})
	}
	return out
}

// placesInside is a reading's hit: its context area, the rows inside it
// and the part.
type placesInside struct {
	context *contextArea
	inside  []located
	part    string
}

type qualifiedRead struct {
	hit       *placesInside
	commaArea *contextArea
	commaRead *qualified
	spaceTown *commaTown
}

func (rv *resolver) readQualified(query string) (qualifiedRead, error) {
	contexts := map[string]*contextArea{}
	kreise := map[string]*contextArea{}
	comma := strings.Contains(query, ",")
	var out qualifiedRead
	if !comma {
		adjective, err := rv.adjectiveTown(query)
		if err != nil || adjective != nil {
			return qualifiedRead{spaceTown: adjective}, err
		}
	}
	for _, reading := range qualifiedReadings(query) {
		area, seen := contexts[reading.context]
		if !seen {
			found, err := rv.findCandidates(reading.context, MaxCandidates*2, CandidateOptions{})
			if err != nil {
				return out, err
			}
			area = contextAreaOf(found, reading.context)
			contexts[reading.context] = area
			if comma && area != nil && area.best.Kind == "city" && !namesContainer(reading.context) {
				r := contextAreaOf(found, "Landkreis "+reading.context)
				if r != nil && r.best.Kind == "district" && !sameAreaKeys(r, area) {
					kreise[reading.context] = r
				} else {
					kreise[reading.context] = nil
				}
			}
		}
		if area == nil {
			continue
		}
		if comma && out.commaArea == nil {
			rd := reading
			out.commaArea, out.commaRead = area, &rd
		}
		if !comma && out.spaceTown == nil && area.best.Kind == "city" {
			out.spaceTown = &commaTown{reading: reading, area: *area}
		}
		found, err := rv.findCandidates(reading.part, partCandidates, CandidateOptions{})
		if err != nil {
			return out, err
		}
		for _, ctx := range []*contextArea{area, kreise[reading.context]} {
			if ctx == nil {
				continue
			}
			var rows, keyedInside, inside []located
			for _, c := range locate(found) {
				if isPlausibleMatch(c.GazetteerRow, reading.part) && c.NameNorm != ctx.best.NameNorm {
					rows = append(rows, c)
				}
			}
			for _, c := range rows {
				if (municipalityKey(c) != "" || containerKey(c) != "") && insideArea(c, ctx, nil) {
					keyedInside = append(keyedInside, c)
				}
			}
			for _, c := range rows {
				if insideArea(c, ctx, keyedInside) {
					inside = append(inside, c)
				}
			}
			if len(inside) == 0 {
				continue
			}
			out.hit = &placesInside{context: ctx, inside: inside, part: reading.part}
			return out, nil
		}
	}
	return out, nil
}

// adjectiveTown reads "<Town>er <landmark>" ("Kölner Dom") as the landmark
// in the town.
func (rv *resolver) adjectiveTown(query string) (*commaTown, error) {
	ws := strings.Fields(query)
	if len(ws) < 2 || len(ws) > 3 {
		return nil, nil
	}
	for i, w := range ws {
		stems := townStems(w)
		if len(stems) == 0 {
			continue
		}
		rest := slices.Concat(ws[:i], ws[i+1:])
		part := strings.Join(rest, " ")
		if landmarkKindOf(part) == nil {
			continue
		}
		for _, stem := range stems {
			found, err := rv.findCandidates(stem, MaxCandidates*2, CandidateOptions{})
			if err != nil {
				return nil, err
			}
			if area := contextAreaOf(found, stem); area != nil && area.best.Kind == "city" {
				return &commaTown{reading: qualified{part: part, context: placeName(area.best.GazetteerRow)}, area: *area}, nil
			}
		}
	}
	return nil, nil
}

func areaName(area contextArea) *string {
	if area.best.Kind == "city" {
		return sp(placeName(area.best.GazetteerRow))
	}
	return nil
}

func largestOfTheNameInArea(folded []located, part string, ctx *contextArea) []located {
	if !containerKinds[ctx.best.Kind] && ctx.best.Kind != "admin" {
		return folded
	}
	fold := FoldQuery(part)
	var named []located
	for _, c := range folded {
		if c.NameNorm == fold && !transportKinds[c.Kind] {
			named = append(named, c)
		}
	}
	slices.SortStableFunc(named, func(a, b located) int { return pop(b) - pop(a) })
	if len(named) == 0 || pop(named[0]) <= 0 {
		return folded
	}
	if len(named) > 1 && pop(named[1]) >= pop(named[0]) {
		return folded
	}
	return named[:1]
}

type insideRead struct {
	res       *Resolution
	area      *contextArea
	exact     bool
	town      *commaTown
	spaceTown *commaTown
}

// resolveInside answers a qualified text with the places inside its
// context.
func (rv *resolver) resolveInside(query string) (insideRead, error) {
	q, err := rv.readQualified(query)
	if err != nil {
		return insideRead{}, err
	}
	out := insideRead{area: q.commaArea, spaceTown: q.spaceTown}
	if q.commaArea != nil && q.commaRead != nil {
		out.town = &commaTown{reading: *q.commaRead, area: *q.commaArea}
	}
	if q.hit == nil {
		return out, nil
	}
	ctx, inside, part := q.hit.context, q.hit.inside, q.hit.part
	folded := largestOfTheNameInArea(foldViews(inside), part, ctx)
	out.exact = folded[0].Score >= 1
	whole := FoldQuery(query)
	decided := decide(folded, rv.lang, inside, "", map[string]string{}, whole, nil)
	if namesOthers(decided) {
		lands, err := rv.landsOf(folded)
		if err != nil {
			return insideRead{}, err
		}
		kreise, err := rv.kreiseOf(folded)
		if err != nil {
			return insideRead{}, err
		}
		decided = decide(folded, rv.lang, inside, "", lands, whole, kreise)
	}
	decided.SourceIDs = credit(append(slices.Clone(decided.SourceIDs), ctx.sourceIDs...)...)
	if decided.Kind == ResolvedPoint && ctx.best.Kind == "city" && !strings.Contains(normalizeName(decided.Label), ctx.best.NameNorm) {
		decided.Label += ", " + placeName(ctx.best.GazetteerRow)
	}
	out.res = &decided
	return out, nil
}

// withinArea keeps a point inside an area, or a list's options inside it.
func withinArea(r Resolution, area contextArea) *Resolution {
	inside := func(lat, lon float64) bool {
		return distanceKm(lat, lon, area.best.lat, area.best.lon) <= area.radiusKm
	}
	switch r.Kind {
	case ResolvedPoint:
		if inside(r.Lat, r.Lon) {
			return &r
		}
		return nil
	case ResolvedCandidates:
		var options []Option
		for _, o := range r.Options {
			if inside(o.Lat, o.Lon) {
				options = append(options, o)
			}
		}
		if len(options) == 0 {
			return nil
		}
		r.Options = options
		return &r
	}
	return &r
}
