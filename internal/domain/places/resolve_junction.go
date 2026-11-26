package places

import (
	"regexp"
	"slices"
	"strings"
)

// A junction asked as one (resolveJunction): "AS Baden-Baden", "Kreuz
// Hannover-Ost (A7)", a junction label of ours handed back.

// NamedJunction is an OpenStreetMap junction node named like the asked one.
type NamedJunction struct {
	ID, Name string
	Lat, Lon float64
	Score    float64
}

// The junction numbers: how many nodes a name read fetches per answer, the
// similarity a node name needs, the score a junction answers with, the
// penalty for the other sort of junction, and how far apart twins lie.
const (
	junctionNodeOverfetch = 4
	junctionNameFloor     = 0.5
	junctionAnswers       = 0.9
	junctionKindPenalty   = 0.2
	sameJunctionKm        = 3.0
)

var (
	junctionLabelSuffix = regexp.MustCompile(`(?i)\s*\((?:anschlussstelle|junction|autobahnkreuz/-dreieck|interchange)\)\s*$`)
	askedRoad           = regexp.MustCompile(`(?i)(?:\(\s*|\s)a\s?(\d{1,3})\s*\)?\s*$`)
	interchangeName     = regexp.MustCompile(`(?i)^(autobahn)?(kreuz|dreieck)\b|^a[kd]\s`)
)

// junctionWordAsks: which sort of junction a junction word asks for.
var junctionWordAsks = map[string]string{
	"autobahnanschlussstelle": "exit", "autobahnanschluss": "exit", "autobahnausfahrt": "exit", "autobahnkreuz": "interchange",
	"autobahndreieck": "interchange", "anschlussstelle": "exit", "motorway junction": "", "motorway exit": "exit", "interchange": "interchange",
	"junction": "", "ausfahrt": "exit", "abfahrt": "exit", "kreuz": "interchange", "dreieck": "interchange", "exit": "exit", "as": "exit",
	"ak": "interchange", "ad": "interchange",
}

// junctionLabelName is a text without the kind word a junction label of
// ours puts after it, and whether it had one.
func junctionLabelName(raw string) (string, bool) {
	t := strings.TrimSpace(raw)
	name := junctionLabelSuffix.ReplaceAllString(t, "")
	return name, name != t
}

// junctionCoreOf is the platform's junctionCore: the fold without a junction
// word in front and a road number behind, and whether a word was there.
func junctionCoreOf(raw string) (string, bool) {
	fold := normalizeName(raw)
	for _, p := range junctionWordsInOrder {
		if strings.HasPrefix(fold, p+" ") {
			return strings.TrimSpace(trailingRoadFold.ReplaceAllString(fold[len(p)+1:], "")), true
		}
	}
	return strings.TrimSpace(trailingRoadFold.ReplaceAllString(fold, "")), false
}

func askedJunctionRoad(raw string) string {
	m := askedRoad.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return ""
	}
	return "A" + strings.TrimLeft(m[1], "0")
}

func askedJunctionKind(raw string) string {
	fold := normalizeName(raw)
	for _, w := range junctionWordsInOrder {
		if strings.HasPrefix(fold, w+" ") {
			return junctionWordAsks[w]
		}
	}
	return ""
}

func isInterchangeName(name string) bool { return interchangeName.MatchString(name) }

func junctionLabel(name, lang string) string {
	if _, prefixed := junctionCoreOf(name); prefixed {
		return name
	}
	word := kindWord("junction", lang)
	if isInterchangeName(name) {
		word = [2]string{"Autobahnkreuz/-dreieck", "Interchange"}[langIdx(lang)]
	}
	return name + " (" + word + ")"
}

type junctionRow struct {
	name     string
	lat, lon float64
	sourceID string
	score    float64
	road     string
	own      string
}

// junctionField scores the rows against the asked junction (its core exact
// is 1, the other sort penalised) and drops a later twin, by score.
func junctionField(rows []junctionRow, asked string) []junctionRow {
	core, _ := junctionCoreOf(asked)
	want := askedJunctionKind(asked)
	var kept []junctionRow
	for _, r := range rows {
		r.own, _ = junctionCoreOf(r.name)
		if r.own == core {
			r.score = 1
		}
		sort := "exit"
		if isInterchangeName(r.name) {
			sort = "interchange"
		}
		if want != "" && want != sort {
			r.score -= junctionKindPenalty
		}
		words := strings.Split(r.own, " ")
		twin := slices.ContainsFunc(kept, func(k junctionRow) bool {
			kw := strings.Split(k.own, " ")
			contains := !slices.ContainsFunc(kw, func(w string) bool { return !slices.Contains(words, w) }) ||
				!slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(kw, w) })
			twoRoads := k.road != "" && r.road != "" && k.road != r.road && !isInterchangeName(r.name)
			return contains && !twoRoads && haversineM(k.lat, k.lon, r.lat, r.lon) <= sameJunctionKm*1_000
		})
		if !twin {
			kept = append(kept, r)
		}
	}
	slices.SortStableFunc(kept, func(a, b junctionRow) int { return cmpFloat(b.score, a.score) })
	return kept
}

func (rv *resolver) junctionsNamed(query string, limit int) ([]junctionRow, error) {
	core, _ := junctionCoreOf(query)
	if runeLen(strings.ReplaceAll(core, " ", "")) < 3 {
		return nil, nil
	}
	rows, err := rv.reads.JunctionsNamed(rv.ctx, core, junctionNameFloor, limit*junctionNodeOverfetch)
	if err != nil {
		return nil, err
	}
	type kept struct {
		r   junctionRow
		key string
	}
	var ks []kept
	var out []junctionRow
	for _, n := range rows {
		if isRestAreaName(n.Name) {
			continue
		}
		key := nameKey(normalizeName(n.Name))
		r := junctionRow{name: n.Name, lat: n.Lat, lon: n.Lon, sourceID: "osm", score: n.Score}
		if key != "" && slices.ContainsFunc(ks, func(k kept) bool {
			return k.key == key && haversineM(k.r.lat, k.r.lon, r.lat, r.lon) <= SameJunctionM
		}) {
			continue
		}
		ks = append(ks, kept{r, key})
		out = append(out, r)
	}
	return out[:min(len(out), limit)], nil
}

var restAreaName = regexp.MustCompile(`^(tank und )?(rastplatz|rastplatze|rastanlage|raststatte|rasthof|rasthaus|parkplatz|pwc|autohof|tankstelle|servicebereich|serviceplatz)( |$)`)

func isRestAreaName(name string) bool { return restAreaName.MatchString(normalizeName(name)) }

// resolveJunction answers a junction core: the gazetteer's Autobahn junctions
// and OpenStreetMap's nodes of the name, on the road asked or hinted where
// that narrows them, one point when one stands out, else a list.
func (rv *resolver) resolveJunction(core, query string) (*Resolution, error) {
	if runeLen(strings.ReplaceAll(core, " ", "")) < 3 {
		return nil, nil
	}
	gaz, err := rv.findCandidates(core, MaxCandidates, CandidateOptions{OnlyKind: "junction", OnlySource: "autobahn"})
	if err != nil {
		return nil, err
	}
	var all []junctionRow
	for _, c := range locate(gaz) {
		road := ""
		if c.Road != nil {
			road = *c.Road
		}
		all = append(all, junctionRow{name: c.Name, lat: c.lat, lon: c.lon, sourceID: "autobahn", score: c.Score, road: road})
	}
	named, _ := junctionLabelName(query)
	osm, err := rv.junctionsNamed(named, MaxCandidates)
	if err != nil {
		return nil, err
	}
	all = junctionField(append(all, osm...), named)
	road := askedJunctionRoad(named)
	if road != "" && len(all) > 1 {
		roads, err := rv.roadsOf(all)
		if err != nil {
			return nil, err
		}
		var on []junctionRow
		for i, j := range all {
			if slices.Contains(strings.Split(roads[i], "/"), road) && roads[i] != "" {
				on = append(on, j)
			}
		}
		if len(on) > 0 {
			all = on
		}
	}
	askedName := normalizeName(named)
	vi := -1
	for i, j := range all {
		if normalizeName(j.name) == askedName {
			if vi >= 0 {
				vi = -2
				break
			}
			vi = i
		}
	}
	if vi >= 0 {
		v := all[vi]
		v.score = 1
		out := []junctionRow{v}
		for i, j := range all {
			if i != vi {
				j.score = min(j.score, 1-WinnerMargin)
				out = append(out, j)
			}
		}
		all = out
	}
	if len(all) == 0 || all[0].score < junctionAnswers-WinnerMargin {
		return nil, nil
	}
	best := all[0]
	var rivals []junctionRow
	for _, x := range all[1:] {
		if best.score-x.score < WinnerMargin {
			rivals = append(rivals, x)
		}
	}
	if len(rivals) == 0 {
		via := "gazetteer"
		if best.sourceID == "osm" {
			via = "poi"
		}
		r := Resolution{Kind: ResolvedPoint, Lat: best.lat, Lon: best.lon, Label: junctionLabel(best.name, rv.lang), SourceIDs: []string{best.sourceID}, Via: via, PlaceKind: "junction"}
		return &r, nil
	}
	options := append([]junctionRow{best}, rivals...)
	options = options[:min(len(options), MaxCandidates)]
	r := Resolution{Kind: ResolvedCandidates, Via: "gazetteer"}
	for _, o := range options {
		r.Options = append(r.Options, Option{Label: junctionLabel(o.name, rv.lang) + ", " + coordinateWord(o.lat, o.lon), Kind: "junction", Lat: o.lat, Lon: o.lon, SourceID: o.sourceID})
		r.SourceIDs = credit(append(r.SourceIDs, o.sourceID)...)
	}
	return &r, nil
}

// roadsOf is the road of each junction: the Autobahn row's own, an
// OpenStreetMap node's from the LCL points near it ("" for none, and for
// every node when the LCL read fails, as the platform reads it).
func (rv *resolver) roadsOf(all []junctionRow) ([]string, error) {
	var lons, lats []float64
	var names []string
	for _, j := range all {
		if j.sourceID == "osm" {
			lons, lats, names = append(lons, j.lon), append(lats, j.lat), append(names, j.name)
		}
	}
	var near [][]LCLPoint
	failed := false
	if len(lons) > 0 {
		var err error
		if near, err = rv.reads.LCLNear(rv.ctx, lons, lats); err != nil {
			if rv.ctx.Err() != nil {
				return nil, err
			}
			failed = true
		}
	}
	out := make([]string, len(all))
	k := 0
	for i, j := range all {
		if j.sourceID != "osm" {
			out[i] = j.road
			continue
		}
		if !failed {
			if r := JunctionRoad(names[k], near[k]); r != nil {
				out[i] = *r
			}
		}
		k++
	}
	return out, nil
}

var labelCoordinate = regexp.MustCompile(`^(.*\S),\s*(-?\d{1,2}\.\d{3}),\s*(-?\d{1,3}\.\d{3})$`)

// resolveJunctionOption answers a junction option of ours handed back with
// its coordinate: the one junction at it.
func (rv *resolver) resolveJunctionOption(query string) (*Resolution, error) {
	m := labelCoordinate.FindStringSubmatch(query)
	if m == nil {
		return nil, nil
	}
	head := m[1]
	bare, labelled := junctionLabelName(head)
	core, prefixed := junctionCoreOf(bare)
	if !prefixed && !labelled {
		return nil, nil
	}
	r, err := rv.resolveJunction(core, head)
	if err != nil || r == nil {
		return nil, err
	}
	at := m[2] + ", " + m[3]
	if r.Kind == ResolvedPoint {
		if coordinateWord(r.Lat, r.Lon) == at {
			return r, nil
		}
		return nil, nil
	}
	var hit []Option
	for _, o := range r.Options {
		if coordinateWord(o.Lat, o.Lon) == at {
			hit = append(hit, o)
		}
	}
	if len(hit) != 1 {
		return nil, nil
	}
	o := hit[0]
	via := "gazetteer"
	if o.SourceID == "osm" {
		via = "poi"
	}
	p := Resolution{Kind: ResolvedPoint, Lat: o.Lat, Lon: o.Lon, Label: head, SourceIDs: credit(o.SourceID), Via: via, PlaceKind: "junction"}
	return &p, nil
}
