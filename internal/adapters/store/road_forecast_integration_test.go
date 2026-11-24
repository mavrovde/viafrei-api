//go:build integration

package store_test

// The road-surface forecast (#222): this test plants road-weather stations
// and their forecast hours into the platform's tables, which the store
// reads directly.

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestTheRoadForecastNearAPosition: the nearest open, active stations of
// the families within 25 km, at most 3, each with its hours from an hour
// before now to 24 hours after, as the platform's weather warnings check
// reads them; a station beyond 25 km, closed, inactive, of another family
// or with no hour in the window is not listed (it still is one of the 3
// nearest); no hour is read while the
// forecast's family is not among the families.
func TestTheRoadForecastNearAPosition(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	now := time.Now().UTC().Truncate(time.Hour)
	st, _ := private(t,
		`insert into public.road_weather_stations (station_id, raw, source_id, name, segment_type, geom, position_source, inactive, source_feed, closed_at)
		 values ('t1', '{}', 'dwd_road', 'Eins', 2, st_setsrid(st_makepoint(9.50, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null),
		        ('t2', '{}', 'dwd_road', 'Zwei', null, st_setsrid(st_makepoint(9.60, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null),
		        ('t3', '{}', 'dwd_road', 'Drei', 1, st_setsrid(st_makepoint(9.70, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null),
		        ('t4', '{}', 'dwd_road', 'Vier', 1, st_setsrid(st_makepoint(9.75, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null),
		        ('t5', '{}', 'dwd_road', 'Fern', 1, st_setsrid(st_makepoint(10.50, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null),
		        ('t6', '{}', 'dwd_road', 'Zu', 1, st_setsrid(st_makepoint(9.51, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', now()),
		        ('t7', '{}', 'dwd_road', 'Aus', 1, st_setsrid(st_makepoint(9.52, 51.30), 4326)::geography, 'swsKatalog', true, 'dwd_road_catalogue', null),
		        ('t8', '{}', 'dwd_road', 'Andere', 1, st_setsrid(st_makepoint(9.53, 51.30), 4326)::geography, 'swsKatalog', false, 'other_catalogue', null),
		        ('t9', '{}', 'dwd_road', 'Alt', 1, st_setsrid(st_makepoint(9.54, 51.30), 4326)::geography, 'swsKatalog', false, 'dwd_road_catalogue', null)`,
		`insert into public.road_weather_forecast (station_id, valid_at, run_at, source_id, rc, ts, source_run_id)
		 select s, date_trunc('hour', now()) + make_interval(hours => h), date_trunc('hour', now()) - interval '1 hour', 'dwd_road',
		        case when s = 't1' and h = 2 then 5 when s = 't2' then null else 1 end, case when s = 't2' then null else -0.5 end, 1
		   from unnest(array['t1','t2','t3','t4','t5','t6','t7','t8']) s, generate_series(-3, 30) h`,
		`insert into public.road_weather_forecast (station_id, valid_at, run_at, source_id, rc, ts, source_run_id)
		 values ('t9', date_trunc('hour', now()) - interval '3 hours', date_trunc('hour', now()) - interval '4 hours', 'dwd_road', 1, 0, 1)`)
	q := weather.RoadQuery{At: geo.Point{Lat: 51.30, Lon: 9.49}, Stations: 3, RadiusM: 25000, From: now.Add(-time.Hour), Until: now.Add(24 * time.Hour)}
	fams := []string{"dwd_road_catalogue", "dwd_road_forecast"}
	ss, err := st.RoadForecast(bounded(t), q, fams)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	// The three nearest stations of the families are t1, t9 and t2; t9 has
	// no hour in the window, so it is not listed and t3 does not take its
	// place, as the platform reads them (the stations first, then hours).
	if len(ss) != 2 || ids[0] != "t1" || ids[1] != "t2" {
		t.Fatalf("stations %v", ids)
	}
	s1 := ss[0]
	if s1.Name != "Eins" || s1.Segment == nil || *s1.Segment != 2 || s1.PositionSource == nil || *s1.PositionSource != "swsKatalog" ||
		s1.Family != "dwd_road_catalogue" || s1.ForecastFamily != "dwd_road_forecast" || s1.DistanceM < 600 || s1.DistanceM > 800 ||
		s1.Location.Lat != 51.30 || s1.Location.Lon != 9.50 {
		t.Errorf("the first station %+v", s1)
	}
	// From an hour before now to 24 hours after: 26 hours, earliest first.
	if len(s1.Hours) != 26 || !s1.Hours[0].ValidAt.Equal(now.Add(-time.Hour)) || !s1.Hours[25].ValidAt.Equal(now.Add(24*time.Hour)) ||
		!s1.Hours[0].RunAt.Equal(now.Add(-time.Hour)) || s1.Hours[3].Class == nil || *s1.Hours[3].Class != 5 ||
		s1.Hours[0].SurfaceTempC == nil || *s1.Hours[0].SurfaceTempC != -0.5 {
		t.Errorf("the first station's hours: %d, %+v", len(s1.Hours), s1.Hours[:4])
	}
	if s2 := ss[1]; s2.Segment != nil || s2.Hours[0].Class != nil || s2.Hours[0].SurfaceTempC != nil {
		t.Errorf("the second station %+v", s2)
	}
	// The forecast's family not read: no station is listed.
	if ss, err := st.RoadForecast(bounded(t), q, fams[:1]); err != nil || len(ss) != 0 {
		t.Errorf("no forecast family: %v, %v", ss, err)
	}
	// No station within 25 km.
	q.At = geo.Point{Lat: 53, Lon: 9.5}
	if ss, err := st.RoadForecast(bounded(t), q, fams); err != nil || len(ss) != 0 {
		t.Errorf("far away: %v, %v", ss, err)
	}
}
