package httpapi

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"

	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// Content codings this service answers in, best first.
const (
	codingBrotli = "br"
	codingGzip   = "gzip"
)

// compress encodes an answer body of at least minBytes in br or gzip, as the
// client's Accept-Encoding allows (Performance-and-Caching). A smaller body,
// a 204 or 304, a body that is already encoded and a type that does not
// compress are sent as they are. Every answer varies by Accept-Encoding.
func compress(minBytes, level int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")
			coding := negotiateCoding(r.Header.Get("Accept-Encoding"))
			if coding == "" || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{ResponseWriter: w, coding: coding, min: minBytes, level: level}
			defer cw.finish()
			next.ServeHTTP(cw, r)
		})
	}
}

// negotiateCoding picks br, else gzip, from an Accept-Encoding value; ""
// when the client accepts neither (RFC 9110 §12.5.3: q=0 refuses a coding,
// and * stands for every coding not named).
func negotiateCoding(accept string) string {
	q := map[string]float64{}
	for part := range strings.SplitSeq(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		weight := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			weight = f
		}
		q[strings.ToLower(strings.TrimSpace(name))] = weight
	}
	for _, c := range []string{codingBrotli, codingGzip} {
		w, named := q[c]
		if !named {
			w, named = q["*"]
		}
		if named && w > 0 {
			return c
		}
	}
	return ""
}

// compressible lists the media types worth compressing.
func compressible(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json") || strings.HasPrefix(mt, "text/")
}

// compressWriter holds the first minBytes of a body back to decide whether
// to encode it.
type compressWriter struct {
	http.ResponseWriter
	coding  string
	min     int
	level   int
	status  int
	held    []byte
	decided bool
	enc     io.WriteCloser
}

func (c *compressWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if !c.decided {
		c.held = append(c.held, p...)
		if len(c.held) < max(c.min, 1) {
			return len(p), nil
		}
		if err := c.decide(true); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if c.enc != nil {
		return c.enc.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

// decide sends the header, encoding the body when big is true and the answer
// can be encoded, then writes what was held back.
func (c *compressWriter) decide(big bool) error {
	c.decided = true
	h := c.Header()
	if big && c.status != http.StatusNoContent && c.status != http.StatusNotModified &&
		h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
		h.Set("Content-Encoding", c.coding)
		h.Del("Content-Length")
		if c.coding == codingBrotli {
			c.enc = brotli.NewWriterLevel(c.ResponseWriter, c.level)
		} else {
			// The level is bounded 1..9 by the declaration, so the writer
			// cannot refuse it.
			c.enc = invariant.Must(gzip.NewWriterLevel(c.ResponseWriter, c.level))
		}
	}
	c.ResponseWriter.WriteHeader(c.status)
	held := c.held
	c.held = nil
	if len(held) == 0 {
		return nil
	}
	if c.enc != nil {
		_, err := c.enc.Write(held)
		return err
	}
	_, err := c.ResponseWriter.Write(held)
	return err
}

// finish sends a body that stayed below the threshold as it is and closes
// the encoder.
func (c *compressWriter) finish() {
	if !c.decided {
		if c.status == 0 {
			// Nothing was written: leave the answer to the server.
			return
		}
		_ = c.decide(false)
	}
	if c.enc != nil {
		_ = c.enc.Close()
	}
}

// Unwrap lets http.ResponseController reach the connection.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
