//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The parity cases of the situation reads (the road, area and Land lists,
// the lamp's view, one situation, the catalogue's roads) and of the
// families' freshness, beside namedCases (merged into it when the package
// loads).
func init() {
	for name, cases := range map[string]func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any{
		"api_road_situations":     roadSituationCases,
		"api_area_situations":     areaSituationCases,
		"api_state_situations":    stateSituationCases,
		"api_road_messages":       roadMessageCases,
		"api_area_messages":       areaMessageCases,
		"api_state_messages":      stateMessageCases,
		"api_state_placed":        stateMessageCases,
		"api_situation":           situationCases,
		"api_road_covering":       coveringCases,
		"api_road_lamp_messages":  roadLampMessageCases,
		"api_road_sources":        sourceCases,
		"api_motorway_families":   motorwayFamilyCases,
		"api_motorways":           motorwaysCases,
		"api_motorways_live_land": motorwaysCases,
	} {
		namedCases[name] = cases
	}
}

func strs[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = string(v)
	}
	return out
}

// eventFamilies are the families of the fixture's situations, and none.
func eventFamilies(t *testing.T, ctx context.Context, c *pgx.Conn) [][]string {
	return [][]string{column(t, ctx, c, `select distinct source_ref from contract.traffic_event order by 1`), {}}
}

// eventTimes are now and instants of the fixture's situations: a minute
// after each start and the end.
func eventTimes(t *testing.T, ctx context.Context, c *pgx.Conn) []time.Time {
	rows, _ := c.Query(ctx, `select distinct t from contract.traffic_event,
		lateral (values (valid_from + interval '1 minute'), (valid_to)) v(t) where t is not null order by 1 limit 2`)
	at, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		t.Fatal(err)
	}
	return append(at, time.Now())
}

// filters are filterArgs's $1 to $7 for the families and now: every kind,
// or only jams over the floor minor.
func filters(fams []string, now time.Time) [][]any {
	mapped := strs(roads.Kinds[:len(roads.Kinds)-1])
	return [][]any{
		{fams, now, strs(roads.Kinds), true, mapped, strs(roads.Ordered()), 0},
		{fams, now, []string{"jam"}, false, mapped, strs(roads.Ordered()), 2},
	}
}

func eventRefs(t *testing.T, ctx context.Context, c *pgx.Conn) []string {
	return append(column(t, ctx, c, `select distinct upper(replace(road_ref, ' ', '')) from contract.traffic_event where road_ref is not null order by 1 limit 3`), "A0")
}

func eventLands(t *testing.T, ctx context.Context, c *pgx.Conn) []string {
	return append(column(t, ctx, c, `select distinct state_code from contract.traffic_event where state_code is not null order by 1`), "de", "zz")
}

func eventPoints(t *testing.T, ctx context.Context, c *pgx.Conn) [][2]float64 {
	return positions(t, ctx, c, `select st_y(location), st_x(location) from contract.traffic_event where location is not null order by event_id limit 3`)
}

func with(base []any, more ...any) []any {
	return append(append([]any{}, base...), more...)
}

func roadSituationCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	f := eventFamilies(t, ctx, c)[0]
	refs := eventRefs(t, ctx, c)
	var out [][]any
	for _, at := range eventTimes(t, ctx, c)[1:] {
		fl := filters(f, at)
		out = append(out, with(fl[0], refs[0], "both", strs(roads.Compass()), 50, at, at.Add(time.Hour), 0, false),
			with(fl[1], refs[len(refs)/2], string(roads.Compass()[0]), strs(roads.Compass()), 1, at, at, 1, false),
			with(fl[0], "A0", "both", strs(roads.Compass()), 50, at, at, 0, true))
	}
	return append(out, with(filters([]string{}, time.Now())[0], refs[0], "both", strs(roads.Compass()), 50, time.Now(), time.Now(), 0, false))
}

func areaSituationCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	mapped := strs(roads.Classes[:len(roads.Classes)-1])
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			for _, fl := range filters(f, at) {
				for _, p := range eventPoints(t, ctx, c) {
					out = append(out, with(fl, p[0], p[1], 50000.0, strs(roads.Classes), true, mapped, 50, at, at, 0),
						with(fl, p[0], p[1], 10.0, []string{"motorway"}, false, mapped, 1, at, at, 1))
				}
			}
		}
	}
	return out
}

func stateSituationCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	mapped := strs(roads.Classes[:len(roads.Classes)-1])
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			for _, fl := range filters(f, at) {
				for _, l := range eventLands(t, ctx, c) {
					out = append(out, with(fl, l, strs(roads.Classes), true, mapped, 50, at, at, 0))
				}
			}
		}
	}
	return out
}

func roadMessageCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			for _, r := range eventRefs(t, ctx, c) {
				out = append(out, []any{f, at, r, "both", strs(roads.Compass())}, []any{f, at, r, string(roads.Compass()[1]), strs(roads.Compass())})
			}
		}
	}
	return out
}

func areaMessageCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			for _, p := range eventPoints(t, ctx, c) {
				out = append(out, []any{f, at, p[0], p[1], 50000.0}, []any{f, at, p[0], p[1], 0.0})
			}
		}
	}
	return out
}

func stateMessageCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			for _, l := range eventLands(t, ctx, c) {
				out = append(out, []any{f, at, l})
			}
		}
	}
	return out
}

func situationCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	ids := append(column(t, ctx, c, `select event_id from contract.traffic_event order by 1 limit 8`), "autobahn:none")
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, id := range ids {
			out = append(out, []any{id, f, time.Unix(0, 0)}, []any{id, f, time.Now()})
		}
	}
	return out
}

func coveringCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, class := range strs(roads.Classes) {
			out = append(out, []any{f, class, time.Now()}, []any{f, class, time.Unix(0, 0)})
		}
	}
	return out
}

func roadLampMessageCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			out = append(out, []any{f, at, eventRefs(t, ctx, c)})
		}
	}
	return out
}

func sourceCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	all := append(column(t, ctx, c, `select source_ref from public.contract_sources order by 1`), "none")
	return [][]any{{all}, {all[:1]}, {[]string{}}}
}

func motorwayFamilyCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			out = append(out, []any{f, at})
		}
	}
	return out
}

func motorwaysCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	refs := column(t, ctx, c, `select road from public.autobahn_roads order by 1 limit 3`)
	lands := column(t, ctx, c, `select distinct contract_junction_land(geom) from public.tmc_locations
		where tcd = 1 and geom is not null and contract_junction_land(geom) is not null order by 1 limit 3`)
	var out [][]any
	for _, f := range eventFamilies(t, ctx, c) {
		for _, at := range eventTimes(t, ctx, c) {
			out = append(out, []any{f, "", "", 100, at, []string{}})
			for _, l := range lands {
				out = append(out, []any{f, l, "", 100, at, refs[:1]})
			}
			for _, r := range refs {
				out = append(out, []any{f, "", r, 100, at, []string{}})
			}
		}
	}
	return out
}
