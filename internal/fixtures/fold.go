package fixtures

import (
	"strings"
	"unicode"
)

// The platform's search forms of the names the generator writes, which
// carry no diacritic but the umlauts: places.NameFold, places.StreetFold
// and places.HouseNumberFold over that alphabet (the generator reads no
// package of the API).
var umlauts = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss")

func nameFold(s string) string {
	s = umlauts.Replace(strings.ToLower(s))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

func streetFold(s string) string {
	words := strings.Fields(nameFold(s))
	for i, w := range words {
		if strings.HasSuffix(w, "str") {
			words[i] = strings.TrimSuffix(w, "str") + "strasse"
		}
	}
	return strings.Join(words, "")
}

func houseNumberFold(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), "") }
