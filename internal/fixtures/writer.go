package fixtures

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"strconv"
	"strings"
	"time"
)

// batchRows is how many rows one INSERT statement carries.
const batchRows = 500

// sqlWriter streams INSERT statements table by table, in a fixed order, and
// hashes every byte it writes so the manifest can carry a checksum.
type sqlWriter struct {
	out     *bufio.Writer
	sum     hash.Hash
	err     error
	table   string
	cols    string
	pending []string
	rows    map[string]int
	order   []string
}

func newSQLWriter(w io.Writer) *sqlWriter {
	s := &sqlWriter{sum: sha256.New(), rows: map[string]int{}}
	s.out = bufio.NewWriterSize(io.MultiWriter(w, s.sum), 1<<16)
	return s
}

func (s *sqlWriter) write(str string) {
	if s.err == nil {
		_, s.err = s.out.WriteString(str)
	}
}

func (s *sqlWriter) begin(table string, cols ...string) {
	s.end()
	s.table, s.cols = table, strings.Join(cols, ", ")
	if _, seen := s.rows[table]; !seen {
		s.order = append(s.order, table)
		s.rows[table] = 0
	}
}

func (s *sqlWriter) row(vals ...string) {
	s.pending = append(s.pending, "("+strings.Join(vals, ", ")+")")
	s.rows[s.table]++
	if len(s.pending) == batchRows {
		s.flush()
	}
}

func (s *sqlWriter) flush() {
	if len(s.pending) == 0 {
		return
	}
	s.write("insert into contract." + s.table + " (" + s.cols + ") values\n  ")
	s.write(strings.Join(s.pending, ",\n  "))
	s.write(";\n")
	s.pending = s.pending[:0]
}

func (s *sqlWriter) end() { s.flush() }

func (s *sqlWriter) close() (string, error) {
	s.end()
	if s.err == nil {
		s.err = s.out.Flush()
	}
	return fmt.Sprintf("sha256:%x", s.sum.Sum(nil)), s.err
}

// SQL literals. Every value is rendered here, so quoting has one home.

func str(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

func optStr(v string) string {
	if v == "" {
		return "null"
	}
	return str(v)
}

const null = "null"

func num(v int) string { return strconv.Itoa(v) }

func dec(v float64, places int) string { return strconv.FormatFloat(v, 'f', places, 64) }

func boolean(v bool) string { return strconv.FormatBool(v) }

func ts(t time.Time) string { return str(t.UTC().Format(time.RFC3339)) }

func optTS(t time.Time) string {
	if t.IsZero() {
		return null
	}
	return ts(t)
}

func date(t time.Time) string { return str(t.UTC().Format(time.DateOnly)) }

func coord(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func point(p lonLat) string {
	return "'SRID=4326;POINT(" + coord(p.lon) + " " + coord(p.lat) + ")'"
}

func box(b bbox) string {
	return fmt.Sprintf("'SRID=4326;MULTIPOLYGON(((%s %s,%s %s,%s %s,%s %s,%s %s)))'",
		coord(b.w), coord(b.s), coord(b.e), coord(b.s), coord(b.e), coord(b.n), coord(b.w), coord(b.n), coord(b.w), coord(b.s))
}

func line(pts []lonLat) string {
	parts := make([]string, len(pts))
	for i, p := range pts {
		parts[i] = coord(p.lon) + " " + coord(p.lat)
	}
	return "'SRID=4326;MULTILINESTRING((" + strings.Join(parts, ",") + "))'"
}

// lines writes a line of several parts, in their order.
func lines(parts ...[]lonLat) string {
	out := make([]string, len(parts))
	for i, pts := range parts {
		coords := make([]string, len(pts))
		for j, p := range pts {
			coords[j] = coord(p.lon) + " " + coord(p.lat)
		}
		out[i] = "(" + strings.Join(coords, ",") + ")"
	}
	return "'SRID=4326;MULTILINESTRING(" + strings.Join(out, ",") + ")'"
}

func array(vals ...string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = `"` + v + `"`
	}
	return str("{" + strings.Join(quoted, ",") + "}")
}
