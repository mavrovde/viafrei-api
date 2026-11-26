package places

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
)

// testdata/resolve_labels.json holds synthetic rows and the labels the
// platform's distinctLabels gave them (generated, not written by hand).
type labelCase struct {
	Rows []struct {
		Name    string    `json:"name"`
		Context []*string `json:"context"`
		Always  []*string `json:"always"`
		Tiers   []*string `json:"tiers"`
		Asked   bool      `json:"asked"`
		Unkeyed bool      `json:"unkeyed"`
		OwnTier *int      `json:"ownTier"`
	} `json:"rows"`
	Answerable  bool     `json:"answerable"`
	Identifying []int    `json:"identifying"`
	Labels      []string `json:"labels"`
}

func TestDistinctLabelsAsThePlatformGivesThem(t *testing.T) {
	b, err := os.ReadFile("testdata/resolve_labels.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []labelCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for k, c := range cases {
		rows := make([]distinguishable, len(c.Rows))
		for i, r := range c.Rows {
			rows[i] = distinguishable{name: r.Name, context: r.Context, always: r.Always, tiers: r.Tiers, asked: r.Asked, unkeyed: r.Unkeyed, ownTier: r.OwnTier}
		}
		if got := distinctLabels(rows, c.Answerable, c.Identifying); !slices.Equal(got, c.Labels) {
			t.Errorf("case %d: got %q, want %q", k, got, c.Labels)
		}
	}
}

// testdata/resolve_place_labels.json holds synthetic gazetteer rows with
// the option labels and the readings back the platform gave them.
type placeLabelCases struct {
	Kreise map[string]string `json:"kreise"`
	Cases  []struct {
		Cands    []GazetteerRow    `json:"cands"`
		Lands    map[string]string `json:"lands"`
		Lang     string            `json:"lang"`
		Asked    string            `json:"asked"`
		Labels   []string          `json:"labels"`
		Readings []struct {
			Label   string  `json:"label"`
			Reading *string `json:"reading"`
		} `json:"readings"`
	} `json:"cases"`
}

func TestPlaceLabelsAsThePlatformGivesThem(t *testing.T) {
	b, err := os.ReadFile("testdata/resolve_place_labels.json")
	if err != nil {
		t.Fatal(err)
	}
	var all placeLabelCases
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	// One subtest a case, side by side, each a leaf within the 2 s bound (TST-012).
	for k, c := range all.Cases {
		t.Run(fmt.Sprintf("%04d", k), func(t *testing.T) {
			t.Parallel()
			cands := locate(c.Cands)
			if got := placeOptionLabels(cands, c.Lang, c.Asked, c.Lands, all.Kreise); !slices.Equal(got, c.Labels) {
				t.Errorf("case %d: got %q, want %q", k, got, c.Labels)
			}
			for i, r := range c.Readings {
				want := ""
				if r.Reading != nil {
					want = *r.Reading
				}
				if got := placeReading(cands[i], r.Label, c.Lands[cands[i].ID], all.Kreise); got != want {
					t.Errorf("case %d row %d %q: got %q, want %q", k, i, r.Label, got, want)
				}
			}
		})
	}
}

func TestNumberedAndCounts(t *testing.T) {
	if got := numbered([]string{"Au", "Au", "Au #2"}); !slices.Equal(got, []string{"Au #1", "Au #3", "Au #2"}) {
		t.Fatalf("%q", got)
	}
	if countIn("de", -1234) != "-1.234" || countIn("en", 1234567) != "1,234,567" {
		t.Fatal(countIn("de", -1234))
	}
}
