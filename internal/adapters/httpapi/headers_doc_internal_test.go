package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

type orphan struct {
	N int `json:"n"`
}

// A schema no operation reaches is refused: left in the registry, it makes
// huma marshal the whole document twice to leave it out (TST-012).
func TestASchemaNoOperationReachesIsRefused(t *testing.T) {
	plant := operation{id: "plantOrphan", method: http.MethodGet, path: "/v1/plant-orphan", register: func(api huma.API, _ *server, op huma.Operation) {
		api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[orphan](), true, "")
		huma.Register(api, op, func(_ context.Context, _ *struct{}) (*struct{}, error) { return nil, nil })
	}}
	_, _, err := build(Deps{Config: config.Defaults()}, []operation{plant}, onPublic)
	if err == nil || !strings.Contains(err.Error(), "[Orphan]") {
		t.Fatalf("err %v, want the orphan refused", err)
	}
}

// The served document defines every shared header once and refers to it
// everywhere else, and every schema it registers is reached.
func TestTheDocumentSharesItsRepeatedHeaders(t *testing.T) {
	_, doc, err := build(Deps{Config: config.Defaults()}, nil, onPublic)
	if err != nil {
		t.Fatal(err)
	}
	b := doc()
	for name := range sharedHeaderDocs {
		if !bytes.Contains(b, []byte(`"$ref": "#/components/headers/`+name+`"`)) {
			t.Errorf("no answer refers to the shared %s", name)
		}
	}
	if got := bytes.Count(b, []byte(`"description": "`+rateLimitDoc)); got != 2 {
		t.Errorf("the RateLimit description is written %d times, want once for each of the two headers", got)
	}
}

// Every place a schema can refer to another counts as reaching it: items,
// additional properties, oneOf, anyOf, allOf and not, from a parameter, a
// request body, an answer body or an answer header.
func TestReachedSchemasFollowsEveryReference(t *testing.T) {
	ref := func(n string) *huma.Schema { return &huma.Schema{Ref: "#/components/schemas/" + n} }
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	m := reg.Map()
	m["Body"] = &huma.Schema{Properties: map[string]*huma.Schema{"list": {Items: ref("Item")}, "bag": {AdditionalProperties: ref("Bag")}}}
	m["Item"] = &huma.Schema{OneOf: []*huma.Schema{ref("One")}, AnyOf: []*huma.Schema{ref("Any")}, AllOf: []*huma.Schema{ref("All")}, Not: ref("Not")}
	for _, n := range []string{"Bag", "One", "Any", "All", "Not", "Param", "Request", "Header", "Lost"} {
		m[n] = &huma.Schema{Type: huma.TypeString}
	}
	o := &huma.OpenAPI{Components: &huma.Components{Schemas: reg}, Paths: map[string]*huma.PathItem{"/x": {Post: &huma.Operation{
		Parameters:  []*huma.Param{{Schema: ref("Param")}},
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Schema: ref("Request")}}},
		Responses: map[string]*huma.Response{"200": {
			Content: map[string]*huma.MediaType{"application/json": {Schema: ref("Body")}},
			Headers: map[string]*huma.Param{"X": {Schema: ref("Header")}},
		}},
	}}}}
	got := reachedSchemas(o)
	for _, n := range []string{"Body", "Item", "Bag", "One", "Any", "All", "Not", "Param", "Request", "Header"} {
		if !got[n] {
			t.Errorf("%s not reached", n)
		}
	}
	if got["Lost"] {
		t.Error("Lost reached")
	}
	if err := everySchemaReached(o); err == nil || !strings.Contains(err.Error(), "[Lost]") {
		t.Errorf("err %v, want Lost refused", err)
	}
}
