package provenance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func tp(t time.Time) *time.Time { return &t }

// The year of retrieval is the Europe/Berlin year of the answer, as the
// platform's credit lines print it: the last minute of the Berlin year
// says that year, the first one the next; the filled text is true until the
// next Berlin new year. A text without the placeholder stays as it is.
func TestFilledStatesTheBerlinYearOfTheAnswer(t *testing.T) {
	req.Covers(t, "LIC-003")
	fams := []provenance.Family{
		{Source: access.Source{Family: "a", AttributionText: "Stadt Aachen ({Jahr}), DL-DE-BY-2.0"}},
		{Source: access.Source{Family: "b", AttributionText: "© DWD"}},
	}
	for _, tc := range []struct {
		at    time.Time
		text  string
		until time.Time
	}{
		{time.Date(2026, 12, 31, 22, 59, 0, 0, time.UTC), "Stadt Aachen (2026), DL-DE-BY-2.0", time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), "Stadt Aachen (2027), DL-DE-BY-2.0", time.Date(2027, 12, 31, 23, 0, 0, 0, time.UTC)},
	} {
		got, err := provenance.Filled(fams, tc.at, "de")
		if err != nil || got[0].Source.AttributionText != tc.text || !got[0].FilledUntil.Equal(tc.until) {
			t.Errorf("at %v: %q until %v, %v", tc.at, got[0].Source.AttributionText, got[0].FilledUntil, err)
		}
		if got[1].Source.AttributionText != "© DWD" || !got[1].FilledUntil.IsZero() {
			t.Errorf("a text without the placeholder: %+v", got[1])
		}
	}
	if fams[0].Source.AttributionText != "Stadt Aachen ({Jahr}), DL-DE-BY-2.0" {
		t.Errorf("the given families changed: %q", fams[0].Source.AttributionText)
	}
}

// A dated condition states the Berlin day of the older read of the families
// whose notes carry it, in the answer's language; another family's read
// does not date it, and with no read of its own it is refused.
func TestFilledDatesAConditionByItsOldestRead(t *testing.T) {
	req.Covers(t, "LIC-003")
	note := provenance.Note{Kind: provenance.Condition, Text: "gelesen am {date}"}
	fams := []provenance.Family{
		{Source: access.Source{Family: "hb1"}, Notes: []provenance.Note{note}, ReadAt: tp(time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC))},
		{Source: access.Source{Family: "hb2"}, Notes: []provenance.Note{note}, ReadAt: tp(time.Date(2026, 11, 22, 6, 0, 0, 0, time.UTC))},
		{Source: access.Source{Family: "other"}, ReadAt: tp(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
	}
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for lang, want := range map[string]string{"de": "gelesen am 22.11.2026", "en": "gelesen am 2026-11-22"} {
		got, err := provenance.Filled(fams, at, lang)
		if err != nil || got[0].Notes[0].Text != want || got[1].Notes[0].Text != want {
			t.Errorf("%s: %+v, %v", lang, got, err)
		}
	}
	if fams[0].Notes[0].Text != note.Text {
		t.Errorf("the given notes changed: %q", fams[0].Notes[0].Text)
	}
	if _, err := provenance.Filled([]provenance.Family{{Notes: []provenance.Note{note}}, fams[2]}, at, "de"); !errors.Is(err, provenance.ErrUndated) {
		t.Errorf("undated: %v", err)
	}
	if got, err := provenance.Filled(fams[2:], at, "de"); err != nil || len(got) != 1 {
		t.Errorf("no dated note: %+v, %v", got, err)
	}
}
