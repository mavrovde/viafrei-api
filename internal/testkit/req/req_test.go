package req_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// recorder stands in for a test and records what Covers asks of it.
type recorder struct {
	testing.TB
	fatals []string
	logs   []string
}

func (r *recorder) Helper()           {}
func (r *recorder) Fatal(args ...any) { r.fatals = append(r.fatals, fmt.Sprint(args...)) }
func (r *recorder) Fatalf(f string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(f, args...))
}
func (r *recorder) Log(args ...any) { r.logs = append(r.logs, fmt.Sprintln(args...)) }

func TestCoversWritesOneMarkerPerID(t *testing.T) {
	req.Covers(t, "TEST-001")
	r := &recorder{}
	req.Covers(r, "CFG-001", "FR-SVC-004")
	if len(r.fatals) != 0 || strings.Join(r.logs, "") != "REQ-ID: CFG-001\nREQ-ID: FR-SVC-004\n" {
		t.Fatalf("fatals %v, logs %q", r.fatals, r.logs)
	}
}

func TestCoversRefusesMalformedIDs(t *testing.T) {
	req.Covers(t, "TEST-001")
	for _, ids := range [][]string{nil, {"cfg-1"}} {
		r := &recorder{}
		req.Covers(r, ids...)
		if len(r.fatals) == 0 {
			t.Errorf("Covers(%q) did not fail the test", ids)
		}
	}
}
