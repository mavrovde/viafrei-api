package places

import (
	"math"
	"regexp"
	"strings"
)

// The resolver's reading of candidate rows (place.ts): which rows are views
// of one place, which are an island and its town, and how far apart two
// rows lie. The numbers are the platform's.

// The resolver's numbers: the candidates asked for, the score margin a
// winner needs, the kilometres two rows of one place lie within, the
// population a dominant row and a namesake town have, the factor a dominant
// row outweighs its rivals by, and the similarity below which a row is noise.
const (
	MaxCandidates          = 5
	WinnerMargin           = 0.15
	samePlaceKm            = 5.0
	DominantPopulation     = 10_000
	DominanceFactor        = 10
	NamesakeTownPopulation = 20_000
	SimilarityFloor        = 0.45
	sameStationKm          = 0.3
	IslandReachKm          = 25.0
	townIsIslandKm         = 10.0
	sameTownPopulationRate = 1.5
	SameTownKm             = 10.0
)

// ForeignPlaces are well-known places abroad, by fold (FOREIGN_PLACES).
var ForeignPlaces = setOf(
	"paris", "wien", "vienna", "zurich", "zuerich", "prag", "prague", "praha", "london", "amsterdam", "brussel", "bruessel",
	"brussels", "bruxelles", "rom", "rome", "roma", "mailand", "milan", "milano", "venedig", "venice", "warschau", "warsaw",
	"kopenhagen", "copenhagen", "budapest", "madrid", "barcelona", "lissabon", "lisbon", "stockholm", "oslo", "helsinki",
	"basel", "bern", "genf", "geneva", "geneve", "salzburg", "innsbruck", "graz", "linz", "strassburg", "strasbourg",
	"luxemburg", "luxembourg", "krakau", "krakow", "moskau", "moscow", "istanbul", "athen", "athens", "new york",
)

// located is a candidate row with a point.
type located struct {
	GazetteerRow
	lat, lon float64
}

// locate keeps the rows with a point.
func locate(rows []GazetteerRow) []located {
	var out []located
	for _, r := range rows {
		if r.Lat != nil && r.Lon != nil {
			out = append(out, located{GazetteerRow: r, lat: *r.Lat, lon: *r.Lon})
		}
	}
	return out
}

func pop(c located) int {
	if c.Population == nil {
		return 0
	}
	return *c.Population
}

var (
	containerKinds = setOf("district", "admin")
	transportKinds = setOf("station", "stop")
	areaKinds      = setOf("city", "district", "admin", "island")
	eightDigits    = regexp.MustCompile(`^\d{8}$`)
	containerCode  = regexp.MustCompile(`^\d{2,5}$`)
	twoDigits      = regexp.MustCompile(`^\d{2}$`)
)

func municipalityKey(c located) string {
	if c.AdminCode != nil && eightDigits.MatchString(*c.AdminCode) {
		return *c.AdminCode
	}
	return ""
}

func containerKey(c located) string {
	if c.AdminCode != nil && containerCode.MatchString(*c.AdminCode) {
		return *c.AdminCode
	}
	return ""
}

// distanceKm is the great-circle distance of two points (haversine, R 6371).
func distanceKm(aLat, aLon, bLat, bLon float64) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := rad(bLat-aLat), rad(bLon-aLon)
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(aLat))*math.Cos(rad(bLat))*math.Pow(math.Sin(dLon/2), 2)
	return 6371 * 2 * math.Asin(math.Min(1, math.Sqrt(h)))
}

func kmOf(a, b located) float64 { return distanceKm(a.lat, a.lon, b.lat, b.lon) }

func belongsToIsland(settlement, island located) bool {
	if settlement.Kind != "city" || island.Kind != "island" || settlement.NameNorm != island.NameNorm {
		return false
	}
	km := kmOf(settlement, island)
	return km <= townIsIslandKm || (pop(settlement) > 0 && km <= IslandReachKm)
}

func islandTownsOf(island located, rows []located) []located {
	var out []located
	for _, c := range rows {
		if c.Score == island.Score && belongsToIsland(c, island) {
			out = append(out, c)
		}
	}
	return out
}

// placeIslands puts an island's village first when the island leads, the
// island when only its towns further off share the name, and drops an
// island no town of its name belongs to while a town of the name is there.
func placeIslands(rows []located) []located {
	if len(rows) == 0 {
		return rows
	}
	head := rows[0]
	dropped := map[string]bool{}
	var lead *located
	for _, island := range rows {
		if island.Kind != "island" {
			continue
		}
		own := islandTownsOf(island, rows)
		leads := island.NameNorm == head.NameNorm && island.Score == head.Score
		var village *located
		for i := range own {
			if kmOf(own[i], island) <= townIsIslandKm {
				village = &own[i]
				break
			}
		}
		if village != nil {
			if leads && lead == nil {
				lead = village
			}
			continue
		}
		if len(own) > 0 {
			if leads && lead == nil {
				is := island
				lead = &is
			}
			continue
		}
		for _, c := range rows {
			if c.Kind == "city" && (c.NameNorm == island.NameNorm || strings.HasPrefix(c.NameNorm, island.NameNorm+" ")) {
				dropped[island.ID] = true
				break
			}
		}
	}
	var kept []located
	for _, c := range rows {
		if !dropped[c.ID] && (lead == nil || c.ID != lead.ID) {
			kept = append(kept, c)
		}
	}
	if lead == nil {
		return kept
	}
	return append([]located{*lead}, kept...)
}

func foldedEnglishName(c located) string {
	if c.NameEn == nil {
		return ""
	}
	return normalizeName(*c.NameEn)
}

// englishFolds are the folded English names of the rows, by their fold.
type englishFolds map[string]map[string]bool

func englishNamesByFold(rows []located) englishFolds {
	out := englishFolds{}
	for _, c := range rows {
		en := foldedEnglishName(c)
		if en == "" {
			continue
		}
		if out[c.NameNorm] == nil {
			out[c.NameNorm] = map[string]bool{}
		}
		out[c.NameNorm][en] = true
	}
	return out
}

func englishNamesOf(c located, byFold englishFolds) map[string]bool {
	if g, ok := byFold[c.NameNorm]; ok {
		return g
	}
	if own := foldedEnglishName(c); own != "" {
		return map[string]bool{own: true}
	}
	return map[string]bool{}
}

func isTranslationOf(a, b located, byFold englishFolds) bool {
	bEns := englishNamesOf(b, byFold)
	for en := range englishNamesOf(a, byFold) {
		if bEns[en] || en == b.NameNorm {
			return true
		}
	}
	for en := range bEns {
		if en == a.NameNorm {
			return true
		}
	}
	return false
}

func isExonymRow(c located) bool {
	en := foldedEnglishName(c)
	return en != "" && en == c.NameNorm
}

// isAnotherViewOf says whether c is another row of the place kept is.
func isAnotherViewOf(kept, c located, byFold englishFolds, municipalities, outerKeys map[string]bool) bool {
	if c.ID == kept.ID || kept.Score-c.Score >= WinnerMargin {
		return false
	}
	if kl := landKeyOf(kept.Kind, kept.AdminCode, kept.NameNorm, kept.NameEn); kl != "" && kl == landKeyOf(c.Kind, c.AdminCode, c.NameNorm, c.NameEn) {
		return true
	}
	if isSameTownTwoNames(kept, c) {
		return true
	}
	if transportKinds[kept.Kind] && transportKinds[c.Kind] && kept.Kind != c.Kind && wordsContain(kept.NameNorm, c.NameNorm) {
		return kmOf(kept, c) <= sameStationKm
	}
	if c.NameNorm != kept.NameNorm && !isTranslationOf(kept, c, byFold) && !isQualifiedNameOf(kept, c) {
		return false
	}
	if belongsToIsland(c, kept) || belongsToIsland(kept, c) {
		return true
	}
	if k := municipalityKey(c); k != "" && municipalities[k] {
		return true
	}
	if isSameNamedTown(kept, c) {
		return true
	}
	if kept.Kind == "city" && containerKinds[c.Kind] {
		for o := range outerKeys {
			for m := range municipalities {
				if strings.HasPrefix(m, o) {
					return true
				}
			}
		}
	}
	sizeless := transportKinds[c.Kind] && !transportKinds[kept.Kind]
	if c.Rank > kept.Rank && !sizeless {
		return false
	}
	if kept.Kind == "city" && containerKinds[c.Kind] {
		return true
	}
	if isSameArea(kept, c) {
		return true
	}
	return kmOf(kept, c) <= samePlaceKm
}

func isSameArea(a, b located) bool {
	if !containerKinds[a.Kind] || a.Kind != b.Kind {
		return false
	}
	if a.Population == nil || *a.Population <= 0 || b.Population == nil || *a.Population != *b.Population {
		return false
	}
	return kmOf(a, b) <= radiusOf(a)
}

func isQualifiedNameOf(a, b located) bool {
	if transportKinds[a.Kind] || transportKinds[b.Kind] {
		return false
	}
	return strings.HasPrefix(a.NameNorm, b.NameNorm+" ") || strings.HasPrefix(b.NameNorm, a.NameNorm+" ")
}

func ratioOver(pa, pb int) bool {
	return float64(max(pa, pb))/float64(min(pa, pb)) > sameTownPopulationRate
}

func isSameNamedTown(a, b located) bool {
	if a.NameNorm != b.NameNorm {
		return false
	}
	var town, other located
	switch {
	case a.Kind == "city":
		town, other = a, b
	case b.Kind == "city":
		town, other = b, a
	default:
		return false
	}
	if pop(town) <= 0 {
		return false
	}
	if transportKinds[other.Kind] {
		return kmOf(a, b) <= SameTownKm
	}
	if other.Kind != "city" {
		return false
	}
	pa, pb := pop(a), pop(b)
	if pa <= 0 || pb <= 0 || ratioOver(pa, pb) {
		return false
	}
	return kmOf(a, b) <= SameTownKm
}

func isSameTownTwoNames(a, b located) bool {
	if a.Kind != "city" || b.Kind != "city" || !isQualifiedNameOf(a, b) {
		return false
	}
	pa, pb := pop(a), pop(b)
	if pa <= 0 || pb <= 0 || ratioOver(pa, pb) {
		return false
	}
	return kmOf(a, b) <= samePlaceKm
}

// AgsLand is a German Land: its two-digit key, its names and their folds.
type AgsLand struct{ AGS, Name, NameNorm, NameEn, NameEnNorm string }

// Laender are the sixteen Länder (AGS_LAENDER).
var Laender = []AgsLand{
	{"01", "Schleswig-Holstein", "schleswig holstein", "Schleswig-Holstein", "schleswig holstein"},
	{"02", "Hamburg", "hamburg", "Hamburg", "hamburg"},
	{"03", "Niedersachsen", "niedersachsen", "Lower Saxony", "lower saxony"},
	{"04", "Bremen", "bremen", "Bremen", "bremen"},
	{"05", "Nordrhein-Westfalen", "nordrhein westfalen", "North Rhine-Westphalia", "north rhine westphalia"},
	{"06", "Hessen", "hessen", "Hesse", "hesse"},
	{"07", "Rheinland-Pfalz", "rheinland pfalz", "Rhineland-Palatinate", "rhineland palatinate"},
	{"08", "Baden-Württemberg", "baden wurttemberg", "Baden-Württemberg", "baden wurttemberg"},
	{"09", "Bayern", "bayern", "Bavaria", "bavaria"},
	{"10", "Saarland", "saarland", "Saarland", "saarland"},
	{"11", "Berlin", "berlin", "Berlin", "berlin"},
	{"12", "Brandenburg", "brandenburg", "Brandenburg", "brandenburg"},
	{"13", "Mecklenburg-Vorpommern", "mecklenburg vorpommern", "Mecklenburg-Western Pomerania", "mecklenburg western pomerania"},
	{"14", "Sachsen", "sachsen", "Saxony", "saxony"},
	{"15", "Sachsen-Anhalt", "sachsen anhalt", "Saxony-Anhalt", "saxony anhalt"},
	{"16", "Thüringen", "thuringen", "Thuringia", "thuringia"},
}

// landKeyOf is the Land key of a Land row: its two-digit key, else the Land
// its name is; "" for any other row.
func landKeyOf(kind string, adminCode *string, nameNorm string, nameEn *string) string {
	if kind != "admin" {
		return ""
	}
	if adminCode != nil && *adminCode != "" {
		if twoDigits.MatchString(*adminCode) {
			return *adminCode
		}
		return ""
	}
	en := ""
	if nameEn != nil {
		en = normalizeName(*nameEn)
	}
	for _, l := range Laender {
		if l.NameNorm == nameNorm || l.NameEnNorm == nameNorm || (nameEn != nil && (l.NameEnNorm == en || l.NameNorm == en)) {
			return l.AGS
		}
	}
	return ""
}

func containerKeysOf(c located, all []located) map[string]bool {
	keys := map[string]bool{}
	if !containerKinds[c.Kind] {
		return keys
	}
	for i, r := range append([]located{c}, all...) {
		key := containerKey(r)
		if key == "" || !containerKinds[r.Kind] || r.NameNorm != c.NameNorm {
			continue
		}
		samePopulation := r.Population != nil && *r.Population > 0 && c.Population != nil && *r.Population == *c.Population
		if i > 0 && r.ID != c.ID && !samePopulation && kmOf(c, r) > samePlaceKm {
			continue
		}
		keys[key] = true
	}
	return keys
}

func wordsContain(a, b string) bool {
	wa, wb := setOf(strings.Fields(a)...), setOf(strings.Fields(b)...)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	small, big := wa, wb
	if len(wa) > len(wb) {
		small, big = wb, wa
	}
	for w := range small {
		if !big[w] {
			return false
		}
	}
	return true
}

func municipalitiesOf(kept located, all []located, byFold englishFolds) map[string]bool {
	keys := map[string]bool{}
	for i, r := range append([]located{kept}, all...) {
		key := municipalityKey(r)
		if key == "" {
			continue
		}
		other := i > 0 && r.ID != kept.ID
		if other && r.NameNorm != kept.NameNorm && !isTranslationOf(kept, r, byFold) {
			continue
		}
		if other && kmOf(kept, r) > samePlaceKm {
			continue
		}
		keys[key] = true
	}
	return keys
}
