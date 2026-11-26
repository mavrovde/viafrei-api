//go:build integration

package store_test

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// errInjected is the fault a faultConn answers its armed statement with.
var errInjected = errors.New("injected: the connection is lost")

// fault counts the statements the store sends (a Bind of the extended
// protocol or a simple Query) and loses the connection at the armed one, so
// a test can fail each statement of a read in turn: the storage fault a
// later statement of a transaction meets in production (a lost connection,
// a server stopping) and that no ended context can stand in for.
type fault struct {
	mu    sync.Mutex
	at    int // the statement to fail, counted from 1; 0 disarmed
	seen  int
	fired bool
}

func (f *fault) arm(at int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at, f.seen, f.fired = at, 0, false
}

// counted is how many statements were sent since arm; arm(-1) counts
// without failing any.
func (f *fault) counted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen
}

func (f *fault) hasFired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fired
}

// statement counts one statement and tells whether it is the armed one.
func (f *fault) statement() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.at == 0 {
		return false
	}
	f.seen++
	if f.seen == f.at {
		f.fired = true
		return true
	}
	return false
}

// faultConn reads the frontend messages it writes: the untyped start-up
// messages first, then typed ones.
type faultConn struct {
	net.Conn
	f       *fault
	started bool
}

func (c *faultConn) Write(b []byte) (int, error) {
	for p := b; len(p) >= 5; {
		if !c.started {
			n := int(binary.BigEndian.Uint32(p[:4]))
			c.started = n >= 8 && binary.BigEndian.Uint32(p[4:8]) == 196608 // protocol 3.0: the start-up message
			p = p[min(n, len(p)):]
			continue
		}
		if (p[0] == 'B' || p[0] == 'Q') && c.f.statement() {
			_ = c.Close()
			return 0, errInjected
		}
		p = p[min(1+int(binary.BigEndian.Uint32(p[1:5])), len(p)):]
	}
	return c.Conn.Write(b)
}

// faulty opens a store of one connection on the package's database whose
// statements f can fail.
func faulty(t *testing.T) (*store.Store, *fault) {
	t.Helper()
	return faultyOn(t, db.ReaderURL)
}

// faultyOn is faulty on the database the reader url names.
func faultyOn(t *testing.T, url string) (*store.Store, *fault) {
	t.Helper()
	// The road snapshot off: its reads are statements of every call, so
	// each statement of a read takes its fault in turn.
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + url, "VFAPI_DB_ROAD_SNAPSHOT_TTL=0s"}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := store.PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &fault{}
	pc.MaxConns, pc.MinConns = 1, 0
	var d net.Dialer
	pc.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &faultConn{Conn: c, f: f}, nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool, cfg)
	t.Cleanup(st.Close)
	return st, f
}

// failEach fails each statement of call in turn, the first to the last:
// every one is the call's error. The call runs once more unfaulted and
// succeeds.
func failEach(t *testing.T, st *store.Store, f *fault, name string, call func(context.Context, *store.Store) error) {
	t.Helper()
	for k := 1; ; k++ {
		if k > 64 {
			t.Fatalf("%s: more than 64 statements", name)
		}
		f.arm(k)
		ctx, cancel := context.WithTimeout(context.Background(), testBound)
		err := call(ctx, st)
		cancel()
		if !f.hasFired() {
			f.arm(0)
			if err != nil {
				t.Errorf("%s unfaulted: %v", name, err)
			}
			return
		}
		if err == nil {
			t.Errorf("%s: statement %d failed and the call succeeded", name, k)
		}
	}
}

// failEnds fails, of a read that sends one statement per item after its
// first (the warm-up's junction read, a road a statement), the first
// statement, the first item's and the last's: the same statement each item,
// so failing every one of them adds a reconnect a road and no case, and its
// time grows with the square of the roads (TST-012: 4.9 s under -race).
func failEnds(t *testing.T, st *store.Store, f *fault, name string, call func(context.Context, *store.Store) error) {
	t.Helper()
	f.arm(-1)
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	err := call(ctx, st)
	cancel()
	n := f.counted()
	f.arm(0)
	if err != nil || n < 3 {
		t.Fatalf("%s unfaulted: %d statements, %v (at least one item is needed)", name, n, err)
	}
	for _, k := range []int{1, 2, n} {
		f.arm(k)
		ctx, cancel := context.WithTimeout(context.Background(), testBound)
		err := call(ctx, st)
		cancel()
		fired := f.hasFired()
		f.arm(0)
		if !fired || err == nil {
			t.Errorf("%s: statement %d of %d failed (%v) and the call answered %v", name, k, n, fired, err)
		}
	}
}

// Every statement of a read that fails fails the read: none is swallowed.
func TestEveryStatementFaultFailsTheRead(t *testing.T) {
	st, f := faulty(t)
	reg, err := seeded(t).Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var every []string
	for _, r := range reg {
		every = append(every, r.Family)
	}
	berlin := geo.Point{Lat: 52.52, Lon: 13.405}
	now := time.Now()
	// A station and a warn cell the fixtures hold, so each read runs to its
	// last statement.
	owner, err := db.Owner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var station, cell, town string
	err = owner.QueryRow(context.Background(), `select (select stop_place_id from contract.stop_place where withdrawn_at is null limit 1),
		(select area_name from contract.weather_warning where area_name is not null limit 1),
		(select name from contract.admin_region where region_level = 'district' order by region_code limit 1)`).Scan(&station, &cell, &town)
	_ = owner.Close(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func(context.Context, *store.Store) error{
		"PlacesCoverage": func(ctx context.Context, s *store.Store) error { _, err := s.PlacesCoverage(ctx, every); return err },
		"Location": func(ctx context.Context, s *store.Store) error {
			_, err := s.Location(ctx, places.LocationQuery{At: berlin}, every)
			return err
		},
		"Location abroad": func(ctx context.Context, s *store.Store) error {
			_, err := s.Location(ctx, places.LocationQuery{At: geo.Point{Lat: 55.9, Lon: 3.1}}, every)
			return err
		},
		"NeedPlaces": func(ctx context.Context, s *store.Store) error {
			_, err := s.NeedPlaces(ctx, places.NearbyQuery{At: berlin, RadiusM: 5000, Needs: []string{"toilets", "fuel"}, Limit: 5}, every)
			return err
		},
		"Junctions": func(ctx context.Context, s *store.Store) error {
			_, err := s.Junctions(ctx, places.NearbyQuery{At: berlin, RadiusM: 50000, Limit: 5}, every)
			return err
		},
		"Places": func(ctx context.Context, s *store.Store) error {
			_, _, err := s.Places(ctx, places.Query{Text: "a", Limit: 5}, every, every, every)
			return err
		},
		"SpeedCameras": func(ctx context.Context, s *store.Store) error {
			_, err := s.SpeedCameras(ctx, roads.SpeedQuery{At: berlin, RadiusM: 50000, Today: now, Limit: 5}, every)
			return err
		},
		"Station": func(ctx context.Context, s *store.Store) error { _, err := s.Station(ctx, station, every); return err },
		"Stations": func(ctx context.Context, s *store.Store) error {
			_, _, err := s.Stations(ctx, transit.Query{Text: "a", Limit: 5}, every)
			return err
		},
		// A text no name holds, read as a town (a district of the seed) and
		// a stop: each statement of the town reading fails the read too.
		"Stations in a town": func(ctx context.Context, s *store.Store) error {
			_, _, err := s.Stations(ctx, transit.Query{Text: town + " Zzqxplatz", Limit: 5}, every)
			return err
		},
		"Departures": func(ctx context.Context, s *store.Store) error {
			_, err := s.Departures(ctx, transit.DepartureQuery{StationID: "x", From: now, Window: time.Hour, Limit: 5}, every)
			return err
		},
		"AffectedStations": func(ctx context.Context, s *store.Store) error {
			_, err := s.AffectedStations(ctx, every, now, 5)
			return err
		},
		"Disruptions": func(ctx context.Context, s *store.Store) error {
			_, err := s.Disruptions(ctx, transit.DisruptionQuery{Area: "09", Line: "RE45", Limit: 5, Now: now, LateOverMin: 5}, every)
			return err
		},
		"WarningsAt": func(ctx context.Context, s *store.Store) error {
			_, err := s.WarningsAt(ctx, weather.Query{LookAhead: lookAhead, At: berlin, MinLevel: 1, Limit: 5}, every, now)
			return err
		},
		"Motorways": func(ctx context.Context, s *store.Store) error {
			_, _, err := s.Motorways(ctx, roads.CatalogueQuery{Limit: 5}, every, now)
			return err
		},
		"RoadLamps": func(ctx context.Context, s *store.Store) error {
			_, err := s.RoadLamps(ctx, []string{"A101"}, roads.Families{Roads: every, Weather: every}, now)
			return err
		},
		"Topic": func(ctx context.Context, s *store.Store) error { _, err := s.Topic(ctx, "lez", "de"); return err },
		"Zones": func(ctx context.Context, s *store.Store) error {
			_, err := s.Zones(ctx, rules.ZoneQuery{At: &berlin}, every, now)
			return err
		},
		"CellWarnings named": func(ctx context.Context, s *store.Store) error {
			_, err := s.CellWarnings(ctx, weather.CellQuery{Cell: cell}, every, now)
			return err
		},
		"CellWarnings at": func(ctx context.Context, s *store.Store) error {
			_, err := s.CellWarnings(ctx, weather.CellQuery{At: &berlin}, every, now)
			if errors.Is(err, weather.ErrNoCell) {
				return nil
			}
			return err
		},
	} {
		// Each read fails at each of its statements in turn, a reconnect
		// each: one subtest per read (TST-012).
		t.Run(name, func(t *testing.T) { failEach(t, st, f, name, call) })
	}
	t.Run("WarmRoads", func(t *testing.T) {
		failEnds(t, st, f, "WarmRoads", func(ctx context.Context, s *store.Store) error { return s.WarmRoads(ctx) })
	})
}
