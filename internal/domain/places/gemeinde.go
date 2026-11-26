package places

import "strings"

// GemeindeByNameM and GemeindeNearestM are how far a Gemeinde named as the
// nearest address's town, and else the nearest Gemeinde, may lie from the
// position, as today's location description reads them.
const (
	GemeindeByNameM  = 25_000
	GemeindeNearestM = 15_000
)

// Gemeinde is the Gemeinde a position lies in, as today's location
// description reads it (GEMEINDE_AT_POINT): the Gemeinde the nearest German
// address names as its town (Inside), else the nearest Gemeinde; with the
// Kreis and the Regierungsbezirk its key names, where the gazetteer holds
// them. The gazetteer holds points, not outlines: the Gemeinde decides the
// Kreis and the Land, never the nearest Kreis point (Helgoland lies in
// Pinneberg, not in Wittmund).
type Gemeinde struct {
	Name   string
	Code   string
	Inside bool
	Kreis  *string
	Bezirk *string
	Family string
}

// GemeindeFold is a town's name folded as the gazetteer's name_norm, the
// administrative words stripped (stripAdminWords: "Sankt Peter-Ording,
// Gemeinde" is Sankt Peter-Ording); nil for none.
func GemeindeFold(town *string) *string {
	if town == nil {
		return nil
	}
	name := *town
	if i := strings.LastIndex(name, ","); i > 0 && suffixAdminWords[normalizeName(name[i+1:])] {
		name = name[:i]
	}
	words := strings.Fields(normalizeName(name))
	start := 0
	for start < len(words)-1 && prefixAdminWords[words[start]] {
		start++
	}
	f := strings.Join(words[start:], " ")
	if f == "" {
		f = normalizeName(*town)
	}
	if f == "" {
		return nil
	}
	return &f
}

// suffixAdminWords are the type labels a name may carry after a comma, and
// prefixAdminWords the unambiguous ones it may carry in front ("Markt
// Schwaben" keeps its first word), as the platform's stripAdminWords lists
// them.
var (
	suffixAdminWords = setOf("landkreis", "kreis", "stadtkreis", "kreisfreie stadt", "stadt", "gemeinde", "verbandsgemeinde",
		"samtgemeinde", "markt", "regierungsbezirk", "bezirk", "land", "freistaat", "bundesland")
	prefixAdminWords = setOf("landkreis", "kreis", "stadtkreis", "regierungsbezirk", "verbandsgemeinde", "samtgemeinde", "bundesland", "freistaat")
)

func setOf(words ...string) map[string]bool {
	out := map[string]bool{}
	for _, w := range words {
		out[w] = true
	}
	return out
}
