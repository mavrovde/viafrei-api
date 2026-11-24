package transit

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// TownRuns are the runs of one to three consecutive words of a station
// text a town may be named by, as the platform's townRuns reads them: each
// folded as the gazetteer folds the names it stores (NameFold), with the
// administrative words a person may write before a town dropped ("Kreis
// Fulda" is Fulda's), two letters at least, and the oe ue ae spelling beside
// each ("Muenchen" is "munchen"). "Marienplatz, München" gives marienplatz,
// munchen and "marienplatz munchen".
func TownRuns(text string) []string {
	words := strings.Fields(NameFold(text))
	var out []string
	seen := map[string]bool{}
	add := func(run string) {
		if !seen[run] {
			seen[run] = true
			out = append(out, run)
		}
	}
	for n := 1; n <= 3; n++ {
		for i := 0; i+n <= len(words); i++ {
			run := stripAdminWords(words[i : i+n])
			if len(run) < 2 {
				continue
			}
			add(run)
			if d := digraphs.Replace(run); d != run {
				add(d)
			}
		}
	}
	return out
}

// digraphs writes oe ue ae as the gazetteer's fold writes ö ü ä.
var digraphs = strings.NewReplacer("oe", "o", "ue", "u", "ae", "a")

// prefixAdminWords are the words that name only the kind of a region
// before its name, as the gazetteer's stripAdminWords drops them in front:
// "Stadt", "Markt", "Land" and "Gemeinde" begin real towns' names and stay.
var prefixAdminWords = map[string]bool{
	"landkreis": true, "kreis": true, "stadtkreis": true, "regierungsbezirk": true,
	"verbandsgemeinde": true, "samtgemeinde": true, "bundesland": true, "freistaat": true,
}

// stripAdminWords is the run of words without the administrative words in
// front of its last word.
func stripAdminWords(words []string) string {
	start := 0
	for start < len(words)-1 && prefixAdminWords[words[start]] {
		start++
	}
	return strings.Join(words[start:], " ")
}

// genericStopWords are the words that name a kind of stop in any town
// ("Bahnhof", "Marktplatz", "Nord"), never the start of a town's name.
var genericStopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`bahnhof bhf bf hbf hauptbahnhof busbahnhof zob omnibusbahnhof bahnhofsvorplatz
		haltestelle bushaltestelle rathaus markt marktplatz kirche post schule friedhof
		mitte ortsmitte zentrum ort dorf dorfplatz krankenhaus klinikum schwimmbad
		sportplatz bahnhofstrasse hauptstrasse kreuzung abzweig nord sud ost west`) {
		genericStopWords[w] = true
	}
}

// TownPrefixes are the words a town's name in a station text may start
// with, as the platform's townPrefixes reads them: the first word that names
// no kind of stop, of the text and of its part after the last comma, four
// letters at least ("am", "bad", "neu" begin dozens of names). A town written
// without its suffix is the Gemeinde whose name starts with it and a space:
// "Frankfurt" for "Frankfurt am Main", "Halle" for "Halle (Saale)".
func TownPrefixes(text string) []string {
	parts := []string{text}
	if c := strings.LastIndex(text, ","); c >= 0 {
		parts = append(parts, text[c+1:])
	}
	var out []string
	for _, part := range parts {
		for _, w := range strings.Fields(NameFold(part)) {
			if genericStopWords[w] {
				continue
			}
			if len([]rune(w)) >= 4 && !slices.Contains(out, w) {
				out = append(out, w)
			}
			break
		}
	}
	return out
}

// TownRest is the stop's name a station text gives beside the town the
// runs named: the text's words, folded, without the words of those runs
// (each in either spelling, TownRuns).
func TownRest(text string, runs []string) string {
	town := map[string]bool{}
	for _, r := range runs {
		for _, w := range strings.Fields(r) {
			town[w] = true
		}
	}
	words := strings.Fields(NameFold(text))
	drop := make([]bool, len(words))
	for i, w := range words {
		if town[w] || town[digraphs.Replace(w)] {
			drop[i] = true
			// and the administrative words before it ("Kreis Fulda")
			for j := i - 1; j >= 0 && prefixAdminWords[words[j]]; j-- {
				drop[j] = true
			}
		}
	}
	var rest []string
	for i, w := range words {
		if !drop[i] {
			rest = append(rest, w)
		}
	}
	return strings.Join(rest, " ")
}

// NameFold folds a name as the gazetteer folds the names it stores
// (name_norm): lower case, ß as ss, accents and umlaut marks dropped, every
// run of other than letters and digits one space.
func NameFold(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ß", "ss")
	s, _, _ = transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// StopSpellings are the main-station spellings of a town reading's stop
// name (TownSplit.Rest): a stop name that is only a main-station phrase
// ("Hbf", "Hauptbahnhof", "main station") is the town's main station, which
// the timetable writes "Hauptbahnhof" and DB "Hbf"; none for another name.
func StopSpellings(rest string) []string {
	words := strings.FieldsFunc(strings.ToLower(rest), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i := 0; i < len(words); {
		n := mainStationPhraseAt(words[i:])
		if n == 0 {
			return nil
		}
		i += n
	}
	if len(words) == 0 {
		return nil
	}
	return []string{"hbf", "hauptbahnhof"}
}
