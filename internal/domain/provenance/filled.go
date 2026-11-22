package provenance

import (
	"errors"
	"strings"
	"time"
)

// The placeholders a register text carries for what only the answer knows.
const (
	// YearPlaceholder in an attribution is the year of retrieval, as the
	// publisher's licence asks it ("© Stadt Aachen ({Jahr})").
	YearPlaceholder = "{Jahr}"
	// DatePlaceholder in a note is the day the data it conditions was read
	// (the VMZ Bremen condition: "read on {date}").
	DatePlaceholder = "{date}"
)

// ErrUndated is a dated note whose families have no read to date it by: the
// platform states no such condition undated, and neither does an answer.
var ErrUndated = errors.New("provenance: a dated note with no read of its family")

var berlin, _ = time.LoadLocation("Europe/Berlin")

// Filled is fams as an answer made at at, in lang, states them, as the
// platform prints its credits:
//   - a year placeholder in an attribution is the Europe/Berlin year of at,
//     and the family's FilledUntil the next Berlin new year, when it stops
//     being true;
//   - a date placeholder in a note is the Europe/Berlin day of the oldest
//     read among the families whose notes carry it (the older file of the
//     answer decides), dd.mm.yyyy in German, yyyy-mm-dd otherwise. Without
//     any such read it is ErrUndated.
//
// fams is not changed.
func Filled(fams []Family, at time.Time, lang string) ([]Family, error) {
	var oldest *time.Time
	dated := false
	for _, f := range fams {
		for _, n := range f.Notes {
			if strings.Contains(n.Text, DatePlaceholder) {
				dated = true
				if f.ReadAt != nil && (oldest == nil || f.ReadAt.Before(*oldest)) {
					oldest = f.ReadAt
				}
			}
		}
	}
	if dated && oldest == nil {
		return nil, ErrUndated
	}
	local := at.In(berlin)
	year := local.Format("2006")
	nextYear := time.Date(local.Year()+1, time.January, 1, 0, 0, 0, 0, berlin).UTC()
	var day string
	if oldest != nil {
		layout := "2006-01-02"
		if lang == "de" {
			layout = "02.01.2006"
		}
		day = oldest.In(berlin).Format(layout)
	}
	out := make([]Family, len(fams))
	for i, f := range fams {
		if strings.Contains(f.Source.AttributionText, YearPlaceholder) {
			f.Source.AttributionText = strings.ReplaceAll(f.Source.AttributionText, YearPlaceholder, year)
			f.FilledUntil = nextYear
		}
		if dated {
			notes := make([]Note, len(f.Notes))
			for j, n := range f.Notes {
				n.Text = strings.ReplaceAll(n.Text, DatePlaceholder, day)
				notes[j] = n
			}
			f.Notes = notes
		}
		out[i] = f
	}
	return out, nil
}
