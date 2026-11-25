package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// roadForecastFamily is the source family of the road-weather forecast
// hours, whose rows carry the issuer's source, not a family.
const roadForecastFamily = "dwd_road_forecast"

// roadForecastNear reads the forecast hours of the nearest open, active
// road-weather stations of the families ($1) within $4 metres of the
// position ($2 latitude, $3 longitude), at most $5 of them, valid from $6
// to $7, as the platform's weather warnings check reads them: nearest
// first, then the station, then the hour. A station with no hour in the
// window is not listed, and the next nearest does not take its place; no
// hour is read while the forecast's family is not
// among the families. Every parameter is read through a subquery, so the
// plan cache keeps the generic plan.
var roadForecastNear = named("api_road_forecast_near", `
with pt as (select st_setsrid(st_makepoint((select $3::float8), (select $2::float8)), 4326)::geography g),
near as (select s.station_id, s.name, s.segment_type::int segment_type, s.position_source, s.source_feed,
                st_y(s.geom::geometry) lat, st_x(s.geom::geometry) lon, st_distance(s.geom, pt.g) distance_m
           from road_weather_stations s, pt
          where s.closed_at is null and not s.inactive
            and s.source_feed = any((select $1::text[])::text[])
            and st_dwithin(s.geom, pt.g, (select $4::float8))
          order by s.geom <-> pt.g
          limit (select $5::int))
select n.station_id, n.name, n.segment_type, n.position_source, n.source_feed, n.lat, n.lon, round(n.distance_m)::int,
       f.valid_at, f.run_at, f.rc::int, f.ts::numeric::float8
from near n
join road_weather_forecast f on f.station_id = n.station_id
where '`+roadForecastFamily+`' = any((select $1::text[])::text[])
  and f.valid_at >= (select $6::timestamptz) and f.valid_at <= (select $7::timestamptz)
order by n.distance_m, n.station_id, f.valid_at`, "text[]", "float8", "float8", "float8", "int", "timestamptz", "timestamptz")

// roadStationsLoaded is whether the station catalogue is loaded, as the
// platform's ROAD_STATIONS_LOADED reads it: any open station, of the
// families ($1), while the forecast's family is among them. "No station
// within 25 km" is a statement about the catalogue, so it is only made
// when this holds.
var roadStationsLoaded = named("api_road_stations_loaded", `
select '`+roadForecastFamily+`' = any((select $1::text[])::text[])
   and exists (select 1 from road_weather_stations s where s.closed_at is null and s.source_feed = any((select $1::text[])::text[])) loaded`, "text[]")

// RoadForecast reads the road-surface forecast of the families q asks
// for: the stations nearest first, each with its hours, earliest first;
// and, when none is
// listed, whether the catalogue is loaded.
func (s *Store) RoadForecast(ctx context.Context, q weather.RoadQuery, families []string) (weather.RoadForecast, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (weather.RoadForecast, error) {
		rows, _ := run(ctx, tx, roadForecastNear, families, q.At.Lat, q.At.Lon, q.RadiusM, q.Stations, q.From, q.Until)
		type row struct {
			st weather.RoadStation
			h  weather.RoadHour
		}
		read, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.st.ID, &x.st.Name, &x.st.Segment, &x.st.PositionSource, &x.st.Family, &x.st.Location.Lat, &x.st.Location.Lon,
				&x.st.DistanceM, &x.h.ValidAt, &x.h.RunAt, &x.h.Class, &x.h.SurfaceTempC)
			x.h.ValidAt, x.h.RunAt = x.h.ValidAt.UTC(), x.h.RunAt.UTC()
			return x, err
		})
		out := []weather.RoadStation{}
		for _, x := range read {
			if n := len(out); n == 0 || out[n-1].ID != x.st.ID {
				x.st.ForecastFamily = roadForecastFamily
				out = append(out, x.st)
			}
			out[len(out)-1].Hours = append(out[len(out)-1].Hours, x.h)
		}
		if err != nil || len(out) > 0 {
			return weather.RoadForecast{Stations: out, Loaded: err == nil}, err
		}
		f := weather.RoadForecast{Stations: out}
		return f, runRow(ctx, tx, roadStationsLoaded, families).Scan(&f.Loaded)
	})
}
