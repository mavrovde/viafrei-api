package places

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The resolver's labels (distinguish.ts): a row's name with the qualifiers
// that tell it apart from the other rows of a list, in the answer's
// language.

// distinguishable is a row to label: its name, context and always-shown
// qualifiers, the tiers added only to tell it apart, whether its name is
// the asked one, whether it carries no key of its own and the tier that
// then names it.
type distinguishable struct {
	name           string
	context        []*string
	always         []*string
	tiers          []*string
	asked, unkeyed bool
	ownTier        *int
}

func sp(s string) *string { return &s }

func present(values []*string) []string {
	var out []string
	for _, v := range values {
		if v != nil && strings.TrimSpace(*v) != "" {
			out = append(out, *v)
		}
	}
	return out
}

func bracketed(name string, qualifiers []string) string {
	if len(qualifiers) == 0 {
		return name
	}
	return name + " (" + strings.Join(qualifiers, ", ") + ")"
}

func labelOf(row distinguishable, spent []int) string {
	s := slices.Clone(spent)
	slices.Sort(s)
	q := append(slices.Clone(row.context), row.always...)
	for _, t := range s {
		q = append(q, tierAt(row, t))
	}
	return bracketed(row.name, present(q))
}

func bareLabel(row distinguishable) string { return bracketed(row.name, present(row.context)) }

func labelReadings(row distinguishable) []string {
	always := present(append(slices.Clone(row.context), row.always...))
	tiers := present(row.tiers)
	var out []string
	for mask := 0; mask < 1<<len(tiers); mask++ {
		q := slices.Clone(always)
		for i, t := range tiers {
			if mask&(1<<i) != 0 {
				q = append(q, t)
			}
		}
		out = append(out, bracketed(row.name, q))
	}
	return out
}

func confusers(rows []distinguishable, i int, label string) []int {
	row := rows[i]
	var others, siblings []int
	for j := range rows {
		if j != i {
			others = append(others, j)
			if rows[j].name == row.name {
				siblings = append(siblings, j)
			}
		}
	}
	bare := label == row.name || label == bareLabel(row)
	if bare && row.asked {
		return others
	}
	if len(siblings) == 0 || bare {
		return siblings
	}
	var out []int
	for _, j := range siblings {
		if slices.Contains(labelReadings(rows[j]), label) {
			out = append(out, j)
		}
	}
	return out
}

func tierAt(row distinguishable, t int) *string {
	if t < len(row.tiers) {
		return row.tiers[t]
	}
	return nil
}

func sameValue(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func maxDepth(rows []distinguishable, of []int) int {
	d := 0
	for _, i := range of {
		d = max(d, len(rows[i].tiers))
	}
	return d
}

func allIdx(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func answerablePass(rows []distinguishable, spent [][]int, identifying map[int]bool) {
	depth := maxDepth(rows, allIdx(len(rows)))
	for round := 0; round <= depth; round++ {
		refined := false
		for i, row := range rows {
			label := labelOf(row, spent[i])
			others := confusers(rows, i, label)
			if len(others) == 0 {
				if !row.unkeyed || round > 0 || (label != row.name && label != bareLabel(row)) {
					continue
				}
				own := row.ownTier
				if own == nil {
					for t := range row.tiers {
						if (identifying == nil || identifying[t]) && row.tiers[t] != nil && strings.TrimSpace(*row.tiers[t]) != "" {
							own = &t
							break
						}
					}
				}
				if own == nil {
					continue
				}
				spent[i] = append(spent[i], *own)
				refined = true
				continue
			}
			usable := func(t int) bool {
				v := row.tiers[t]
				return (identifying == nil || identifying[t]) && !slices.Contains(spent[i], t) && v != nil && strings.TrimSpace(*v) != ""
			}
			differs := func(t, j int) bool { return !sameValue(tierAt(rows[j], t), row.tiers[t]) }
			var tiers []int
			for t := range row.tiers {
				if usable(t) {
					tiers = append(tiers, t)
				}
			}
			pick := -1
			for _, t := range tiers {
				if !slices.ContainsFunc(others, func(j int) bool { return !differs(t, j) }) {
					pick = t
					break
				}
			}
			if pick < 0 {
				for _, t := range tiers {
					if slices.ContainsFunc(others, func(j int) bool { return differs(t, j) }) {
						pick = t
						break
					}
				}
			}
			if pick < 0 {
				continue
			}
			spent[i] = append(spent[i], pick)
			refined = true
		}
		if !refined {
			return
		}
	}
}

func collisions(labels []string) [][]int {
	var order []string
	by := map[string][]int{}
	for i, l := range labels {
		if _, ok := by[l]; !ok {
			order = append(order, l)
		}
		by[l] = append(by[l], i)
	}
	var out [][]int
	for _, l := range order {
		if len(by[l]) > 1 {
			out = append(out, by[l])
		}
	}
	return out
}

func separatingTier(rows []distinguishable, group []int, spent map[int]bool) int {
	for t := 0; t < maxDepth(rows, group); t++ {
		if spent[t] {
			continue
		}
		values := map[string]bool{}
		for _, i := range group {
			v := ""
			if p := tierAt(rows[i], t); p != nil {
				v = *p
			}
			values[v] = true
		}
		if len(values) > 1 {
			return t
		}
	}
	return -1
}

func numbered(labels []string) []string {
	counts := map[string]int{}
	for _, l := range labels {
		counts[l]++
	}
	used := map[string]bool{}
	for _, l := range labels {
		if counts[l] == 1 {
			used[l] = true
		}
	}
	out := make([]string, len(labels))
	for i, l := range labels {
		if counts[l] == 1 {
			out[i] = l
			continue
		}
		n := i + 1
		c := l + " #" + strconv.Itoa(n)
		for used[c] {
			n++
			c = l + " #" + strconv.Itoa(n)
		}
		used[c] = true
		out[i] = c
	}
	return out
}

// distinctLabels labels every row so no two read the same: the tiers that
// separate a clash first, then (answerable) the tiers a person can answer
// by, then a number for what still reads the same.
func distinctLabels(rows []distinguishable, answerable bool, identifying []int) []string {
	spent := make([][]int, len(rows))
	labels := func() []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = labelOf(r, spent[i])
		}
		return out
	}
	depth := maxDepth(rows, allIdx(len(rows)))
	for round := 0; round <= depth; round++ {
		clashing := collisions(labels())
		if len(clashing) == 0 {
			break
		}
		refined := false
		for _, group := range clashing {
			used := map[int]bool{}
			for _, i := range group {
				for _, t := range spent[i] {
					used[t] = true
				}
			}
			t := separatingTier(rows, group, used)
			if t < 0 {
				continue
			}
			for _, i := range group {
				spent[i] = append(spent[i], t)
			}
			refined = true
		}
		if !refined {
			break
		}
	}
	if answerable {
		var id map[int]bool
		if identifying != nil {
			id = map[int]bool{}
			for _, t := range identifying {
				id[t] = true
			}
		}
		answerablePass(rows, spent, id)
	}
	return numbered(labels())
}

var kindWords = map[string][2]string{
	"city": {"Stadt/Gemeinde", "town"}, "district": {"Kreis", "district"}, "admin": {"Verwaltungseinheit", "administrative area"},
	"station": {"Bahnhof", "station"}, "stop": {"Haltestelle", "stop"}, "motorway": {"Autobahn", "motorway"},
	"junction": {"Anschlussstelle", "junction"}, "island": {"Insel", "island"},
}

func langIdx(lang string) int {
	if lang == "en" {
		return 1
	}
	return 0
}

// kindWord is a gazetteer kind in the language.
func kindWord(kind, lang string) string {
	if w, ok := kindWords[kind]; ok {
		return w[langIdx(lang)]
	}
	return kind
}

// countIn is a count grouped by thousands as the language writes it.
func countIn(lang string, n int) string {
	digits := strconv.Itoa(max(n, -n))
	sep := "."
	if lang == "en" {
		sep = ","
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	if n < 0 {
		return "-" + b.String()
	}
	return b.String()
}

func populationWord(population int, lang string) string {
	if lang == "de" {
		return countIn(lang, population) + " Einw."
	}
	return "pop. " + countIn(lang, population)
}

var sourceWords = map[string]string{"geonames": "GeoNames", "bkg_gvisys": "BKG", "db_stada": "DB StaDa", "delfi_zhv": "DELFI zHV", "autobahn": "Autobahn GmbH"}

func sourceWord(id string) string {
	if w, ok := sourceWords[id]; ok {
		return w
	}
	return id
}

func coordinateWord(lat, lon float64) string {
	return strconv.FormatFloat(lat, 'f', 3, 64) + ", " + strconv.FormatFloat(lon, 'f', 3, 64)
}

// placeName is a row's shown name: a motorway's road number, else its name.
func placeName(c GazetteerRow) string {
	if c.Kind == "motorway" && c.Road != nil && strings.TrimSpace(*c.Road) != "" {
		return *c.Road
	}
	return c.Name
}

func adminWord(c GazetteerRow) *string {
	if c.AdminCode != nil && strings.TrimSpace(*c.AdminCode) != "" {
		return c.AdminCode
	}
	return nil
}

var (
	rowInAKreis   = setOf("city", "island", "station", "stop", "junction")
	cityStateCode = regexp.MustCompile(`^\d{2}000000$`)
	landPrefix    = regexp.MustCompile(`^\d{2,8}$`)
)

func rowGemeindeKey(kind string, adminCode *string) string {
	if !rowInAKreis[kind] || adminCode == nil || !eightDigits.MatchString(*adminCode) || (*adminCode)[2:] == "000000" {
		return ""
	}
	return *adminCode
}

func kreisCode(kind string, adminCode *string) string {
	key := rowGemeindeKey(kind, adminCode)
	if key == "" || (strings.HasSuffix(key, "000") && kind == "city") {
		return ""
	}
	return key[:5]
}

func kreisWord(kind string, adminCode *string, names map[string]string, lang string) *string {
	key := rowGemeindeKey(kind, adminCode)
	if key == "" {
		return nil
	}
	free := strings.HasSuffix(key, "000")
	if free && kind == "city" {
		return sp([2]string{"kreisfreie Stadt", "independent city"}[langIdx(lang)])
	}
	name, ok := names[key[:5]]
	if !ok {
		return nil
	}
	switch {
	case free:
		return sp([2]string{"kreisfreie Stadt ", "independent city "}[langIdx(lang)] + name)
	case lang == "en":
		return sp("district " + name)
	case kreisNamed.MatchString(normalizeName(name)):
		return sp(name)
	}
	return sp("Kreis " + name)
}

func placeTiers(c located, lang string) []*string {
	var population *string
	if c.Population != nil && *c.Population > 0 {
		population = sp(populationWord(*c.Population, lang))
	}
	return []*string{sp(kindWord(c.Kind, lang)), population, sp(sourceWord(c.SourceID)), sp(coordinateWord(c.lat, c.lon))}
}

var placeIdentifyingTiers = []int{1, 3}

// landWord is the Land a row's key (else land) names, in the language.
func landWord(c GazetteerRow, lang, land string) *string {
	prefix := land
	if c.AdminCode != nil && landPrefix.MatchString(*c.AdminCode) {
		prefix = (*c.AdminCode)[:2]
	}
	for _, l := range Laender {
		if prefix != "" && l.AGS == prefix {
			if lang == "en" {
				return sp(l.NameEn)
			}
			return sp(l.Name)
		}
	}
	return nil
}

func nameSays(c GazetteerRow, word string) bool {
	return strings.Contains(" "+normalizeName(placeName(c))+" ", " "+normalizeName(word)+" ")
}

func placeRow(c located, lang, asked, land string, kreise map[string]string) distinguishable {
	var word *string
	if c.Kind != "admin" || c.AdminCode == nil || len(*c.AdminCode) != 2 {
		word = landWord(c.GazetteerRow, lang, land)
	}
	var context []*string
	if word != nil && !nameSays(c.GazetteerRow, *word) {
		context = []*string{word}
	}
	kreis := kreisWord(c.Kind, c.AdminCode, kreise, lang)
	tiers := placeTiers(c, lang)
	name := placeName(c.GazetteerRow)
	isAsked := asked != "" && stripAdminWords(normalizeName(name)) == asked
	if kreis != nil {
		var kreisName *string
		if c.AdminCode != nil && len(*c.AdminCode) >= 5 {
			if n, ok := kreise[(*c.AdminCode)[:5]]; ok {
				kreisName = &n
			}
		}
		always := []*string{kreis}
		if kreisName != nil && c.Kind != "city" && nameSays(c.GazetteerRow, *kreisName) {
			always = nil
		}
		return distinguishable{name: name, context: context, always: always, tiers: tiers, asked: isAsked}
	}
	if rowInAKreis[c.Kind] && c.AdminCode != nil && cityStateCode.MatchString(*c.AdminCode) {
		return distinguishable{name: name, context: context, always: []*string{tiers[0]}, tiers: append([]*string{nil}, tiers[1:]...), asked: isAsked}
	}
	container := c.Kind == "district" || c.Kind == "admin"
	unkeyed := container || (adminWord(c.GazetteerRow) == nil && (c.Kind == "city" || c.Kind == "island"))
	var always *string
	if kreisCode(c.Kind, c.AdminCode) != "" {
		always = adminWord(c.GazetteerRow)
	}
	row := distinguishable{name: name, context: context, always: []*string{always}, tiers: tiers, asked: isAsked, unkeyed: unkeyed}
	if container {
		row.ownTier = new(int)
	}
	return row
}

// placeOptionLabels labels a list of rows so a person can answer it.
func placeOptionLabels(cands []located, lang, asked string, lands, kreise map[string]string) []string {
	rows := make([]distinguishable, len(cands))
	for i, c := range cands {
		rows[i] = placeRow(c, lang, asked, lands[c.ID], kreise)
	}
	return distinctLabels(rows, true, placeIdentifyingTiers)
}
