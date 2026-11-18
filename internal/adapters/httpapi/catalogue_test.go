package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func TestListMotorwaysStatesARoadsJunctionsInNumberOrder(t *testing.T) {
	no, state, km := "12", "BY", 778.0
	f := &fakeFeatures{motorways: features.MotorwaysAnswer{Motorways: []roads.Motorway{{
		Road: "A3", Name: "A3", LengthKm: &km, States: []string{"BY"}, Junctions: 1, Family: "autobahn", Lamp: roads.Lamp("amber"),
		Exits: []roads.Junction{{ID: "j-1", Number: &no, Name: "Regensburg", Kind: "junction", State: &state, Lat: 49.0, Lon: 12.1}},
	}}}}
	body := decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/motorways?road=A3", bearer))
	rows, _ := body["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows %v", body)
	}
	row, _ := rows[0].(map[string]any)
	exits, _ := row["exits"].([]any)
	if row["lamp"] != "amber" || len(exits) != 1 || exits[0].(map[string]any)["number"] != "12" {
		t.Errorf("row %v", row)
	}
	if f.gotCat.Road != "A3" || f.gotCat.Limit != 200 {
		t.Errorf("query %+v", f.gotCat)
	}
}
