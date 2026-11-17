package places

import "time"

// KindCount is how many places of one kind one family holds.
type KindCount struct {
	Family string
	Kind   string
	Rows   int
	// EnglishNames is how many of them carry an English name (exonym).
	EnglishNames int
	// NewestLoad is when the newest of them was recorded; nil when unknown.
	NewestLoad *time.Time
}

// Land is a Land the gazetteer holds.
type Land struct {
	Code   string
	Name   string
	Family string
}

// AddressLand is a Land the address families hold addresses of.
type AddressLand struct {
	// Land is the Land's name as the address source writes it; nil for the
	// addresses placed in no Land.
	Land *string
	// DataAsOf is the extract date of its newest address; nil when unknown.
	DataAsOf *time.Time
	Family   string
}

// Coverage is what the gazetteer and the addresses hold (the platform's
// gazetteer and addresses resources).
type Coverage struct {
	Kinds     []KindCount
	Lands     []Land
	Addresses []AddressLand
}

// Families are the families of the coverage, each once, in order of first
// appearance.
func (c Coverage) Families() []string {
	var out []string
	seen := map[string]bool{}
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, k := range c.Kinds {
		add(k.Family)
	}
	for _, l := range c.Lands {
		add(l.Family)
	}
	for _, a := range c.Addresses {
		add(a.Family)
	}
	return out
}
