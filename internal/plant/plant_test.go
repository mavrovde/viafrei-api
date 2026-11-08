package plant

import "testing"

// The planted failing test.
func TestPlantedFailure(t *testing.T) {
	t.Fatal("planted failure on the throwaway branch")
}
