package energy

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The platform's texts and keys of a charging place: its shown street,
// postcode, town and name (address-text), the fold keys of one answer row
// (answer-fold), the same-address rules of OpenStreetMap hits
// (same-object) and the plug names (connectorLabel).

// German is the language of every answer unless English is asked.
const German = "de"

var (
	postcodeIn    = regexp.MustCompile(`\b(\d{5})\b`)
	trailingParen = regexp.MustCompile(`\s*\([^()]*\)$`)
	underscoreID  = regexp.MustCompile(`^[\p{L}\p{N}.-]+(?:_[\p{L}\p{N}.-]+)+$`)
	upperIDChars  = regexp.MustCompile(`^[\p{Lu}\p{N}_;,.\s/#:-]+$`)
	lowerLetter   = regexp.MustCompile(`\p{Ll}`)
	digitOrUnder  = regexp.MustCompile(`[\d_]`)
	anyDigit      = regexp.MustCompile(`\d`)
	strWord       = regexp.MustCompile(`(strasse|str)( |$)`)
	addressStr    = regexp.MustCompile(`str(?:\.|asse\b|\b)`)
	nonWordRun    = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	domesticPlug  = regexp.MustCompile(`^domestic([a-p])$`)
)

func houseNumberShown(raw *string) *string { return places.HouseNumberShown(raw) }

func streetLine(street, houseNumber *string) *string { return places.StreetLine(street, houseNumber) }

func postcodeShown(raw *string) *string { return places.PostcodeShown(raw) }

func townLine(postcode, town *string) string { return places.TownLine(postcode, town) }

// isRecordIDName tells a name that is a record's id rather than a name.
func isRecordIDName(name string) bool {
	t := strings.TrimSpace(name)
	if t == "" {
		return false
	}
	if !lowerLetter.MatchString(t) && digitOrUnder.MatchString(t) && upperIDChars.MatchString(t) {
		return true
	}
	head := trailingParen.ReplaceAllString(t, "")
	return underscoreID.MatchString(head) && anyDigit.MatchString(head)
}

// siteNameShown is a site's own name worth printing, or nil.
func siteNameShown(name *string) *string {
	if name == nil || strings.TrimSpace(*name) == "" || isRecordIDName(*name) {
		return nil
	}
	t := strings.TrimSpace(*name)
	return &t
}

// addressFold is a text as the label compares a name with its address.
func addressFold(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ß", "ss")
	s = addressStr.ReplaceAllString(s, "strasse")
	return " " + strings.TrimSpace(nonWordRun.ReplaceAllString(s, " ")) + " "
}

// foldWordKey is a name or a street as one answer row compares it: folded,
// "strasse" as "str", no spaces, a doubled letter once.
func foldWordKey(text *string) *string {
	if text == nil {
		return nil
	}
	k := places.NameFold(*text)
	k = strWord.ReplaceAllString(k, "str$2")
	k = strings.ReplaceAll(k, " ", "")
	var b strings.Builder
	var last rune = -1
	for _, r := range k {
		if r == last && unicode.IsLetter(r) {
			continue
		}
		b.WriteRune(r)
		last = r
	}
	if b.Len() == 0 {
		return nil
	}
	s := b.String()
	return &s
}

// houseFoldKey is a house number as one answer row compares it.
func houseFoldKey(house *string) *string {
	if house == nil {
		return nil
	}
	k := strings.Map(func(r rune) rune {
		if r >= '‐' && r <= '―' {
			return '-'
		}
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.ToLower(*house))
	if k == "" {
		return nil
	}
	return &k
}

func addressFoldKey(street, house *string) *string {
	s, h := foldWordKey(street), houseFoldKey(house)
	if s == nil || h == nil {
		return nil
	}
	k := *s + "#" + *h
	return &k
}

var genericOperatorWords = map[string]bool{"gmbh": true, "mbh": true, "ag": true, "kg": true, "se": true, "co": true, "ug": true,
	"ohg": true, "eg": true, "ev": true, "und": true, "the": true, "deutschland": true, "germany": true, "gruppe": true,
	"group": true, "holding": true, "de": true}

func operatorWords(text *string) []string {
	if text == nil {
		return nil
	}
	var out []string
	for _, w := range strings.Split(places.NameFold(*text), " ") {
		if len([]rune(w)) >= 2 && !genericOperatorWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// answerFacts are what decides that two answer rows are one place.
type answerFacts struct {
	at                                      geo.Point
	name, street, house, postcode, operator *string
}

func operatorsAgree(a, b answerFacts) bool {
	oa, ob := operatorWords(a.operator), operatorWords(b.operator)
	if len(oa) == 0 || len(ob) == 0 {
		return true
	}
	in := func(ws ...[]string) map[string]bool {
		m := map[string]bool{}
		for _, w := range ws {
			for _, x := range w {
				m[x] = true
			}
		}
		return m
	}
	inB, inA := in(ob, operatorWords(b.name)), in(oa, operatorWords(a.name))
	for _, w := range oa {
		if inB[w] {
			return true
		}
	}
	for _, w := range ob {
		if inA[w] {
			return true
		}
	}
	return false
}

// answerFoldM is how near two rows of one place lie (ANSWER_FOLD_M).
const answerFoldM = 50

// sameAnswerRow: within 50 m, no two postcodes apart, and one name, or one
// address of agreeing operators (sameAnswerRow, no works period here).
func sameAnswerRow(a, b answerFacts) bool {
	if geo.MetresBetween(a.at, b.at) > answerFoldM {
		return false
	}
	pa, pb := postcodeShown(a.postcode), postcodeShown(b.postcode)
	if pa != nil && pb != nil && *pa != *pb {
		return false
	}
	if na := foldWordKey(a.name); na != nil {
		if nb := foldWordKey(b.name); nb != nil && *na == *nb {
			return true
		}
	}
	aa := addressFoldKey(a.street, a.house)
	if aa == nil {
		return false
	}
	ab := addressFoldKey(b.street, b.house)
	return ab != nil && *aa == *ab && operatorsAgree(a, b)
}

// transliterateDigraphs is the umlaut-free spelling of a folded text (oe →
// o, ue → u, ae → a), or the text when it has none.
func transliterateDigraphs(folded string) string {
	return strings.NewReplacer("oe", "o", "ue", "u", "ae", "a").Replace(folded)
}

// postcodeOf is the five-digit postcode in a text, or "".
func postcodeOf(raw *string) string {
	if raw == nil {
		return ""
	}
	if m := postcodeIn.FindStringSubmatch(*raw); m != nil {
		return m[1]
	}
	return ""
}

// ConnectorLabel is a plug's name in the language (connectorLabel).
func ConnectorLabel(value, lang string) string {
	type pair struct{ de, en string }
	labels := map[string]pair{
		"iec62196t2combo": {"CCS2", "CCS2"}, "iec62196t2": {"Typ 2", "Type 2"}, "iec62196t1": {"Typ 1", "Type 1"},
		"mcs": {"MCS", "MCS"}, "iec62196t1combo": {"CCS Typ 1", "CCS Type 1"}, "iec62196t3a": {"Typ 3A", "Type 3A"},
		"iec62196t3c": {"Typ 3C", "Type 3C"}, "other": {"anderer Stecker", "other plug"}, "chademo": {"CHAdeMO", "CHAdeMO"},
		"domesticf": {"Schuko", "Schuko"}, "teslas": {"Tesla", "Tesla"},
	}
	key := strings.ToLower(strings.TrimSpace(value))
	if l, ok := labels[key]; ok {
		if lang == German {
			return l.de
		}
		return l.en
	}
	if d := domesticPlug.FindStringSubmatch(key); d != nil {
		if lang == German {
			return "Haushaltsstecker Typ " + strings.ToUpper(d[1])
		}
		return "household plug type " + strings.ToUpper(d[1])
	}
	return value
}

// toFixed is JavaScript's Number.prototype.toFixed: the exact value of x
// rounded to digits places, a tie to the larger magnitude, a negative x
// signed even when it rounds to zero ("-0.00").
func toFixed(x float64, digits int) string {
	exact := new(big.Float).SetPrec(2048).SetFloat64(math.Abs(x)).Text('f', 1100)
	dot := strings.IndexByte(exact, '.')
	keep, rest := exact[:dot+1+digits], exact[dot+1+digits:]
	keep = strings.TrimSuffix(keep, ".")
	if rest != "" && rest[0] >= '5' {
		n, _ := new(big.Int).SetString(strings.Replace(keep, ".", "", 1), 10)
		n.Add(n, big.NewInt(1))
		s := n.String()
		if digits > 0 {
			s = strings.Repeat("0", max(0, digits+1-len(s))) + s
			s = s[:len(s)-digits] + "." + s[len(s)-digits:]
		}
		keep = s
	}
	if x < 0 {
		keep = "-" + keep
	}
	return keep
}

// localDecimal is a number text with the language's decimal mark.
func localDecimal(s, lang string) string {
	if lang == German {
		return strings.Replace(s, ".", ",", 1)
	}
	return s
}

// fmtKw is a power as the platform prints it ("22 kW", "3,7 kW").
func fmtKw(kw float64, lang string) string {
	s := strconv.FormatFloat(kw, 'f', -1, 64)
	if kw != math.Trunc(kw) {
		s = toFixed(kw, 1)
	}
	return localDecimal(s, lang) + " kW"
}

// fmtEur is a price per kWh as the platform prints it.
func fmtEur(eur float64, includesVat bool, lang string) string {
	vat := ""
	if !includesVat {
		vat = " net"
		if lang == German {
			vat = " netto"
		}
	}
	return localDecimal(toFixed(eur, 2), lang) + " €/kWh" + vat
}
