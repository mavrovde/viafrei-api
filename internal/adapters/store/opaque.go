package store

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"
)

// opaque rewrites every parameter $n of sql to ((select $n::T)::T), T the
// nth of types: the type Postgres infers for $n in sql as written.
//
// Every statement is prepared and cached per connection (cache_statement),
// and under plan_cache_mode auto Postgres plans its first five executions
// with the values bound, then keeps the generic plan only when its estimate
// is not dearer than those custom plans'. A read whose values sharpen the
// estimates (a point, a radius, a key that is rare or absent) is so planned
// afresh on every call. Read through a one-row subquery a value is opaque to
// the planner, so the custom and generic estimates agree and auto keeps the
// one generic plan; the outer cast keeps an expression where one is asked
// (= any(...) over an array, fetch first ... rows), and the parentheses
// keep it one in every place a parameter stands (main 2.6.43, carried).
//
// types must name each parameter sql uses and no more: a parameter without a
// type, or a type no parameter takes, panics when the package loads, so a
// statement edited without its list fails every test. The statement as
// written and its types are kept, so a test can hold the types to those
// Postgres infers.
func opaque(sql string, types ...string) string {
	used := make([]bool, len(types))
	out := parameter.ReplaceAllStringFunc(sql, func(p string) string {
		n, _ := strconv.Atoi(p[1:])
		if n < 1 || n > len(types) {
			panic(fmt.Sprintf("store: opaque: $%d has no type in %d", n, len(types)))
		}
		used[n-1] = true
		t := types[n-1]
		return "((select " + p + "::" + t + ")::" + t + ")"
	})
	for i, u := range used {
		if !u {
			panic(fmt.Sprintf("store: opaque: no parameter takes type %d (%s)", i+1, types[i]))
		}
	}
	opaqueMu.Lock()
	defer opaqueMu.Unlock()
	opaqueReads[out] = opaqueRead{SQL: sql, Types: types}
	return out
}

// opaqueRead is a statement opaque rewrote: as written, and its types.
type opaqueRead struct {
	SQL   string
	Types []string
}

var (
	parameter   = regexp.MustCompile(`\$[0-9]+`)
	opaqueMu    sync.Mutex
	opaqueReads = map[string]opaqueRead{}
)
