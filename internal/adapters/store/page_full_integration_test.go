//go:build integration

package store_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// A text search reads more than its page and leaves out the stops of
// another station after; where what it read is one station's platforms
// (DELFI files them under the station's DHID with no parent named), it
// reads on, so the page is never short and tells right whether more
// answer: with a limit of one (28 rows read first), the 40 platforms of
// Zettelplatz hid Zettelplatz Nord and Zum Zettelplatz and the page told
// no more.
func TestAPageOfOneStationsPlatformsReadsOn(t *testing.T) {
	st, _ := private(t,
		`insert into public.ags_regions (ags, level, name, name_norm, parent_ags, source_id)
		 values ('09998', 'kreis', 'Rosenstadt', 'rosenstadt', '09', 'destatis_gv') on conflict do nothing`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 select v.* from (select st_setsrid(st_makepoint(11.5, 48.5), 4326) p) g,
		 lateral (values (1, 'de:09998:950', 'Zettelplatz', g.p, 1), (1, 'de:09998:951', 'Zettelplatz Nord', g.p, 1),
		                 (1, 'de:09998:952', 'Zum Zettelplatz', g.p, 1), (1, 'de:09998:950:1:1', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:2', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:3', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:4', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:5', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:6', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:7', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:8', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:9', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:10', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:11', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:12', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:13', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:14', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:15', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:16', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:17', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:18', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:19', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:20', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:21', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:22', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:23', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:24', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:25', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:26', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:27', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:28', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:29', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:30', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:31', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:32', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:33', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:34', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:35', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:36', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:37', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:38', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:39', 'Zettelplatz', g.p, 0), (1, 'de:09998:950:1:40', 'Zettelplatz', g.p, 0)) v
		 on conflict do nothing`)
	fam := []string{"delfi_static"} // the source of the timetable the stops are planted in
	at := geo.Point{Lat: 48.5, Lon: 11.5}
	ids := func(ss []transit.Station) []string {
		out := []string{}
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return out
	}
	for name, c := range map[string]struct {
		q    transit.Query
		want []string
		more bool
	}{
		"text":            {transit.Query{Text: "Zettelplatz", Limit: 1}, []string{"1:de:09998:950"}, true},
		"text, all":       {transit.Query{Text: "Zettelplatz", Limit: 3}, []string{"1:de:09998:950", "1:de:09998:951", "1:de:09998:952"}, false},
		"text in an area": {transit.Query{Text: "Zettelplatz", Area: "09998", Limit: 1}, []string{"1:de:09998:950"}, true},
		"text near":       {transit.Query{Text: "Zettelplatz", At: &at, RadiusM: 1000, Limit: 1}, []string{"1:de:09998:950"}, true},
		"text near, all":  {transit.Query{Text: "Zettelplatz", At: &at, RadiusM: 1000, Limit: 3}, []string{"1:de:09998:950", "1:de:09998:951", "1:de:09998:952"}, false},
	} {
		ss, more, err := st.Stations(bounded(t), c.q, fam)
		if err != nil || !slices.Equal(ids(ss), c.want) || more != c.more {
			t.Errorf("%s: %v (more %v), %v", name, ids(ss), more, err)
		}
	}
}
