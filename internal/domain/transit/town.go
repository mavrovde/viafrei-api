package transit

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// TownSplit is one reading of a station text as a town and a stop name in
// it, as the platform's stop resolver reads "town, stop" ("München
// Marienplatz", "Stuttgart Hbf"): Runs are the folded spellings of a run of
// one to three of the text's words a town may be named by (the gazetteer's
// fold, with the oe ue ae spelling beside it), Rest the other words, the
// stop's name.
type TownSplit struct {
	Runs []string
	Rest string
}

// TownSplits are the readings of text as a town and a stop: every run of
// one to three consecutive words, longest first, then in the order of the
// text, that leaves a stop name; none for a text of one word.
func TownSplits(text string) []TownSplit {
	words := strings.Fields(NameFold(text))
	var out []TownSplit
	for n := min(3, len(words)-1); n >= 1; n-- {
		for i := 0; i+n <= len(words); i++ {
			run := strings.Join(words[i:i+n], " ")
			runs := []string{run}
			if d := strings.NewReplacer("oe", "o", "ue", "u", "ae", "a").Replace(run); d != run {
				runs = append(runs, d)
			}
			rest := strings.Join(append(append([]string{}, words[:i]...), words[i+n:]...), " ")
			out = append(out, TownSplit{Runs: runs, Rest: rest})
		}
	}
	return out
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
