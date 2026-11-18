package features

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// A withheld family loses its condition note and keeps its other notes; a
// family shown keeps its condition.
func TestWithholdConditionsDropsOnlyTheWithheldFamilysCondition(t *testing.T) {
	cond := provenance.Note{Kind: provenance.Condition, Text: "c"}
	purpose := provenance.Note{Kind: provenance.Purpose, Text: "p"}
	got := withholdConditions([]provenance.Family{
		{Source: access.Source{Family: "shown"}, Notes: []provenance.Note{cond}},
		{Source: access.Source{Family: "held"}, Notes: []provenance.Note{cond, purpose}},
	}, []string{"held"})
	if len(got[0].Notes) != 1 || len(got[1].Notes) != 1 || got[1].Notes[0] != purpose {
		t.Fatalf("%+v", got)
	}
}
