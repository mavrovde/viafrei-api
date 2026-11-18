package rules

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ZoneCity is the curated zone list's entry for an asked city, as the
// platform's driving rules name it: an entry, or the asked city uncovered,
// which is not that the city has no zone.
type ZoneCity struct {
	// Name is the entry's name, or the asked city when none speaks for it.
	Name string
	// Zone is the entry's description of its zone; nil while the list
	// carries none.
	Zone *string
	// Plakette is the plaque the zone takes; nil when none is stated.
	Plakette *string
	// Covered is whether the list holds an entry whose zone it states.
	Covered bool
	// Caveat is the condition an answer for a city with no entry states:
	// that no zone held is not that there is none. Empty for an entry.
	Caveat string
}

// NormaliseCity folds a city's name as the curated list keys its entries
// and aliases: lower case, umlauts and ß spelt out, accents dropped, the
// letters a to z only.
func NormaliseCity(s string) string {
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CityOf is the answer for the asked city given the list's entry for it,
// nil for none; the caveat is in language ("en", or German otherwise).
func CityOf(asked string, e *ListEntry, language string) ZoneCity {
	if e == nil {
		return ZoneCity{Name: asked, Caveat: NoBoundaryCaveat(asked, language)}
	}
	return ZoneCity{Name: e.Name, Plakette: e.Plaque, Covered: e.Covered}
}

// NoBoundaryCaveat is the condition the platform states for a place it
// holds no zone for, in language ("en", or German otherwise).
func NoBoundaryCaveat(where, language string) string {
	if language == "en" {
		return "Caution: we hold no low-emission-zone boundary for " + where + ". That does NOT mean you may drive there without a plaque — there may be a zone; the signs (sign 270.1) decide, and the Umweltbundesamt keeps the complete list of zones."
	}
	return "Achtung: Für " + where + " liegt uns keine Umweltzonen-Grenze vor. Daraus folgt NICHT, dass Sie dort ohne Plakette fahren dürfen — es kann dort eine Umweltzone geben; maßgeblich ist die Beschilderung (Zeichen 270.1), die vollständige Liste der Zonen führt das Umweltbundesamt."
}
