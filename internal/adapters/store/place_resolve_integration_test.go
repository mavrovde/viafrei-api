//go:build integration

package store_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

func gunzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The resolver over the store's statements answers the synthetic world
// (testdata/resolve_world.sql.gz, the rows the domain's replay fixture was
// recorded on) as the platform did, text for text, in both languages.
func TestResolveTheWorldOverTheStatements(t *testing.T) {
	st, _ := private(t, string(gunzip(t, "testdata/resolve_world.sql.gz")))
	var w struct {
		Expect []struct {
			Q    string            `json:"q"`
			Lang string            `json:"lang"`
			R    places.Resolution `json:"r"`
		} `json:"expect"`
	}
	if err := json.Unmarshal(gunzip(t, "../../domain/places/testdata/resolve_world.json.gz"), &w); err != nil {
		t.Fatal(err)
	}
	reads := st.ResolveReads([]string{"geonames", "db_stada", "delfi_zhv", "bkg_gvisys", "osm", "bast_lcl", "autobahn"})
	norm := func(r places.Resolution) places.Resolution {
		if len(r.SourceIDs) == 0 {
			r.SourceIDs = nil
		}
		if len(r.Alternatives) == 0 {
			r.Alternatives = nil
		}
		if len(r.Options) == 0 {
			r.Options = nil
		}
		return r
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for range 8 {
		wg.Go(func() {
			for i := range next {
				e := w.Expect[i]
				// Under the test's context, so the parity replay reads the world too.
				ctx, cancel := context.WithTimeout(store.WithTest(context.Background(), t), 30*time.Second)
				got, err := places.Resolve(ctx, reads, e.Q, e.Lang)
				cancel()
				if err != nil || !reflect.DeepEqual(norm(got), norm(e.R)) {
					t.Errorf("%s %q:\n got  %+v %v\n want %+v", e.Lang, e.Q, got, err, e.R)
				}
			}
		})
	}
	for i := range w.Expect {
		next <- i
	}
	close(next)
	wg.Wait()
}
