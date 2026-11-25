package places

import (
	"regexp"
	"slices"
	"strings"
)

// The place resolver's reading of places of interest (place.ts resolvePoi,
// judgePois, landmarkOf, organisationOf; distinguish.ts poiLabel,
// poiOptionLabels; osm-categories.ts poiCategoryWord).

func readableValue(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "_", " "), ";", ", "))
}

// poiCategoryWord is the word of a POI's key and value.
func poiCategoryWord(category, value, lang string) string {
	en := langIdx(lang) == 1
	family := "Ort"
	if en {
		family = "place"
	}
	if w, ok := poiFamilyWord[category]; ok {
		family = w[langIdx(lang)]
	}
	v := strings.TrimSpace(value)
	if v == "" || v == "yes" {
		return family
	}
	if entry, ok := poiValueWord[category][v]; ok {
		if !en {
			return entry[0]
		}
		if len(entry) > 1 {
			return entry[1]
		}
		return readableValue(v)
	}
	if !en {
		return family
	}
	return readableValue(v)
}

// osmLandName is a Land as OpenStreetMap writes it, in the language asked.
func osmLandName(land *string, lang string) *string {
	if land == nil || *land == "" {
		return nil
	}
	fold := strings.ReplaceAll(strings.ReplaceAll(*land, "-", " "), "ue", "u")
	norm := normalizeName(*land)
	for _, l := range Laender {
		if l.NameNorm == fold || l.NameNorm == norm || l.NameEnNorm == norm {
			if langIdx(lang) == 0 {
				return sp(l.Name)
			}
			return sp(l.NameEn)
		}
	}
	return land
}

var quotedName = regexp.MustCompile(`^\s*["„“”'‚‘’«»]([^"„“”«»]+)["„“”'‚‘’«»]\s*$`)

func unquotedName(name string) string {
	if m := quotedName.FindStringSubmatch(name); m != nil {
		return strings.TrimSpace(m[1])
	}
	return name
}

// poiLabel is a POI's name and where it is: its town, else its postcode,
// else its Land.
func poiLabel(h PoiHit, lang string) string {
	where := h.Place
	if where == nil {
		where = h.Postcode
	}
	if where == nil {
		where = osmLandName(h.Land, lang)
	}
	name := unquotedName(h.Name)
	if where == nil || *where == "" {
		return name
	}
	return name + ", " + *where
}

func poiOptionLabels(hits []PoiHit, lang string) []string {
	rows := make([]distinguishable, len(hits))
	for i, h := range hits {
		rows[i] = distinguishable{name: poiLabel(h, lang), tiers: []*string{
			StreetLine(h.Street, h.HouseNumber), h.Postcode, sp(poiCategoryWord(h.Category, h.CategoryValue, lang)), sp(coordinateWord(h.Lat, h.Lon)),
		}}
	}
	return distinctLabels(rows, false, nil)
}

func poiAnswer(h PoiHit, label string) Resolution {
	return Resolution{Kind: ResolvedPoint, Lat: h.Lat, Lon: h.Lon, Label: label, SourceIDs: []string{"osm"}, Via: "poi"}
}

// poiInTownLabel is a POI's label, its town named when it carries none.
func poiInTownLabel(h PoiHit, townName *string, lang string) string {
	if h.Place == nil && townName != nil {
		return h.Name + ", " + *townName
	}
	return poiLabel(h, lang)
}

// judgePois answers with the leading POI when it leads by the margin, else
// lists them.
func judgePois(hits []PoiHit, townName *string, lang string) *Resolution {
	if len(hits) == 0 {
		return nil
	}
	best := hits[0]
	if len(hits) == 1 || best.Score-hits[1].Score >= WinnerMargin {
		r := poiAnswer(best, poiInTownLabel(best, townName, lang))
		return &r
	}
	labels := poiOptionLabels(hits, lang)
	r := &Resolution{Kind: ResolvedCandidates, SourceIDs: []string{"osm"}, Via: "poi"}
	for i, h := range hits {
		label := poiLabel(h, lang)
		if i < len(labels) && labels[i] != "" {
			label = labels[i]
		}
		r.Options = append(r.Options, Option{Label: label, Kind: "poi", Lat: h.Lat, Lon: h.Lon, SourceID: "osm"})
	}
	return r
}

func landmarkOf(hits []PoiHit, townName *string, lang string) *Resolution {
	if len(hits) == 0 || hits[0].Tier < ExactLandmarkTier {
		return nil
	}
	if j := judgePois(hits, townName, lang); j.Kind == ResolvedPoint {
		return j
	}
	return nil
}

var castleHeads = []string{"schloss", "burg"}

func capitalised(w string) string { return strings.ToUpper(w[:1]) + w[1:] }

// castleVariants are a name with "Schloss" and "Burg" swapped at its first
// or last word.
func castleVariants(name string) []string {
	ws := strings.Fields(name)
	if len(ws) < 2 {
		return nil
	}
	var out []string
	for _, at := range []int{0, len(ws) - 1} {
		head := normalizeName(ws[at])
		if !slices.Contains(castleHeads, head) {
			continue
		}
		for _, other := range castleHeads {
			if other == head {
				continue
			}
			v := slices.Clone(ws)
			v[at] = capitalised(other)
			if s := strings.Join(v, " "); !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// castleForms are "Schloss <name>" and "Burg <name>" of a one- or
// two-word name with no castle word.
func castleForms(name string) []string {
	ws := strings.Fields(name)
	if len(ws) == 0 || len(ws) > 2 || slices.ContainsFunc(ws, func(w string) bool { return slices.Contains(castleHeads, normalizeName(w)) }) {
		return nil
	}
	var out []string
	for _, h := range castleHeads {
		out = append(out, capitalised(h)+" "+strings.Join(ws, " "))
	}
	return out
}

var legalForms = [][]string{
	{"haftungsbeschrankt"}, {"gmbh"}, {"mbh"}, {"ug"}, {"ag"}, {"se"}, {"kg"}, {"kgaa"}, {"ohg"}, {"gbr"}, {"eg"}, {"ek"}, {"e", "k"},
	{"ev"}, {"e", "v"}, {"co"}, {"ltd"}, {"inc"}, {"llc"}, {"plc"}, {"sa"}, {"s", "a"}, {"bv"}, {"nv"},
}

// legalCore is a folded name without its trailing legal forms.
func legalCore(folded string) string {
	ws := words(folded)
	for stripped := true; stripped; {
		stripped = false
		for _, form := range legalForms {
			if len(ws) <= len(form) {
				continue
			}
			if slices.Equal(ws[len(ws)-len(form):], form) {
				ws = ws[:len(ws)-len(form)]
				stripped = true
			}
		}
	}
	return strings.Join(ws, " ")
}

const oneSiteM = 300

var organisationKeys = setOf("office", "shop", "craft")

// organisationOf answers an organisation's one site, by name or brand.
func organisationOf(hits []PoiHit, thing string, townName *string) *Resolution {
	if len(hits) == 0 {
		return nil
	}
	best := hits[0]
	core := legalCore(normalizeName(thing))
	if core == "" {
		return nil
	}
	byBrand := func(h PoiHit) bool {
		return h.Brand != nil && slices.ContainsFunc(strings.Split(*h.Brand, ";"), func(b string) bool { return legalCore(normalizeName(b)) == core })
	}
	names := func(h PoiHit) bool { return legalCore(normalizeName(h.Name)) == core || byBrand(h) }
	if !names(best) || (!organisationKeys[best.Category] && !byBrand(best)) {
		return nil
	}
	if slices.ContainsFunc(hits, func(h PoiHit) bool { return names(h) && haversineM(best.Lat, best.Lon, h.Lat, h.Lon) > oneSiteM }) {
		return nil
	}
	r := poiAnswer(best, poiInTownLabel(best, townName, "de"))
	return &r
}

// resolvePoi looks a name up as a POI: in the town a reading names, else
// in the place after its last comma, else anywhere (with the other castle
// word too, exact landmarks first).
func (rv *resolver) resolvePoi(query string, town *commaTown, opts poiOptions) ([]PoiHit, error) {
	if town != nil {
		return rv.findPoisInTown(town.reading.part, poiTown{place: town.reading.context, point: &poiPoint{lat: town.area.best.lat, lon: town.area.best.lon}}, MaxCandidates, opts)
	}
	name, tag := query, ""
	if comma := strings.LastIndex(query, ","); comma >= 0 {
		name, tag = query[:comma], strings.TrimSpace(query[comma+1:])
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	scope := poiScope{}
	if tag != "" {
		scope.place = &tag
	}
	only := poiOptions{noFuzzyAnywhere: true}
	asked, err := rv.findPois(name, scope, MaxCandidates, only)
	if err != nil {
		return nil, err
	}
	variants := castleVariants(name)
	if len(variants) == 0 {
		return asked, nil
	}
	var order []string
	all := map[string]PoiHit{}
	put := func(h PoiHit) {
		if _, ok := all[h.ID]; !ok {
			order = append(order, h.ID)
		}
		all[h.ID] = h
	}
	for _, h := range asked {
		put(h)
	}
	for _, v := range variants {
		hits, err := rv.findPois(v, scope, MaxCandidates, only)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			put(h)
		}
	}
	merged := make([]PoiHit, 0, len(order))
	for _, id := range order {
		merged = append(merged, all[id])
	}
	slices.SortStableFunc(merged, comparePoiHits)
	var landmarks, visited []PoiHit
	for _, h := range merged {
		if h.Tier >= ExactLandmarkTier {
			landmarks = append(landmarks, h)
			if h.Category == "tourism" {
				visited = append(visited, h)
			}
		}
	}
	chosen := landmarks
	if len(visited) > 0 && len(visited) < len(landmarks) {
		chosen = visited
	}
	if len(chosen) > 0 {
		return chosen[:min(len(chosen), MaxCandidates)], nil
	}
	return merged[:min(len(merged), MaxCandidates)], nil
}
