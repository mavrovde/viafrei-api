package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// stmt is a named statement of the store: a stable name (api_<what>), its
// text, and the type of each of its parameters, which the text states at
// every parameter's use ($n::type), so its prepared parameters never take a
// type from a caller's value and its plan is never made again for one.
type stmt struct {
	Name, SQL string
	Types     []string
}

var (
	namedMu sync.Mutex
	// namedStmts is every named statement, by name: a name is taken once.
	namedStmts = map[string]stmt{}
	// paramUse is a parameter and the cast at its use.
	paramUse = regexp.MustCompile(`\$([0-9]+)(::[a-z][a-z0-9_]*(?: precision| with time zone| without time zone)?(?:\[\])?)?`)
)

// named registers the statement name with sql, whose parameters $1..$n take
// types, in order. A name taken twice, a parameter without a type, a type no
// parameter takes, or a use of a parameter without its type's cast panics
// when the package loads, so a statement edited without its list fails every
// test.
func named(name, sql string, types ...string) stmt {
	used := make([]bool, len(types))
	for _, m := range paramUse.FindAllStringSubmatch(sql, -1) {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > len(types) {
			panic(fmt.Sprintf("store: %s: $%d has no type in %d", name, n, len(types)))
		}
		if m[2] != "::"+types[n-1] {
			panic(fmt.Sprintf("store: %s: $%d is used without its cast ::%s", name, n, types[n-1]))
		}
		used[n-1] = true
	}
	for i, ty := range types {
		if !used[i] {
			panic(fmt.Sprintf("store: %s: no parameter takes type %d (%s)", name, i+1, ty))
		}
	}
	namedMu.Lock()
	defer namedMu.Unlock()
	if _, dup := namedStmts[name]; dup {
		panic("store: the statement name " + name + " is taken")
	}
	s := stmt{Name: name, SQL: sql, Types: types}
	namedStmts[name] = s
	return s
}

// namedList is every named statement, ordered by name.
func namedList() []stmt {
	namedMu.Lock()
	defer namedMu.Unlock()
	out := make([]stmt, 0, len(namedStmts))
	for _, s := range namedStmts {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// run prepares s on tx's connection under its name, once per connection
// (pgx keeps it), and runs it with args: the server keeps one prepared
// statement per name and connection, whose plan it caches.
// A statement that cannot be prepared answers rows that carry the error, as
// a query that fails does, so a caller reads one error either way.
func run(ctx context.Context, tx pgx.Tx, s stmt, args ...any) (pgx.Rows, error) {
	if beside != nil {
		beside(ctx, tx, s, args)
	}
	if _, err := tx.Conn().Prepare(ctx, s.Name, s.SQL); err != nil {
		return failedRows{err: err}, err
	}
	return tx.Query(ctx, s.Name, args...)
}

// failedRows are the rows of a statement that could not be prepared.
type failedRows struct{ err error }

func (failedRows) Close()                                       {}
func (r failedRows) Err() error                                 { return r.err }
func (failedRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (failedRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (failedRows) Next() bool                                   { return false }
func (r failedRows) Scan(...any) error                          { return r.err }
func (r failedRows) Values() ([]any, error)                     { return nil, r.err }
func (failedRows) RawValues() [][]byte                          { return nil }
func (failedRows) Conn() *pgx.Conn                              { return nil }
func (failedRows) TypeMap() *pgtype.Map                         { return nil }

// beside, when set (by the integration tests), runs before each named
// statement in its transaction: the parity check of the statement against
// the view statement it replaced, with the same arguments.
var beside func(ctx context.Context, tx pgx.Tx, s stmt, args []any)

// namedOpaque is named over opaque(sql, types...): every parameter is read
// through a subquery, so a custom plan sees no more than the generic one and
// the plan cache settles on the generic plan after its custom ones.
func namedOpaque(name, sql string, types ...string) stmt {
	return named(name, opaque(sql, types...), types...)
}

// runRow is run for a statement that answers one row.
func runRow(ctx context.Context, tx pgx.Tx, s stmt, args ...any) pgx.Row {
	rows, _ := run(ctx, tx, s, args...)
	return oneRow{rows}
}

// oneRow is the first row of rows, as pgx.Row: no row is pgx.ErrNoRows.
type oneRow struct{ rows pgx.Rows }

func (r oneRow) Scan(dest ...any) error {
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	r.rows.Close()
	return r.rows.Err()
}
