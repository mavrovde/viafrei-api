package places

import (
	"context"
	"regexp"
	"slices"
	"strings"
)

// The place resolver (place.ts resolvePlace for a name alone: no position
// and no road asked): the one place a free text names, as the platform's
// server resolves it.

// Resolve resolves a place text in a language (de, en) through reads.
func Resolve(ctx context.Context, reads ResolveReads, text, lang string) (Resolution, error) {
	rv := &resolver{ctx: ctx, reads: reads, lang: lang, memo: map[string][]GazetteerRow{}}
	asked := strings.TrimSpace(text)
	if _, query, ok := germanNameOf(asked); ok {
		asked = query
	}
	return rv.resolveInput(asked)
}

var exonymByFold = func() map[string]string {
	own := map[string]bool{}
	for _, e := range exonyms {
		own[normalizeName(e.de)] = true
	}
	m := map[string]string{}
	for _, e := range exonyms {
		for _, n := range e.others {
			if k := normalizeName(n); k != "" && !own[k] {
				m[k] = e.de
			}
		}
	}
	return m
}()

// germanNameOf is a text with its head (before a comma) in German where it
// is an English or French name of a big town or a Land.
func germanNameOf(query string) (name, rewritten string, ok bool) {
	comma := strings.Index(query, ",")
	head := query
	if comma >= 0 {
		head = query[:comma]
	}
	de, found := exonymByFold[normalizeName(strings.TrimSpace(head))]
	if !found {
		return "", "", false
	}
	if comma < 0 {
		return de, de, true
	}
	return de, de + query[comma:], true
}

var centreSuffix = regexp.MustCompile(`(?i)^(.+?)[\s,]+(innenstadt|stadtmitte|stadtzentrum|zentrum|city\s+centre|city\s+center|centre|center|downtown)$`)

// splitCentreHint is a text and the centre word it ends with.
func splitCentreHint(query string) (base, hint string, ok bool) {
	m := centreSuffix.FindStringSubmatch(strings.TrimSpace(query))
	if m == nil {
		return "", "", false
	}
	// The text is trimmed, so the lazy head starts with a letter and is
	// never blank.
	return strings.TrimSpace(m[1]), strings.Join(strings.Fields(m[2]), " "), true
}

func (rv *resolver) resolveInput(query string) (Resolution, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return unresolved("not_found", query), nil
	}
	if r, err := rv.resolveOwnLabel(query); err != nil || r != nil {
		return deref(r), err
	}
	if r, err := rv.resolveJunctionOption(query); err != nil || r != nil {
		return deref(r), err
	}
	if looksLikeHouseNumber(query) {
		name, labelled := junctionLabelName(query)
		core, prefixed := junctionCoreOf(name)
		if prefixed || labelled {
			if j, err := rv.resolveJunction(core, query); err != nil || j != nil {
				return deref(j), err
			}
		}
		return rv.resolveAddress(query)
	}
	if base, hint, ok := splitCentreHint(query); ok {
		b, err := rv.resolveInput(base)
		if err != nil {
			return Resolution{}, err
		}
		if b.Kind == ResolvedPoint {
			b.Label += ", " + hint
			return b, nil
		}
		whole, err := rv.resolveAsWritten(query)
		if err != nil {
			return Resolution{}, err
		}
		if b.Kind == ResolvedCandidates && whole.Kind != ResolvedPoint {
			return b, nil
		}
		return whole, nil
	}
	return rv.resolveAsWritten(query)
}

func deref(r *Resolution) Resolution {
	if r == nil {
		return Resolution{}
	}
	return *r
}

// resolveAsWritten reads a text as written, in the platform's order.
func (rv *resolver) resolveAsWritten(asked string) (Resolution, error) {
	query, found, ok, err := rv.withoutCountry(asked)
	if err != nil {
		return Resolution{}, err
	}
	if !ok {
		if found, err = rv.findCandidates(query, MaxCandidates, CandidateOptions{}); err == nil {
			found, err = rv.withNamesakeGemeinden(query, found)
		}
		if err != nil {
			return Resolution{}, err
		}
	}
	if ForeignPlaces[FoldQuery(query)] && !slices.ContainsFunc(found, func(c GazetteerRow) bool {
		return c.Score >= 1 && c.Population != nil && *c.Population >= DominantPopulation
	}) {
		return unresolved("outside_germany", query), nil
	}
	name, labelled := junctionLabelName(query)
	if core, prefixed := junctionCoreOf(name); prefixed || labelled {
		if j, err := rv.resolveJunction(core, query); err != nil || j != nil {
			return deref(j), err
		}
	}
	asWritten := judgeCandidates(found, query, rv.lang, nil, nil)
	listed := func(j *judged) (Resolution, error) { return rv.listedResolution(j, found, query) }
	if asWritten != nil && asWritten.exact {
		nobody := nobodyLivesThere(asWritten)
		if nobody {
			if l, err := rv.withLandmarks(query, asWritten); err != nil || l != nil {
				return deref(l), err
			}
		}
		answer, err := listed(asWritten)
		if err != nil || !nobody {
			return answer, err
		}
		return rv.withLocalityLand(answer, asWritten.rows)
	}
	var spelled []GazetteerRow
	for _, c := range found {
		if spellsOutTownName(c, query) {
			spelled = append(spelled, c)
		}
	}
	if len(spelled) > 0 {
		if short := judgeCandidates(spelled, query, rv.lang, nil, nil); short != nil {
			return rv.listedResolution(short, spelled, query)
		}
	}
	if plain := afterEpithet(query); plain != nil {
		rows, err := rv.findCandidates(*plain, MaxCandidates*2, CandidateOptions{})
		if err != nil {
			return Resolution{}, err
		}
		var towns []GazetteerRow
		for _, c := range rows {
			if c.Kind == "city" && c.NameNorm == *plain {
				towns = append(towns, c)
			}
		}
		if len(towns) > 0 {
			if t := judgeCandidates(towns, *plain, rv.lang, nil, nil); t != nil && t.exact && t.res.Kind == ResolvedPoint {
				return t.res, nil
			}
		}
	}
	if s, err := rv.resolveMainStation(query); err != nil || s != nil {
		return deref(s), err
	}
	in, err := rv.resolveInside(query)
	if err != nil {
		return Resolution{}, err
	}
	if in.res != nil && in.exact {
		return *in.res, nil
	}
	town := in.town
	if town == nil {
		town = in.spaceTown
	}
	var townName *string
	var named []PoiHit
	if town != nil {
		townName = areaName(town.area)
		if named, err = rv.resolvePoi(query, town, poiOptions{noFuzzy: true}); err != nil {
			return Resolution{}, err
		}
		if l := landmarkOf(named, townName, rv.lang); l != nil {
			return *l, nil
		}
		if o := organisationOf(named, town.reading.part, townName); o != nil {
			if s, err := rv.resolveStreet(query); err != nil || s != nil {
				return deref(s), err
			}
			return *o, nil
		}
	}
	if in.res != nil {
		return *in.res, nil
	}
	if town != nil && townPartWords.MatchString(strings.TrimSpace(town.reading.part)) {
		return townPart(*town), nil
	}
	if s, err := rv.resolveStreet(query); err != nil || s != nil {
		return deref(s), err
	}
	commaTown, spaceTown := in.town, in.spaceTown
	if asWritten != nil && in.area != nil {
		if r, ok, err := rv.boundedByComma(query, asWritten, found, *in.area, commaTown); err != nil || ok {
			return r, err
		}
	}
	if asWritten != nil && in.area == nil && spaceTown != nil {
		reach := spaceTown.area
		reach.radiusKm *= spaceTownReach
		if bounded := withinArea(asWritten.res, reach); bounded != nil {
			all, err := listed(asWritten)
			if err != nil {
				return Resolution{}, err
			}
			resolved := *bounded
			if w := withinArea(all, reach); w != nil {
				resolved = *w
			}
			if !isTownItself(resolved, *spaceTown) {
				return resolved, nil
			}
			return rv.thingOrCentre(query, *spaceTown, resolved, true)
		}
		thing, err := rv.thingInTown(query, *spaceTown, true)
		if err != nil || thing.found != nil {
			return deref(thing.found), err
		}
		if poi := judgePois(named, townName, rv.lang); poi != nil {
			return *poi, nil
		}
		if carriesTownName(asWritten.res, *spaceTown) {
			return listed(asWritten)
		}
		return townCentre(*spaceTown, rv.lang), nil
	}
	if asWritten != nil && in.area == nil {
		return listed(asWritten)
	}
	if spaceTown != nil {
		thing, err := rv.thingInTown(query, *spaceTown, true)
		if err != nil || thing.found != nil {
			return deref(thing.found), err
		}
	}
	hits, err := rv.resolvePoi(query, commaTown, poiOptions{})
	if err != nil {
		return Resolution{}, err
	}
	var commaName *string
	if commaTown != nil {
		commaName = townName
	}
	if poi := judgePois(hits, commaName, rv.lang); poi != nil {
		return *poi, nil
	}
	address, err := rv.resolveAddress(query)
	if err != nil || address.Kind != ResolvedNone {
		return address, err
	}
	return rv.nameMissReason(query)
}

// boundedByComma answers a text read as written inside the area its comma
// names.
func (rv *resolver) boundedByComma(query string, asWritten *judged, found []GazetteerRow, area contextArea, commaTown *commaTown) (Resolution, bool, error) {
	ofPart := &asWritten.res
	if commaTown != nil && !isTownItself(asWritten.res, *commaTown) {
		ofPart = ofThePart(asWritten.res, commaTown.reading.part)
	}
	if ofPart == nil {
		return Resolution{}, false, nil
	}
	bounded := withinArea(*ofPart, area)
	if bounded == nil {
		return Resolution{}, false, nil
	}
	all, err := rv.listedResolution(asWritten, found, query)
	if err != nil {
		return Resolution{}, false, err
	}
	listedPart := &all
	if commaTown != nil && !isTownItself(all, *commaTown) {
		listedPart = ofThePart(all, commaTown.reading.part)
	}
	resolved := *bounded
	if listedPart != nil {
		if w := withinArea(*listedPart, area); w != nil {
			resolved = *w
		}
	}
	if commaTown == nil || !isTownItself(resolved, *commaTown) {
		return resolved, true, nil
	}
	r, err := rv.thingOrCentre(query, *commaTown, resolved, false)
	return r, true, err
}

// thingOrCentre is the thing a town holds, else the town (with the centre
// note when the town holds nothing of the name).
func (rv *resolver) thingOrCentre(query string, town commaTown, resolved Resolution, noComma bool) (Resolution, error) {
	thing, err := rv.thingInTown(query, town, noComma)
	if err != nil {
		return Resolution{}, err
	}
	if thing.found != nil {
		return *thing.found, nil
	}
	if thing.held {
		return resolved, nil
	}
	return withCentreNote(resolved, town, rv.lang), nil
}

var (
	hbfWord          = regexp.MustCompile(`(?i)\bhbf\b`)
	edgeCommaSpace   = regexp.MustCompile(`^[\s,]+|[\s,]+$`)
	englishTownForms = []struct {
		re *regexp.Regexp
		de string
	}{
		{regexp.MustCompile(`(?i)\bmunich\b`), "münchen"},
		{regexp.MustCompile(`(?i)\bcologne\b`), "köln"},
		{regexp.MustCompile(`(?i)\bnuremberg\b`), "nürnberg"},
		{regexp.MustCompile(`(?i)\bhanover\b`), "hannover"},
		{regexp.MustCompile(`(?i)\bbrunswick\b`), "braunschweig"},
		{regexp.MustCompile(`(?i)\bfrankfort\b`), "frankfurt"},
		{phrasesPattern(`\b(?:`, []string{"central railway station", "main railway station", "central station", "main station", "hauptbahnhof", "main st", "central"}, `)\b`), "hbf"},
	}
	stationWord = phrasesPattern(`(^|[\s,]+)(?:`, []string{"railway station", "train station", "bahnhof", "station", "bhf", "bf"}, `)\.?($|[\s,]+)`)
)

// phrasesPattern is a case-blind alternation of phrases, the longest first,
// their spaces any white space.
func phrasesPattern(head string, phrases []string, tail string) *regexp.Regexp {
	ps := slices.Clone(phrases)
	slices.SortStableFunc(ps, func(a, b string) int { return len(b) - len(a) })
	for i, p := range ps {
		ps[i] = strings.Join(strings.Split(p, " "), `\s+`)
	}
	return regexp.MustCompile(`(?i)` + head + strings.Join(ps, "|") + tail)
}

func germanStationQuery(query string) string {
	out := strings.TrimSpace(query)
	for _, f := range englishTownForms {
		out = f.re.ReplaceAllString(out, f.de)
	}
	return strings.Join(strings.Fields(out), " ")
}

// withoutStationWord is a text without its station words; nil when it has
// none or nothing else.
func withoutStationWord(query string) *string {
	if !stationWord.MatchString(query) {
		return nil
	}
	// The platform's lookahead keeps the separator after a word for the
	// next one; replacing until nothing changes does the same.
	rest := query
	for {
		next := stationWord.ReplaceAllString(rest, " $2")
		if next == rest {
			break
		}
		rest = next
	}
	rest = edgeCommaSpace.ReplaceAllString(strings.Join(strings.Fields(rest), " "), "")
	if normalizeName(rest) == "" {
		return nil
	}
	return &rest
}

// townStationSpellings are the main-station spellings of the town a station
// text names, and the town.
func townStationSpellings(query string) ([]string, string, bool) {
	rewritten := germanStationQuery(query)
	var place string
	if hbfWord.MatchString(rewritten) {
		place = hbfWord.ReplaceAllString(rewritten, " ")
	} else if p := withoutStationWord(rewritten); p != nil {
		place = *p
	} else {
		return nil, "", false
	}
	town := edgeCommaSpace.ReplaceAllString(strings.Join(strings.Fields(place), " "), "")
	if normalizeName(town) == "" {
		return nil, "", false
	}
	return []string{town + " hbf", town + " hauptbahnhof"}, town, true
}

const qualifiedStationRows = 10

var qualifiedMainStation = regexp.MustCompile(`(?i)^(.+?)\s*\(([^()]+)\)\s*(?:hbf|hauptbahnhof)\.?$`)

func isQualifiedMainStationOf(name, town string) bool {
	m := qualifiedMainStation.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return false
	}
	base, qualifier, typed := words(FoldQuery(m[1])), words(normalizeName(m[2])), words(FoldQuery(town))
	forms := func(t string) []string {
		x, _ := transliterateDigraphs(t)
		return []string{t, x}
	}
	if len(base) == 0 || len(typed) < len(base) {
		return false
	}
	for i, w := range base {
		if !slices.Contains(forms(typed[i]), w) {
			return false
		}
	}
	var rest []string
	for _, w := range typed[len(base):] {
		if !nameJoiners[w] {
			rest = append(rest, w)
		}
	}
	if len(rest) == 0 {
		return len(typed) == len(base)
	}
	if len(rest) != len(qualifier) {
		return false
	}
	for i, w := range rest {
		if !slices.ContainsFunc(forms(w), func(f string) bool { return strings.HasPrefix(f, qualifier[i]) }) {
			return false
		}
	}
	return true
}

// resolveMainStation answers "<town> Hbf" (and "<town> station") with the
// town's main station.
func (rv *resolver) resolveMainStation(query string) (*Resolution, error) {
	typed := FoldQuery(query)
	spellings, town, ok := townStationSpellings(query)
	if !ok {
		return nil, nil
	}
	for _, spelling := range spellings {
		if FoldQuery(spelling) == typed {
			continue
		}
		rows, err := rv.findCandidates(spelling, MaxCandidates, CandidateOptions{})
		if err != nil {
			return nil, err
		}
		var transport []GazetteerRow
		for _, c := range rows {
			if transportKinds[c.Kind] {
				transport = append(transport, c)
			}
		}
		if j := judgeCandidates(transport, spelling, rv.lang, nil, nil); j != nil && j.exact {
			return &j.res, nil
		}
	}
	bare := FoldQuery(town)
	station, source := "station", "db_stada"
	rows, err := rv.findCandidates(town, MaxCandidates, CandidateOptions{OnlyKind: station, OnlySource: source})
	if err != nil {
		return nil, err
	}
	var stations []GazetteerRow
	for _, c := range rows {
		if c.NameNorm == bare {
			stations = append(stations, c)
		}
	}
	if len(stations) > 0 {
		if j := judgeCandidates(stations, town, rv.lang, nil, nil); j != nil && j.exact {
			return &j.res, nil
		}
	}
	rows, err = rv.findCandidates(town+" hbf", qualifiedStationRows, CandidateOptions{Unqualified: true, OnlyKind: station, OnlySource: source})
	if err != nil {
		return nil, err
	}
	var main []GazetteerRow
	for _, c := range rows {
		if isQualifiedMainStationOf(c.Name, town) {
			c.Score = 1
			main = append(main, c)
		}
	}
	if len(main) == 0 {
		return nil, nil
	}
	// Every row scores 1 now, so the judgement is never empty.
	return &judgeCandidates(main, query, rv.lang, nil, nil).res, nil
}

// ChosenName is a point's label without the namesakes it names after it
// (place.ts chosenPlaceName).
func ChosenName(r Resolution, lang string) string {
	if len(r.Alternatives) == 0 {
		return r.Label
	}
	ls := make([]string, len(r.Alternatives))
	for i, a := range r.Alternatives {
		ls[i] = a.Label
	}
	return strings.TrimSuffix(r.Label, ", "+alsoWord(lang)+": "+strings.Join(ls, ", "))
}

// ResolvedKinds are the kinds a resolved place is said as.
var ResolvedKinds = []string{"municipality", "district", "state", "region", "island", "station_area", "stop", "poi", "address", "junction", "motorway", "other"}

// ResolvedKind is the kind a resolved place is said as: its gazetteer
// kind's, else what it was read as (a POI, an address). An area whose key
// is two digits is a Land, said as a state; any other area a region.
func ResolvedKind(placeKind, via, ags string) string {
	switch placeKind {
	case "city":
		return "municipality"
	case "admin":
		if len(ags) == 2 {
			return "state"
		}
		return "region"
	case "station":
		return "station_area"
	case "road":
		return "motorway"
	case "district", "island", "stop", "junction", "motorway":
		return placeKind
	}
	switch via {
	case "poi", "address":
		return via
	}
	return "other"
}
