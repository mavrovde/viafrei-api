package weather

import (
	"errors"
	"strings"
	"unicode"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// ErrNoCell means no warn cell answers a weather watch's target: no cell held
// at the position, or a name that is neither a place nor a cell's name.
var ErrNoCell = errors.New("weather: no warn cell")

// CellQuery asks for the warnings in force of one DWD warn cell at or above
// MinLevel (0 to 4), as the platform's weather watch reads them: the cell
// Cell names (its id, or the DWD's name of its area), or, when At is set, the
// cell a Gemeinde or coast cell holds the position in.
type CellQuery struct {
	Cell     string
	At       *geo.Point
	MinLevel int
}

// Cell is the warn cell a CellQuery resolved to and its warnings in force,
// worst first.
type Cell struct {
	// Key is the cell the warnings are read by: its id, or the name given.
	Key string
	// Name is the DWD's name of the cell's area, when one is held.
	Name     *string
	Warnings []Warning
}

// LooksLikeCell reports whether a weather watch's target is taken as a cell
// key as given, as the platform takes it: it holds a digit and no space.
func LooksLikeCell(target string) bool {
	return strings.ContainsFunc(target, unicode.IsDigit) && !strings.ContainsFunc(target, unicode.IsSpace)
}
