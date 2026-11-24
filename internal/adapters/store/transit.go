package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport repositories (anti-corruption layer).

// stationByID reads one station: $1 the id, $2 the families. A station
// stored with no position is placed at 0,0 nowhere: the board names it. It
// reads the station's family too, the stops the platform's departure board
// reads (stationFamily).
var stationByID = namedOpaque("api_station_by_id", `
select s.stop_place_id, s.name, coalesce(st_y(s.location), 0), coalesce(st_x(s.location), 0), s.national_stop_code,
       s.region_code, coalesce(s.modes, '{}'), s.source_ref,
       array(select f.id from (`+stationFamily("s.stop_place_id")+`) f(id) where f.id <> s.stop_place_id order by f.id)
from (`+relStopPlace+`) s
where s.stop_place_id = $1 and s.withdrawn_at is null and s.source_ref = any($2) and `+permitted("s"), "text", "text[]")

// stationFamily is the family of the station of the id id (an SQL
// expression), as the platform's board
// reads it: the station a stop belongs to is the first three parts of its
// DHID (country, district, station), of its own id or else its parent's,
// else its parent, else itself, so the platforms DELFI files under a
// station's DHID with no parent named (most of them: München Marienplatz's
// platforms, Stuttgart's Stadtbahn platforms) are the station's, and a
// parent of a suffixed id (de:09162:2_G) is the station de:09162:2. Its
// members are the station, the stops naming it as their parent and those
// whose id continues it after a colon or an underscore, each in an arm of
// its own, so each is a range of one index. The stops of the station's feed,
// by id, once each.
func stationFamily(id string) string {
	return `
select (k.feed || ':') || m.id from (
  select r.feed, coalesce(substring(r.own from '` + dhidStation + `'),
                          (select coalesce(substring(x.id from '` + dhidStation + `'), substring(x.parent_id from '` + dhidStation + `'), x.parent_id, x.id)
                             from stops x where x.gtfs_feed_key = r.feed and x.id = r.own and x.deleted_at is null), r.own) root
  from (select case when ` + id + ` ~ '^[0-9]+:' then split_part(` + id + `, ':', 1)::smallint end feed,
               substr(` + id + `, strpos(` + id + `, ':') + 1) own) r) k
cross join lateral (
  select m.id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id = k.root
  union
  select m.id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.parent_id = k.root
  union
  select m.id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id ~>=~ (k.root || ':') and m.id ~<~ (k.root || ';')
  union
  select m.id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id ~>=~ (k.root || '_') and m.id ~<~ (k.root || '` + "`" + `')) m
order by m.id`
}

// dhidStation is the station part of a DHID: country, district, station;
// it stops at an underscore, so de:09162:2_G is de:09162:2.
const dhidStation = `^([a-z]{2}:[0-9]{5,8}:[^:_]+)`

// Station reads the station with this id (features.Transit).
func (s *Store) Station(ctx context.Context, id string, families []string) (transit.Station, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Station, error) {
		var (
			v      transit.Station
			modes  []string
			points []string
		)
		err := runRow(ctx, tx, stationByID, id, families).Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon,
			&v.NationalCode, &v.RegionCode, &modes, &v.Family, &points)
		v.StopIDs = transit.FamilyOf(v.ID, points)
		if errors.Is(err, pgx.ErrNoRows) {
			// No such station is an answer, not a failure of storage.
			return transit.Station{}, nil
		}
		v.Modes = transit.MapModes(modes)
		return v, err
	})
	if err == nil && out.ID == "" {
		err = transit.ErrNotFound
	}
	return out, err
}

// stationsPage reads the stations of the given families that answer a
// search: $1 families; $2, $3 the position's latitude and longitude, both
// null for none; $4 the folded text, empty for none; $5 the radius; $6 the
// limit; $7 a station code, empty for none; $8 railway stations only; $9 an
// official region key the district code starts with, empty for any (a
// station with no district then left out); $10 the main-station spellings of
// the text (stationStarts), empty for none. A station stored with no
// position is left out: an answer places every station it lists. The folded
// text holds letters and digits only, so it carries no LIKE wildcard.
//
// Each search shape (which of position, text, code, railway and region it
// asks) is a statement of its own, holding only its own filters, so each
// settles on a generic plan that fits it: one statement with every filter
// optional ran past 15 s on prod under its generic plan, where the asked
// plan took 7 ms. Every shape takes all ten parameters (the unused ones in
// a CTE the planner drops), so a caller sends the same arguments to each.
var stationsPage = func() map[stationShape]stmt {
	out := map[stationShape]stmt{}
	for m := range stationShape(1 << 5) {
		where := ""
		staged := m&shapeText != 0 && m&shapeNear == 0
		if m&shapeText != 0 && !staged {
			where += "\n  and " + stationHolds
		}
		if m&shapeNear != 0 {
			where += "\n  and st_dwithin(s.location::geography, here.g, $5::double precision)"
		}
		if m&shapeCode != 0 {
			where += "\n  and (s.eva_no = $7::text or s.national_stop_code = $7::text)" + stationsOfCode
		}
		if m&shapeRailway != 0 {
			where += "\n  and s.eva_no is not null"
		}
		if m&shapeArea != 0 && m&shapeText == 0 {
			where += "\n  and s.region_code like $9::text || '%'"
		}
		if m&shapeArea != 0 && m&shapeText != 0 {
			where += "\n  and s.area_key like $9::text || '%' and s.region_code is not null"
		}
		pick := func(match string) string {
			return `
select s.stop_place_id, s.name, st_y(s.location) lat, st_x(s.location) lon, s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}') modes, s.source_ref,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist,
       s.placement_source_ref, s.eva_no, s.eva_numbers
from (` + relStopPlace + `) s, here
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1::text[])
  and ` + permitted("s") + match + where + `
order by dist, s.name, s.stop_place_id`
		}
		body := pick("") + "\nlimit $6::bigint"
		if m&shapeText != 0 {
			// A text search reads its stations by key first (keyed), and
			// the rest of each station only for the page's own rows.
			keys := func(match string) string {
				return `
select s.stop_place_id, s.name,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist
from (` + stationKeys(m&(shapeCode|shapeRailway) != 0) + `) s, here
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1::text[])
  and ` + permitted("s") + match + where + `
order by dist, s.name, s.stop_place_id`
			}
			body = `,` + keyed("page", keys(""), "$6") + `
` + keyedPage("page", "k.dist, k.name, k.stop_place_id", "$6")
			if staged {
				// A text with no position answers the names that start with
				// it before those holding it as a part, so the part matches
				// can be among the answers only while the starting ones
				// leave the page short, and only then are they read:
				// "Berlin" is held by about 21 000 stop places and starts
				// about 1 000, and reading and sorting all of them took 0.6
				// to 1.1 s a search on prod (2026-11-24). Each part is
				// collapsed on its own, before the two are put together.
				// The holding ones are read only where every starting one
				// of a station of its own is in first, so they leave out
				// first's rows and not every starting name: the name's fold
				// is then worked out once a stop, not three times (a stop
				// that held "strasse" cost 34 µs a fold on prod, 79 601
				// held it).
				body = `,` + keyed("first", keys("\n  and "+stationStarts), "$6") + `,` +
					keyed("rest", keys("\n  and (select count(*) from first) < $6::bigint\n  and "+stationHolds+
						"\n  and not exists (select 1 from first f where f.stop_place_id = s.stop_place_id)"), "$6") + `,
arms as (select 0 o, f.stop_place_id, f.name, f.dist from first f union all select 1 o, r.stop_place_id, r.name, r.dist from rest r)
` + keyedPage("arms", "k.o, k.dist, k.name, k.stop_place_id", "$6")
			}
		}
		out[m] = namedOpaque(fmt.Sprintf("api_stations_page_%02d", m), `
with args as (select $1::text[], $2::double precision, $3::double precision, $4::text, $5::double precision, $6::bigint, $7::text, $8::boolean, $9::text, $10::text[]),
here as (
  select case when $2::double precision is null then null
              else st_setsrid(st_makepoint($3::double precision, $2::double precision), 4326)::geography end g)`+body,
			"text[]", "double precision", "double precision", "text", "double precision", "bigint", "text", "boolean", "text", "text[]")
	}
	return out
}()

// spellingsOf are the main-station spellings of a text, none as an empty
// list (a statement parameter is never null).
func spellingsOf(text string) []string {
	if sp := transit.MainStationSpellings(text); sp != nil {
		return sp
	}
	return []string{}
}

// stationStarts and stationHolds are a stop place s whose name starts with
// or holds the folded text $4, or one of the main-station spellings $10 the
// text gives (transit.MainStationSpellings): "Berlin Hbf" finds "S+U Berlin
// Hauptbahnhof", the timetable's name of the station, as the platform's
// station search does (#526).
const (
	stationStarts = `(s.search_fold like $4::text || '%' or s.search_fold like any(array(select x || '%' from unnest($10::text[]) x)))`
	stationHolds  = `(s.search_fold like '%' || $4::text || '%' or s.search_fold like any(array(select '%' || x || '%' from unnest($10::text[]) x)))`
)

// keyed is the CTE name: the keys (stop_place_id, name, dist) the ordered
// select sel reads, without the stops of another station, in sel's order,
// limit at most: DELFI files most platforms under the station's DHID with
// no parent named, so each read as a station of its own ("Marienplatz"
// listed each platform of München's and Stuttgart's Marienplatz), where the
// platform's name search answers the station once. A stop is another
// station's when the stop its DHID names is a station (stationFamily), or,
// where that DHID is no stop of its own (de:09161:505, only its platforms
// :0:1 and :0:2 are stops), when a platform under the DHID with a lower id
// stands for it.
//
// That lowest-id platform stands for the station even where it fails the
// search's own filters (another family, outside the area): the family it
// names is the station's either way.
//
// The stops are left out before the limit, so the page is never short, and
// on the keys alone (stationKeys): a station's modes, DB record and
// placement are read for the page's rows only (keyedPage). Reading them for
// every stop that held "strasse" (71 924 on prod) took 8 s, and leaving the
// stops out after a page was read left it short where the page was one
// station's platforms ("hauptbahnhof" at 61 lost six stations, 2026-11-24).
func keyed(name, sel, limit string) string {
	return `
` + name + ` as (select k.stop_place_id, k.name, k.dist from (` + sel + `
offset 0) k
where ` + ownStation("k.stop_place_id") + `
order by k.dist, k.name, k.stop_place_id
limit ` + limit + `::bigint)`
}

// keyedPage answers the stations of the keys of the CTE name, in order,
// limit at most, each read by its key.
func keyedPage(name, order, limit string) string {
	return `select p.stop_place_id, p.name, st_y(p.location) lat, st_x(p.location) lon, p.national_stop_code, p.region_code,
       coalesce(p.modes, '{}') modes, p.source_ref, k.dist, p.placement_source_ref, p.eva_no, p.eva_numbers
from ` + name + ` k
cross join lateral (select s.stop_place_id, s.name, s.location, s.national_stop_code, s.region_code, s.modes, s.source_ref,
                           s.placement_source_ref, s.eva_no, s.eva_numbers
                      from (` + relStopPlace + `) s
                     where s.stop_place_id = k.stop_place_id
                     limit 1) p
order by ` + order + `
limit ` + limit + `::bigint`
}

// stationKeys are the stop places of relStopPlace with only the columns a
// search filters and orders on, read from the stops and their feeds alone
// (the DB record too where the search asks a code or a railway station):
// the same rows, keyed by stop_place_id. Its area_key is the district key
// the region_code is read under, where there is one: a search tests it
// before it looks the region up ("strasse" in 11000 looked up 79 601).
func stationKeys(withDB bool) string {
	eva, db := "null::text", ""
	if withDB {
		eva = "d.eva_no"
		db = `
     LEFT JOIN LATERAL ( SELECT x.eva_no
           FROM db_stations x
          WHERE x.ifopt = s.id AND x.closed_at IS NULL
          ORDER BY (x.source_id = 'db_stada'::text) DESC, x.station_no
         LIMIT 1) d ON true`
	}
	return `
 SELECT (s.gtfs_feed_key || ':'::text) || s.id AS stop_place_id,
    s.name,
    s.geom::geometry(Point,4326) AS location,
        CASE
            WHEN s.id ~ '^[a-z]{2}:[0-9]{5}:'::text THEN (((split_part(s.id, ':'::text, 1) || ':'::text) || split_part(s.id, ':'::text, 2)) || ':'::text) || split_part(s.id, ':'::text, 3)
            ELSE NULL::text
        END AS national_stop_code,
    ( SELECT a.ags
           FROM ags_regions a
          WHERE a.ags = split_part(s.id, ':'::text, 2)) AS region_code,
    split_part(s.id, ':'::text, 2) AS area_key,
    contract_search_fold(s.name) AS search_fold,
    f.source_feed AS source_ref,
    NULL::timestamp with time zone AS withdrawn_at,
    'open'::text AS display_class,
    'public'::text AS audience,
    ` + eva + ` AS eva_no
   FROM stops s
     JOIN feeds f ON f.gtfs_feed_key = s.gtfs_feed_key` + db + `
  WHERE s.deleted_at IS NULL AND s.geom IS NOT NULL AND s.name IS NOT NULL AND (s.location_type = 1 OR COALESCE(s.location_type::integer, 0) = 0 AND s.parent_id IS NULL)`
}

// ownStation is whether the stop place id names a station of its own, no
// stop of another station (collapsed), by the stops' key and id range.
func ownStation(id string) string {
	feed := "(case when " + id + " ~ '^[0-9]+:' then split_part(" + id + ", ':', 1)::smallint end)"
	own := "substr(" + id + ", strpos(" + id + ", ':') + 1)"
	root := "substring(" + own + " from '" + dhidStation + "')"
	return `not exists (
    select 1 from stops r
     where r.gtfs_feed_key = ` + feed + ` and r.id = ` + root + ` and r.id <> ` + own + `
       and r.deleted_at is null and r.geom is not null and r.name is not null
       and (r.location_type = 1 or coalesce(r.location_type::integer, 0) = 0 and r.parent_id is null)
    union all
    select 1 from stops r
     where r.gtfs_feed_key = ` + feed + ` and r.id ~>=~ (` + root + ` || ':') and r.id ~<~ (` + root + ` || ';') and r.id ~<~ ` + own + `
       and r.deleted_at is null and r.geom is not null and r.name is not null
       and coalesce(r.location_type::integer, 0) = 0 and r.parent_id is null)`
}

// stationsOfCode narrows a code search to the stops that can carry the
// code $7 before the station relation is built: the stops whose id starts
// with it (one range of the feed's id index: the stop of that id, and those
// below it, whose national stop code is the id's first three parts), and
// the stop a DB station of that EVA number names. The narrowing may hold
// more; the filter above still decides.
const stationsOfCode = `
  and s.stop_place_id in (
    select (f2.gtfs_feed_key || ':') || s2.id from feeds f2 join stops s2 on s2.gtfs_feed_key = f2.gtfs_feed_key
     where s2.deleted_at is null and s2.id ~>=~ $7::text and s2.id ~<~ ($7::text || ';')
    union all
    select (f3.gtfs_feed_key || ':') || s3.id from db_stations x join feeds f3 on true
      join stops s3 on s3.gtfs_feed_key = f3.gtfs_feed_key and s3.id = x.ifopt
     where x.eva_no = $7::text and x.closed_at is null and s3.deleted_at is null)`

// stationShape is which filters a station search asks.
type stationShape uint8

const (
	shapeNear stationShape = 1 << iota
	shapeText
	shapeCode
	shapeRailway
	shapeArea
)

// shapeOf is the shape of the station search q, whose folded text is text.
func shapeOf(q transit.Query, text string) stationShape {
	var m stationShape
	for bit, on := range map[stationShape]bool{shapeNear: q.At != nil, shapeText: text != "", shapeCode: q.Code != "", shapeRailway: q.Railway, shapeArea: q.Area != ""} {
		if on {
			m |= bit
		}
	}
	return m
}

// Stations reads the stations that answer q (features.Transit), one more
// than the limit, to tell whether more answer.
func (s *Store) Stations(ctx context.Context, q transit.Query, families []string) ([]transit.Station, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Station, error) {
		text := transit.Fold(q.Text)
		rows, _ := run(ctx, tx, stationsPage[shapeOf(q, text)], families, lat, lon, text, q.RadiusM, q.Limit+1, q.Code, q.Railway, q.Area, spellingsOf(q.Text))
		return pgx.CollectRows(rows, stationRow)
	})
	if err == nil && len(out) == 0 && q.Code == "" && q.Area == "" {
		out, err = s.stationsInTown(ctx, q, families)
		if q.Railway {
			// The town and stop read lists every stop: a railway search
			// keeps the DB stations among them.
			out = slices.DeleteFunc(out, func(st transit.Station) bool { return st.EVA == nil })
		}
	}
	if err != nil {
		return nil, false, err
	}
	if len(out) > q.Limit {
		return out[:q.Limit], true, nil
	}
	return out, false, nil
}

// stationTownKreise are the districts (Kreise and kreisfreie Städte) a
// station text's town names, as the platform's TOWN_KREISE_SQL reads them,
// in one statement: a district by its name, or a Gemeinde by its name, in
// the district its key starts with, for every run $1 of the text
// (transit.TownRuns); and, flagged not exact, a Gemeinde whose name starts
// with a prefix $2 and a space (transit.TownPrefixes): "Frankfurt" for
// "Frankfurt am Main". Exact rows first; sixty at most.
var stationTownKreise = namedOpaque("api_station_town_kreise", `
select distinct k.ags, k.run, k.exact from (
  select a.ags, r.run, true exact
    from unnest($1::text[]) r(run)
    join ags_regions a on a.level = 'kreis' and (a.name_norm = r.run or a.name_en_norm = r.run)
  union all
  select left(g.admin_code, 5), g.name_norm, true from gazetteer_places g
   where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$'
     and g.name_norm = any($1::text[])
  union all
  select left(g.admin_code, 5), p.w, false
    from unnest($2::text[]) p(w)
    join gazetteer_places g on left(g.name_norm, length(p.w) + 1) = p.w || ' '
   where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$') k
order by k.exact desc, k.ags, k.run
limit 60`, "text[]", "text[]")

// stationsInTowns are the stations of the districts $4 whose name holds the
// stop name $2 or a spelling $3 of it, by name then id, the other stations'
// stops left out (collapsed), $5 at most: one statement over every district
// the town named.
var stationsInTowns = namedOpaque("api_stations_in_towns", `
with`+keyed("page", `
select s.stop_place_id, s.name, null::integer dist
from (`+stationKeys(false)+`) s
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1::text[])
  and `+permitted("s")+`
  and left(s.area_key, 5) = any($4::text[]) and s.region_code is not null
  and `+strings.NewReplacer("$4", "$2", "$10", "$3").Replace(stationHolds)+`
order by s.name, s.stop_place_id`, "$5")+`
`+keyedPage("page", "k.name, k.stop_place_id", "$5"), "text[]", "text", "text[]", "text[]", "bigint")

// stationsInTown reads a text no station's name holds as a town and a stop
// in it, as the platform's stop resolver reads "town, stop": the districts
// its town names (stationTownKreise: the exact names where there are any,
// else the Gemeinden its prefix starts), and the stations of those districts
// whose name holds the rest of the text (transit.TownRest). Two statements,
// whatever the text.
func (s *Store) stationsInTown(ctx context.Context, q transit.Query, families []string) ([]transit.Station, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Station, error) {
		rows, _ := run(ctx, tx, stationTownKreise, transit.TownRuns(q.Text), transit.TownPrefixes(q.Text))
		towns, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (townRow, error) {
			var v townRow
			return v, r.Scan(&v.ags, &v.run, &v.exact)
		})
		if err != nil || len(towns) == 0 {
			return nil, err
		}
		var kreise, runs []string
		for _, tw := range towns {
			if tw.exact == towns[0].exact {
				if !slices.Contains(kreise, tw.ags) {
					kreise = append(kreise, tw.ags)
				}
				runs = append(runs, tw.run)
			}
		}
		rest := transit.TownRest(q.Text, runs)
		if rest == "" {
			return nil, nil
		}
		rows, _ = run(ctx, tx, stationsInTowns, families, transit.Fold(rest), townSpellings(rest), kreise, q.Limit+1)
		return pgx.CollectRows(rows, inTown)
	})
}

// townRow is one district a town names.
type townRow struct {
	ags, run string
	exact    bool
}

// townSpellings are the spellings a town reading's stop name is searched
// under: the main station's where it names only the main station
// ("Stuttgart Hbf" finds the town's "Hauptbahnhof").
func townSpellings(rest string) []string {
	if sp := transit.StopSpellings(rest); sp != nil {
		return sp
	}
	return spellingsOf(rest)
}

// inTown is one station a town and a stop name found.
func inTown(r pgx.CollectableRow) (transit.Station, error) {
	v, err := stationRow(r)
	v.InTown = true
	return v, err
}

// stationRow is one station of a station search.
func stationRow(r pgx.CollectableRow) (transit.Station, error) {
	var (
		v     transit.Station
		modes []string
	)
	err := r.Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon, &v.NationalCode, &v.RegionCode,
		&modes, &v.Family, &v.DistanceM, &v.PlacedBy, &v.EVA, &v.EVANumbers)
	v.Modes = transit.MapModes(modes)
	return v, err
}

// boardCalls reads the departing calls of a station in a window: $1 the
// station (a stop place), whose family (stationFamily, the stops
// Station.StopIDs states) it reads, as the platform's board reads the calls
// of each of the station's stops; $2 and $3 the window's first and end instant
// (planned departure), $4 the families: every family a call carries (its
// timetable's and each live figure's, source_refs) is one of them, so the
// licence gate's post-check never meets one it refuses. A key whose plan
// does not hold the Trip Updates' family loses the calls carrying a Trip
// Updates figure from its board, not just their delay: chosen on purpose,
// since a call row stamped with that family is a row the key may not read,
// and dropping the figure alone would state a scheduled time as the
// answer where a live one exists. A call is one a passenger can board
// (the platform's boarding flag) of a trip that calls at a stop outside the
// family after it: a trip's last call at the station is an arrival. The
// destination is the trip's headsign, else the name of its last stop. The
// delay is read in seconds and stated in minutes by transit.DelayMinOf.
//
//nolint:misspell // materialized is the SQL keyword
var boardCalls = namedOpaque("api_board_calls", `
with fam as materialized (select array(`+stationFamily("$1::text")+`) ids)
select d.journey_id, d.stop_point_id, d.call_order, d.line_name, d.transport_mode, d.route_type,
       d.train_number, d.train_category, nullif(btrim(d.board_headsign), ''),
       coalesce(nullif(btrim(d.board_headsign), ''),
                (select nullif(btrim(ls.name), '') from stop_times lc
                   join stops ls on ls.gtfs_feed_key = lc.gtfs_feed_key and ls.id = lc.stop_id
                  where lc.gtfs_feed_key = d.board_feed and lc.trip_id = d.board_trip and lc.deleted_at is null
                    and ls.deleted_at is null and coalesce(ls.location_type::integer, 0) = 0
                  order by lc.stop_sequence desc limit 1)),
       d.planned_departure_at, d.platform, d.planned_platform, d.platform_changed, d.realtime, d.realtime_source,
       d.cancelled, d.delay_s, d.expected_departure_at, d.no_forecast, d.source_ref, d.source_refs
from (`+departuresOfTrips+`) d
where d.board_stop_feed = any(array(select split_part(x, ':', 1)::smallint from unnest((select f.ids from fam f)) x))
  and d.board_stop = any(array(select substr(x, strpos(x, ':') + 1) from unnest((select f.ids from fam f)) x))
  and d.stop_point_id = any((select f.ids from fam f)::text[])
  and d.planned_departure_at >= $2::timestamp with time zone and d.planned_departure_at < $3::timestamp with time zone
  and d.boarding
  and d.source_ref = any($4::text[])
  and d.source_refs <@ $4::text[]
  and `+permitted("d")+`
  and exists (select 1 from stop_times nx
                join stops np on np.gtfs_feed_key = nx.gtfs_feed_key and np.id = nx.stop_id
               where nx.gtfs_feed_key = d.board_feed and nx.trip_id = d.board_trip and nx.stop_sequence > d.call_order
                 and nx.deleted_at is null and np.deleted_at is null and coalesce(np.location_type::integer, 0) = 0
                 and (np.gtfs_feed_key || ':') || np.id <> all((select f.ids from fam f)::text[]))
order by d.planned_departure_at, d.journey_id, d.call_order`, "text", "timestamp with time zone", "timestamp with time zone", "text[]")

// departuresOfTrips is the platform's departure relation with the trip's
// feed, id and headsign beside it (board_feed, board_trip,
// board_headsign), so the board reads the trip's other calls on the stop
// times' trip key, not on a journey id no index holds: on prod the view
// statement hashed every scheduled call of the network for each board and
// ran past 60 s.
var departuresOfTrips = "\n SELECT t.gtfs_feed_key AS board_feed, t.id AS board_trip, t.headsign AS board_headsign, s.gtfs_feed_key AS board_stop_feed, s.id AS board_stop," +
	strings.TrimPrefix(relDeparture, "\n SELECT")

// Departures reads the departing calls of q's station in q's window
// (features.Transit); the board's rules are transit.Board's.
func (s *Store) Departures(ctx context.Context, q transit.DepartureQuery, families []string) ([]transit.Departure, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Departure, error) {
		rows, _ := run(ctx, tx, boardCalls, q.StationID, q.From, q.From.Add(q.Window), families)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Departure, error) {
			var (
				v      transit.Departure
				mode   string
				delayS *int
			)
			err := r.Scan(&v.JourneyID, &v.StopPointID, &v.CallOrder, &v.Line, &mode, &v.RouteType, &v.TrainNumber,
				&v.Category, &v.Headsign, &v.Destination, &v.PlannedAt, &v.Platform, &v.PlannedPlatform,
				&v.PlatformChanged, &v.Live, &v.Source, &v.Cancelled, &delayS, &v.ExpectedAt, &v.NoForecast,
				&v.Family, &v.Families)
			v.DelayMin = transit.DelayMinOf(delayS)
			v.Mode = transit.MapMode(mode)
			return v, err
		})
	})
}

// stationFacilities reads the facilities of a station: $1 the station, $2
// the families, $3 now. A state last observed (last restated by its feed)
// longer ago than its family's stale_after_s, or never observed, is
// unknown: a state is current while its feed restates it, whenever it
// began. Judged on when the state began, every lift in service for more
// than three of its feed's cadences read as unknown (Hamburg Hbf: 49 of 49,
// prod 2026-11-24, where the platform states 26 with 1 out of service).
var stationFacilities = namedOpaque("api_station_facilities", `
select f.facility_id, f.facility_kind, f.position_text,
       case when f.status_observed_at is null then 'unknown'
            when s.stale_after_s is not null and f.status_observed_at < $3::timestamptz - make_interval(secs => s.stale_after_s) then 'unknown'
            else f.facility_status end,
       f.status_observed_at, f.source_ref
from (`+stationFacilityOf+`) f
join (`+relSource+`) s on s.source_ref = f.source_ref
where f.source_ref = any($2) and `+permitted("f")+`
order by f.facility_id`, "text", "text[]", "timestamp with time zone")

// stationFacilityOf is the station facility relation of the station $1
// only: of the DHIDs of its stop family and of the stops the family's
// stops name as their parent, as DB files a station under the DHID its
// platforms name (S+U Berlin Hauptbahnhof is de:11000:900003200 in the
// timetable, its platforms' parent de:11000:900003201 at DB). A facility's stop place is contract_stop_place_of its ref, the
// timetable's station whose id is the ref's DHID or starts with it and a
// colon: so the DHID of that station's id is the ref's DHID, and only refs
// of that DHID are mapped (on prod mapping every ref took 850 ms a station).
//
// A unit both DB's FaSta list and OpenStation list (one DB equipment
// number) is FaSta's alone when the key may read FaSta's family, as the
// platform's station facilities check joins the two lists: FaSta's row wins
// on every field, OpenStation adds the units FaSta does not list. Listed
// twice, each unit of Hamburg Hbf read as two (prod 2026-11-24).
var stationFacilityOf = `
with fam as (select substr(f.id, 3) id from (` + stationFamily("$1::text") + `) f(id) where left($1::text, 2) = '1:'),
k as (select distinct substring(y.id, '` + dhidOf + `') d from (
        select f.id from fam f
        union all
        select p.parent_id from stops p join fam f on p.gtfs_feed_key = 1 and p.id = f.id where p.parent_id is not null) y),
n as (select d.station_no from db_stations d where substring(d.ifopt, '` + dhidOf + `') in (select k.d from k))
select 'equipment:' || e.equipment_id facility_id, contract_stop_place_of(e.stop_place_id) stop_place_id, e.kind facility_kind,
       e.description position_text, coalesce(st.state, 'unknown') facility_status,
       coalesce(st.last_seen_at, e.last_seen_at) status_observed_at, coalesce(st.source_feed, e.source_feed) source_ref,
       'open' display_class, 'public' audience
from station_equipment e
left join station_equipment_state st on st.equipment_id = e.equipment_id
where e.closed_at is null and substring(e.stop_place_id, '` + dhidOf + `') in (select k.d from k)
  and not exists (select 1 from db_facilities x where x.equipment_no = e.equipment_no and x.station_no in (select n.station_no from n)
                   and x.source_feed = any($2))
union all
select 'fasta:' || f.equipment_no, (select contract_stop_place_of(d.ifopt) from db_stations d where d.station_no = f.station_no),
       f.kind, f.description, f.state, f.last_seen_at, f.source_feed, 'open', 'public'
from db_facilities f
where f.station_no in (select n.station_no from n)`

// dhidOf is the DHID a ref names, as contract_stop_place_of reads it.
const dhidOf = `^(?:dhid:)?([a-z]{2}:[0-9]{5}:[0-9]+)`

// Facilities reads the facilities of a station (features.Transit).
func (s *Store) Facilities(ctx context.Context, station string, families []string, now time.Time) ([]transit.Facility, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Facility, error) {
		rows, _ := run(ctx, tx, stationFacilities, station, families, now)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Facility, error) {
			var v transit.Facility
			err := r.Scan(&v.ID, &v.Kind, &v.Position, &v.State, &v.ObservedAt, &v.Family)
			return v, err
		})
	})
}

// The affected-stations board reads each licence boundary in a statement of
// its own and puts the answers side by side, as the platform's board does:
// the departures of the next hour cancelled or late, the service alerts in
// force and the lifts out of service name the stations (by stop place id or
// IFOPT); then the stations themselves.

// affectedAlerts are the refs the alerts in force at $2 of the families ($1)
// name, with the alert and its family; only of a live publication, as the
// platform's station card asks only the alert sources alive (alertLive).
var affectedAlerts = namedOpaque("api_affected_alerts", `
select distinct a.alert_id, a.source_ref, u.sid
from (`+relServiceAlert+`) a, unnest(a.stop_ids) u(sid)
where a.source_ref = any($1) and `+"a.display_class <> 'withhold' and (a.audience = 'public' or "+ownKeyOn+")"+`
  and (a.active_from is null or a.active_from <= $2) and (a.active_to is null or a.active_to > $2)
  and `+alertLive("a", "$2"), "text[]", "timestamp with time zone")

// affectedLifts are the stations' lifts and escalators out of service, of
// the families ($1). The out-of-service rows are kept first, in a subquery
// the filter on the stop place id is not pushed into (offset 0): the view's
// stop place id is a lookup per row, which that filter would otherwise run
// for every facility, in service or not.
var affectedLifts = namedOpaque("api_affected_lifts", `
select f.stop_place_id, count(*)::int, array_agg(distinct f.source_ref order by f.source_ref)
from (
  select f.stop_place_id, f.source_ref
  from (`+relStationFacility+`) f
  where f.facility_status = 'inactive' and f.source_ref = any($1)
    and `+"f.display_class <> 'withhold' and (f.audience = 'public' or "+ownKeyOn+")"+`
  offset 0
) f
where f.stop_place_id is not null
group by 1`, "text[]")

// affectedCalls are the stations (by IFOPT) with departures of the next hour
// cancelled or late, of the families ($1), as the platform's board counts
// them: per station, minute and line the sources are folded (the most
// cancelled, and as late the most cancelled or late less the most
// cancelled), then summed per station; with the sources read.
// The sums and the sources are read in one aggregate per station: a join of
// two aggregates of d was planned as a loop over one for each row of the
// other.
var affectedCalls = namedOpaque("api_affected_calls", `
with d as (
  select d.station_ifopt, d.aimed_minute, d.line_key, d.cancelled, d.late, d.source_ref
  from (`+stationDisruptionOfHour+`) d
  where d.source_ref = any($1) and `+"d.display_class <> 'withhold' and (d.audience = 'public' or "+ownKeyOn+")"+`
), f as (
  select station_ifopt, aimed_minute, line_key, max(cancelled) as c, greatest(max(cancelled + late) - max(cancelled), 0) as l
  from d group by 1, 2, 3
), u as (
  select station_ifopt, c, l, null::text as src from f
  union all
  select station_ifopt, 0, 0, source_ref from d
)
select station_ifopt, sum(c)::int, sum(l)::int, array_agg(distinct src order by src) filter (where src is not null)
from u group by 1 having sum(c) + sum(l) > 0`, "text[]")

// stationDisruptionOfHour is the platform's station disruption relation
// with the calls of the hour read in one range of the aimed departures,
// joined to the stations' refs, not probed ref by ref (12 580 probes on
// prod, most of a second; the hour's calls are one range of about 300 000
// rows); the recently checked feeds and the source rules (per family of
// $1) are read once, not per call or per group. The relation's limit of 500 calls per ref changes nothing: the
// most a ref holds in an hour on prod is under a hundred. The calls are
// those of the families ($1): a statement reading it keeps only those.
var stationDisruptionOfHour = strings.NewReplacer(
	callsPerRef, callsOfHour,
	"\n                 LIMIT 500) c_1\n", ") c_1 ON c_1.stop_ref = r.ref\n",
	freshPerCall, freshOnce,
	rulesPerRow("c.source_feed"), rulesOfFamilies("c.source_feed"),
).Replace(relStationDisruption)

// freshPerCall is the relation's test of a call's feed against the recently
// checked ones, a scan of them per call; freshOnce reads them once.
const (
	freshPerCall = " AND x.source_feed IN ( SELECT fr.feed_key FROM fresh fr)"
	freshOnce    = " AND x.source_feed = ANY (ARRAY( SELECT fr.feed_key FROM fresh fr))"
)

// callsPerRef is the station disruption relation's read of the calls, one
// probe per ref.
const callsPerRef = `FROM refs r
             CROSS JOIN LATERAL ( SELECT x.aimed_dep,
                    x.journey_ref,
                    x.line,
                    x.cancelled,
                    x.journey_cancelled,
                    x.delay_s,
                    x.source_feed,
                    x.observed_at
                   FROM delfi_siri_et_calls_latest x
                  WHERE x.stop_ref = r.ref AND `

// callsOfHour reads the same calls in one range, joined to the refs; the
// relation's own conditions follow it unchanged.
const callsOfHour = `FROM refs r
             JOIN ( SELECT x.stop_ref,
                    x.aimed_dep,
                    x.journey_ref,
                    x.line,
                    x.cancelled,
                    x.journey_cancelled,
                    x.delay_s,
                    x.source_feed,
                    x.observed_at
                   FROM delfi_siri_et_calls_latest x
                  WHERE x.source_feed = any($1::text[]) AND `

// affectedPlaces are the stations of the families ($1) with these ids or
// IFOPTs ($2), each with the ref it was named by.
var affectedPlaces = namedOpaque("api_affected_places", `
with hits as (
  select distinct q.ref, s.gtfs_feed_key, s.id
  from (select distinct ref from unnest($2::text[]) u(ref)) q
  cross join lateral (
    select s.gtfs_feed_key, s.id from stops s
    where (s.gtfs_feed_key::text || ':') || s.id = q.ref and s.deleted_at is null
    union all
    select s.gtfs_feed_key, s.id from feeds f join stops s on s.gtfs_feed_key = f.gtfs_feed_key and s.id = q.ref
    where s.deleted_at is null
      and (s.id ~ '^[a-z]{2}:[0-9]{5}:[0-9]+$' or exists (select 1 from db_stations x where x.ifopt = s.id and x.closed_at is null))) s)
select r.ref, s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code, coalesce(s.modes, '{}'), s.source_ref
from unnest($2::text[]) r(ref)
join hits h on h.ref = r.ref
join (`+relStopPlace+`) s on s.stop_place_id = (h.gtfs_feed_key::text || ':') || h.id
where s.withdrawn_at is null and s.location is not null and s.source_ref = any($1)
  and `+"s.display_class <> 'withhold' and (s.audience = 'public' or "+ownKeyOn+")", "text[]", "text[]")

// affectedRef is what one ref (a stop place id or an IFOPT) is named by.
type affectedRef struct {
	alerts          map[string]bool
	lifts           int
	cancelled, late int
	fams            []string
}

// AffectedStations ranks the stations of the families by what affects them
// at now (features.Affected): cancelled departures of the next hour first,
// then late ones, then alerts in force, then lifts out of service; ties by
// name, then id. A station is listed when it has a departure of the next
// hour cancelled or late, or an alert in force or a lift out of service
// names it. The departures are counted by the read contract at the instant
// of the read.
func (s *Store) AffectedStations(ctx context.Context, families []string, now time.Time, limit int) ([]transit.Affected, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Affected, error) {
		by, err := affectedRefs(ctx, tx, families, now)
		if err != nil {
			return nil, err
		}
		stations, err := affectedPlacesOf(ctx, tx, families, by)
		if err != nil {
			return nil, err
		}
		out := make([]transit.Affected, 0, len(stations))
		for _, a := range stations {
			fs := slices.Clone(a.Families)
			slices.Sort(fs)
			own := a.Station.Family
			a.Families = append([]string{own}, slices.DeleteFunc(slices.Compact(fs), func(f string) bool { return f == own })...)
			out = append(out, *a)
		}
		slices.SortFunc(out, func(x, y transit.Affected) int {
			return cmp.Or(cmp.Compare(y.Cancelled, x.Cancelled), cmp.Compare(y.Late, x.Late), cmp.Compare(y.Alerts, x.Alerts),
				cmp.Compare(y.LiftsOut, x.LiftsOut), cmp.Compare(x.Station.Name, y.Station.Name), cmp.Compare(x.Station.ID, y.Station.ID))
		})
		return out[:min(limit, len(out))], nil
	})
}

// affectedRefs reads what the alerts in force and the lifts out of service
// name, by ref.
func affectedRefs(ctx context.Context, tx pgx.Tx, families []string, now time.Time) (map[string]*affectedRef, error) {
	by := map[string]*affectedRef{}
	at := func(ref string) *affectedRef {
		if by[ref] == nil {
			by[ref] = &affectedRef{alerts: map[string]bool{}}
		}
		return by[ref]
	}
	var (
		id, fam, ref string
		lifts        int
		fams         []string
	)
	rows, _ := run(ctx, tx, affectedAlerts, families, now)
	if _, err := pgx.ForEachRow(rows, []any{&id, &fam, &ref}, func() error {
		n := at(ref)
		n.alerts[fam+":"+id], n.fams = true, append(n.fams, fam)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, _ = run(ctx, tx, affectedLifts, families)
	if _, err := pgx.ForEachRow(rows, []any{&ref, &lifts, &fams}, func() error {
		n := at(ref)
		n.lifts, n.fams = lifts, append(n.fams, fams...)
		return nil
	}); err != nil {
		return nil, err
	}
	var cancelled, late int
	rows, _ = run(ctx, tx, affectedCalls, families)
	_, err := pgx.ForEachRow(rows, []any{&ref, &cancelled, &late, &fams}, func() error {
		n := at(ref)
		n.cancelled, n.late, n.fams = cancelled, late, append(n.fams, fams...)
		return nil
	})
	return by, err
}

// affectedPlacesOf reads the stations the refs name, each with the alerts and
// lifts its refs carry.
func affectedPlacesOf(ctx context.Context, tx pgx.Tx, families []string, by map[string]*affectedRef) (map[string]*transit.Affected, error) {
	refs := make([]string, 0, len(by))
	for r := range by {
		refs = append(refs, r)
	}
	slices.Sort(refs)
	stations := map[string]*transit.Affected{}
	alerts := map[string]map[string]bool{}
	var (
		ref   string
		st    transit.Station
		modes []string
	)
	rows, _ := run(ctx, tx, affectedPlaces, families, refs)
	_, err := pgx.ForEachRow(rows, []any{&ref, &st.ID, &st.Name, &st.Location.Lat, &st.Location.Lon, &st.NationalCode, &st.RegionCode, &modes, &st.Family}, func() error {
		a := stations[st.ID]
		if a == nil {
			st.Modes = transit.MapModes(modes)
			a = &transit.Affected{Station: st}
			stations[st.ID], alerts[st.ID] = a, map[string]bool{}
		}
		n := by[ref]
		a.LiftsOut += n.lifts
		a.Cancelled += n.cancelled
		a.Late += n.late
		for k := range n.alerts {
			alerts[st.ID][k] = true
		}
		a.Alerts = len(alerts[st.ID])
		a.Families = append(a.Families, n.fams...)
		return nil
	})
	return stations, err
}
