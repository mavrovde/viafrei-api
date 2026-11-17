package pgtest

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func fixed(url string, err error) func(context.Context, ...string) (string, error) {
	return func(context.Context, ...string) (string, error) { return url, err }
}

func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestFinishReportsEachFailure(t *testing.T) {
	cases := map[string]func(context.Context, ...string) (string, error){
		"connection string": fixed("", errors.New("no port mapped")),
		"owner URL":         fixed("postgres://owner@%zz/contract", nil),
		"connect as owner":  fixed("postgres://owner:pw@"+closedPort(t)+"/contract?sslmode=disable", nil),
	}
	for want, connString := range cases {
		db := &DB{}
		err := db.finish(context.Background(), connString)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("finish = %v, want an error about %s", err, want)
		}
		if db.OwnerURL != "" || db.ReaderURL != "" {
			t.Errorf("%s: a failed finish left URLs behind", want)
		}
	}
}

func TestStopWithoutContainer(t *testing.T) {
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("Stop without a container panicked: %v", v)
		}
	}()
	db := &DB{}
	db.Stop()
	if db.container != nil {
		t.Fatal("Stop created a container")
	}
}

const pinSrc = `# a comment
ghcr.io/mavrovde/viafrei-database:0.2.0@sha256:98f0e01b8bb2f34afac8b482bd701ee61fa7bf142cc03d413e83c3b8b605ec9f

schema-head 306
`

func TestParsePin(t *testing.T) {
	p, err := ParsePin(pinSrc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "0.2.0" || p.Head != 306 || !strings.HasSuffix(p.Ref, "@sha256:98f0e01b8bb2f34afac8b482bd701ee61fa7bf142cc03d413e83c3b8b605ec9f") ||
		p.VersionLine() != "viafrei-database 0.2.0 schema-head 306" || p.Ready() != "READY 306" {
		t.Fatalf("parsed %+v", p)
	}
	if p.Local {
		t.Fatal("a published release read as local")
	}
	l, err := ParsePin("viafrei-database:0.3.0-local\nschema-head 309\n")
	if err != nil || !l.Local || l.Version != "0.3.0" || l.Ref != "viafrei-database:0.3.0-local" || l.VersionLine() != "viafrei-database 0.3.0 schema-head 309" {
		t.Fatalf("a local pin: %+v %v", l, err)
	}
	digest := "@sha256:" + strings.Repeat("a", 64)
	for name, c := range map[string]struct{ src, want string }{
		"one line":            {"ghcr.io/x/y:1.2.3" + digest, "holds 1 lines"},
		"three lines":         {pinSrc + "schema-head 307\n", "holds 3 lines"},
		"a tag, no digest":    {"ghcr.io/x/y:1.2.3\nschema-head 1", "is not <image>"},
		"a moving tag":        {"ghcr.io/x/y:latest" + digest + "\nschema-head 1", "is not <image>"},
		"a short digest":      {"ghcr.io/x/y:1.2.3@sha256:abc\nschema-head 1", "is not <image>"},
		"a local registry":    {"ghcr.io/x/y:1.2.3-local\nschema-head 1", "is not <image>"},
		"a local, no version": {"viafrei-database:local\nschema-head 1", "is not <image>"},
		"the lines reversed":  {"schema-head 1\nghcr.io/x/y:1.2.3" + digest, "is not <image>"},
		"no head number":      {"ghcr.io/x/y:1.2.3" + digest + "\nschema-head x", "is not 'schema-head N'"},
		"a zero head":         {"ghcr.io/x/y:1.2.3" + digest + "\nschema-head 0", "is not 'schema-head N'"},
	} {
		if _, err := ParsePin(c.src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// TestReadPinFindsTheModuleRoot: the pin is read from the nearest module
// root upwards; this repository's own pin is a valid one.
func TestReadPinFindsTheModuleRoot(t *testing.T) {
	wd, _ := os.Getwd()
	p, err := ReadPin(wd)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref == "" || p.Head < 1 {
		t.Fatalf("the repository's pin: %+v", p)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPin(deep); err == nil || !strings.Contains(err.Error(), "read database.pin") {
		t.Errorf("a module without a pin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, PinFile), []byte(pinSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := ReadPin(deep); err != nil || p.Head != 306 {
		t.Errorf("the pin two levels up: %+v %v", p, err)
	}
	if _, err := ReadPin(string(filepath.Separator)); err == nil || !strings.Contains(err.Error(), "no go.mod above") {
		t.Errorf("no module: %v", err)
	}
}

type answer struct {
	out  string
	code int
	err  error
}

// fakeRun answers each step of the pinned image from answers, keyed by the
// step's first word, and records the environment of each.
type fakeRun struct {
	answers map[string]answer
	env     []map[string]string
	files   []map[string]string
	args    [][]string
}

func (f *fakeRun) run(_ context.Context, _ string, env, files map[string]string, args ...string) (string, int, error) {
	f.env = append(f.env, env)
	f.files = append(f.files, files)
	f.args = append(f.args, args)
	a := f.answers[args[0]]
	return a.out, a.code, a.err
}

func healthyRun() *fakeRun {
	return &fakeRun{answers: map[string]answer{
		"version":    {out: "viafrei-database 0.2.0 schema-head 306\n"},
		"migrate":    {out: "applying 0306\n✓ 306 migration(s) applied\n"},
		"partitions": {out: "✓ partitions ensured\n"},
		"head":       {out: "READY 306\n"},
	}}
}

// TestMigrateWithRefusesEveryWrongAnswer: the pinned image must name the
// pinned release, every step must succeed, and head must print READY <head>.
func TestMigrateWithRefusesEveryWrongAnswer(t *testing.T) {
	pin, err := ParsePin(pinSrc)
	if err != nil {
		t.Fatal(err)
	}
	ok := healthyRun()
	if err := MigrateWith(context.Background(), pin, "postgres://db", ok.run); err != nil {
		t.Fatal(err)
	}
	if len(ok.env) != 4 || ok.env[0] != nil || ok.env[1]["DATABASE_URL"] != "postgres://db" || ok.env[3]["DATABASE_URL"] != "postgres://db" {
		t.Fatalf("the steps' environments: %v", ok.env)
	}
	for want, plant := range map[string]func(*fakeRun){
		"version: no runtime": func(f *fakeRun) { f.answers["version"] = answer{err: errors.New("no runtime")} },
		"version exited 1":    func(f *fakeRun) { f.answers["version"] = answer{out: "usage", code: 1} },
		`says "viafrei-database 0.1.1 schema-head 300"`: func(f *fakeRun) {
			f.answers["version"] = answer{out: "viafrei-database 0.1.1 schema-head 300"}
		},
		"migrate exited 1: relation exists":   func(f *fakeRun) { f.answers["migrate"] = answer{out: "applying\nrelation exists\n", code: 1} },
		"partitions ensure --days 7 exited 2": func(f *fakeRun) { f.answers["partitions"] = answer{code: 2} },
		"head: no runtime":                    func(f *fakeRun) { f.answers["head"] = answer{err: errors.New("no runtime")} },
		`head printed "PENDING 305"`:          func(f *fakeRun) { f.answers["head"] = answer{out: "PENDING 305"} },
	} {
		f := healthyRun()
		plant(f)
		if err := MigrateWith(context.Background(), pin, "postgres://db", f.run); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("planted %q: %v", want, err)
		}
	}
}

type fakeExited struct {
	stateErr, logsErr error
	logs              io.ReadCloser
}

func (f fakeExited) State(context.Context) (*container.State, error) {
	return &container.State{ExitCode: 3}, f.stateErr
}

func (f fakeExited) Logs(context.Context) (io.ReadCloser, error) { return f.logs, f.logsErr }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("cut") }

func TestReadExit(t *testing.T) {
	out, code, err := readExit(context.Background(), fakeExited{logs: io.NopCloser(strings.NewReader("READY 1\n"))})
	if err != nil || code != 3 || out != "READY 1\n" {
		t.Fatalf("%q %d %v", out, code, err)
	}
	for name, f := range map[string]fakeExited{
		"state": {stateErr: errors.New("state")},
		"logs":  {logsErr: errors.New("logs")},
		"read":  {logs: io.NopCloser(failingReader{})},
	} {
		if _, _, err := readExit(context.Background(), f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestMigrateReportsEachFailure(t *testing.T) {
	ip := func(s string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return s, err }
	}
	for want, c := range map[string]struct {
		ip   func(context.Context) (string, error)
		conn func(context.Context, ...string) (string, error)
	}{
		"container address": {ip("", errors.New("no network")), fixed("postgres://owner:pw@localhost:1/contract", nil)},
		"connection string": {ip("172.17.0.2", nil), fixed("", errors.New("no port mapped"))},
		"owner URL":         {ip("172.17.0.2", nil), fixed("postgres://owner@%zz/contract", nil)},
	} {
		db := &DB{}
		if err := db.migrate(context.Background(), c.ip, c.conn); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("migrate = %v, want an error about %s", err, want)
		}
	}
}

// TestSeedWithHandsTheRowsToThePinnedImage: the rows go to the image's test
// seed as a file, with the test-fixtures switch and the database URL; a
// failed seed names the step and the image's last line.
func TestSeedWithHandsTheRowsToThePinnedImage(t *testing.T) {
	pin, err := ParsePin(pinSrc)
	if err != nil {
		t.Fatal(err)
	}
	ok := &fakeRun{answers: map[string]answer{"test-fixtures": {out: "seeded\n"}}}
	if err := SeedWith(context.Background(), pin, "postgres://db/test_contract", "insert into contract.source ...", ok.run); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ok.args[0], " ") != "test-fixtures seed "+SeedPath || ok.files[0][SeedPath] != "insert into contract.source ..." ||
		ok.env[0]["DATABASE_URL"] != "postgres://db/test_contract" || ok.env[0]["VF_TEST_FIXTURES"] != "1" {
		t.Fatalf("the seed step: %v %v %v", ok.args, ok.files, ok.env)
	}
	for want, a := range map[string]answer{
		"test-fixtures seed: no runtime":                  {err: errors.New("no runtime")},
		"test-fixtures seed exited 1: no such relation x": {out: "reading\nno such relation x\n", code: 1},
	} {
		f := &fakeRun{answers: map[string]answer{"test-fixtures": a}}
		if err := SeedWith(context.Background(), pin, "postgres://db", "x", f.run); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("planted %q: %v", want, err)
		}
	}
}
