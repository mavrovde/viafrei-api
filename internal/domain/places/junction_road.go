package places

import (
	"math"
	"regexp"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The junction answer's sources, as today's nearestJunctions names them:
// OpenStreetMap's junction nodes, else the gazetteer's junction points.
const (
	JunctionsFromOSM       = "osm"
	JunctionsFromGazetteer = "gazetteer"
)

// Junctions are the junctions near a position and the source they came
// from.
type Junctions struct {
	From string
	Rows []Junction
}

// JunctionRoadNearM, SameJunctionM and LCLReachM are today's bounds: the
// nearest LCL motorway point names a junction's road within 500 m when no
// name agrees; two same-named nodes within 1.5 km are one junction; the LCL
// points within 1.5 km of a node are its candidates.
const (
	JunctionRoadNearM = 500
	SameJunctionM     = 1_500
	LCLReachM         = 1_500
)

// LCLPoint is a motorway point of the BASt Location Code List near a
// junction node.
type LCLPoint struct {
	Road      string
	Name      *string
	DistanceM float64
}

// JunctionRoad is the motorway an OpenStreetMap junction node lies on, as
// today's junctionRoad reads it from the LCL points near it (nearest
// first): every road whose point's name agrees with the node's (one
// holding the other, without the junction words), joined by a slash; else
// the nearest point's road within JunctionRoadNearM; else nil, a junction
// off any motorway that can be named.
func JunctionRoad(name string, near []LCLPoint) *string {
	core := junctionCoreFold(name)
	var agreeing []string
	if core != "" {
		for _, c := range near {
			if c.Name == nil {
				continue
			}
			other := junctionCoreFold(*c.Name)
			if other == "" || !strings.Contains(other, core) && !strings.Contains(core, other) {
				continue
			}
			if !contains(agreeing, c.Road) {
				agreeing = append(agreeing, c.Road)
			}
		}
	}
	if len(agreeing) > 0 {
		r := strings.Join(agreeing, "/")
		return &r
	}
	if len(near) > 0 && near[0].DistanceM <= JunctionRoadNearM {
		return &near[0].Road
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// junctionWordsInOrder are the words that only say "a junction", as the
// platform's JUNCTION_WORDS lists them, in its order: the first that starts
// the name is taken.
var junctionWordsInOrder = []string{"autobahnanschlussstelle", "autobahnanschluss", "autobahnausfahrt", "autobahnkreuz", "autobahndreieck",
	"anschlussstelle", "motorway junction", "motorway exit", "interchange", "junction", "ausfahrt", "abfahrt", "kreuz", "dreieck", "exit",
	"as", "ak", "ad"}

var trailingRoadFold = regexp.MustCompile(`\s+a\s?\d{1,3}$`)

// junctionCoreFold is a junction's name folded without the junction words
// in front and a road number behind, as the platform's junctionCore.
func junctionCoreFold(raw string) string {
	fold := normalizeName(raw)
	for _, p := range junctionWordsInOrder {
		if strings.HasPrefix(fold, p+" ") {
			fold = fold[len(p)+1:]
			break
		}
	}
	return strings.TrimSpace(trailingRoadFold.ReplaceAllString(fold, ""))
}

// FoldSameNamed keeps the junctions in their order without a later one of
// the same name (case, umlaut spelling and punctuation folded) within
// withinM of an earlier one, as today's foldSameNamed.
func FoldSameNamed(js []Junction, withinM float64) []Junction {
	type kept struct {
		j   Junction
		key string
	}
	var ks []kept
	out := []Junction{}
	for _, j := range js {
		key := nameKey(j.Name)
		twin := false
		for _, k := range ks {
			if key != "" && k.key == key && haversineM(k.j.Location, j.Location) <= withinM {
				twin = true
				break
			}
		}
		if !twin {
			ks = append(ks, kept{j, key})
			out = append(out, j)
		}
	}
	return out
}

var notLetterOrDigit = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// nameKey is a name as a comparison key, as the platform's nameKey.
func nameKey(name string) string {
	s := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(strings.TrimSpace(name)))
	return strings.TrimSpace(notLetterOrDigit.ReplaceAllString(s, " "))
}

// haversineM is the distance between two points on the sphere, in metres,
// as the platform's haversineM.
func haversineM(a, b geo.Point) float64 {
	const r = 6_371_008.8
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}
