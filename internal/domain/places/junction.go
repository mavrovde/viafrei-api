package places

import (
	"regexp"
	"strings"
)

// A junction asked by name (G5), as the platform's place search
// (find_place) reads the words in front of it.

// junctionWords say "a motorway junction" in front of its name, as the
// platform's site search and place search share them (JUNCTION_WORDS), in
// their order: a word is taken only with a space after it, so "kreuz" never
// takes "kreuzberg".
var junctionWords = []string{
	"autobahnanschlussstelle", "autobahnanschluss", "autobahnausfahrt", "autobahnkreuz", "autobahndreieck", "anschlussstelle",
	"motorway junction", "motorway exit", "interchange", "junction", "ausfahrt", "abfahrt", "kreuz", "dreieck", "exit", "as", "ak", "ad",
}

var (
	spaces       = regexp.MustCompile(`\s+`)
	trailingRoad = regexp.MustCompile(`\s+\(?a\s?\d{1,3}\)?$`)
	roadNumber   = regexp.MustCompile(`(?i)^\s*([ab])\s*(\d{1,3})\s*(?:autobahn|motorway)?\s*$`)
	roadWords    = regexp.MustCompile(`(?i)^\s*(?:bundesautobahn|autobahn|bab|motorway)\s*a?\s*(\d{1,3})\s*$`)
)

// JunctionCore is a junction's name without the words that only say it is
// one, lower-cased with single spaces, and whether such a word was there:
// "AK Hannover-Ost" and "Kreuz Hannover-Ost (A7)" are "hannover-ost",
// prefixed. A trailing road number is dropped too.
func JunctionCore(raw string) (core string, prefixed bool) {
	s := strings.TrimSpace(spaces.ReplaceAllString(strings.ToLower(raw), " "))
	for _, w := range junctionWords {
		if rest, ok := strings.CutPrefix(s, w+" "); ok {
			s, prefixed = rest, true
			break
		}
	}
	return strings.TrimSpace(trailingRoad.ReplaceAllString(s, "")), prefixed
}

// RoadForm is the road a text names and nothing else, as the platform's place
// search reads it (roadForm): "A7", "a 7", "B27", "Autobahn 9", "BAB 9", "A9
// motorway"; "" when the text is not a road number. A leading zero is
// dropped (A07 is A7).
func RoadForm(text string) string {
	if m := roadNumber.FindStringSubmatch(text); m != nil {
		return strings.ToUpper(m[1]) + strings.TrimLeft(m[2], "0")
	}
	if m := roadWords.FindStringSubmatch(text); m != nil {
		return "A" + strings.TrimLeft(m[1], "0")
	}
	return ""
}
