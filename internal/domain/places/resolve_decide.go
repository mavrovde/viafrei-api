package places

import (
	"math"
	"regexp"
	"slices"
	"strings"
)

// The resolver's verdict over judged rows (judgeCandidates, decide): one
// point, a list to choose from, or no answer, as today's server gives it.

// Resolution kinds.
const (
	ResolvedPoint      = "point"
	ResolvedCandidates = "candidates"
	ResolvedNone       = "unresolved"
)

// Option is a row of a list, or a place a point names beside itself.
type Option struct {
	Label    string
	Kind     string
	Lat, Lon float64
	SourceID string
	AGS      string
	ExtentKm *float64
}

// Resolution is what a place text resolves to: a point with its label, the
// sources it rests on, the namesakes its label names, its gazetteer kind,
// extent, area key and AGS; a list of options; or no answer, with the
// reason.
type Resolution struct {
	Kind         string
	Lat, Lon     float64
	Label        string
	SourceIDs    []string
	Via          string
	Alternatives []Option
	PlaceKind    string
	ExtentKm     *float64
	AreaCode     string
	AGS          string
	// CentreOf is set when a thing in a town was not found and the town's
	// centre answers: the thing missing.
	CentreOf string
	Options  []Option
	Reason   string
	Query    string
}

func unresolved(reason, query string) Resolution {
	return Resolution{Kind: ResolvedNone, Reason: reason, Query: query}
}

const (
	cityDensity = 2_000.0
	areaDensity = 150.0
	oneFieldKm  = 2.0
)

var agsCode = regexp.MustCompile(`^\d{2,12}$`)

func agsOf(c GazetteerRow) string {
	if c.AdminCode != nil && agsCode.MatchString(*c.AdminCode) {
		return *c.AdminCode
	}
	return ""
}

func resolvedAgsOf(best located, all []located) string {
	if own := agsOf(best.GazetteerRow); own != "" || best.Kind != "city" {
		return own
	}
	keys := municipalitiesOf(best, all, englishNamesByFold(all))
	if len(keys) == 1 {
		for k := range keys {
			return k
		}
	}
	return ""
}

func areaOf(c located) string {
	if !containerKinds[c.Kind] {
		return ""
	}
	if key := containerKey(c); key != "" {
		return key
	}
	if c.Kind != "admin" || c.AdminCode != nil {
		return ""
	}
	for _, l := range Laender {
		if l.NameNorm == c.NameNorm || l.NameEnNorm == c.NameNorm {
			return l.AGS
		}
	}
	return ""
}

func extentOf(c located) *float64 {
	if c.Kind != "city" {
		return nil
	}
	r := radiusOf(c)
	return &r
}

// radiusOf is how far a row's place reaches, in km, from its population.
// pi is π as a float64 variable: the platform multiplies and divides the
// rounded π at run time, which a constant expression would fold exactly.
var pi = math.Pi

func radiusOf(c located) float64 {
	p := float64(pop(c))
	switch c.Kind {
	case "city":
		return math.Min(25, math.Max(samePlaceKm, math.Sqrt(p/(cityDensity*pi))))
	case "island":
		return samePlaceKm
	}
	return math.Min(150, math.Max(10, math.Sqrt(p/(areaDensity*pi))))
}

// haversineM is the platform's distance in metres (R 6 371 008.8 m).
func haversineM(aLat, aLon, bLat, bLon float64) float64 {
	const r = 6_371_008.8
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := rad(bLat-aLat), rad(bLon-aLon)
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(aLat))*math.Cos(rad(bLat))*math.Pow(math.Sin(dLon/2), 2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

func mOf(a, b located) float64 { return haversineM(a.lat, a.lon, b.lat, b.lon) }

func oneTownOf(best located, contenders []located) *located {
	if len(contenders) == 0 || (best.Score >= 1 && best.Kind != "city") {
		return nil
	}
	field := append([]located{best}, contenders...)
	if best.Score >= 1 && best.Kind == "city" && pop(best) <= 0 {
		reach := radiusOf(best) * 1_000
		for _, c := range contenders {
			if !((transportKinds[c.Kind] || c.Kind == "junction") && c.Score < 1 && mOf(best, c) <= reach) {
				return nil
			}
		}
		return &best
	}
	var towns []located
	for _, c := range field {
		if c.Kind == "city" {
			towns = append(towns, c)
		}
	}
	if len(towns) == 0 || pop(towns[0]) <= 0 {
		return nil
	}
	town := towns[0]
	keys := map[string]bool{}
	for _, t := range towns {
		if t.NameNorm != town.NameNorm || mOf(town, t) > samePlaceKm*1_000 {
			return nil
		}
		if t.AdminCode != nil && eightDigits.MatchString(*t.AdminCode) {
			keys[*t.AdminCode] = true
		}
	}
	reach := radiusOf(town) * 1_000
	for _, c := range field {
		if c.Kind == "city" {
			continue
		}
		if !transportKinds[c.Kind] || !((c.AdminCode != nil && keys[*c.AdminCode]) || mOf(town, c) <= reach) {
			return nil
		}
	}
	return &town
}

func significantWords(fold string) []string {
	var out []string
	for _, w := range strings.Split(fold, " ") {
		if runeLen(w) >= 3 && !nameJoiners[w] {
			out = append(out, w)
		}
	}
	return out
}

// nameJoiners are the words a German name joins its parts with (NAME_JOINERS).
var nameJoiners = setOf("an", "am", "der", "die", "dem", "den", "des", "a", "d", "im", "in", "i", "ob", "bei", "b", "auf", "unter", "vor", "zu", "zum", "zur")

func oneStopFieldOf(best located, contenders []located, asked string) *located {
	if asked == "" || len(contenders) == 0 || best.Score >= 1 {
		return nil
	}
	words := significantWords(asked)
	if len(words) < 2 {
		return nil
	}
	carries := func(c located) bool {
		own := strings.Split(c.NameNorm, " ")
		for _, w := range words {
			if !slices.ContainsFunc(own, func(o string) bool { return o == w || (strings.HasPrefix(o, w) && runeLen(o) <= runeLen(w)+2) }) {
				return false
			}
		}
		return true
	}
	for _, c := range append([]located{best}, contenders...) {
		if !transportKinds[c.Kind] || !carries(c) || kmOf(best, c) > oneFieldKm {
			return nil
		}
	}
	return &best
}

// alsoWord joins a point's label to the namesakes it names.
func alsoWord(lang string) string { return [2]string{"auch", "also"}[langIdx(lang)] }

func islandCompanions(best located, all []located, lang string) []Option {
	var other *located
	switch best.Kind {
	case "island":
		towns := islandTownsOf(best, all)
		slices.SortStableFunc(towns, func(a, b located) int { return pop(b) - pop(a) })
		if len(towns) > 0 {
			other = &towns[0]
		}
	case "city":
		var isl []located
		for _, c := range all {
			if c.Score == best.Score && belongsToIsland(best, c) {
				isl = append(isl, c)
			}
		}
		slices.SortStableFunc(isl, func(a, b located) int { return cmpFloat(kmOf(a, best), kmOf(b, best)) })
		if len(isl) > 0 {
			other = &isl[0]
		}
	}
	if other == nil {
		return nil
	}
	return []Option{{Label: placeName(other.GazetteerRow) + " (" + kindWord(other.Kind, lang) + ")", Kind: other.Kind, Lat: other.lat, Lon: other.lon, SourceID: other.SourceID}}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func credit(ids ...string) []string {
	var out []string
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func optionOf(c located, label string) Option {
	return Option{Label: label, Kind: c.Kind, Lat: c.lat, Lon: c.lon, SourceID: c.SourceID, AGS: agsOf(c.GazetteerRow)}
}

func pointOf(c located, label string) Resolution {
	return Resolution{Kind: ResolvedPoint, Lat: c.lat, Lon: c.lon, Label: label, SourceIDs: credit(c.SourceID), Via: "gazetteer", PlaceKind: c.Kind,
		ExtentKm: extentOf(c), AreaCode: areaOf(c)}
}

func labelsOr(labels []string, i int, c located) string {
	if i < len(labels) && labels[i] != "" {
		return labels[i]
	}
	return placeName(c.GazetteerRow)
}

func withAlternatives(r Resolution, alts []Option, lang string) Resolution {
	r.Alternatives = alts
	var ls []string
	for _, a := range alts {
		ls = append(ls, a.Label)
		r.SourceIDs = credit(append(r.SourceIDs, a.SourceID)...)
	}
	if len(alts) > 0 {
		r.Label += ", " + alsoWord(lang) + ": " + strings.Join(ls, ", ")
	}
	return r
}

// decide turns folded rows into a point or a list.
func decide(rows []located, lang string, all []located, asked string, lands map[string]string, wholeQuery string, kreise map[string]string) Resolution {
	best := rows[0]
	var contenders []located
	for _, c := range rows[1:] {
		if best.Score-c.Score < WinnerMargin {
			contenders = append(contenders, c)
		}
	}
	forecourt := forecourtAnswers(best, contenders)
	if len(rows) == 1 || best.Score-rows[1].Score >= WinnerMargin || answersAlone(best, contenders) || forecourt {
		if companions := islandCompanions(best, all, lang); len(companions) > 0 {
			r := pointOf(best, placeName(best.GazetteerRow)+" ("+kindWord(best.Kind, lang)+")")
			r.Alternatives = companions
			var ls []string
			for _, t := range companions {
				ls = append(ls, t.Label)
				r.SourceIDs = credit(append(r.SourceIDs, t.SourceID)...)
			}
			r.Label += ", " + alsoWord(lang) + ": " + strings.Join(ls, ", ")
			return r
		}
		r := pointOf(best, placeName(best.GazetteerRow))
		r.AGS = resolvedAgsOf(best, all)
		decidedByPopulation := len(rows) > 1 && best.Score-rows[1].Score < WinnerMargin && !forecourt
		if decidedByPopulation {
			var namesakes []located
			for _, c := range contenders {
				if c.Kind == "city" && pop(c) >= NamesakeTownPopulation && len(namesakes) < 2 {
					namesakes = append(namesakes, c)
				}
			}
			if len(namesakes) > 0 {
				labels := placeOptionLabels(append([]located{best}, namesakes...), lang, asked, lands, kreise)
				var alts []Option
				for i, c := range namesakes {
					alts = append(alts, optionOf(c, labelsOr(labels, i+1, c)))
				}
				return withAlternatives(r, alts, lang)
			}
		}
		return r
	}
	if town := oneTownOf(best, contenders); town != nil {
		r := pointOf(*town, placeName(town.GazetteerRow))
		r.AGS = resolvedAgsOf(*town, all)
		return r
	}
	if field := oneStopFieldOf(best, contenders, wholeQuery); field != nil {
		return Resolution{Kind: ResolvedPoint, Lat: field.lat, Lon: field.lon, Label: placeName(field.GazetteerRow), AGS: agsOf(field.GazetteerRow),
			SourceIDs: credit(field.SourceID), Via: "gazetteer", PlaceKind: field.Kind}
	}
	if kreisfreiAnswers(best, contenders, all) {
		var others []located
		for _, c := range contenders {
			if pop(c) > 0 && len(others) < 2 {
				others = append(others, c)
			}
		}
		labels := placeOptionLabels(append([]located{best}, others...), lang, asked, lands, kreise)
		var alts []Option
		for i, c := range others {
			alts = append(alts, optionOf(c, labelsOr(labels, i+1, c)))
		}
		r := pointOf(best, placeName(best.GazetteerRow))
		r.AGS = resolvedAgsOf(best, all)
		return withAlternatives(r, alts, lang)
	}
	labels := placeOptionLabels(rows, lang, asked, lands, kreise)
	out := Resolution{Kind: ResolvedCandidates, Via: "gazetteer"}
	for i, c := range rows {
		o := optionOf(c, labelsOr(labels, i, c))
		o.ExtentKm = extentOf(c)
		out.Options = append(out.Options, o)
		out.SourceIDs = credit(append(out.SourceIDs, c.SourceID)...)
	}
	return out
}

// judged is a verdict over candidate rows: the resolution, whether its
// leading row matched exactly, and the folded rows it rests on.
type judged struct {
	res   Resolution
	exact bool
	rows  []located
}

// judgeCandidates reads candidate rows as the platform does: plausible
// matches, the area a container word names, a road number as the road, then
// typos ranked, views folded and the verdict decided.
func judgeCandidates(found []GazetteerRow, query, lang string, lands, kreise map[string]string) *judged {
	var matched, containers, rural []GazetteerRow
	for _, c := range found {
		if isPlausibleMatch(c, query) {
			matched = append(matched, c)
		}
	}
	if namesContainer(query) {
		for _, c := range matched {
			if containerKinds[c.Kind] {
				containers = append(containers, c)
			}
		}
	}
	rural = containers
	if namesLandkreis(query) {
		rural = nil
		for _, c := range containers {
			if !isCityKreis(c, matched) {
				rural = append(rural, c)
			}
		}
	}
	plausible := matched
	switch {
	case len(rural) > 0:
		plausible = rural
	case len(containers) > 0:
		plausible = containers
	}
	if len(plausible) == 0 {
		return nil
	}
	if road := RoadForm(query); road != "" {
		for _, c := range plausible {
			if c.Kind == "motorway" && c.Road != nil && *c.Road == road {
				return &judged{res: unresolved("no_coordinate", query), exact: true}
			}
		}
	}
	all := locate(plausible)
	if len(all) == 0 {
		exact := slices.ContainsFunc(plausible, func(c GazetteerRow) bool { return c.Score >= 1 })
		return &judged{res: unresolved("no_coordinate", query), exact: exact}
	}
	ranked := rankTypos(all, query)
	folded := foldViews(ranked)
	asked := FoldQuery(query)
	return &judged{res: decide(folded, lang, ranked, asked, lands, asked, kreise), exact: folded[0].Score >= 1, rows: folded}
}
