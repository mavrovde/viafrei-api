package places

import (
	"math"
	"regexp"
	"slices"
	"strings"
)

// The place resolver's search of OpenStreetMap's places of interest
// (poi-search.ts findOsmPois, findOsmPoisInTown), as the resolver asks it.

// PoiHit is a place of interest a name search found, with its score, tier
// (exact, landmark and whole-word matches) and vicinity (how many places
// lie around an exact landmark).
type PoiHit struct {
	ID, Name, Category, CategoryValue                           string
	Brand, Operator, Street, HouseNumber, Postcode, Place, Land *string
	Lat, Lon                                                    float64
	ExtractDate                                                 string
	DistanceM                                                   *float64
	Score                                                       float64
	Tier, Vicinity                                              int
}

// PoiRead is one read of a POI statement: which statement, and its
// parameters in the platform's order.
type PoiRead struct {
	Statement string
	Args      []any
}

// The POI statements the resolver reads.
const (
	PoiAnywhere         = "anywhere"
	PoiByPlace          = "by_place"
	PoiByPlaceFuzzy     = "by_place_fuzzy"
	PoiNear             = "near"
	PoiNearFuzzy        = "near_fuzzy"
	PoiCategoryByPlace  = "category_by_place"
	PoiCategoryNear     = "category_near"
	PoiCategoryNearest  = "category_nearest"
	poiOverfetch        = 3
	poiScoreFloor       = 0.3
	poiFuzzyFloor       = 0.4
	ExactLandmarkTier   = 6
	categoryRadiusM     = 10_000
	categoryTier        = 4
	namedKindTier       = 6
	poiTownRadiusM      = 5_000
	poiTownRadiusMaxM   = 10_000
	sameObjectM         = 150
	poiFloorIndexPlace  = 4
	poiFloorIndexNear   = 5
	poiFloorIndexAnyway = 2
)

func comparePoiHits(a, b PoiHit) int {
	if a.Tier != b.Tier {
		return b.Tier - a.Tier
	}
	if a.Score != b.Score {
		return cmpFloat(b.Score, a.Score)
	}
	inf := math.Inf(1)
	da, db := inf, inf
	if a.DistanceM != nil {
		da = *a.DistanceM
	}
	if b.DistanceM != nil {
		db = *b.DistanceM
	}
	if near := da - db; near != 0 && !math.IsNaN(near) {
		return cmpFloat(da, db)
	}
	return b.Vicinity - a.Vicinity
}

var motoringTags = map[string][]string{
	"amenity":  {"fuel", "charging_station", "car_wash", "parking", "parking_entrance", "parking_space", "motorcycle_parking", "bicycle_parking"},
	"building": {"parking", "garages", "carport"},
	"shop":     {"fuel"},
}

func isMotoringPoi(h PoiHit) bool { return slices.Contains(motoringTags[h.Category], h.CategoryValue) }

var (
	motoringWords = regexp.MustCompile(`(^| )(tank|tanke|tanken|fuel|petrol|gas station|filling station|diesel|benzin|charging|charger|chargers|supercharger|parken|parking|car park|carpark|garage|garagen|car wash|carwash)( |$)`)
	motoringHeads = regexp.MustCompile(`(tankstelle|tankstellen|ladesaule|ladesaulen|ladestation|ladepunkt|ladepark|parkhaus|parkplatz|parkplatze|tiefgarage|parkgarage|waschanlage|waschstrasse|autowasche)$`)
	parcelShop    = regexp.MustCompile(`(^| )(dpd|hermes|gls|ups|fedex)( |$)`)
)

func asksMotoringKind(raw string) bool {
	folded := normalizeName(raw)
	return motoringWords.MatchString(folded) || slices.ContainsFunc(strings.Split(folded, " "), motoringHeads.MatchString)
}

var incidentalValues = []string{"taxi", "parking", "parking_entrance", "parking_space", "bicycle_parking", "motorcycle_parking",
	"charging_station", "car_sharing", "bicycle_rental", "post_box", "atm", "vending_machine",
	"telephone", "bench", "shelter", "toilets", "waste_basket", "recycling", "drinking_water",
	"clock", "loading_dock", "parcel_locker", "ticket_validator"}

func isIncidentalPoi(h PoiHit) bool {
	return (h.Category == "amenity" && slices.Contains(incidentalValues, h.CategoryValue)) || (h.Category == "tourism" && h.CategoryValue == "information")
}

func isCarPark(category, value string) bool {
	return category == "amenity" && (value == "parking" || value == "parking_entrance" || value == "parking_space")
}

func isParcelShop(h PoiHit) bool {
	for _, t := range []*string{&h.Name, h.Brand, h.Operator} {
		if t != nil && parcelShop.MatchString(normalizeName(*t)) {
			return true
		}
	}
	return false
}

// tag is an OpenStreetMap key and value.
type tag [2]string

type nameMust struct {
	values  []string
	pattern string
}

type categoryHint struct {
	heads     []string
	tags      []tag
	notAfter  []string
	compounds bool
	must      *nameMust
}

func hint(heads []string, tags []tag) categoryHint {
	return categoryHint{heads: heads, tags: tags, compounds: true}
}

var categoryHints = func() []categoryHint {
	h := []categoryHint{
		hint([]string{"museum"}, []tag{{"tourism", "museum"}}),
		hint([]string{"klinikum", "klinik", "krankenhaus", "hospital", "uniklinik", "clinic"}, []tag{{"amenity", "hospital"}, {"healthcare", "hospital"}, {"amenity", "clinic"}, {"healthcare", "clinic"}}),
		hint([]string{"rathaus", "town hall", "city hall"}, []tag{{"amenity", "townhall"}}),
		hint([]string{"schwimmbad", "hallenbad", "freibad", "spassbad", "erlebnisbad", "stadtbad", "therme", "swimming pool", "swimming baths", "indoor pool", "outdoor pool", "lido", "pool"}, []tag{{"leisure", "swimming_pool"}, {"leisure", "water_park"}, {"amenity", "public_bath"}, {"leisure", "sports_centre"}}),
		hint([]string{"zoo", "tierpark"}, []tag{{"tourism", "zoo"}}),
		hint([]string{"bibliothek", "bucherei", "library"}, []tag{{"amenity", "library"}}),
		hint([]string{"theater", "theatre"}, []tag{{"amenity", "theatre"}}),                              //nolint:misspell // the words a person may type, German and English spellings both
		hint([]string{"kino", "cinema", "movie theater", "movie theatre"}, []tag{{"amenity", "cinema"}}), //nolint:misspell // the words a person may type, German and English spellings both
		hint([]string{"stadion", "stadium"}, []tag{{"leisure", "stadium"}}),
		hint([]string{"apotheke", "pharmacy"}, []tag{{"amenity", "pharmacy"}, {"healthcare", "pharmacy"}}),
		hint([]string{"polizei", "polizeiprasidium", "polizeirevier", "police", "police station"}, []tag{{"amenity", "police"}}),
		hint([]string{"feuerwache", "feuerwehr", "fire station"}, []tag{{"amenity", "fire_station"}}),
		hint([]string{"universitat", "hochschule", "university"}, []tag{{"amenity", "university"}, {"building", "university"}}),
		hint([]string{"kirche", "dom", "munster", "kathedrale", "moschee", "synagoge", "church", "cathedral", "mosque", "synagogue"}, []tag{{"amenity", "place_of_worship"}}),
		hint([]string{"schloss", "burg", "castle"}, []tag{{"historic", "castle"}}),
		hint([]string{"friedhof", "cemetery", "graveyard"}, []tag{{"amenity", "grave_yard"}}),
		hint([]string{"messe", "messegelande", "messezentrum", "ausstellungsgelande", "trade fair", "trade fair grounds", "trade fair ground", "fairground", "fairgrounds", "exhibition centre", "exhibition center", "exhibition grounds"}, []tag{{"amenity", "exhibition_centre"}}), //nolint:misspell // the words a person may type: "center" too
		hint([]string{"werk", "werke", "werksgelande", "fabrik", "factory", "plant", "works", "car plant"}, []tag{{"industrial", "factory"}}),
		hint([]string{"hotel", "hotels"}, []tag{{"tourism", "hotel"}, {"building", "hotel"}}),
		hint([]string{"kinderarzt", "kinderarztin", "kinderarzte", "kinderarztpraxis", "kinder und jugendarzt", "paediatrician", "pediatrician", "children s doctor"}, []tag{{"amenity", "doctors"}, {"healthcare", "doctor"}}), //nolint:misspell // the words a person may type, German and English spellings both
		hint([]string{"zahnarzt", "zahnarztin", "zahnarzte", "zahnarztpraxis", "zahnklinik", "dentist", "dentists", "dental practice"}, []tag{{"amenity", "dentist"}, {"healthcare", "dentist"}}),
		hint([]string{"tierarzt", "tierarztin", "tierarzte", "tierarztpraxis", "tierklinik", "vet", "vets", "veterinary", "veterinarian", "animal hospital"}, []tag{{"amenity", "veterinary"}}),
		hint([]string{"arzt", "arztin", "arzte", "arztpraxis", "hausarzt", "hausarztin", "allgemeinmediziner", "doctor", "doctors", "gp", "general practitioner", "medical practice"}, []tag{{"amenity", "doctors"}, {"healthcare", "doctor"}}),
		hint([]string{"toilette", "toiletten", "wc", "offentliche toilette", "toilet", "toilets", "public toilet", "public toilets", "restroom", "restrooms"}, []tag{{"amenity", "toilets"}}),
		hint([]string{"geldautomat", "geldautomaten", "bankautomat", "atm", "atms", "cash machine", "cashpoint"}, []tag{{"amenity", "atm"}, {"amenity", "bank"}}),
		hint([]string{"post", "postamt", "postfiliale", "postagentur", "postbank", "hauptpost", "post office", "postoffice"}, []tag{{"amenity", "post_office"}}),
		hint([]string{"supermarkt", "supermarkte", "lebensmittelgeschaft", "lebensmittelmarkt", "discounter", "supermarket", "supermarkets", "grocery", "grocery store"}, []tag{{"shop", "supermarket"}}), //nolint:misspell // the words a person may type, German and English spellings both
		hint([]string{"schuhgeschaft", "schuhladen", "schuhhaus", "shoe shop", "shoe store", "shoes"}, []tag{{"shop", "shoes"}}),
		hint([]string{"schuhe"}, []tag{{"shop", "shoes"}}),
		hint([]string{"backerei", "backer", "backstube", "bakery", "bakeries", "baker"}, []tag{{"shop", "bakery"}}),
		hint([]string{"jugendherberge", "herberge", "hostel", "hostels", "youth hostel"}, []tag{{"tourism", "hostel"}}),
		hint([]string{"pizzeria", "pizzerien", "pizza", "pizza place"}, []tag{{"amenity", "restaurant"}, {"amenity", "fast_food"}}),
		hint([]string{"restaurant", "restaurants", "gaststatte", "gasthaus", "wirtshaus"}, []tag{{"amenity", "restaurant"}}),
	}
	h[1].notAfter = []string{"tier", "tierarzt", "puppen", "handy", "auto", "fahrrad", "rad"}
	h[3].must = &nameMust{[]string{"sports_centre"}, `bad\M|schwimm|therme|aqua|pool|swim|lido`}
	h[17].compounds = false
	h[19].must = &nameMust{[]string{"doctors", "doctor"}, `kinder|jugend|padiat|pediat|paediat`}
	h[22].notAfter = []string{"tier", "zahn", "kinder"}
	h[23].compounds, h[25].compounds, h[27].compounds = false, false, false
	h[31].must = &nameMust{[]string{"restaurant", "fast_food"}, `pizz|italia|trattoria|ristorante|osteria`}
	return h
}()

// kindRead is a kind of place a name asks for: its tags, the name a value
// must carry, the words left, whether it is the kind alone, and (from
// hintedKinds) the words kept, the place words, whether words were
// stripped and the town.
type kindRead struct {
	tags      []tag
	must      *nameMust
	rest      *string
	pure      bool
	words     string
	placeWord map[string]bool
	stripped  bool
	town      *string
}

var connectorWords = setOf("am", "an", "im", "in", "der", "die", "das", "ob", "bei", "vor", "v", "d", "a", "i")

func endsWithWords(words, head []string) bool {
	return len(head) <= len(words) && slices.Equal(words[len(words)-len(head):], head)
}

type headOf struct {
	h    *categoryHint
	head []string
}

var hintHeads, hintHeadsByLength = func() ([]headOf, []headOf) {
	var all []headOf
	for i := range categoryHints {
		for _, hd := range categoryHints[i].heads {
			all = append(all, headOf{&categoryHints[i], strings.Split(hd, " ")})
		}
	}
	by := slices.Clone(all)
	slices.SortStableFunc(by, func(a, b headOf) int { return len(b.head) - len(a.head) })
	return all, by
}()

func kindOf(words []string) *kindRead {
	if len(words) == 0 {
		return nil
	}
	for _, t := range hintHeadsByLength {
		if endsWithWords(words, t.head) {
			rest := words[:len(words)-len(t.head)]
			k := &kindRead{tags: t.h.tags, must: t.h.must, pure: len(rest) == 0}
			if len(rest) > 0 {
				k.rest = sp(strings.Join(rest, " "))
			}
			return k
		}
	}
	last := words[len(words)-1]
	for _, t := range hintHeads {
		if len(t.head) == 1 && t.h.compounds && strings.HasSuffix(last, t.head[0]) &&
			!slices.ContainsFunc(t.h.notAfter, func(stem string) bool { return last == stem+t.head[0] }) {
			return &kindRead{tags: t.h.tags, must: t.h.must}
		}
	}
	for _, t := range hintHeadsByLength {
		if len(t.head) < len(words) && slices.Equal(words[:len(t.head)], t.head) {
			return &kindRead{tags: t.h.tags, must: t.h.must, rest: sp(strings.Join(words[len(t.head):], " "))}
		}
	}
	return nil
}

func isWholeHead(word string) bool {
	return slices.ContainsFunc(categoryHints, func(h categoryHint) bool { return slices.Contains(h.heads, word) })
}

func mustParams(m *nameMust) (any, any) {
	if m == nil {
		return []string(nil), (*string)(nil)
	}
	return m.values, m.pattern
}

func meetsName(h PoiHit, m *nameMust) bool {
	if m == nil || !slices.Contains(m.values, h.CategoryValue) {
		return true
	}
	return regexp.MustCompile(strings.ReplaceAll(m.pattern, `\M`, `\b`)).MatchString(normalizeName(h.Name))
}

func isHinted(h PoiHit, tags []tag, m *nameMust) bool {
	return slices.ContainsFunc(tags, func(t tag) bool { return h.Category == t[0] && h.CategoryValue == t[1] }) && meetsName(h, m)
}

func kindExcludes(h PoiHit, tags []tag, pure bool) bool {
	asksParking := slices.ContainsFunc(tags, func(t tag) bool { return isCarPark(t[0], t[1]) })
	if pure && !asksParking && isCarPark(h.Category, h.CategoryValue) {
		return true
	}
	asksPost := slices.ContainsFunc(tags, func(t tag) bool { return t[0] == "amenity" && t[1] == "post_office" })
	return asksPost && h.Category == "amenity" && h.CategoryValue == "post_office" && isParcelShop(h)
}

func words(s string) []string { return strings.Fields(s) }

// withoutTown is a name without the town's words, where they appear in it
// and leave two letters or more.
func withoutTown(name, town string) *string {
	ws, tw := words(normalizeName(name)), words(normalizeName(town))
	if len(tw) == 0 || len(ws) <= len(tw) {
		return nil
	}
	for i := 0; i+len(tw) <= len(ws); i++ {
		if slices.Equal(ws[i:i+len(tw)], tw) {
			rest := strings.Join(append(slices.Clone(ws[:i]), ws[i+len(tw):]...), " ")
			if runeLen(rest) >= 2 {
				return &rest
			}
			return nil
		}
	}
	return nil
}

// hintedKinds reads the kind a name asks for, place words of the town (or
// trailing place names the gazetteer holds) set aside.
func (rv *resolver) hintedKinds(raw string, townName *string) (*kindRead, error) {
	folded := normalizeName(raw)
	asked := words(folded)
	ws := asked
	var townWords []string
	if townName != nil {
		townWords = words(normalizeName(*townName))
		if folded == normalizeName(*townName) {
			return nil, nil
		}
		if rest := withoutTown(raw, *townName); rest != nil {
			ws = words(*rest)
		}
	}
	if kindOf(ws) == nil {
		return nil, nil
	}
	found, loaded, err := rv.reads.PlaceWords(rv.ctx, ws)
	if err != nil || !loaded {
		return nil, err
	}
	places := setOf(found...)
	if townName == nil {
		for len(ws) > 0 && places[ws[len(ws)-1]] && (!isWholeHead(ws[len(ws)-1]) || kindOf(ws[:len(ws)-1]) != nil) {
			ws = ws[:len(ws)-1]
		}
	}
	kind := kindOf(ws)
	if kind == nil {
		return nil, nil
	}
	left := setOf(ws...)
	kind.placeWord = map[string]bool{}
	for _, w := range append(slices.Clone(townWords), asked...) {
		if (slices.Contains(townWords, w) || !left[w]) && !connectorWords[w] {
			kind.placeWord[w] = true
		}
	}
	kind.words, kind.stripped, kind.town = strings.Join(ws, " "), len(ws) < len(asked), townName
	return kind, nil
}

func tagParams(tags []tag) ([]string, []string) {
	cats, vals := []string{}, []string{}
	for _, t := range tags {
		cats, vals = append(cats, t[0]), append(vals, t[1])
	}
	return cats, vals
}

func pairsOf(tags []tag) []string {
	out := []string{}
	for _, t := range tags {
		out = append(out, t[0]+"="+t[1])
	}
	return out
}

// poiScope is where a name is looked for: in a place (by its tag), within a
// radius of a point, or anywhere.
type poiScope struct {
	place          *string
	near           bool
	lat, lon, radM float64
}

// poiOptions: fuzzyAnywhere false keeps the fuzzy statement off anywhere,
// fuzzy false keeps it (and the kind read) off everywhere; town is the town
// a near search is in.
type poiOptions struct {
	noFuzzyAnywhere, noFuzzy bool
	town                     *string
}

func (rv *resolver) poiRows(read PoiRead) ([]PoiHit, error) { return rv.reads.PoiRows(rv.ctx, read) }

// findPois is the platform's findOsmPois at the default floor.
func (rv *resolver) findPois(name string, scope poiScope, limit int, opts poiOptions) ([]PoiHit, error) {
	raw := strings.TrimSpace(name)
	needle := normalizeName(raw)
	if needle == "" {
		return nil, nil
	}
	asked := limit * poiOverfetch
	motoring := asksMotoringKind(raw)
	floor := poiScoreFloor
	fuzzyFloor := math.Max(floor, poiFuzzyFloor)
	run := func(exact string, fuzzy string, args []any, floorAt int) ([]PoiHit, error) {
		first, err := rv.poiRows(PoiRead{exact, args})
		if err != nil {
			return nil, err
		}
		if len(first) > 0 || fuzzy == "" || opts.noFuzzy {
			return dedupePois(first, asked), nil
		}
		fargs := slices.Clone(args)
		fargs[floorAt] = fuzzyFloor
		second, err := rv.poiRows(PoiRead{fuzzy, append(fargs, motoring)})
		if err != nil {
			return nil, err
		}
		var kept []PoiHit
		for _, h := range second {
			if h.Score >= fuzzyFloor && (motoring || !isMotoringPoi(h)) {
				kept = append(kept, h)
			}
		}
		return dedupePois(kept, asked), nil
	}
	var kind *kindRead
	anywhere := scope.place == nil && !scope.near
	if !opts.noFuzzy && (!anywhere || asksNamedKindAnywhere(raw, opts)) {
		town := opts.town
		if scope.place != nil {
			town = scope.place
		}
		var err error
		if kind, err = rv.hintedKinds(raw, town); err != nil {
			return nil, err
		}
	}
	noFamily := []any{(*string)(nil), []string(nil)}
	switch {
	case scope.place != nil:
		placeNorm := normalizeName(*scope.place)
		expanded, _ := transliterateDigraphs(placeNorm)
		named, err := run(PoiByPlace, PoiByPlaceFuzzy, append([]any{needle, raw, placeNorm, expanded, floor, asked}, noFamily...), poiFloorIndexPlace)
		if err != nil || kind == nil {
			return named[:min(len(named), limit)], err
		}
		cats, vals := tagParams(kind.tags)
		mv, mp := mustParams(kind.must)
		hinted, err := rv.poiRows(PoiRead{PoiCategoryByPlace, []any{needle, raw, placeNorm, expanded, cats, vals, asked, mv, mp}})
		if err != nil {
			return nil, err
		}
		var restHits []PoiHit
		if kind.rest != nil {
			if restHits, err = rv.poiRows(PoiRead{PoiByPlace, []any{*kind.rest, *kind.rest, placeNorm, expanded, floor, asked, (*string)(nil), pairsOf(kind.tags)}}); err != nil {
				return nil, err
			}
		}
		return withHinted(named, hinted, restHits, kind, limit), nil
	case scope.near:
		named, err := run(PoiNear, PoiNearFuzzy, append([]any{needle, raw, scope.lon, scope.lat, scope.radM, floor, asked}, noFamily...), poiFloorIndexNear)
		if err != nil || kind == nil {
			return named[:min(len(named), limit)], err
		}
		cats, vals := tagParams(kind.tags)
		mv, mp := mustParams(kind.must)
		stmt := PoiCategoryNear
		if kind.pure {
			stmt = PoiCategoryNearest
		}
		hinted, err := rv.poiRows(PoiRead{stmt, []any{needle, raw, scope.lon, scope.lat, math.Min(scope.radM, categoryRadiusM), cats, vals, asked, mv, mp}})
		if err != nil {
			return nil, err
		}
		var restHits []PoiHit
		if kind.rest != nil {
			if restHits, err = rv.poiRows(PoiRead{PoiNear, []any{*kind.rest, *kind.rest, scope.lon, scope.lat, scope.radM, floor, asked, (*string)(nil), pairsOf(kind.tags)}}); err != nil {
				return nil, err
			}
		}
		return withHinted(named, hinted, restHits, kind, limit), nil
	}
	// Anywhere the resolver always asks without the fuzzy statement, so no
	// kind is read (asksNamedKindAnywhere is false).
	named, err := run(PoiAnywhere, "", append([]any{needle, raw, floor, asked}, noFamily...), poiFloorIndexAnyway)
	return named[:min(len(named), limit)], err
}

func asksNamedKindAnywhere(raw string, opts poiOptions) bool {
	if opts.noFuzzyAnywhere || opts.noFuzzy {
		return false
	}
	read := kindOf(words(normalizeName(raw)))
	return read != nil && read.rest != nil && runeLen(strings.ReplaceAll(*read.rest, " ", "")) >= 3
}

func withHinted(named, hinted, rest []PoiHit, kind *kindRead, limit int) []PoiHit {
	namedSo := func(h PoiHit) bool {
		if kind.stripped && h.Tier >= categoryTier {
			return true
		}
		own := words(normalizeName(h.Name))
		base := own
		if kind.town != nil {
			if phrase := withoutTown(h.Name, *kind.town); phrase != nil {
				base = words(*phrase)
			}
		}
		var bare []string
		for _, w := range base {
			if !kind.placeWord[w] {
				bare = append(bare, w)
			}
		}
		return len(bare) < len(own) && strings.Join(bare, " ") == kind.words
	}
	lift := func(h PoiHit, tier int) PoiHit {
		if kind.pure {
			h.Tier, h.Score = categoryTier, 1
			if namedSo(h) {
				h.Tier++
			}
			return h
		}
		h.Tier = max(h.Tier, tier)
		return h
	}
	other := func(h PoiHit) PoiHit {
		if kind.pure {
			h.Tier = min(h.Tier, categoryTier-1)
		}
		return h
	}
	var order []string
	best := map[string]PoiHit{}
	offer := func(h PoiHit) {
		held, ok := best[h.ID]
		if !ok {
			order = append(order, h.ID)
		}
		if !ok || h.Tier > held.Tier || (h.Tier == held.Tier && held.DistanceM == nil && h.DistanceM != nil) {
			best[h.ID] = h
		}
	}
	out := func(h PoiHit) bool { return kindExcludes(h, kind.tags, kind.pure) }
	for _, h := range named {
		if !out(h) {
			if isHinted(h, kind.tags, kind.must) {
				offer(lift(h, categoryTier))
			} else {
				offer(other(h))
			}
		}
	}
	for _, h := range hinted {
		if !out(h) && isHinted(h, kind.tags, kind.must) {
			offer(lift(h, categoryTier))
		}
	}
	for _, h := range rest {
		if !out(h) && isHinted(h, kind.tags, kind.must) {
			offer(lift(h, namedKindTier))
		}
	}
	all := make([]PoiHit, 0, len(order))
	for _, id := range order {
		all = append(all, best[id])
	}
	slices.SortStableFunc(all, comparePoiHits)
	return dedupePois(all, limit)
}

// poiTown is a town a name is looked for in: its tag and its point.
type poiTown struct {
	place string
	point *poiPoint
}

type poiPoint struct {
	lat, lon float64
	radiusM  *float64
}

func townRadiusM(p poiPoint) float64 {
	own := float64(poiTownRadiusM)
	if p.radiusM != nil && !math.IsInf(*p.radiusM, 0) && !math.IsNaN(*p.radiusM) {
		own = *p.radiusM
	}
	return math.Round(math.Min(poiTownRadiusMaxM, math.Max(poiTownRadiusM, own)))
}

// findPoisInTown is the platform's findOsmPoisInTown: the name in the town
// (tagged there and around its point), else the name without the town's
// words when the full name answered nothing by name.
func (rv *resolver) findPoisInTown(name string, town poiTown, limit int, opts poiOptions) ([]PoiHit, error) {
	full, err := rv.searchInTown(name, town, limit, opts)
	if err != nil {
		return nil, err
	}
	stripped := withoutTown(name, town.place)
	if stripped == nil || slices.ContainsFunc(full, func(h PoiHit) bool { return h.Tier >= categoryTier || h.Tier%2 == 1 }) {
		return full, nil
	}
	bare, err := rv.searchInTown(*stripped, town, limit, opts)
	if err != nil {
		return nil, err
	}
	if len(bare) > 0 {
		return bare, nil
	}
	return full, nil
}

func (rv *resolver) searchInTown(name string, town poiTown, limit int, opts poiOptions) ([]PoiHit, error) {
	opts.town = &town.place
	tagged, err := rv.findPois(name, poiScope{place: &town.place}, limit, opts)
	if err != nil || town.point == nil {
		return tagged, err
	}
	p := *town.point
	near, err := rv.findPois(name, poiScope{near: true, lat: p.lat, lon: p.lon, radM: townRadiusM(p)}, limit, opts)
	if err != nil {
		return nil, err
	}
	radial := map[string]PoiHit{}
	for _, h := range near {
		radial[h.ID] = h
	}
	seen := map[string]bool{}
	var merged []PoiHit
	for _, h := range tagged {
		seen[h.ID] = true
		if r, ok := radial[h.ID]; ok && r.Tier >= h.Tier {
			merged = append(merged, r)
		} else {
			merged = append(merged, h)
		}
	}
	for _, h := range near {
		if !seen[h.ID] {
			merged = append(merged, h)
		}
	}
	slices.SortStableFunc(merged, comparePoiHits)
	return dedupePois(merged, limit), nil
}

func has(s *string) int {
	if s == nil || strings.TrimSpace(*s) == "" {
		return 0
	}
	return 1
}

func objFold(s *string) string {
	if s == nil {
		return ""
	}
	return strings.ReplaceAll(normalizeName(*s), " ", "")
}

func compatible(a, b *string) bool {
	return a == nil || b == nil || *a == "" || *b == "" || objFold(a) == objFold(b)
}

// objectMetres is the platform's same-object distance (R 6 371 000 m).
func objectMetres(aLat, aLon, bLat, bLon float64) float64 {
	rad := pi / 180
	dLat, dLon := (bLat-aLat)*rad, (bLon-aLon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(aLat*rad)*math.Cos(bLat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * 6_371_000 * math.Asin(math.Min(1, math.Sqrt(h)))
}

func samePoiAddress(a, b PoiHit) bool {
	if has(a.Street)+has(a.HouseNumber)+has(b.Street)+has(b.HouseNumber) < 4 {
		return false
	}
	if has(a.Postcode)+has(a.Place) == 0 || has(b.Postcode)+has(b.Place) == 0 {
		return false
	}
	return objFold(a.Street) == objFold(b.Street) && objFold(a.HouseNumber) == objFold(b.HouseNumber) && compatible(a.Postcode, b.Postcode) && compatible(a.Place, b.Place)
}

func isSamePoi(a, b PoiHit) bool {
	if a.Category != b.Category && a.Category != "building" && b.Category != "building" {
		return false
	}
	if normalizeName(a.Name) != normalizeName(b.Name) {
		return false
	}
	return objectMetres(a.Lat, a.Lon, b.Lat, b.Lon) <= sameObjectM || samePoiAddress(a, b)
}

func poiRichness(h PoiHit) int {
	r := 2*(has(h.Street)+has(h.HouseNumber)) + has(h.Postcode) + has(h.Place) + has(h.Brand) + has(h.Operator)
	if h.Category != "building" {
		r++
	}
	return r
}

// foldSamePois keeps one hit an object, the richest of its group at the
// lead's place in the order.
func foldSamePois(hits []PoiHit) []PoiHit {
	var groups [][]PoiHit
	for _, r := range hits {
		gi := slices.IndexFunc(groups, func(g []PoiHit) bool { return slices.ContainsFunc(g, func(m PoiHit) bool { return isSamePoi(m, r) }) })
		if gi < 0 {
			groups = append(groups, []PoiHit{r})
		} else {
			groups[gi] = append(groups[gi], r)
		}
	}
	out := make([]PoiHit, 0, len(groups))
	for _, g := range groups {
		lead, richest := g[0], g[0]
		for _, m := range g {
			if poiRichness(m) > poiRichness(richest) {
				richest = m
			}
		}
		if richest.ID != lead.ID {
			richest.DistanceM, richest.Score, richest.Tier = lead.DistanceM, lead.Score, lead.Tier
		}
		out = append(out, richest)
	}
	return out
}

func dedupePois(hits []PoiHit, limit int) []PoiHit {
	f := foldSamePois(hits)
	return f[:min(len(f), limit)]
}
