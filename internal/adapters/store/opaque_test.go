package store

import (
	"strings"
	"testing"
)

func TestOpaqueReadsEveryParameterThroughATypedOneRowSubquery(t *testing.T) {
	sql := `select $2 = any($1) and ($3, $4, $5, $6, $7, $8, $9) is not null limit $10 -- $1`
	got := opaque(sql, "text[]", "text", "c", "d", "e", "f", "g", "h", "i", "bigint")
	t.Cleanup(func() { forget(got) })
	want := `select ((select $2::text)::text) = any(((select $1::text[])::text[])) and (((select $3::c)::c), ((select $4::d)::d), ((select $5::e)::e), ((select $6::f)::f), ((select $7::g)::g), ((select $8::h)::h), ((select $9::i)::i)) is not null limit ((select $10::bigint)::bigint) -- ((select $1::text[])::text[])`
	if got != want {
		t.Fatalf("opaque:\n got %s\nwant %s", got, want)
	}
	if r := opaqueReads[got]; r.SQL != sql || len(r.Types) != 10 || r.Types[9] != "bigint" {
		t.Fatalf("kept %+v", r)
	}
}

func TestOpaqueRefusesAParameterWithoutAType(t *testing.T) {
	for sql, msg := range map[string]string{
		`select $2`: "$2 has no type in 1",
		`select $0`: "$0 has no type in 1",
	} {
		func() {
			defer func() {
				if r, _ := recover().(string); !strings.Contains(r, msg) {
					t.Fatalf("%s: panic %q, want %q", sql, r, msg)
				}
			}()
			forget(opaque(sql, "text"))
		}()
	}
}

func TestOpaqueRefusesATypeNoParameterTakes(t *testing.T) {
	defer func() {
		if r, _ := recover().(string); !strings.Contains(r, "no parameter takes type 2 (integer)") {
			t.Fatalf("panic %q", r)
		}
	}()
	forget(opaque(`select $1`, "text", "integer"))
}

// forget drops a statement a test rewrote, so the package's list holds only
// its own reads.
func forget(sent string) {
	opaqueMu.Lock()
	defer opaqueMu.Unlock()
	delete(opaqueReads, sent)
}
