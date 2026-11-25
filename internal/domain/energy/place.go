package energy

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The charging answer grouped by place, as the platform's charging search
// answers it (find_charging_station): the records of one place (a site an
// operator publishes, the Bundesnetzagentur register's rows of it, one
// building listed per charging unit) are one line, labelled by its name
// and address, with its points' status, price and plugs.

// SiteSource is a site source of the charging source register.
type SiteSource struct {
	Key  string
	Rank int
	// TwinRadiusM is set on a register source: its rows within this radius of
	// an operator's site are that site's twins.
	TwinRadiusM *int
	// AnswersUntil is when a retired source stops answering.
	AnswersUntil *time.Time
}

// StatusSource is a status source of the charging source register, with its age
// limits (staleAfterMs, statementMaxAgeMs, frozenAfterMs) and the site source
// whose points it speaks for.
type StatusSource struct {
	Key                         string
	StaleAfter, StatementMaxAge time.Duration
	FrozenAfter                 *time.Duration
	PointsFrom                  *string
}

// SourceRegister is the charging source register, of the families a key reads.
type SourceRegister struct {
	Sites  []SiteSource
	Status []StatusSource
}

// Masters are the site sources that answer at now: every one not retired, or
// retired and still in its window.
func (r SourceRegister) Masters(now time.Time) []string {
	var out []string
	for _, f := range r.Sites {
		if f.AnswersUntil == nil || now.Before(*f.AnswersUntil) {
			out = append(out, f.Key)
		}
	}
	return out
}

// Ended are the retired sources still answering at now.
func (r SourceRegister) Ended(now time.Time) map[string]bool {
	out := map[string]bool{}
	for _, f := range r.Sites {
		if f.AnswersUntil != nil && now.Before(*f.AnswersUntil) {
			out[f.Key] = true
		}
	}
	return out
}

// Twins are the register sources and the widest twin radius.
func (r SourceRegister) Twins() ([]string, float64) {
	out, radius := []string{}, 0
	for _, f := range r.Sites {
		if f.TwinRadiusM != nil {
			out = append(out, f.Key)
			radius = max(radius, *f.TwinRadiusM)
		}
	}
	return out, float64(radius)
}

// IsRegister tells a register source's record.
func (r SourceRegister) IsRegister(source string) bool {
	for _, f := range r.Sites {
		if f.Key == source {
			return f.TwinRadiusM != nil
		}
	}
	return false
}

// Ranks are the site sources and their ranks.
func (r SourceRegister) Ranks() ([]string, []int) {
	keys, ranks := []string{}, []int{}
	for _, f := range r.Sites {
		keys, ranks = append(keys, f.Key), append(ranks, f.Rank)
	}
	return keys, ranks
}

// StatusKeys are the status sources, and for each the site source whose points
// it speaks for (nil for none).
func (r SourceRegister) StatusKeys() ([]string, []*string) {
	keys, from := []string{}, []*string{}
	for _, f := range r.Status {
		keys, from = append(keys, f.Key), append(from, f.PointsFrom)
	}
	return keys, from
}

// Pairs are the status sources that speak for a site source, and those site
// sources.
func (r SourceRegister) Pairs() (status, master []string) {
	status, master = []string{}, []string{}
	for _, f := range r.Status {
		if f.PointsFrom != nil {
			status, master = append(status, f.Key), append(master, *f.PointsFrom)
		}
	}
	return status, master
}

// WidestWindow is the widest of every status source's limits, and the widest
// statement age alone.
func (r SourceRegister) WidestWindow() (window, statement time.Duration) {
	for _, f := range r.Status {
		window = max(window, f.StaleAfter, f.StatementMaxAge)
		statement = max(statement, f.StatementMaxAge)
	}
	return window, statement
}

// PlaceRead is the place-grouped site read: the circle, how many records,
// the power and plug filters (nil and empty for none).
type PlaceRead struct {
	At         geo.Point
	RadiusM    float64
	Scan       int
	MinKw      *float64
	Connectors []string
}

// PlacePoint is one charge point of a place record, with its price and
// its newest status row.
type PlacePoint struct {
	PointKey         string
	SiteID           *string
	EvseID           *string
	ConnectorTypes   []string
	MaxPowerKw       *float64
	PricePerKwh      *float64
	PriceTaxIncluded *bool
	PriceTaxRate     *float64
	PriceCurrency    *string
	StatusSource     *string
	Status           *string
	OperationStatus  *string
	OpeningStatus    *string
	ObservedAt       *time.Time
	SourceTs         *time.Time
	LastUpdated      *time.Time
	ExchangeProtocol *string
}

// statusRow is the point's newest status, or nil.
func (p PlacePoint) statusRow() *StatusRow {
	if p.StatusSource == nil || p.ObservedAt == nil {
		return nil
	}
	return &StatusRow{Source: *p.StatusSource, ObservedAt: *p.ObservedAt, SourceTs: p.SourceTs, LastUpdated: p.LastUpdated,
		ExchangeProtocol: p.ExchangeProtocol, Status: p.Status, OperationStatus: p.OperationStatus, OpeningStatus: p.OpeningStatus}
}

// PlaceRecord is one record of the platform's site read: one source's site
// at one coordinate, with every record id of it.
type PlaceRecord struct {
	Source, SiteID                      string
	Name, Operator, Street, HouseNumber *string
	Postcode, City, SourceVersion       *string
	Location                            geo.Point
	DistanceM                           float64
	Points                              []PlacePoint
	SiteIDs                             []string
	// Reported is the point the operator published, where the record was
	// placed at its address instead.
	Reported *geo.Point
}

// Key is the record's source and id.
func (r PlaceRecord) Key() string { return r.Source + "\x00" + r.SiteID }

// AddressHit is one OpenStreetMap address found for a record's address.
type AddressHit struct {
	Street, HouseNumber string
	Postcode, Place     *string
	Location            geo.Point
	LiteralScore        int
	ByPostcode          bool
}

// The platform's relocation reach: a register row within TownPointM of its
// town's point is placed at its address, when that lies more than
// AddressElsewhereM away; at most AddressHitsPerSite hits of each kind.
const (
	TownPointM         = 1_000
	AddressElsewhereM  = 1_000
	AddressHitsPerSite = 3
	// MovedHeadroom is how many more records the read takes than it lists,
	// for those placed out of the radius at their address.
	MovedHeadroom = 5
	// ScanPerLine is how many records a line may fold.
	ScanPerLine = 4
	// MaxScanSites is the most records examined.
	MaxScanSites = 100
	// RegisterAddressFarM is how far a register row's town may lie before
	// its address is judged the operator's own.
	RegisterAddressFarM = 25_000
	// endedYieldM is how far an ended source's record yields.
	endedYieldM = 100
	// onePlaceM is how near two records of one line lie.
	onePlaceM = 50
	// sameObjectM is how near two OpenStreetMap hits of one address lie.
	sameObjectM = 150
)

// LedgerDays is how far back the run ledger is read (the platform's
// source-movement look-back).
const LedgerDays = deltaTrustDays

// RegisterAddressElsewhere is the platform's rule for a register row's
// filed address (registerAddressElsewhere): its town is known, lies more
// than RegisterAddressFarM away, and no site nearby shares its postcode.
func RegisterAddressElsewhere(known *bool, nearestM *float64, postcodeSeen *bool) bool {
	return known != nil && *known && nearestM != nil && *nearestM > RegisterAddressFarM && postcodeSeen != nil && !*postcodeSeen
}

// Relocatable tells a register record with a street, number and town.
func Relocatable(r PlaceRecord, reg SourceRegister) bool {
	filled := func(s *string) bool { return s != nil && strings.TrimSpace(*s) != "" }
	return reg.IsRegister(r.Source) && filled(r.Street) && filled(r.HouseNumber) && filled(r.City)
}

// AddressQueryOf is the address search of a record: the folded street and
// its umlaut-free spelling, the town and its, the house number, the
// postcode.
func AddressQueryOf(r PlaceRecord) [6]string {
	street, town := streetFold(*r.Street), nameFold(*r.City)
	return [6]string{street, transliterateDigraphs(street), town, transliterateDigraphs(town), houseNumberFold(*r.HouseNumber), postcodeOf(r.Postcode)}
}

// TownFold is a record's town as the town point read compares it.
func TownFold(r PlaceRecord) string {
	if r.City == nil {
		return ""
	}
	return nameFold(*r.City)
}

// RelocationOf is where a record flagged at its town's point lies by its
// address: the one town of the best hits, more than AddressElsewhereM away.
func RelocationOf(r PlaceRecord, hits []AddressHit) (geo.Point, bool) {
	postcode := postcodeOf(r.Postcode)
	var own []AddressHit
	for _, a := range hits {
		if a.ByPostcode {
			own = append(own, a)
		}
	}
	if len(own) == 0 {
		for _, a := range hits {
			if postcode == "" || a.Postcode == nil || *a.Postcode == postcode {
				own = append(own, a)
			}
		}
	}
	if len(own) == 0 {
		return geo.Point{}, false
	}
	best := own[0].LiteralScore
	for _, a := range own {
		best = max(best, a.LiteralScore)
	}
	var top []AddressHit
	for _, a := range own {
		if a.LiteralScore == best {
			top = append(top, a)
		}
	}
	towns := addressTowns(foldSameAddresses(top))
	if len(towns) != 1 {
		return geo.Point{}, false
	}
	hit := towns[0][0]
	if geo.MetresBetween(r.Location, hit.Location) <= AddressElsewhereM {
		return geo.Point{}, false
	}
	return hit.Location, true
}

// nameFold, streetFold and houseNumberFold are the platform's search
// forms (normalizeName, normalizeStreet, normalizeHouseNumber).
var (
	nameFold        = places.NameFold
	streetFold      = places.StreetFold
	houseNumberFold = places.HouseNumberFold
)

// fold is a text with no space, as the same-object rules compare it.
func fold(s *string) string {
	if s == nil {
		return ""
	}
	return strings.ReplaceAll(nameFold(*s), " ", "")
}

func compatible(a, b *string) bool {
	return a == nil || b == nil || *a == "" || *b == "" || fold(a) == fold(b)
}

// sameObjectMetres is the same-object rules' distance (the haversine on
// 6,371 km).
func sameObjectMetres(a, b geo.Point) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * 6_371_000 * math.Asin(math.Min(1, math.Sqrt(h)))
}

func isSameAddress(a, b AddressHit) bool {
	return fold(&a.Street) == fold(&b.Street) && fold(&a.HouseNumber) == fold(&b.HouseNumber) &&
		compatible(a.Postcode, b.Postcode) && compatible(a.Place, b.Place) && sameObjectMetres(a.Location, b.Location) <= sameObjectM
}

// foldSameAddresses keeps one hit of each address, the one with the most
// of postcode and place.
func foldSameAddresses(hits []AddressHit) []AddressHit {
	var groups [][]AddressHit
	for _, h := range hits {
		at := slices.IndexFunc(groups, func(g []AddressHit) bool {
			return slices.ContainsFunc(g, func(m AddressHit) bool { return isSameAddress(m, h) })
		})
		if at < 0 {
			groups = append(groups, []AddressHit{h})
		} else {
			groups[at] = append(groups[at], h)
		}
	}
	has := func(s *string) int {
		if s == nil || strings.TrimSpace(*s) == "" {
			return 0
		}
		return 1
	}
	out := make([]AddressHit, len(groups))
	for i, g := range groups {
		kept := g[0]
		for _, m := range g[1:] {
			if has(m.Postcode)+has(m.Place) > has(kept.Postcode)+has(kept.Place) {
				kept = m
			}
		}
		out[i] = kept
	}
	return out
}

func sameTown(a, b AddressHit) bool {
	if a.Place != nil && b.Place != nil && nameFold(*a.Place) != nameFold(*b.Place) {
		return false
	}
	return a.Postcode == nil || b.Postcode == nil || *a.Postcode == *b.Postcode
}

// addressTowns groups hits by town: every hit of a group of one town.
func addressTowns(hits []AddressHit) [][]AddressHit {
	var groups [][]AddressHit
	for _, h := range hits {
		at := slices.IndexFunc(groups, func(g []AddressHit) bool {
			return !slices.ContainsFunc(g, func(m AddressHit) bool { return !sameTown(m, h) })
		})
		if at < 0 {
			groups = append(groups, []AddressHit{h})
		} else {
			groups[at] = append(groups[at], h)
		}
	}
	return groups
}

// PlaceAtAddresses places each relocated record at its address, drops one
// that lands outside the radius or on an operator's site (a register
// twin), and keeps the nearest limit.
func PlaceAtAddresses(recs []PlaceRecord, moved map[string]geo.Point, centre geo.Point, radiusM float64, limit int, reg SourceRegister) []PlaceRecord {
	if len(moved) == 0 {
		return recs[:min(limit, len(recs))]
	}
	twins, radius := reg.Twins()
	var placed []PlaceRecord
	for _, r := range recs {
		to, ok := moved[r.Key()]
		if !ok {
			placed = append(placed, r)
			continue
		}
		d := geo.MetresBetween(centre, to)
		if d > radiusM {
			continue
		}
		twin := slices.Contains(twins, r.Source) && slices.ContainsFunc(recs, func(o PlaceRecord) bool {
			return !slices.Contains(twins, o.Source) && geo.MetresBetween(to, o.Location) <= radius
		})
		if twin {
			continue
		}
		was := r.Location
		r.Location, r.DistanceM, r.Reported = to, d, &was
		placed = append(placed, r)
	}
	sort.SliceStable(placed, func(i, j int) bool { return placed[i].DistanceM < placed[j].DistanceM })
	return placed[:min(limit, len(placed))]
}

// Price is a price per kWh, and whether it includes VAT.
type Price struct {
	Eur         float64
	IncludesVat bool
}

// grossPrice is a point's ad-hoc price per kWh, gross where the row says
// how; nil when none is published.
func grossPrice(p PlacePoint) *Price {
	if p.PricePerKwh == nil {
		return nil
	}
	if p.PriceTaxIncluded != nil && *p.PriceTaxIncluded {
		return &Price{Eur: *p.PricePerKwh, IncludesVat: true}
	}
	if p.PriceTaxRate == nil {
		return &Price{Eur: *p.PricePerKwh}
	}
	return &Price{Eur: *p.PricePerKwh * (1 + *p.PriceTaxRate/100), IncludesVat: true}
}

// Judged is a place record judged at now.
type Judged struct {
	Record PlaceRecord
	JudgedPoints
	MaxPowerKw *float64
	Price      *Price
	// Connectors are the plug names of its points, in the language.
	Connectors []string
	// Ended: its source is retired and in its window.
	Ended bool
}

// Judge judges each record: its points' status (none for an ended
// source's), its highest power, its cheapest price, its plugs.
func Judge(recs []PlaceRecord, reg SourceRegister, liveness []SourceLiveness, now time.Time, lang string) []Judged {
	ended := reg.Ended(now)
	out := make([]Judged, len(recs))
	for i, r := range recs {
		windows := reg.Status
		if ended[r.Source] {
			windows = nil
		}
		j := Judged{Record: r, JudgedPoints: JudgePoints(r.Points, windows, liveness, now), Ended: ended[r.Source], Connectors: []string{}}
		for _, p := range r.Points {
			if p.MaxPowerKw != nil && (j.MaxPowerKw == nil || *p.MaxPowerKw > *j.MaxPowerKw) {
				v := *p.MaxPowerKw
				j.MaxPowerKw = &v
			}
			if g := grossPrice(p); g != nil && (j.Price == nil || g.Eur < j.Price.Eur) {
				j.Price = g
			}
			for _, c := range p.ConnectorTypes {
				if l := ConnectorLabel(c, lang); !slices.Contains(j.Connectors, l) {
					j.Connectors = append(j.Connectors, l)
				}
			}
		}
		out[i] = j
	}
	return out
}

// outOfOrder is 1 for a record whose every point is out of order now.
func (j Judged) outOfOrder() int {
	if allOutOfOrder(j.States) {
		return 1
	}
	return 0
}

// rankM is the distance a record is ranked by: an ended source's yields.
func (j Judged) rankM() float64 {
	if j.Ended {
		return j.Record.DistanceM + endedYieldM
	}
	return j.Record.DistanceM
}

// Elsewhere marks a register record filed under an address that is not
// where it lies: the town it lies in, when one is known.
type Elsewhere struct{ LocatedNear *string }

// Place is one line of the answer: its records, nearest first, and its
// label.
type Place struct {
	Members []Judged
	Label   string
	// Elsewhere is set when the lead record's address is not where it lies.
	Elsewhere *Elsewhere
}

// line is a fold group: its key, facts and operator text, and members.
type line struct {
	key, label, facts, operator string
	members                     []int
}

// statusSlot stands for the status in a line's facts.
const statusSlot = "\x00status\x00"

// Lines is the platform's rough fold of the candidates: listed best first
// (wholly out of order last, then by rank distance), at most limit ×
// ScanPerLine, records of one label, operator and facts within 50 m one
// line, then lines of one place one line.
type Lines struct {
	Listed []Judged
	// More tells that the rough fold left more lines than the limit.
	More  bool
	reg   SourceRegister
	lang  string
	facts []string
	ops   []string
	rough []line
}

// RoughLines folds the candidates and keeps the first limit lines.
func RoughLines(candidates []Judged, limit int, reg SourceRegister, lang string) Lines {
	listed := slices.Clone(candidates)
	sort.SliceStable(listed, func(a, b int) bool {
		x, y := listed[a], listed[b]
		if x.outOfOrder() != y.outOfOrder() {
			return x.outOfOrder() < y.outOfOrder()
		}
		return x.rankM() < y.rankM()
	})
	listed = listed[:min(len(listed), limit*ScanPerLine)]
	l := Lines{Listed: listed, reg: reg, lang: lang}
	rows := make([]foldRow, len(listed))
	for i, j := range listed {
		facts, op := l.body(j)
		l.facts, l.ops = append(l.facts, facts), append(l.ops, op)
		rows[i] = foldRow{j: i, label: SiteLabel(j.Record, lang, nil), facts: facts, operator: op}
	}
	l.rough = l.samePlace(l.foldLines(rows))
	l.More = len(l.rough) > limit
	l.rough = l.rough[:min(len(l.rough), limit)]
	return l
}

// RoughRecords are the records of the rough lines kept.
func (l Lines) RoughRecords() []PlaceRecord {
	var out []PlaceRecord
	for _, g := range l.rough {
		for _, m := range g.members {
			out = append(out, l.Listed[m].Record)
		}
	}
	return out
}

// body is a record's facts and operator text, as the line prints them.
func (l Lines) body(j Judged) (facts, operator string) {
	de := l.lang == German
	var bits []string
	if len(j.Connectors) > 0 {
		bits = append(bits, strings.Join(j.Connectors, "/"))
	}
	if j.MaxPowerKw != nil {
		upTo := "up to"
		if de {
			upTo = "bis"
		}
		bits = append(bits, upTo+" "+fmtKw(*j.MaxPowerKw, l.lang))
	}
	if j.Price != nil {
		bits = append(bits, fmtEur(j.Price.Eur, j.Price.IncludesVat, l.lang))
	}
	if j.Ended {
		bits = append(bits, "\x00ended\x00"+j.Record.Source)
	} else {
		bits = append(bits, statusSlot)
	}
	if l.reg.IsRegister(j.Record.Source) {
		bits = append(bits, map[bool]string{true: "laut Ladesäulenregister", false: "per charging point register"}[de])
	}
	if j.Record.Reported != nil {
		bits = append(bits, map[bool]string{true: "Lage nach Anschrift", false: "located by its address"}[de])
	}
	if j.Record.Operator != nil && strings.TrimSpace(*j.Record.Operator) != "" {
		operator = " (" + *j.Record.Operator + ")"
	}
	return strings.Join(bits, ", "), operator
}

type foldRow struct {
	j                      int
	label, facts, operator string
}

// foldLines: one key (label, operator, facts) within onePlaceM of the
// line's first record is that line.
func (l Lines) foldLines(rows []foldRow) []line {
	var out []line
	for _, r := range rows {
		key := r.label + r.operator + " — " + r.facts
		at := slices.IndexFunc(out, func(g line) bool {
			return g.key == key && geo.MetresBetween(l.Listed[g.members[0]].Record.Location, l.Listed[r.j].Record.Location) <= onePlaceM
		})
		if at < 0 {
			out = append(out, line{key: key, label: r.label, facts: r.facts, operator: r.operator, members: []int{r.j}})
		} else {
			out[at].members = append(out[at].members, r.j)
		}
	}
	return out
}

// samePlace folds lines of one place (sameAnswerRow of their first
// records): the first line kept, the facts of each joined.
func (l Lines) samePlace(groups []line) []line {
	factsOf := func(g line) answerFacts {
		r := l.Listed[g.members[0]].Record
		return answerFacts{at: r.Location, name: r.Name, operator: r.Operator, street: r.Street, house: r.HouseNumber, postcode: r.Postcode}
	}
	type held struct {
		kept    line
		members []line
	}
	var hs []held
	for _, g := range groups {
		f := factsOf(g)
		at := slices.IndexFunc(hs, func(h held) bool { return sameAnswerRow(factsOf(h.kept), f) })
		if at < 0 {
			hs = append(hs, held{kept: g, members: []line{g}})
		} else {
			hs[at].members = append(hs[at].members, g)
		}
	}
	out := make([]line, len(hs))
	for i, h := range hs {
		out[i] = h.kept
		if len(h.members) == 1 {
			continue
		}
		var facts []string
		var members []int
		for _, m := range h.members {
			if !slices.Contains(facts, m.facts) {
				facts = append(facts, m.facts)
			}
			members = append(members, m.members...)
		}
		out[i].facts, out[i].members = strings.Join(facts, "; "), members
	}
	return out
}

// Places refolds the rough lines under each record's own label (a
// register record filed elsewhere named by where it lies), best first, and
// keeps the first limit places.
func (l Lines) Places(elsewhere map[string]Elsewhere, limit int) []Place {
	labelOf := func(i int) string {
		r := l.Listed[i].Record
		if e, ok := elsewhere[r.Key()]; ok {
			return SiteLabel(r, l.lang, &e)
		}
		return SiteLabel(r, l.lang, nil)
	}
	var rows []foldRow
	for _, g := range l.rough {
		for _, m := range g.members {
			rows = append(rows, foldRow{j: m, label: labelOf(m), facts: g.facts, operator: g.operator})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		x, y := l.Listed[rows[a].j], l.Listed[rows[b].j]
		if x.outOfOrder() != y.outOfOrder() {
			return x.outOfOrder() < y.outOfOrder()
		}
		return x.rankM() < y.rankM()
	})
	groups := l.samePlace(l.foldLines(rows))
	groups = groups[:min(len(groups), limit)]
	out := make([]Place, len(groups))
	for i, g := range groups {
		p := Place{Label: labelOf(g.members[0])}
		for _, m := range g.members {
			p.Members = append(p.Members, l.Listed[m])
		}
		if e, ok := elsewhere[p.Members[0].Record.Key()]; ok {
			p.Elsewhere = &e
		}
		out[i] = p
	}
	return out
}

// coordinateText is a coordinate at five decimals in the language.
func coordinateText(at geo.Point, lang string) string {
	lat, lon := toFixed(at.Lat, 5), toFixed(at.Lon, 5)
	if lang == German {
		return localDecimal(lat, lang) + "; " + localDecimal(lon, lang)
	}
	return lat + ", " + lon
}

// registerElsewhereLabel names a register record by where it lies, with
// the town it was filed under.
func registerElsewhereLabel(r PlaceRecord, lang string, near *string) string {
	de := lang == German
	var at string
	switch {
	case near == nil && de:
		at = "Standort bei " + coordinateText(r.Location, lang)
	case near == nil:
		at = "site at " + coordinateText(r.Location, lang)
	case de:
		at = "Standort bei " + *near
	default:
		at = "site near " + *near
	}
	town := townLine(r.Postcode, r.City)
	if de {
		return at + " (im Register mit der Adresse " + town + " gemeldet, die nicht am Standort liegt — vermutlich die Anschrift des Betreibers)"
	}
	return at + " (filed in the register under an address in " + town + ", which is not where the charger is — probably the operator's own address)"
}

// SiteLabel is a record's label: its name with what of its address the
// name does not say, else its address, else "Ladestation"; a register
// record filed elsewhere by where it lies.
func SiteLabel(r PlaceRecord, lang string, elsewhere *Elsewhere) string {
	if elsewhere != nil {
		at := registerElsewhereLabel(r, lang, elsewhere.LocatedNear)
		if own := siteNameShown(r.Name); own != nil {
			return *own + ", " + at
		}
		return at
	}
	street := streetLine(r.Street, r.HouseNumber)
	town := townLine(r.Postcode, r.City)
	var where []string
	if street != nil && *street != "" {
		where = append(where, *street)
	}
	if town != "" {
		where = append(where, town)
	}
	if name := siteNameShown(r.Name); name != nil {
		named := addressFold(*name)
		var rest []string
		if street != nil && *street != "" && !strings.Contains(named, addressFold(*street)) {
			rest = append(rest, *street)
		}
		if town != "" && !strings.Contains(named, addressFold(town)) {
			rest = append(rest, town)
		}
		if len(rest) == 0 {
			return *name
		}
		return *name + ", " + strings.Join(rest, ", ")
	}
	if len(where) == 0 {
		return "Ladestation"
	}
	return strings.Join(where, ", ")
}

// RegisterReleaseOf is the newest release date (YYYY-MM-DD) among the
// register records, or nil.
func RegisterReleaseOf(recs []PlaceRecord, reg SourceRegister) *string {
	var best *string
	for _, r := range recs {
		if !reg.IsRegister(r.Source) || r.SourceVersion == nil || !isoDate.MatchString(*r.SourceVersion) {
			continue
		}
		if best == nil || *r.SourceVersion > *best {
			v := *r.SourceVersion
			best = &v
		}
	}
	return best
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
