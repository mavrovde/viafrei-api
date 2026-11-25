package places

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The place resolver's addresses and streets (place.ts resolveAddress,
// resolveStreet; the platform's findOsmAddresses and findOsmStreet).

// AddressHit is an address row of a typed address read.
type AddressHit struct {
	ID, Street, HouseNumber string
	Postcode, Place, Land   *string
	Lat, Lon                float64
	ExtractDate             string
	LiteralScore            int
	HouseNumberNorm         string
	numberMatch             string
}

// StreetHit is one stretch of a street: its name, postcode and town, the
// addresses it holds and the point of the one nearest its middle.
type StreetHit struct {
	Street          string
	Postcode, Place *string
	Addresses       int
	Lat, Lon        float64
	ExtractDate     string
}

// The address and street statements the resolver reads.
const (
	AddrByPostcode       = "address_by_postcode"
	AddrStreetByPostcode = "address_street_by_postcode"
	AddrByPlace          = "address_by_place"
	AddrByPlacePrefix    = "address_by_place_prefix"
	AddrStreetByPlace    = "address_street_by_place"
	StreetByPlace        = "street_by_place"
	StreetByPostcode     = "street_by_postcode"
	// LoadedAddresses and LoadedGazetteer name the two "is anything loaded"
	// reads.
	LoadedAddresses = "addresses"
	LoadedGazetteer = "gazetteer"

	placePrefixHits   = 10
	defaultStreetHits = 5
)

var (
	houseNumberLike  = regexp.MustCompile(`(?i)[a-zäöüß.]\s*\d{1,4}\s*[a-z]?\s*(,|$)`)
	roadNumberAlone  = regexp.MustCompile(`(?i)^\s*[ab]\s*\d{1,3}\s*$`)
	firstPostcode    = regexp.MustCompile(`\b\d{5}\b`)
	postcodeGroup    = regexp.MustCompile(`\b(\d{5})\b`)
	houseNumberPart  = regexp.MustCompile(`^(\d{1,4})([a-z]*|/\d{1,3})(?:[-–](\d{1,4})([a-z]*))?$`)
	oneLetter        = regexp.MustCompile(`^[a-z]$`)
	numberPartsSplit = regexp.MustCompile(`[,;]`)
)

// looksLikeHouseNumber: a word then a number (and a letter) at the end of
// the text or of its first part, not a road number alone.
func looksLikeHouseNumber(query string) bool {
	q := strings.TrimSpace(query)
	return houseNumberLike.MatchString(q) && !roadNumberAlone.MatchString(q)
}

// normalizeStreet is the platform's street fold: "str" written out, the
// words run together.
func normalizeStreet(raw string) string {
	ws := strings.Split(normalizeName(raw), " ")
	for i, t := range ws {
		if t == "str" {
			ws[i] = "strasse"
		} else if strings.HasSuffix(t, "str") {
			ws[i] = strings.TrimSuffix(t, "str") + "strasse"
		}
	}
	return strings.TrimSpace(strings.Join(ws, ""))
}

func postcodeOfText(raw string) *string {
	if m := postcodeGroup.FindStringSubmatch(raw); m != nil {
		return &m[1]
	}
	return nil
}

// addressAsked is a typed address in its search forms.
type addressAsked struct {
	streetNorm, houseNumberNorm string
	postcode, placeNorm         *string
}

func buildAddress(street, number, rest string) *addressAsked {
	postcode := postcodeOfText(rest)
	place := rest
	if loc := firstPostcode.FindStringIndex(rest); loc != nil {
		place = rest[:loc[0]] + rest[loc[1]:]
	}
	place = strings.Join(strings.Fields(place), " ")
	var placeNorm *string
	if place != "" {
		placeNorm = sp(normalizeName(place))
	}
	if placeNorm == nil && postcode == nil {
		return nil
	}
	return &addressAsked{streetNorm: normalizeStreet(street), houseNumberNorm: strings.Join(strings.Fields(strings.ToLower(number)), ""), postcode: postcode, placeNorm: placeNorm}
}

// parseAddressQuery reads "Street 12, 12345 Town" as the platform does.
func parseAddressQuery(raw string) *addressAsked {
	in := strings.TrimSpace(raw)
	if in == "" {
		return nil
	}
	var segs []string
	for _, s := range strings.Split(in, ",") {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) > 1 {
		for i, s := range segs {
			if m := streetAndNumber.FindStringSubmatch(s); m != nil {
				rest := append(slices.Clone(segs[:i]), segs[i+1:]...)
				return buildAddress(m[1], m[2], strings.Join(rest, " "))
			}
		}
	}
	if m := streetNumberRest.FindStringSubmatch(in); m != nil {
		return buildAddress(m[1], m[2], m[3])
	}
	return nil
}

type numberPart struct {
	lo, hi  int
	suffix  string
	letters *[2]string
}

func houseNumberParts(norm string) []numberPart {
	var parts []numberPart
	for _, raw := range numberPartsSplit.Split(norm, -1) {
		m := houseNumberPart.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			return nil
		}
		lo, _ := strconv.Atoi(m[1])
		hi := lo
		if m[3] != "" {
			hi, _ = strconv.Atoi(m[3])
		}
		if hi < lo {
			return nil
		}
		from, to := m[2], m[4]
		if lo == hi && oneLetter.MatchString(from) && oneLetter.MatchString(to) && from <= to {
			parts = append(parts, numberPart{lo: lo, hi: hi, letters: &[2]string{from, to}})
			continue
		}
		p := numberPart{lo: lo, hi: hi}
		if m[3] == "" {
			p.suffix = from
		}
		parts = append(parts, p)
	}
	return parts
}

func rangeHolds(p numberPart, n int) bool {
	if n < p.lo || n > p.hi {
		return false
	}
	return p.lo%2 != p.hi%2 || n%2 == p.lo%2
}

// houseNumberMatch is how a mapped house number holds the one asked:
// contained, base (the number without the letter asked) or suffixed (the
// number with a letter), else "".
func houseNumberMatch(asked, mapped string) string {
	want, have := houseNumberParts(asked), houseNumberParts(mapped)
	if want == nil || have == nil || len(want) != 1 || want[0].lo != want[0].hi {
		return ""
	}
	n, letter := want[0].lo, want[0].suffix
	single := func(p numberPart) bool { return p.lo == p.hi }
	switch {
	case len(have) > 1 && slices.ContainsFunc(have, func(p numberPart) bool { return single(p) && p.letters == nil && p.lo == n && p.suffix == letter }):
		return "contained"
	case letter != "" && slices.ContainsFunc(have, func(p numberPart) bool {
		return p.letters != nil && p.lo == n && letter >= p.letters[0] && letter <= p.letters[1]
	}):
		return "contained"
	case letter == "" && slices.ContainsFunc(have, func(p numberPart) bool { return !single(p) && rangeHolds(p, n) }):
		return "contained"
	case letter != "" && slices.ContainsFunc(have, func(p numberPart) bool {
		if single(p) {
			return p.lo == n && p.suffix == "" && p.letters == nil
		}
		return rangeHolds(p, n)
	}):
		return "base"
	case letter == "" && slices.ContainsFunc(have, func(p numberPart) bool { return single(p) && p.lo == n && (p.suffix != "" || p.letters != nil) }):
		return "suffixed"
	}
	return ""
}

func closestToTyped(rows []AddressHit, match string) []AddressHit {
	best := math.MinInt
	for _, r := range rows {
		best = max(best, r.LiteralScore)
	}
	var out []AddressHit
	for _, r := range rows {
		if r.LiteralScore == best {
			r.numberMatch = match
			out = append(out, r)
		}
	}
	return out
}

func byHouseNumber(rows []AddressHit, asked string) []AddressHit {
	for _, tier := range []string{"contained", "base", "suffixed"} {
		var at []AddressHit
		for _, r := range rows {
			if houseNumberMatch(asked, r.HouseNumberNorm) == tier {
				at = append(at, r)
			}
		}
		if len(at) > 0 {
			return closestToTyped(at, tier)
		}
	}
	return nil
}

func (rv *resolver) addressRows(stmt string, args ...any) ([]AddressHit, error) {
	return rv.reads.AddressRows(rv.ctx, stmt, args)
}

// findAddresses is the platform's findOsmAddresses.
func (rv *resolver) findAddresses(q addressAsked, limit int) ([]AddressHit, error) {
	street, _ := transliterateDigraphs(q.streetNorm)
	samePostcode := func(rows []AddressHit) []AddressHit {
		if q.postcode == nil {
			return rows
		}
		var out []AddressHit
		for _, r := range rows {
			if r.Postcode == nil || *r.Postcode == *q.postcode {
				out = append(out, r)
			}
		}
		return out
	}
	if q.postcode != nil {
		by, err := rv.addressRows(AddrByPostcode, q.streetNorm, street, *q.postcode, q.houseNumberNorm, limit)
		if err != nil || len(by) > 0 {
			return closestToTyped(by, ""), err
		}
		on, err := rv.addressRows(AddrStreetByPostcode, q.streetNorm, street, *q.postcode)
		if err != nil {
			return nil, err
		}
		if near := byHouseNumber(on, q.houseNumberNorm); len(near) > 0 {
			return near[:min(len(near), limit)], nil
		}
	}
	if q.placeNorm == nil {
		return nil, nil
	}
	place, _ := transliterateDigraphs(*q.placeNorm)
	by, err := rv.addressRows(AddrByPlace, q.streetNorm, street, *q.placeNorm, place, q.houseNumberNorm, limit)
	if err != nil {
		return nil, err
	}
	if by = samePostcode(by); len(by) > 0 {
		return closestToTyped(by, ""), nil
	}
	if q.postcode == nil {
		prefixed, err := rv.addressRows(AddrByPlacePrefix, q.streetNorm, street, *q.placeNorm, q.houseNumberNorm, placePrefixHits)
		if err != nil || len(prefixed) > 0 {
			return closestToTyped(prefixed, ""), err
		}
	}
	on, err := rv.addressRows(AddrStreetByPlace, q.streetNorm, street, *q.placeNorm, place)
	if err != nil {
		return nil, err
	}
	near := byHouseNumber(samePostcode(on), q.houseNumberNorm)
	return near[:min(len(near), limit)], nil
}

func isSameAddress(a, b AddressHit) bool {
	return objFold(&a.Street) == objFold(&b.Street) && objFold(&a.HouseNumber) == objFold(&b.HouseNumber) &&
		compatible(a.Postcode, b.Postcode) && compatible(a.Place, b.Place) && objectMetres(a.Lat, a.Lon, b.Lat, b.Lon) <= sameObjectM
}

// foldSameAddresses keeps one hit an address: of a group, the one with
// most of postcode and town.
func foldSameAddresses(hits []AddressHit) []AddressHit {
	var groups [][]AddressHit
	for _, r := range hits {
		gi := slices.IndexFunc(groups, func(g []AddressHit) bool {
			return slices.ContainsFunc(g, func(m AddressHit) bool { return isSameAddress(m, r) })
		})
		if gi < 0 {
			groups = append(groups, []AddressHit{r})
		} else {
			groups[gi] = append(groups[gi], r)
		}
	}
	out := make([]AddressHit, 0, len(groups))
	for _, g := range groups {
		kept := g[0]
		for _, m := range g {
			if has(m.Postcode)+has(m.Place) > has(kept.Postcode)+has(kept.Place) {
				kept = m
			}
		}
		out = append(out, kept)
	}
	return out
}

func sameAddressTown(a, b AddressHit) bool {
	if a.Place != nil && b.Place != nil && normalizeName(*a.Place) != normalizeName(*b.Place) {
		return false
	}
	return a.Postcode == nil || b.Postcode == nil || *a.Postcode == *b.Postcode
}

// addressTowns groups hits by town: a hit joins the first group whose every
// member is of its town.
func addressTowns(hits []AddressHit) [][]AddressHit {
	var groups [][]AddressHit
	for _, h := range hits {
		gi := slices.IndexFunc(groups, func(g []AddressHit) bool {
			for _, m := range g {
				if !sameAddressTown(m, h) {
					return false
				}
			}
			return true
		})
		if gi < 0 {
			groups = append(groups, []AddressHit{h})
		} else {
			groups[gi] = append(groups[gi], h)
		}
	}
	return groups
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func addressLabel(h AddressHit) string {
	street := ""
	if s := StreetLine(&h.Street, &h.HouseNumber); s != nil {
		street = *s
	}
	return joinNonEmpty(street, TownLine(h.Postcode, h.Place))
}

// resolveAddress answers a typed address: one town's address as a point,
// several towns' as a list.
func (rv *resolver) resolveAddress(query string) (Resolution, error) {
	parsed := parseAddressQuery(query)
	if parsed == nil {
		return rv.missReason(query)
	}
	found, err := rv.findAddresses(*parsed, MaxCandidates*2)
	if err != nil {
		return Resolution{}, err
	}
	towns := addressTowns(foldSameAddresses(found))
	if len(towns) > 1 {
		r := Resolution{Kind: ResolvedCandidates, SourceIDs: []string{"osm"}, Via: "address"}
		for _, t := range towns[:min(len(towns), MaxCandidates)] {
			r.Options = append(r.Options, Option{Label: addressLabel(t[0]), Kind: "address", Lat: t[0].Lat, Lon: t[0].Lon, SourceID: "osm"})
		}
		return r, nil
	}
	if len(towns) == 1 {
		h := towns[0][0]
		return Resolution{Kind: ResolvedPoint, Lat: h.Lat, Lon: h.Lon, Label: addressLabel(h), SourceIDs: []string{"osm"}, Via: "address"}, nil
	}
	return rv.missReason(query)
}

func (rv *resolver) missReason(query string) (Resolution, error) {
	loaded, err := rv.reads.IsLoaded(rv.ctx, LoadedAddresses)
	if err != nil {
		return Resolution{}, err
	}
	if loaded {
		return unresolved("address_not_found", query), nil
	}
	return unresolved("house_number", query), nil
}

func (rv *resolver) nameMissReason(query string) (Resolution, error) {
	loaded, err := rv.reads.IsLoaded(rv.ctx, LoadedGazetteer)
	if err != nil {
		return Resolution{}, err
	}
	if loaded {
		return unresolved("not_found", query), nil
	}
	return unresolved("gazetteer_empty", query), nil
}

// findStreet is the platform's findOsmStreet.
func (rv *resolver) findStreet(streetNorm string, placeNorm, postcode *string) ([]StreetHit, error) {
	if streetNorm == "" {
		return nil, nil
	}
	street, _ := transliterateDigraphs(streetNorm)
	if postcode != nil {
		by, err := rv.reads.StreetRows(rv.ctx, StreetByPostcode, []any{streetNorm, street, *postcode, defaultStreetHits})
		if err != nil || len(by) > 0 {
			return by, err
		}
	}
	if placeNorm == nil {
		return nil, nil
	}
	place, _ := transliterateDigraphs(*placeNorm)
	return rv.reads.StreetRows(rv.ctx, StreetByPlace, []any{streetNorm, street, *placeNorm, place, defaultStreetHits})
}

func streetLabel(h StreetHit) string {
	return joinNonEmpty(h.Street, TownLine(h.Postcode, h.Place))
}

func streetResolution(hits []StreetHit, lang string) *Resolution {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) == 1 {
		h := hits[0]
		return &Resolution{Kind: ResolvedPoint, Lat: h.Lat, Lon: h.Lon, Label: streetLabel(h), SourceIDs: []string{"osm"}, Via: "address"}
	}
	labels := make([]string, len(hits))
	for i, h := range hits {
		labels[i] = streetLabel(h)
	}
	r := &Resolution{Kind: ResolvedCandidates, SourceIDs: []string{"osm"}, Via: "address"}
	for i, h := range hits {
		label := labels[i]
		twice := 0
		for _, l := range labels {
			if l == label {
				twice++
			}
		}
		if twice > 1 {
			label += " (" + strconv.Itoa(h.Addresses) + [2]string{" Adressen", " addresses"}[langIdx(lang)] + ")"
		}
		r.Options = append(r.Options, Option{Label: label, Kind: "street", Lat: h.Lat, Lon: h.Lon, SourceID: "osm"})
	}
	return r
}

// resolveStreet reads "street, town" (either way round) as a street.
func (rv *resolver) resolveStreet(query string) (*Resolution, error) {
	parts := strings.Split(query, ",")
	if len(parts) != 2 {
		return nil, nil
	}
	a, b := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if a == "" || b == "" {
		return nil, nil
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		streetText, town := pair[0], pair[1]
		postcode := postcodeOfText(town)
		placeText := town
		if loc := firstPostcode.FindStringIndex(town); loc != nil {
			placeText = town[:loc[0]] + town[loc[1]:]
		}
		placeText = strings.Join(strings.Fields(placeText), " ")
		var placeNorm *string
		if placeText != "" {
			placeNorm = sp(normalizeName(placeText))
		}
		hits, err := rv.findStreet(normalizeStreet(streetText), placeNorm, postcode)
		if err != nil {
			return nil, err
		}
		if r := streetResolution(hits, rv.lang); r != nil {
			return r, nil
		}
	}
	return nil, nil
}
