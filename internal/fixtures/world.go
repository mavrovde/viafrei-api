package fixtures

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"unicode"
	"unicode/utf8"
)

type lonLat struct{ lon, lat float64 }

type bbox struct{ w, s, e, n float64 }

func (b bbox) contains(p lonLat) bool {
	return p.lon >= b.w && p.lon <= b.e && p.lat >= b.s && p.lat <= b.n
}

// Germany is the bounding box every generated position lies in.
var Germany = bbox{w: 5.87, s: 47.27, e: 15.04, n: 55.06}

// GermanyBox returns the bounding box as west, south, east, north.
func GermanyBox() [4]float64 { return [4]float64{Germany.w, Germany.s, Germany.e, Germany.n} }

// emptyArea is a box no generated position falls into (edge case
// empty_area): a radius or corridor query there must answer an empty list.
var emptyArea = bbox{w: 6.0, s: 47.4, e: 6.3, n: 47.6}

// land is one of the sixteen Länder. Codes follow the official two-digit
// format; names and cells are synthetic: the sixteen cells are a 4 x 4 grid
// over the bounding box, not the real borders.
type land struct {
	code string
	name string
	cell bbox
}

func lands(names *namer) []land {
	out := make([]land, 16)
	cw := (Germany.e - Germany.w) / 4
	ch := (Germany.n - Germany.s) / 4
	for i := range out {
		col, row := float64(i%4), float64(i/4)
		out[i] = land{
			code: twoDigits(i + 1),
			name: names.region(),
			cell: bbox{
				w: Germany.w + col*cw, e: Germany.w + (col+1)*cw,
				s: Germany.s + row*ch, n: Germany.s + (row+1)*ch,
			},
		}
	}
	return out
}

func twoDigits(n int) string { return fmt.Sprintf("%02d", n) }

// within returns a position inside b, outside the empty area, with a small
// margin from b's edges.
func within(r *rand.Rand, b bbox) lonLat {
	for {
		p := lonLat{
			lon: b.w + 0.02 + r.Float64()*(b.e-b.w-0.04),
			lat: b.s + 0.02 + r.Float64()*(b.n-b.s-0.04),
		}
		if !emptyArea.contains(p) {
			return p
		}
	}
}

// near returns a position around centre (about 3 km spread), clamped inside
// b and outside the empty area.
func near(r *rand.Rand, centre lonLat, b bbox) lonLat {
	for {
		p := lonLat{
			lon: clamp(centre.lon+r.NormFloat64()*0.04, b.w+0.001, b.e-0.001),
			lat: clamp(centre.lat+r.NormFloat64()*0.025, b.s+0.001, b.n-0.001),
		}
		if !emptyArea.contains(p) {
			return p
		}
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Syllables are our own work, published with the generator under CC0 1.0.
// Names are built from them and therefore belong to no real-world list.
var (
	onsets  = []string{"b", "br", "d", "f", "g", "gr", "h", "k", "kl", "l", "m", "n", "p", "r", "s", "sch", "st", "t", "w", "z"}
	nuclei  = []string{"a", "e", "i", "o", "u", "ä", "ö", "ü", "au", "ei", "ie"}
	codas   = []string{"", "n", "r", "l", "m", "s", "t", "ß", "ck", "ng"}
	endings = []string{"ingen", "hausen", "bach", "stedt", "feld", "au", "brück", "rode", "heim", "tal", "werder", "kamp"}
)

type namer struct{ r *rand.Rand }

func (n *namer) pick(list []string) string { return list[n.r.IntN(len(list))] }

func (n *namer) syllable() string { return n.pick(onsets) + n.pick(nuclei) + n.pick(codas) }

func (n *namer) settlement() string {
	return capitalise(n.syllable() + n.syllable() + n.pick(endings))
}

func (n *namer) region() string { return capitalise(n.syllable()+n.syllable()) + "land" }

func (n *namer) person() string { return capitalise(n.syllable() + n.syllable()) }

func capitalise(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// fold is the search form of a name: lower case, umlauts and ß spelled out,
// everything but letters and digits removed.
func fold(s string) string {
	repl := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "Ä", "ae", "Ö", "oe", "Ü", "ue")
	s = strings.ToLower(repl.Replace(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}
