package invariant_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/invariant"
)

func TestMustReturnsTheValue(t *testing.T) {
	if got := invariant.Must(42, nil); got != 42 {
		t.Fatalf("Must = %d, want 42", got)
	}
}

func TestCheckPassesNil(t *testing.T) {
	invariant.Check(nil)
}

func TestMustPanicsOnAnError(t *testing.T) {
	defer func() {
		v := recover()
		if s, _ := v.(string); !strings.Contains(s, "invariant broken: boom") {
			t.Fatalf("recovered %v, want the invariant message", v)
		}
	}()
	invariant.Must(0, errors.New("boom"))
	t.Fatal("Must did not panic")
}
