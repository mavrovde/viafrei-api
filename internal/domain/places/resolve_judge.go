package places

import (
	"regexp"
	"slices"
	"strings"
)

// The resolver's judgement of candidate rows (place.ts): which rows are a
// match at all, typos ranked, views of one place folded into one.

// answersAlone: a populated winner outweighs every contender tenfold.
func answersAlone(best located, contenders []located) bool {
	own := pop(best)
	if own < DominantPopulation {
		return false
	}
	for _, c := range contenders {
		if pop(c)*DominanceFactor > own {
			return false
		}
	}
	return true
}

var (
	containerPrefix = regexp.MustCompile(`^(landkreis|kreis|lkr|lk|stadtkreis|regierungsbezirk|bundesland|freistaat)\s`)
	landkreisPrefix = regexp.MustCompile(`^(landkreis|kreis|lkr|lk)\s`)
)

func namesContainer(query string) bool { return containerPrefix.MatchString(normalizeName(query)) }
func namesLandkreis(query string) bool { return landkreisPrefix.MatchString(normalizeName(query)) }

func isCityKreis(c GazetteerRow, rows []GazetteerRow) bool {
	if c.Kind != "district" {
		return false
	}
	for _, r := range rows {
		if r.Kind != "city" || r.NameNorm != c.NameNorm {
			continue
		}
		if c.AdminCode != nil && r.AdminCode != nil && *r.AdminCode == *c.AdminCode+"000" {
			return true
		}
		if c.Population != nil && *c.Population > 0 && r.Population != nil && *r.Population == *c.Population {
			return true
		}
	}
	return false
}

var notASharedName = setOf("bad", "sankt", "st", "der", "die", "das", "den", "dem", "des", "bei", "auf", "und", "vor", "von", "vom", "zum", "zur",
	"ober", "unter", "nieder", "gross", "klein", "alt", "neu", "hohen")

// The typo numbers: the fewest letters a one-slip match needs, and the
// slips a typo may make.
const (
	OneSlipMinLetters = 5
	TypoDistance      = 2
)

func typedForms(query string) (typed, expanded string) {
	typed = FoldQuery(query)
	expanded, _ = transliterateDigraphs(typed)
	return typed, expanded
}

func rowNames(c GazetteerRow) []string {
	names := []string{c.NameNorm}
	if c.NameEn != nil {
		names = append(names, normalizeName(*c.NameEn))
	}
	return names
}

// isPlausibleMatch: a row above the noise that resembles the string in more
// than letters.
func isPlausibleMatch(c GazetteerRow, query string) bool {
	if c.Score >= 1-TransliterationPenalty {
		return true
	}
	typed, expanded := typedForms(query)
	names := rowNames(c)
	if isOneSlip(typed, expanded, names) {
		return true
	}
	if c.Score < SimilarityFloor {
		return false
	}
	slips := max(TypoDistance, runeLen(typed)/5)
	for _, n := range names {
		if typoDistance(typed, n) <= slips || typoDistance(expanded, n) <= slips {
			return true
		}
	}
	words := map[string]bool{}
	for _, w := range append(strings.Split(typed, " "), strings.Split(expanded, " ")...) {
		if runeLen(w) >= 3 && !notASharedName[w] {
			words[w] = true
		}
	}
	for _, n := range names {
		for _, w := range strings.Split(n, " ") {
			if words[w] {
				return true
			}
		}
	}
	return false
}

func runeLen(s string) int { return len([]rune(s)) }

func isOneSlip(typed, expanded string, names []string) bool {
	if runeLen(strings.ReplaceAll(typed, " ", "")) < OneSlipMinLetters {
		return false
	}
	for _, n := range names {
		if typoDistance(typed, n) <= 1 || typoDistance(expanded, n) <= 1 {
			return true
		}
	}
	return false
}

// typoDistance is the optimal-string-alignment distance of two texts.
func typoDistance(as, bs string) int {
	a, b := []rune(as), []rune(bs)
	m, n := len(a), len(b)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}
	d := make([][]int, m+1)
	for i := range d {
		d[i] = make([]int, n+1)
		d[i][0] = i
	}
	for j := 0; j <= n; j++ {
		d[0][j] = j
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, d[i-2][j-2]+1)
			}
			d[i][j] = v
		}
	}
	return d[m][n]
}

// rankTypos: with no exact row, the rows a typo or two away first, by rank.
func rankTypos(rows []located, query string) []located {
	out := slices.Clone(rows)
	for _, c := range rows {
		if c.Score >= 1 {
			return out
		}
	}
	typed, expanded := typedForms(query)
	band := func(c located) int {
		d := min(typoDistance(typed, c.NameNorm), typoDistance(expanded, c.NameNorm))
		if d <= TypoDistance {
			return d
		}
		return TypoDistance + 1
	}
	slices.SortStableFunc(out, func(a, b located) int {
		da, db := band(a), band(b)
		if da != db {
			return da - db
		}
		if da <= TypoDistance && a.Rank != b.Rank {
			return b.Rank - a.Rank
		}
		return 0
	})
	return out
}

func moveFirst(rows []located, first located) []located {
	out := []located{first}
	for _, c := range rows {
		if c.ID != first.ID {
			out = append(out, c)
		}
	}
	return out
}

// foldViews keeps one row a place: the endonym before its exonym, the Land's
// official row, a town before its stations, a city before the district of
// its name, an island's town, then every other view of a kept place folded.
func foldViews(all []located) []located {
	head := all[0]
	byFold := englishNamesByFold(all)
	winner := head
	if isExonymRow(head) {
		for _, c := range all {
			if c.Score == head.Score && c.Rank == head.Rank && c.NameNorm != head.NameNorm && isTranslationOf(head, c, byFold) && !isExonymRow(c) {
				winner = c
				break
			}
		}
	}
	byScore := all
	if winner.ID != all[0].ID {
		byScore = moveFirst(all, winner)
	}
	if landHead := landKeyOf(byScore[0].Kind, byScore[0].AdminCode, byScore[0].NameNorm, byScore[0].NameEn); landHead != "" {
		for _, c := range byScore {
			if c.Kind == "admin" && c.AdminCode != nil && *c.AdminCode == landHead && c.Score == byScore[0].Score {
				if c.ID != byScore[0].ID {
					byScore = moveFirst(byScore, c)
				}
				break
			}
		}
	}
	stationFirst := townsBeforeTheirStations(byScore)
	ordered := stationFirst
	if area := stationFirst[0]; containerKinds[area.Kind] {
		for _, c := range stationFirst {
			if c.Kind == "city" && c.NameNorm == area.NameNorm && c.Score == area.Score && pop(c) >= DominantPopulation && liesInside(c, area, stationFirst, byFold) {
				ordered = moveFirst(stationFirst, c)
				break
			}
		}
	}
	ordered = placeIslands(ordered)
	var kept []located
	keysOf := map[string]map[string]bool{}
	municipalities := func(k located) map[string]bool {
		if keys, ok := keysOf[k.ID]; ok {
			return keys
		}
		keys := municipalitiesOf(k, ordered, byFold)
		keysOf[k.ID] = keys
		return keys
	}
	for _, c := range ordered {
		outer := containerKeysOf(c, ordered)
		if !slices.ContainsFunc(kept, func(k located) bool { return isAnotherViewOf(k, c, byFold, municipalities(k), outer) }) {
			kept = append(kept, c)
		}
	}
	return kept
}

func townsBeforeTheirStations(rows []located) []located {
	var out []located
	placed := map[string]bool{}
	for _, c := range rows {
		if placed[c.ID] {
			continue
		}
		if transportKinds[c.Kind] {
			for _, t := range rows {
				if !placed[t.ID] && t.Kind == "city" && t.NameNorm == c.NameNorm && t.Score == c.Score && (kmOf(c, t) <= samePlaceKm || isSameNamedTown(t, c)) {
					out = append(out, t)
					placed[t.ID] = true
					break
				}
			}
		}
		out = append(out, c)
		placed[c.ID] = true
	}
	return out
}

func liesInside(city, area located, all []located, byFold englishFolds) bool {
	outer, own := containerKeysOf(area, all), municipalitiesOf(city, all, byFold)
	if len(outer) > 0 && len(own) > 0 {
		for o := range outer {
			for m := range own {
				if strings.HasPrefix(m, o) {
					return true
				}
			}
		}
		return false
	}
	return kmOf(city, area) <= radiusOf(area)
}

func isKreisfrei(c located, all []located, byFold englishFolds) bool {
	if c.Kind != "city" {
		return false
	}
	for k := range municipalitiesOf(c, all, byFold) {
		if strings.HasSuffix(k, "000") {
			return true
		}
	}
	return false
}

func kreisfreiAnswers(best located, contenders, all []located) bool {
	own := pop(best)
	if own < DominantPopulation {
		return false
	}
	byFold := englishNamesByFold(all)
	if !isKreisfrei(best, all, byFold) {
		return false
	}
	for _, c := range contenders {
		if c.Kind != "city" || pop(c) >= own || isKreisfrei(c, all, byFold) || (c.NameNorm != best.NameNorm && !strings.HasPrefix(c.NameNorm, best.NameNorm+" ")) {
			return false
		}
	}
	return true
}

func forecourtAnswers(best located, contenders []located) bool {
	if best.Score < 1 || !transportKinds[best.Kind] {
		return false
	}
	for _, c := range contenders {
		if !(transportKinds[c.Kind] && kmOf(best, c) <= sameStationKm) && !isJunctionNamedAfter(c, best) {
			return false
		}
	}
	return true
}

func isJunctionNamedAfter(c, best located) bool {
	if c.Kind != "junction" || c.Score >= 1 {
		return false
	}
	core, prefixed := JunctionCore(c.Name)
	return prefixed && core == best.NameNorm
}
