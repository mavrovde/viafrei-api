package places

import (
	"regexp"
	"slices"
	"strings"
)

// The place resolver's things in a town: its airport, a street, a landmark,
// its centre (place.ts thingInTown, landmarkInTown, townCentre), and the
// readings around them (afterEpithet, spellsOutTownName, townPart,
// withLandmarks, withLocalityLand).

func isTownItself(r Resolution, town commaTown) bool {
	if r.Kind != ResolvedPoint || r.Via != "gazetteer" || r.PlaceKind == "" || !areaKinds[r.PlaceKind] {
		return false
	}
	return distanceKm(r.Lat, r.Lon, town.area.best.lat, town.area.best.lon) <= samePlaceKm
}

func townNameOf(town commaTown) string {
	if n := areaName(town.area); n != nil {
		return *n
	}
	return placeName(town.area.best.GazetteerRow)
}

func townWordsOf(town commaTown) map[string]bool {
	out := map[string]bool{}
	for _, w := range append(strings.Split(normalizeName(townNameOf(town)), " "), strings.Split(normalizeName(town.reading.context), " ")...) {
		if w != "" && !nameJoiners[w] {
			out[w] = true
		}
	}
	return out
}

func carriesTownName(r Resolution, town commaTown) bool {
	ws := townWordsOf(town)
	carries := func(label string) bool {
		return slices.ContainsFunc(strings.Split(normalizeName(label), " "), func(w string) bool { return ws[w] })
	}
	switch r.Kind {
	case ResolvedPoint:
		return carries(r.Label)
	case ResolvedCandidates:
		return slices.ContainsFunc(r.Options, func(o Option) bool { return carries(o.Label) })
	}
	return false
}

var sameWord = map[string]string{"hauptbahnhof": "hbf", "bahnhof": "bf", "strasse": "str", "sankt": "st", "platz": "pl"}

// nameWords are a text's words as names compare them.
func nameWords(text string) []string {
	var out []string
	for _, w := range strings.Split(FoldQuery(text), " ") {
		if s, ok := sameWord[w]; ok {
			w = s
		}
		if runeLen(w) >= 2 && !nameJoiners[w] {
			out = append(out, w)
		}
	}
	return out
}

func ofThePart(r Resolution, part string) *Resolution {
	var want []string
	for _, w := range nameWords(part) {
		if runeLen(w) >= 3 {
			want = append(want, w)
		}
	}
	if len(want) == 0 {
		return &r
	}
	carries := func(label string) bool {
		have := nameWords(label)
		return slices.ContainsFunc(want, func(w string) bool {
			return slices.ContainsFunc(have, func(h string) bool {
				return h == w || strings.HasPrefix(h, w) || (runeLen(w) >= 5 && typoDistance(w, h) <= 1)
			})
		})
	}
	switch r.Kind {
	case ResolvedPoint:
		if carries(r.Label) {
			return &r
		}
		return nil
	case ResolvedCandidates:
		var options []Option
		for _, o := range r.Options {
			if carries(o.Label) {
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

var centreWords = regexp.MustCompile(`(?i)^(city|centre|center|city\s+centre|city\s+center|downtown|zentrum|innenstadt|stadtmitte|stadtzentrum|ortsmitte|ortskern|stadtkern)$`) //nolint:misspell // the words a person may type: "center" too

func thingOf(town commaTown) string {
	ws := strings.Fields(town.reading.part)
	for len(ws) > 1 && nameJoiners[normalizeName(ws[len(ws)-1])] {
		ws = ws[:len(ws)-1]
	}
	for len(ws) > 1 && nameJoiners[normalizeName(ws[0])] {
		ws = ws[1:]
	}
	return strings.Join(ws, " ")
}

func withCentreNote(r Resolution, town commaTown, lang string) Resolution {
	if r.Kind != ResolvedPoint {
		return r
	}
	thing := thingOf(town)
	if centreWords.MatchString(strings.TrimSpace(thing)) {
		return r
	}
	if langIdx(lang) == 1 {
		r.Label += ` (measured from the town centre — I found no "` + thing + `" there)`
	} else {
		r.Label += " (gemessen ab der Ortsmitte — „" + thing + "“ finde ich dort nicht)"
	}
	r.CentreOf = thing
	return r
}

func townPoint(town commaTown, label string) Resolution {
	best := town.area.best
	return Resolution{Kind: ResolvedPoint, Lat: best.lat, Lon: best.lon, Label: label, AGS: agsOf(best.GazetteerRow), SourceIDs: town.area.sourceIDs,
		Via: "gazetteer", PlaceKind: best.Kind}
}

func townCentre(town commaTown, lang string) Resolution {
	p := townPoint(town, placeName(town.area.best.GazetteerRow))
	p.ExtentKm, p.AreaCode = extentOf(town.area.best), areaOf(town.area.best)
	return withCentreNote(p, town, lang)
}

type landmarkKind struct{ words, heads []string }

var landmarkKinds = []landmarkKind{
	{[]string{"dom", "munster", "kathedrale", "kaiserdom", "cathedral", "minster"}, []string{"dom", "munster", "kathedrale", "kaiserdom", "mariendom"}},
	{[]string{"kirche", "basilika", "kapelle", "church", "basilica", "chapel"}, []string{"kirche", "basilika", "kapelle"}},
	{[]string{"schloss", "burg", "palais", "palast", "festung", "zitadelle", "residenz", "castle", "palace", "fortress", "citadel"}, []string{"schloss", "burg", "palais", "festung", "zitadelle", "residenz"}},
	{[]string{"rathaus", "town hall", "city hall"}, []string{"rathaus"}},
	{[]string{"kloster", "abtei", "abbey", "monastery", "convent"}, []string{"kloster", "abtei"}},
	{[]string{"zoo", "tierpark"}, []string{"zoo", "tierpark"}},
	{[]string{"museum"}, []string{"museum"}},
	{[]string{"stadion", "arena", "stadium"}, []string{"stadion", "arena"}},
	{[]string{"denkmal", "monument", "memorial"}, []string{"denkmal"}},
	{[]string{"tor", "gate"}, []string{"tor"}},
	{[]string{"turm", "tower"}, []string{"turm"}},
}

func landmarkKindOf(thing string) *landmarkKind {
	folded := normalizeName(thing)
	for i := range landmarkKinds {
		if slices.Contains(landmarkKinds[i].words, folded) {
			return &landmarkKinds[i]
		}
	}
	return nil
}

var (
	airportWords = setOf("flughafen", "flugplatz", "airport", "airfield")
	townAdjForm  = regexp.MustCompile(`^[a-z]{3,}er$`)
)

// townStems are the town names an adjective ("Kölner") may come from.
func townStems(word string) []string {
	w := normalizeName(word)
	if !townAdjForm.MatchString(w) {
		return nil
	}
	base := w[:len(w)-2]
	stems := []string{base, base + "en"}
	if strings.HasSuffix(w, "ner") {
		stems = append(stems, w[:len(w)-3]+"en")
	}
	var out []string
	for _, s := range stems {
		if !slices.Contains(out, s) && len(s) >= 3 {
			out = append(out, s)
		}
	}
	return out
}

func isTownAdjective(word string, townWords map[string]bool) bool {
	return slices.ContainsFunc(townStems(word), func(s string) bool { return townWords[s] })
}

// isLandmarkPlace is a POI that is a landmark a town is known by.
func isLandmarkPlace(h PoiHit) bool {
	if isIncidentalPoi(h) {
		return false
	}
	switch h.Category {
	case "historic", "natural":
		return true
	case "tourism":
		return slices.Contains([]string{"attraction", "museum", "viewpoint", "zoo", "theme_park", "gallery"}, h.CategoryValue)
	case "amenity":
		return slices.Contains([]string{"place_of_worship", "townhall", "monastery", "theatre", "exhibition_centre"}, h.CategoryValue)
	case "leisure":
		return slices.Contains([]string{"stadium", "park"}, h.CategoryValue)
	case "building":
		return slices.Contains([]string{"civic", "cathedral", "church", "castle", "government"}, h.CategoryValue)
	}
	return false
}

func landmarkGrade(h PoiHit, heads []string, townWords map[string]bool) int {
	if !isLandmarkPlace(h) {
		return 0
	}
	var ws []string
	for _, w := range strings.Split(normalizeName(h.Name), " ") {
		if w != "" && !townWords[w] && !isTownAdjective(w, townWords) && !nameJoiners[w] {
			ws = append(ws, w)
		}
	}
	if len(ws) == 0 {
		return 0
	}
	if len(ws) == 1 && slices.Contains(heads, ws[0]) {
		return 3
	}
	last := ws[len(ws)-1]
	if slices.Contains(heads, ws[0]) || slices.Contains(heads, last) || slices.ContainsFunc(heads, func(h string) bool { return strings.HasSuffix(last, h) }) {
		return 2
	}
	if slices.ContainsFunc(ws, func(w string) bool { return slices.Contains(heads, w) }) {
		return 1
	}
	return 0
}

func (rv *resolver) airportOf(town commaTown) (*Resolution, error) {
	townWords := townWordsOf(town)
	found, err := rv.findCandidates(townNameOf(town)+" Flughafen", MaxCandidates*2, CandidateOptions{})
	if err != nil {
		return nil, err
	}
	for _, c := range locate(found) {
		ws := strings.Split(c.NameNorm, " ")
		if transportKinds[c.Kind] && slices.Contains(ws, "flughafen") && slices.ContainsFunc(ws, func(w string) bool { return townWords[w] }) {
			return &Resolution{Kind: ResolvedPoint, Lat: c.lat, Lon: c.lon, Label: placeName(c.GazetteerRow), AGS: agsOf(c.GazetteerRow),
				SourceIDs: credit(c.SourceID), Via: "gazetteer", PlaceKind: c.Kind}, nil
		}
	}
	return nil, nil
}

func (rv *resolver) streetInTown(thing string, town commaTown) (*Resolution, error) {
	hits, err := rv.findStreet(normalizeStreet(thing), sp(normalizeName(townNameOf(town))), nil)
	if err != nil {
		return nil, err
	}
	return streetResolution(hits, rv.lang), nil
}

// thingInTown is what a thing in a town found (nil when nothing), and
// whether the town holds it at all.
type thingInTown struct {
	found *Resolution
	held  bool
}

func (rv *resolver) thingInTown(query string, town commaTown, noComma bool) (thingInTown, error) {
	thing := thingOf(town)
	folded := normalizeName(thing)
	if folded == "" {
		return thingInTown{}, nil
	}
	if airportWords[folded] {
		a, err := rv.airportOf(town)
		return thingInTown{found: a, held: a != nil}, err
	}
	kind := landmarkKindOf(thing)
	if noComma && kind == nil {
		street, err := rv.streetInTown(thing, town)
		if err != nil || street != nil {
			return thingInTown{found: street, held: street != nil}, err
		}
	}
	landmark, err := rv.landmarkInTown(query, thing, kind, town)
	if err != nil || landmark.found != nil || !noComma || kind == nil {
		return landmark, err
	}
	street, err := rv.streetInTown(thing, town)
	if err != nil || street == nil {
		return landmark, err
	}
	return thingInTown{found: street, held: true}, nil
}

func (rv *resolver) landmarkInTown(query, thing string, kind *landmarkKind, town commaTown) (thingInTown, error) {
	townName := townNameOf(town)
	townWords := townWordsOf(town)
	whole := strings.Join(strings.Fields(strings.ReplaceAll(query, ",", " ")), " ")
	var heads []string
	if kind != nil {
		for _, h := range kind.heads {
			if !slices.ContainsFunc(kind.heads, func(o string) bool { return o != h && strings.Contains(h, o) }) {
				heads = append(heads, h)
			}
		}
	}
	var needles []string
	seenFold := map[string]bool{}
	for _, n := range append([]string{whole, thing}, heads...) {
		f := normalizeName(n)
		if f != "" && !seenFold[f] {
			seenFold[f] = true
			needles = append(needles, strings.TrimSpace(n))
		}
	}
	radius := town.area.radiusKm * 1_000
	point := &poiPoint{lat: town.area.best.lat, lon: town.area.best.lon, radiusM: &radius}
	exactNames := setOf(normalizeName(whole), normalizeName(thing))
	isExact := func(h PoiHit) bool {
		return exactNames[normalizeName(h.Name)] || (h.Brand != nil && exactNames[normalizeName(*h.Brand)])
	}
	gradeOf := func(h PoiHit) int {
		if isExact(h) && isLandmarkPlace(h) {
			return 4
		}
		if kind == nil {
			return 0
		}
		return landmarkGrade(h, kind.heads, townWords)
	}
	var order []string
	seen := map[string]PoiHit{}
	for asked, needle := range needles {
		hits, err := rv.findPoisInTown(needle, poiTown{place: townName, point: point}, MaxCandidates*3, poiOptions{noFuzzy: true})
		if err != nil {
			return thingInTown{}, err
		}
		for _, h := range hits {
			if _, ok := seen[h.ID]; !ok {
				order = append(order, h.ID)
			}
			seen[h.ID] = h
		}
		top := 0
		for _, h := range seen {
			top = max(top, gradeOf(h))
		}
		if top >= 3 || (top >= 2 && asked+1 >= 2) {
			break
		}
	}
	type graded struct {
		h     PoiHit
		grade int
	}
	var gs []graded
	held := false
	for _, id := range order {
		h := seen[id]
		if g := gradeOf(h); g > 0 {
			gs = append(gs, graded{h, g})
		}
		if isExact(h) && !isIncidentalPoi(h) {
			held = true
		}
	}
	if len(gs) == 0 {
		return thingInTown{held: held}, nil
	}
	from := town.area.best
	slices.SortStableFunc(gs, func(a, b graded) int {
		if a.grade != b.grade {
			return b.grade - a.grade
		}
		return cmpFloat(haversineM(from.lat, from.lon, a.h.Lat, a.h.Lon), haversineM(from.lat, from.lon, b.h.Lat, b.h.Lon))
	})
	best := gs[0].h
	label := poiLabel(best, "de")
	if best.Place == nil {
		label = best.Name + ", " + townName
	}
	r := poiAnswer(best, label)
	return thingInTown{found: &r, held: true}, nil
}

var nameEpithets = []string{
	"lutherstadt", "hansestadt", "universitatsstadt", "universitaetsstadt", "reuterstadt", "barlachstadt", "fontanestadt",
	"klingenstadt", "kreisstadt", "landeshauptstadt", "wissenschaftsstadt", "residenzstadt", "bergstadt", "kurstadt",
}

// afterEpithet is a town's name after an epithet ("Hansestadt Rostock").
func afterEpithet(query string) *string {
	fold := normalizeName(query)
	for _, e := range nameEpithets {
		if strings.HasPrefix(fold, e+" ") {
			rest := strings.TrimSpace(fold[len(e)+1:])
			if runeLen(rest) >= 2 {
				return &rest
			}
			return nil
		}
	}
	return nil
}

func spellsOutTownName(lead GazetteerRow, query string) bool {
	if !areaKinds[lead.Kind] || lead.Kind == "island" {
		return false
	}
	asked := words(normalizeName(query))
	if len(asked) < 2 {
		return false
	}
	var named []string
	for _, w := range strings.Split(lead.NameNorm, " ") {
		if w != "" && !nameJoiners[w] {
			named = append(named, w)
		}
	}
	return slices.Equal(named, asked)
}

const spaceTownReach = 2

var townPartWords = regexp.MustCompile(`(?i)^(altstadt|mitte|stadtkern|ortskern|ortsmitte|old\s+town|historic\s+(?:centre|center))$`) //nolint:misspell // the words a person may type: "center" too

func townPart(town commaTown) Resolution {
	return townPoint(town, placeName(town.area.best.GazetteerRow)+", "+strings.TrimSpace(town.reading.part))
}

func nobodyLivesThere(j *judged) bool {
	n := 0
	for _, c := range j.rows {
		if c.Score >= 1 {
			n++
			if c.Kind != "city" || pop(c) > 0 {
				return false
			}
		}
	}
	return n > 0
}

var shortAdminCode = regexp.MustCompile(`^\d{2,8}$`)

func (rv *resolver) withLocalityLand(r Resolution, rows []located) (Resolution, error) {
	if r.Kind != ResolvedPoint || len(r.Alternatives) > 0 {
		return r, nil
	}
	i := slices.IndexFunc(rows, func(c located) bool { return c.lat == r.Lat && c.lon == r.Lon })
	if i < 0 {
		return r, nil
	}
	row := rows[i]
	if row.Kind != "city" || pop(row) > 0 || (row.AdminCode != nil && shortAdminCode.MatchString(*row.AdminCode)) {
		return r, nil
	}
	lands, err := rv.landsOf([]located{row})
	if err != nil {
		return r, err
	}
	land := landWord(row.GazetteerRow, rv.lang, lands[row.ID])
	if land == nil || strings.Contains(normalizeName(r.Label), normalizeName(*land)) {
		return r, nil
	}
	r.Label += " (" + *land + ")"
	return r, nil
}

const landmarkLocalityKm = 2

// withLandmarks answers a name nobody lives at with the landmark it names,
// or lists the landmarks beside the settlements.
func (rv *resolver) withLandmarks(query string, j *judged) (*Resolution, error) {
	only := poiOptions{noFuzzyAnywhere: true}
	hits, err := rv.findPois(query, poiScope{}, MaxCandidates, only)
	if err != nil {
		return nil, err
	}
	var order []string
	byID := map[string]PoiHit{}
	for _, h := range hits {
		if _, ok := byID[h.ID]; !ok {
			order = append(order, h.ID)
		}
		byID[h.ID] = h
	}
	for _, v := range castleForms(query) {
		more, err := rv.findPois(v, poiScope{}, MaxCandidates, only)
		if err != nil {
			return nil, err
		}
		for _, h := range more {
			had, ok := byID[h.ID]
			if !ok {
				order = append(order, h.ID)
			}
			if !ok || had.Tier < h.Tier {
				byID[h.ID] = h
			}
		}
	}
	var landmarks []PoiHit
	for _, id := range order {
		if h := byID[id]; h.Tier >= ExactLandmarkTier {
			landmarks = append(landmarks, h)
		}
	}
	if len(landmarks) == 0 {
		return nil, nil
	}
	var exact []located
	for _, c := range j.rows {
		if c.Score >= 1 {
			exact = append(exact, c)
		}
	}
	lone := landmarks[0]
	if len(landmarks) == 1 && !slices.ContainsFunc(exact, func(c located) bool { return haversineM(lone.Lat, lone.Lon, c.lat, c.lon) > landmarkLocalityKm*1_000 }) {
		label := lone.Name
		if w := poiCategoryWord(lone.Category, lone.CategoryValue, rv.lang); w != "" {
			label += " (" + w + ")"
		}
		r := poiAnswer(lone, label)
		return &r, nil
	}
	settlements := exact[:min(len(exact), max(1, MaxCandidates-len(landmarks)))]
	lands, err := rv.landsOf(settlements)
	if err != nil {
		return nil, err
	}
	kreise, err := rv.kreiseOf(settlements)
	if err != nil {
		return nil, err
	}
	labels := placeOptionLabels(settlements, rv.lang, FoldQuery(query), lands, kreise)
	var options []Option
	base := make([]string, len(landmarks))
	for i, h := range landmarks {
		var qs []string
		if w := poiCategoryWord(h.Category, h.CategoryValue, rv.lang); w != "" {
			qs = append(qs, w)
		}
		if l := osmLandName(h.Land, rv.lang); l != nil && *l != "" {
			qs = append(qs, *l)
		}
		base[i] = h.Name + " (" + strings.Join(qs, ", ") + ")"
	}
	for i, h := range landmarks {
		label := base[i]
		twice := 0
		for _, b := range base {
			if b == label {
				twice++
			}
		}
		if twice > 1 {
			label = label[:len(label)-1] + ", " + coordinateWord(h.Lat, h.Lon) + ")"
		}
		options = append(options, Option{Label: label, Kind: "poi", Lat: h.Lat, Lon: h.Lon, SourceID: "osm"})
	}
	for i, c := range settlements {
		label := labelsOr(labels, i, c)
		if land := landWord(c.GazetteerRow, rv.lang, lands[c.ID]); land != nil && !strings.Contains(normalizeName(label), normalizeName(*land)) {
			label += " (" + *land + ")"
		}
		options = append(options, Option{Label: label, Kind: c.Kind, Lat: c.lat, Lon: c.lon, SourceID: c.SourceID, AGS: agsOf(c.GazetteerRow), ExtentKm: extentOf(c)})
	}
	options = options[:min(len(options), MaxCandidates)]
	ids := make([]string, len(options))
	for i, o := range options {
		ids[i] = o.SourceID
	}
	return &Resolution{Kind: ResolvedCandidates, Options: options, SourceIDs: credit(ids...), Via: "gazetteer"}, nil
}
