package energy

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// The plugs a point can be asked for, as the platform's charging filter
// names them: the contract's normalised connector values.
var Connectors = []string{"ccs2", "type2", "chademo", "mcs"}

// The charging terms a point can be asked to meet, as the platform's: open
// around the clock, pay by card, free of charge, open to the public.
var Terms = []string{"open247", "card", "free", "public"}

// FreePriceMaxAgeDays is how recent a published zero ad-hoc price must be to
// make a point free of charge, as the platform's.
const FreePriceMaxAgeDays = 90

// OnlyAvailableScan is how many of the nearest sites are examined when only
// sites with a free point are asked for, as the platform's bound.
const OnlyAvailableScan = 100

// NoOperatorKey is the key of an operator name that names no brand.
const NoOperatorKey = "-"

// operatorStopWords are the legal-form and generic words an operator key
// skips, the platform's list verbatim.
var operatorStopWords = []string{
	"gmbh", "mbh", "kgaa", "ohg", "gbr", "und", "haftungsbeschränkt",
	"stadtwerke", "energie", "energieversorgung", "mobility", "mobilität",
	"deutschland", "germany", "charging", "service", "services", "solution",
	"solutions", "ladestation", "ladestationen", "holding", "gesellschaft",
	"für", "der", "die", "das", "the", "emobility", "recharge", "inc", "ltd",
	"cokg", "gmbhcokg",
}

// operatorShortBrands are the brands shorter than three letters.
var operatorShortBrands = []string{"bp", "gp"}

var wordSplit = regexp.MustCompile(`[^a-z0-9äöüß]+`)

// OperatorKey is an operator name's brand key, as the platform's: the first
// word, lower case and without dots, of at least three letters (or a short
// brand) that is not a legal-form or generic word. ok is false when the name
// names no brand.
func OperatorKey(name string) (key string, ok bool) {
	for _, w := range wordSplit.Split(strings.ReplaceAll(strings.ToLower(name), ".", ""), -1) {
		if (len([]rune(w)) >= 3 || slices.Contains(operatorShortBrands, w)) && !slices.Contains(operatorStopWords, w) {
			return w, true
		}
	}
	return "", false
}

// OperatorKeySQL is OperatorKey in SQL over the text expression expr, NULL
// when the name names no brand: the platform's statement.
func OperatorKeySQL(expr string) string {
	return `(select w.t from unnest(regexp_split_to_array(lower(replace(coalesce(` + expr + `, ''), '.', '')), '[^a-z0-9äöüß]+')) with ordinality as w(t, i)` +
		` where (length(w.t) >= 3 or w.t = any (array['` + strings.Join(operatorShortBrands, `', '`) + `']::text[]))` +
		` and w.t <> all (array['` + strings.Join(operatorStopWords, `', '`) + `']::text[]) order by w.i limit 1)`
}

// Filter narrows the charge points of a site as the platform's charging
// filter does: a point is kept when it meets every asked condition, and a
// site when its operator matches and at least one point is kept.
type Filter struct {
	// Connector keeps the points with this plug; empty keeps every point.
	Connector string
	// MaxKw keeps the points of at most this power with a stated power.
	MaxKw float64
	// OperatorKey keeps the sites whose operator has this key (OperatorKey,
	// NoOperatorKey for none); empty keeps every site.
	OperatorKey string
	// Terms keeps the points that meet every one (Terms).
	Terms []string
	// OnlyAvailable keeps the sites with a point reported free now, a report
	// counting while no older than StatusMaxAge or its family's threshold.
	OnlyAvailable bool
	StatusMaxAge  time.Duration
}

// FreeNow is what a site's live reports say about a free point now.
type FreeNow string

// The free-now verdicts, as the platform's.
const (
	Free     FreeNow = "free"
	NotFree  FreeNow = "not_free"
	Stale    FreeNow = "stale"
	NoStatus FreeNow = "no_status"
)

// FreeNow is the platform's verdict over the site's points: free when one
// live report says available; otherwise not free when a live report says
// occupied or out of order; otherwise stale when a report has aged past its
// limit; otherwise no status.
func (s Site) FreeNow(now time.Time, limit func(family string) time.Duration) FreeNow {
	free, taken, stale := false, false, false
	for _, p := range s.Points {
		a := p.Availability
		if a == nil || !a.Says() {
			continue
		}
		switch {
		case !a.Live(now, limit(a.Family)):
			stale = true
		case a.State == StateAvailable:
			free = true
		case a.State == StateOccupied || a.State == StateOutOfOrder:
			taken = true
		}
	}
	switch {
	case free:
		return Free
	case taken:
		return NotFree
	case stale:
		return Stale
	}
	return NoStatus
}

// Dropped counts the sites an only-available answer left out, by reason.
type Dropped struct {
	NotFree  int
	Stale    int
	NoStatus int
}
