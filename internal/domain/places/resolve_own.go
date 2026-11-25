package places

import (
	"regexp"
	"slices"
	"strings"
)

// A label of ours handed back (resolveOwnLabel): "Neustadt (Hessen, Kreis
// Marburg-Biedenkopf)" is the row the list labelled so, and the Lands and
// Kreise a list names its options with (withLands).

const (
	partCandidates = 30
	landNearM      = 15_000.0
)

var ownQualifier = func() string {
	parts := []string{
		`-?\d{1,3}\.\d{3}, -?\d{1,3}\.\d{3}`,
		`\d{1,3}(?:\.\d{3})* Einw\.`,
		`pop\. \d{1,3}(?:,\d{3})*`,
		`\d{2,8}`,
		`[^,()]*(?:kreis|Kreis)(?:[ -][^,()]+)?(?: \([^,()]+\))?`,
		`district [^,()]+(?: \([^,()]+\))?`,
		`(?:kreisfreie Stadt|independent city)(?: [^,()]+(?: \([^,()]+\))?)?`,
		`(?:Region|Stadtverband|Stadtregion|Städteregion) [^,()]+`,
	}
	for _, k := range []string{"city", "district", "admin", "station", "stop", "motorway", "junction", "island"} {
		parts = append(parts, regexp.QuoteMeta(kindWords[k][0]), regexp.QuoteMeta(kindWords[k][1]))
	}
	for _, s := range []string{"geonames", "bkg_gvisys", "db_stada", "delfi_zhv", "autobahn"} {
		parts = append(parts, regexp.QuoteMeta(sourceWords[s]))
	}
	for _, l := range Laender {
		parts = append(parts, regexp.QuoteMeta(l.Name), regexp.QuoteMeta(l.NameEn))
	}
	return strings.Join(parts, "|")
}()

var (
	ownBracket  = regexp.MustCompile(`^(.*?\S)\s*\(((?:` + ownQualifier + `)(?:, (?:` + ownQualifier + `))*)\)$`)
	trailingNum = regexp.MustCompile(`\s+#\d+$`)
	eightWord   = regexp.MustCompile(`\b\d{8}\b`)
)

// ownLabelName is the name of a label of ours (its qualifiers off) and
// whether it was numbered; ok false for a text no list labels so.
func ownLabelName(label string) (name string, isNumbered, ok bool) {
	trimmed := strings.TrimSpace(label)
	unnumbered := trailingNum.ReplaceAllString(trimmed, "")
	isNumbered = unnumbered != trimmed
	if m := ownBracket.FindStringSubmatch(unnumbered); m != nil {
		return m[1], isNumbered, true
	}
	if isNumbered && unnumbered != "" {
		return unnumbered, true, true
	}
	return "", false, false
}

// namesLand says whether a label of ours names a Land after its name.
func namesLand(label string) bool {
	name, _, ok := ownLabelName(label)
	if !ok {
		return false
	}
	tail := " " + normalizeName(label[len(name):]) + " "
	for _, l := range Laender {
		if strings.Contains(tail, " "+l.NameNorm+" ") || strings.Contains(tail, " "+l.NameEnNorm+" ") {
			return true
		}
	}
	return false
}

// placeReading is the reading of a row a label is, in either language, or "".
func placeReading(c located, label, land string, kreise map[string]string) string {
	want := normalizeName(label)
	for _, lang := range []string{"de", "en"} {
		row := placeRow(c, lang, "", land, kreise)
		keyed := row
		keyed.always = []*string{adminWord(c.GazetteerRow)}
		keyed.tiers = placeTiers(c, lang)
		noCtx, keyedNoCtx := row, keyed
		noCtx.context, keyedNoCtx.context = nil, nil
		for _, f := range []distinguishable{row, noCtx, keyed, keyedNoCtx} {
			for _, r := range labelReadings(f) {
				if normalizeName(r) == want {
					return r
				}
			}
		}
	}
	return ""
}

// landsOf are the Land keys of the rows whose own key names none, by id.
func (rv *resolver) landsOf(rows []located) (map[string]string, error) {
	var need []located
	for _, c := range rows {
		if c.AdminCode == nil || !landPrefix.MatchString(*c.AdminCode) {
			need = append(need, c)
		}
	}
	lands := map[string]string{}
	if len(need) == 0 {
		return lands, nil
	}
	lons, lats, ids := make([]float64, len(need)), make([]float64, len(need)), make([]string, len(need))
	for i, c := range need {
		lons[i], lats[i], ids[i] = c.lon, c.lat, c.ID
	}
	got, err := rv.reads.LandsNear(rv.ctx, lons, lats, landNearM, ids)
	if err != nil {
		return nil, err
	}
	for i, land := range got {
		if i >= 1 && i <= len(need) && twoDigits.MatchString(land) {
			lands[need[i-1].ID] = land
		}
	}
	return lands, nil
}

// kreiseOf are the names of the Kreise the rows lie in, by five-digit key.
func (rv *resolver) kreiseOf(rows []located) (map[string]string, error) {
	var codes []string
	for _, c := range rows {
		if k := kreisCode(c.Kind, c.AdminCode); k != "" && !slices.Contains(codes, k) {
			codes = append(codes, k)
		}
	}
	if len(codes) == 0 {
		return map[string]string{}, nil
	}
	return rv.reads.KreisNames(rv.ctx, codes)
}

func namesOthers(r Resolution) bool {
	return r.Kind == ResolvedCandidates || (r.Kind == ResolvedPoint && len(r.Alternatives) > 0)
}

// withLands judges again with the Lands and Kreise named, when the verdict
// names other places.
func (rv *resolver) withLands(j *judged, again func(lands, kreise map[string]string) *judged) (*judged, error) {
	if j == nil || !namesOthers(j.res) {
		return j, nil
	}
	lands, err := rv.landsOf(j.rows)
	if err != nil {
		return nil, err
	}
	kreise, err := rv.kreiseOf(j.rows)
	if err != nil {
		return nil, err
	}
	return again(lands, kreise), nil
}

// listedResolution is a verdict with its Lands and Kreise named.
func (rv *resolver) listedResolution(j *judged, found []GazetteerRow, query string) (Resolution, error) {
	w, err := rv.withLands(j, func(l, k map[string]string) *judged { return judgeCandidates(found, query, rv.lang, l, k) })
	if err != nil {
		return Resolution{}, err
	}
	if w == nil {
		return j.res, nil
	}
	return w.res, nil
}

// resolveOwnLabel answers a label of ours with the one row it reads as.
func (rv *resolver) resolveOwnLabel(query string) (*Resolution, error) {
	name, _, ok := ownLabelName(query)
	if !ok {
		return nil, nil
	}
	unnumbered := trailingNum.ReplaceAllString(strings.TrimSpace(query), "")
	raw, err := rv.findCandidates(name, partCandidates, CandidateOptions{})
	if err != nil {
		return nil, err
	}
	trigram := locate(raw)
	whole := normalizeName(unnumbered)
	if slices.ContainsFunc(trigram, func(c located) bool { return normalizeName(c.Name) == whole }) {
		return nil, nil
	}
	first, err := rv.readOwnLabel(unnumbered, locate(listedField(raw)))
	if err == nil && first == nil {
		first, err = rv.readOwnLabel(unnumbered, trigram)
	}
	if err == nil && first == nil {
		var shown []located
		shown, err = rv.withShownStops(name, trigram)
		if err == nil {
			first, err = rv.readOwnLabel(unnumbered, shown)
		}
	}
	if err != nil || first == nil {
		return nil, err
	}
	r := Resolution{Kind: ResolvedPoint, Lat: first.c.lat, Lon: first.c.lon, Label: first.reading, SourceIDs: credit(first.c.SourceID), Via: "gazetteer",
		PlaceKind: first.c.Kind, AreaCode: areaOf(first.c)}
	return &r, nil
}

func (rv *resolver) withShownStops(name string, trigram []located) ([]located, error) {
	fold := normalizeName(name)
	if fold == "" {
		return nil, nil
	}
	rows, err := rv.reads.ShownStops(rv.ctx, fold, partCandidates)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, c := range trigram {
		ids[c.ID] = true
	}
	var shown []located
	for _, c := range locate(rows) {
		if normalizeName(c.Name) == fold && !ids[c.ID] {
			shown = append(shown, c)
		}
	}
	if len(shown) == 0 {
		return nil, nil
	}
	return append(slices.Clone(trigram), shown...), nil
}

type reading struct {
	c       located
	reading string
}

func (rv *resolver) readOwnLabel(unnumbered string, rows []located) (*reading, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	lands, kreise := map[string]string{}, map[string]string{}
	var err error
	if namesLand(unnumbered) {
		if lands, err = rv.landsOf(rows); err != nil {
			return nil, err
		}
	}
	if !eightWord.MatchString(unnumbered) {
		if kreise, err = rv.kreiseOf(rows); err != nil {
			return nil, err
		}
	}
	var read, verbatim []reading
	for _, c := range rows {
		if r := placeReading(c, unnumbered, lands[c.ID], kreise); r != "" {
			read = append(read, reading{c, r})
			if r == unnumbered {
				verbatim = append(verbatim, reading{c, r})
			}
		}
	}
	matches := read
	if len(verbatim) == 1 {
		matches = verbatim
	}
	if one := oneReading(matches); one != nil {
		return one, nil
	}
	return oneReading(viewsListed(matches, rows)), nil
}

func oneReading(matches []reading) *reading {
	if len(matches) == 0 {
		return nil
	}
	lead := matches[0]
	at := coordinateWord(lead.c.lat, lead.c.lon)
	for _, m := range matches {
		if coordinateWord(m.c.lat, m.c.lon) != at {
			return townOfReadings(matches)
		}
	}
	return &lead
}

func viewsListed(matches []reading, rows []located) []reading {
	if len(matches) < 2 {
		return nil
	}
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b located) int {
		if c := cmpFloat(b.Score, a.Score); c != 0 {
			return c
		}
		return b.Rank - a.Rank
	})
	kept := map[string]bool{}
	for _, c := range foldViews(sorted) {
		kept[c.ID] = true
	}
	var out []reading
	for _, m := range matches {
		if kept[m.c.ID] {
			out = append(out, m)
		}
	}
	return out
}

func townOfReadings(matches []reading) *reading {
	li := slices.IndexFunc(matches, func(m reading) bool { return m.c.Kind == "city" })
	if li < 0 {
		return nil
	}
	var others []located
	for i, m := range matches {
		if i != li {
			others = append(others, m.c)
		}
	}
	town := oneTownOf(matches[li].c, others)
	if town == nil {
		return nil
	}
	// The town is one of the readings' rows.
	return &matches[slices.IndexFunc(matches, func(m reading) bool { return m.c.ID == town.ID })]
}

var trailingCountry = regexp.MustCompile(`(?i)^(.+?)[\s,]+(germany|deutschland|brd)[.\s]*$`)

// withoutCountry drops a trailing "Germany" when the rest names an area
// exactly; found is then the rest's candidates.
func (rv *resolver) withoutCountry(query string) (string, []GazetteerRow, bool, error) {
	m := trailingCountry.FindStringSubmatch(strings.TrimSpace(query))
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return query, nil, false, nil
	}
	rest := strings.TrimSpace(m[1])
	found, err := rv.findCandidates(rest, MaxCandidates, CandidateOptions{})
	if err != nil {
		return "", nil, false, err
	}
	if slices.ContainsFunc(found, func(c GazetteerRow) bool { return c.Score >= 1 && areaKinds[c.Kind] }) {
		return rest, found, true, nil
	}
	return query, nil, false, nil
}

func (rv *resolver) withNamesakeGemeinden(query string, found []GazetteerRow) ([]GazetteerRow, error) {
	if !isFullExactField(found) {
		return found, nil
	}
	wider, err := rv.findCandidates(query, partCandidates, CandidateOptions{})
	if err != nil {
		return nil, err
	}
	return listedField(wider), nil
}

func isFullExactField(found []GazetteerRow) bool {
	if len(found) < MaxCandidates {
		return false
	}
	for _, c := range found[:MaxCandidates] {
		if c.Score < 1 {
			return false
		}
	}
	return true
}

func listedField(wider []GazetteerRow) []GazetteerRow {
	first := wider[:min(len(wider), MaxCandidates)]
	if !isFullExactField(first) {
		return slices.Clone(first)
	}
	out := slices.Clone(first)
	for _, c := range wider[MaxCandidates:] {
		if c.Score >= 1 && c.Kind == "city" && c.AdminCode != nil && eightDigits.MatchString(*c.AdminCode) && c.Population != nil && *c.Population > 0 {
			out = append(out, c)
		}
	}
	return out
}
