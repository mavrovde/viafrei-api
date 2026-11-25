package places

import (
	"regexp"
	"strings"
)

// The platform's shown address texts (address-text.ts): a street with its
// number, a postcode and a town.

var (
	houseDigit     = regexp.MustCompile(`[1-9]`)
	starRun        = regexp.MustCompile(`\s*\*+\s*`)
	fiveDigits     = regexp.MustCompile(`^\d{5}$`)
	fourDigits     = regexp.MustCompile(`^\d{4}$`)
	fiveDigitGroup = regexp.MustCompile(`\d+`)
)

// HouseNumberShown is a house number worth printing: one with a digit
// other than 0.
func HouseNumberShown(raw *string) *string {
	if raw == nil {
		return nil
	}
	t := strings.TrimSpace(*raw)
	if t == "" || !houseDigit.MatchString(t) {
		return nil
	}
	return &t
}

// StreetLine is the street and its number as one line; a street that ends
// in its own number already is not given it twice.
func StreetLine(street, houseNumber *string) *string {
	name := ""
	if street != nil {
		name = strings.TrimSpace(starRun.ReplaceAllString(*street, " "))
	}
	if name == "" {
		return nil
	}
	no := HouseNumberShown(houseNumber)
	if no == nil {
		return &name
	}
	var spelled strings.Builder
	for i, ch := range strings.Join(strings.Fields(*no), "") {
		if i > 0 {
			spelled.WriteString(`\s*`)
		}
		spelled.WriteString(regexp.QuoteMeta(string(ch)))
	}
	if regexp.MustCompile(`(?i)\S\s+` + spelled.String() + `$`).MatchString(name) {
		return &name
	}
	out := name + " " + *no
	return &out
}

// postcodeEdge is the white space the platform trims off a postcode (not
// the other Unicode spaces).
func postcodeEdge(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' '
}

// PostcodeShown is a postcode as printed: five digits, four given their
// leading 0, or the one five-digit group of a longer text.
func PostcodeShown(raw *string) *string {
	if raw == nil {
		return nil
	}
	t := strings.TrimFunc(*raw, postcodeEdge)
	if fiveDigits.MatchString(t) {
		return &t
	}
	if fourDigits.MatchString(t) {
		s := "0" + t
		return &s
	}
	var groups []string
	for _, g := range fiveDigitGroup.FindAllString(t, -1) {
		if len(g) == 5 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 1 {
		return &groups[0]
	}
	return nil
}

// TownLine is the postcode and the town as one line, or "".
func TownLine(postcode, town *string) string {
	var parts []string
	if p := PostcodeShown(postcode); p != nil && *p != "" {
		parts = append(parts, *p)
	}
	if town != nil && strings.TrimSpace(*town) != "" {
		parts = append(parts, strings.TrimSpace(*town))
	}
	return strings.Join(parts, " ")
}
