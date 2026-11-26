package places

import (
	"context"
	"slices"
	"strings"
)

// The place resolver (resolvePlace) is the platform's: the one place a free
// text names, as today's server resolves every place argument. This file is
// its gazetteer read (findPlaceCandidates): the folds the text is asked by,
// the candidate rows, and the word-order rule.

// GazetteerRow is a row of the place gazetteer as the resolver reads it
// (PlaceCandidate): NameNorm is the stored fold of its searchable name,
// Score 1 for an exact fold or road match, down to the trigram threshold.
type GazetteerRow struct {
	ID         string
	Kind       string
	Name       string
	NameNorm   string
	NameEn     *string
	AdminCode  *string
	Population *int
	Road       *string
	Rank       int
	SourceID   string
	ExternalID string
	Lat, Lon   *float64
	Score      float64
}

// The candidate read's numbers, as the platform's: a caller's default count,
// how far below a literal match a transliterated one is placed, the fewest
// rows the statement is asked for (so every branch's limit is one cached
// read), and how far below an exact match a row whose name only permutes the
// asked words is placed.
const (
	DefaultCandidates      = 5
	TransliterationPenalty = 0.2
	CandidateFetchMin      = 30
	WordOrderPenalty       = TransliterationPenalty
)

// PrefixQualifierWords are the words a Gemeinde's official name may carry in
// front of the name a person says ("Bad Homburg", "Neu-Isenburg").
var PrefixQualifierWords = []string{"bad", "neu", "alt", "gross", "klein", "nieder", "ober", "hohen"}

// PrefixQualifiedPatterns are, for each qualifier word, the name that is
// "<word> <folded>" and the LIKE pattern of the names that continue it.
func PrefixQualifiedPatterns(folded string) []string {
	out := make([]string, 0, 2*len(PrefixQualifierWords))
	for _, w := range PrefixQualifierWords {
		out = append(out, w+" "+folded, w+" "+folded+" %")
	}
	return out
}

// CandidateRead is one read of the candidate statement (CANDIDATE_SQL):
// the folded text and its umlaut-expanded form, the penalty, how many rows,
// whether the Gemeinde rule applies, an optional kind and source the rows
// are narrowed to, and the prefix-qualified patterns of both forms.
type CandidateRead struct {
	Folded, Expanded string
	Penalty          float64
	Limit            int
	Qualified        bool
	OnlyKind         *string
	OnlySource       *string
	Patterns         []string
	ExpandedPatterns []string
}

// CandidateOptions narrow a candidate read: Unqualified turns the Gemeinde
// rule off (a station lookup), OnlyKind and OnlySource keep one kind of one
// source inside the limit.
type CandidateOptions struct {
	Unqualified bool
	OnlyKind    string
	OnlySource  string
}

// stripAdminWords is a name's fold without its administrative words: a type
// label after a comma ("München, Landeshauptstadt" keeps the town) and the
// unambiguous ones in front ("Landkreis Harz" is harz).
func stripAdminWords(name string) string {
	candidate := name
	if i := strings.LastIndex(name, ","); i > 0 && suffixAdminWords[normalizeName(name[i+1:])] {
		candidate = name[:i]
	}
	words := strings.Fields(normalizeName(candidate))
	start := 0
	for start < len(words)-1 && prefixAdminWords[words[start]] {
		start++
	}
	if kept := words[start:]; len(kept) > 0 {
		return strings.Join(kept, " ")
	}
	return normalizeName(name)
}

// FoldQuery is the fold a text is asked by: normalizeName, then the
// administrative words stripped.
func FoldQuery(text string) string { return stripAdminWords(normalizeName(text)) }

// digraphs are the umlauts written out, and their bare vowels.
var digraphs = [][2]string{{"oe", "o"}, {"ue", "u"}, {"ae", "a"}}

// transliterateDigraphs is a folded text with oe, ue and ae as the bare
// vowels the stored fold holds ("koeln" is koln), and whether it differs.
func transliterateDigraphs(folded string) (string, bool) {
	out := folded
	for _, d := range digraphs {
		out = strings.ReplaceAll(out, d[0], d[1])
	}
	return out, out != folded
}

// findCandidates is the platform's findPlaceCandidates: the rows a text
// names, a road number by the road alone, else the candidate statement with
// the literal and the expanded fold, cut to limit before the word order.
func (rv *resolver) findCandidates(query string, limit int, opts CandidateOptions) ([]GazetteerRow, error) {
	folded := FoldQuery(query)
	if folded == "" {
		return nil, nil
	}
	var rows []GazetteerRow
	var err error
	if road := RoadForm(query); road != "" {
		rows, err = rv.reads.RoadRows(rv.ctx, road, limit)
	} else {
		expanded, _ := transliterateDigraphs(folded)
		read := CandidateRead{Folded: folded, Expanded: expanded, Penalty: TransliterationPenalty, Limit: max(limit, CandidateFetchMin),
			Qualified: !opts.Unqualified, Patterns: PrefixQualifiedPatterns(folded), ExpandedPatterns: PrefixQualifiedPatterns(expanded)}
		if opts.OnlyKind != "" {
			read.OnlyKind, read.OnlySource = &opts.OnlyKind, &opts.OnlySource
		}
		rows, err = rv.candidates(read)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	expanded, _ := transliterateDigraphs(folded)
	return withWordOrder(rows, []string{folded, expanded}), nil
}

// candidates answers a repeated candidate read from the first answer, for
// one resolution only (memoizedPlaceReads); a failed read is not kept.
func (rv *resolver) candidates(read CandidateRead) ([]GazetteerRow, error) {
	key := candidateKey(read)
	if hit, ok := rv.memo[key]; ok {
		return hit, nil
	}
	rows, err := rv.reads.CandidateRows(rv.ctx, read)
	if err != nil {
		return nil, err
	}
	rv.memo[key] = rows
	return rows, nil
}

func candidateKey(r CandidateRead) string {
	opt := func(s *string) string {
		if s == nil {
			return "\x00"
		}
		return *s
	}
	q := "0"
	if r.Qualified {
		q = "1"
	}
	return strings.Join([]string{r.Folded, r.Expanded, q, opt(r.OnlyKind), opt(r.OnlySource), strings.Repeat("x", r.Limit)}, "\x1f")
}

// wordSet is a fold's words as a set: sorted, each once.
func wordSet(s string) string {
	words := strings.Fields(s)
	slices.Sort(words)
	return strings.Join(slices.Compact(words), " ")
}

// withWordOrder places the exact-scored rows whose name only permutes (or
// repeats) the asked words WordOrderPenalty below, then sorts stably.
func withWordOrder(rows []GazetteerRow, asked []string) []GazetteerRow {
	sets := map[string]bool{}
	exact := map[string]bool{}
	for _, a := range asked {
		sets[wordSet(a)], exact[a] = true, true
	}
	out := slices.Clone(rows)
	for i, c := range out {
		if c.Score < 1 {
			continue
		}
		names := []string{c.NameNorm}
		if c.NameEn != nil {
			names = append(names, FoldQuery(*c.NameEn))
		}
		if slices.ContainsFunc(names, func(n string) bool { return exact[n] }) {
			continue
		}
		if slices.ContainsFunc(names, func(n string) bool { return sets[wordSet(n)] }) {
			out[i].Score = 1 - WordOrderPenalty
		}
	}
	slices.SortStableFunc(out, func(a, b GazetteerRow) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return 0
	})
	return out
}

// resolver is one resolution: its reads, its language and the candidate
// reads it has answered.
type resolver struct {
	ctx   context.Context
	reads ResolveReads
	lang  string
	memo  map[string][]GazetteerRow
}

// ResolveReads are the reads the place resolver makes (the store).
type ResolveReads interface {
	// CandidateRows runs the candidate statement.
	CandidateRows(ctx context.Context, r CandidateRead) ([]GazetteerRow, error)
	// RoadRows are the rows of one road number, by rank.
	RoadRows(ctx context.Context, road string, limit int) ([]GazetteerRow, error)
	// LandsNear are the Land keys of points (LAND_NEAR_POINTS), by their
	// 1-based position.
	LandsNear(ctx context.Context, lons, lats []float64, withinM float64, ids []string) (map[int]string, error)
	// KreisNames are the names of the Kreise of five-digit keys.
	KreisNames(ctx context.Context, keys []string) (map[string]string, error)
	// ShownStops are the stops whose search name ends with a fold.
	ShownStops(ctx context.Context, fold string, limit int) ([]GazetteerRow, error)
	// JunctionsNamed are the OpenStreetMap junction nodes named like a core.
	JunctionsNamed(ctx context.Context, core string, floor float64, limit int) ([]NamedJunction, error)
	// LCLNear are the LCL motorway points near each point, nearest first.
	LCLNear(ctx context.Context, lons, lats []float64) ([][]LCLPoint, error)
	// PoiRows runs one POI statement.
	PoiRows(ctx context.Context, r PoiRead) ([]PoiHit, error)
	// PlaceWords are the words of a name that are names of places, and
	// whether the gazetteer holds any place at all.
	PlaceWords(ctx context.Context, words []string) ([]string, bool, error)
	// AddressRows runs one address statement.
	AddressRows(ctx context.Context, stmt string, args []any) ([]AddressHit, error)
	// StreetRows runs one street statement.
	StreetRows(ctx context.Context, stmt string, args []any) ([]StreetHit, error)
	// IsLoaded tells whether any address (LoadedAddresses) or any place
	// (LoadedGazetteer) is loaded.
	IsLoaded(ctx context.Context, what string) (bool, error)
}
