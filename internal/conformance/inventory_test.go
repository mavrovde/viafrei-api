package conformance_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The inventory gate: the API serves everything its two consumers ask for.
// test/inventory holds what the MCP server's release offers (mcp.tsv: tools,
// their arguments, resources), the fragment routes the platform's release
// registers (routes.tsv) and the fragment paths the site's release asks for
// (web.tsv), each exported by internal/release/inventory from the release it
// names; mapping.tsv says which operation and parity case serve each entry.

// inventoryRows returns the data rows of an inventory file, split at tabs.
func inventoryRows(src string) [][]string {
	var out [][]string
	for _, l := range strings.Split(src, "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, strings.Split(l, "\t"))
		}
	}
	return out
}

// inventoryEntries lists every entry the API must serve: each MCP tool,
// argument and resource, and each fragment path of the site.
func inventoryEntries(mcp, web string) []string {
	var out []string
	for _, r := range inventoryRows(mcp) {
		out = append(out, r[0]+":"+r[1])
	}
	for _, r := range inventoryRows(web) {
		out = append(out, "web:"+r[0])
	}
	return out
}

// routeMatches: a site path ({} for a part it fills in) is one a route
// (:name for a parameter) answers, part for part.
func routeMatches(path, route string) bool {
	p, r := strings.Split(path, "/"), strings.Split(route, "/")
	if len(p) != len(r) {
		return false
	}
	for i := range p {
		if p[i] != r[i] && !strings.HasPrefix(r[i], ":") {
			return false
		}
	}
	return true
}

type apiOperation struct{ params []string }

// apiOperations reads each operation of the OpenAPI document with its
// parameter names.
func apiOperations(doc []byte) (map[string]apiOperation, error) {
	var d struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	out := map[string]apiOperation{}
	for _, item := range d.Paths {
		var shared []string
		if raw, ok := item["parameters"]; ok {
			shared = paramNames(raw)
		}
		for method, raw := range item {
			if method == "parameters" {
				continue
			}
			var op struct {
				OperationID string          `json:"operationId"`
				Parameters  json.RawMessage `json:"parameters"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, err
			}
			out[op.OperationID] = apiOperation{params: append(slices.Clone(shared), paramNames(op.Parameters)...)}
		}
	}
	return out, nil
}

func paramNames(raw json.RawMessage) []string {
	var ps []struct {
		Name string `json:"name"`
		Ref  string `json:"$ref"`
	}
	_ = json.Unmarshal(raw, &ps) // absent: no parameters
	var out []string
	for _, p := range ps {
		if p.Name == "" {
			p.Name = p.Ref[strings.LastIndex(p.Ref, "/")+1:]
		}
		out = append(out, p.Name)
	}
	return out
}

var datedRuling = regexp.MustCompile(`\b20[0-9]{2}-[01][0-9]-[0-3][0-9]\b`)

// inventoryFindings lists every entry nothing serves and every mapping row
// that does not hold, and returns how many entries are served.
func inventoryFindings(entries []string, routes, mapping string, ops map[string]apiOperation, cases map[string]string, web []string) (findings []string, served int) {
	rows := map[string][]string{}
	for _, r := range inventoryRows(mapping) {
		if len(r) != 4 {
			findings = append(findings, fmt.Sprintf("mapping.tsv: the row %q does not hold 4 fields", strings.Join(r, "\t")))
			continue
		}
		if _, dup := rows[r[0]]; dup {
			findings = append(findings, "mapping.tsv: "+r[0]+" has two rows")
		}
		rows[r[0]] = r
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e] = true
		r, ok := rows[e]
		if !ok {
			findings = append(findings, "nothing serves "+e)
			continue
		}
		if f := mappingFindings(r, ops, cases); len(f) > 0 {
			findings = append(findings, f...)
			continue
		}
		served++
	}
	for e := range rows {
		if !known[e] {
			findings = append(findings, "mapping.tsv: "+e+" is in no inventory file; remove the row")
		}
	}
	// A route of a private listener is recorded by an opaque id
	// (mcp-internal-NN), not its path: the site's path it answers must have a
	// mapping row that names that id.
	routeList := inventoryRows(routes)
	for _, p := range web {
		if slices.ContainsFunc(routeList, func(r []string) bool { return routeMatches(p, r[1]) }) {
			continue
		}
		r := rows["web:"+p]
		if r != nil && slices.ContainsFunc(routeList, func(rt []string) bool {
			return !strings.HasPrefix(rt[1], "/") && strings.Contains(r[3], rt[1])
		}) {
			continue
		}
		findings = append(findings, "web.tsv: the site asks for "+p+", which no route of routes.tsv answers by path, and no mapping row names the opaque route that does; export both from their releases")
	}
	sort.Strings(findings)
	return findings, served
}

// mappingFindings checks one row: its operations and parameters exist, and a
// parity case of that operation checks it; or it carries a dated ruling.
func mappingFindings(r []string, ops map[string]apiOperation, cases map[string]string) []string {
	entry, serves, parity, ruling := r[0], r[1], r[2], r[3]
	if serves == "-" {
		if !datedRuling.MatchString(ruling) {
			return []string{entry + ": served by nothing, and no dated owner ruling says it need not be"}
		}
		return nil
	}
	var out []string
	var served []string
	for _, s := range strings.Split(serves, ",") {
		id, param, _ := strings.Cut(s, "?")
		op, ok := ops[id]
		switch {
		case !ok:
			out = append(out, entry+": the API has no operation "+id)
		case param != "" && !slices.Contains(op.params, param):
			out = append(out, entry+": "+id+" has no parameter "+param)
		}
		served = append(served, id)
	}
	checked := false
	for _, c := range strings.Split(parity, ",") {
		op, ok := cases[c]
		if !ok {
			out = append(out, entry+": no parity case "+c)
			continue
		}
		checked = checked || slices.Contains(served, op)
	}
	if !checked && len(out) == 0 {
		out = append(out, entry+": no parity case checks the operation that serves it")
	}
	return out
}

func parityCases(t *testing.T) map[string]string {
	t.Helper()
	var q struct {
		Cases []struct {
			ID        string `json:"id"`
			Operation string `json:"operation"`
		} `json:"cases"`
	}
	if err := json.Unmarshal([]byte(readText(t, "test", "parity", "questions.json")), &q); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range q.Cases {
		out[c.ID] = c.Operation
	}
	return out
}

// TestInventoryIsServed: every entry of the inventory is served by an
// operation a parity case checks, or ruled out by the owner in mapping.tsv.
func TestInventoryIsServed(t *testing.T) {
	req.Covers(t, "REL-015")
	mcp, web := readText(t, "test", "inventory", "mcp.tsv"), readText(t, "test", "inventory", "web.tsv")
	ops, err := apiOperations([]byte(readText(t, "api", "openapi.json")))
	if err != nil {
		t.Fatal(err)
	}
	entries := inventoryEntries(mcp, web)
	if len(entries) < 50 {
		t.Fatalf("only %d inventory entries read; the files were not read", len(entries))
	}
	var paths []string
	for _, r := range inventoryRows(web) {
		paths = append(paths, r[0])
	}
	findings, served := inventoryFindings(entries, readText(t, "test", "inventory", "routes.tsv"), readText(t, "test", "inventory", "mapping.tsv"), ops, parityCases(t), paths)
	for _, f := range findings {
		t.Error(f)
	}
	t.Logf("inventory: %d of %d entries served (%d missing)", served, len(entries), len(entries)-served)
}

func TestInventoryRulesCanSayNo(t *testing.T) {
	req.Covers(t, "REL-015")
	ops := map[string]apiOperation{"listX": {params: []string{"at"}}}
	cases := map[string]string{"x-1": "listX", "y-1": "listY"}
	routes := "GET\t/internal/x/:id\n"
	entries := []string{"tool:find_x", "argument:find_x.lat", "web:/internal/x/{}"}
	good := "tool:find_x\tlistX\tx-1\t-\nargument:find_x.lat\tlistX?at\tx-1\t-\nweb:/internal/x/{}\t-\t-\towner 2026-11-16: the site's own\n"
	if f, n := inventoryFindings(entries, routes, good, ops, cases, []string{"/internal/x/{}"}); len(f) != 0 || n != 3 {
		t.Fatalf("the good mapping: %v, %d served", f, n)
	}
	opaque := routes + "GET\town-internal-01\n"
	named := good + "web:/internal/y\t-\t-\towner 2026-11-16: own-internal-01 answers it\n"
	if f, _ := inventoryFindings(append(slices.Clone(entries), "web:/internal/y"), opaque, named, ops, cases, []string{"/internal/x/{}", "/internal/y"}); len(f) != 0 {
		t.Fatalf("a path an opaque route answers, named in its row: %v", f)
	}
	for want, c := range map[string]struct {
		mapping string
		web     []string
	}{
		"nothing serves tool:find_x":          {strings.Replace(good, "tool:find_x\tlistX\tx-1\t-\n", "", 1), nil},
		"is in no inventory file":             {good + "tool:gone\tlistX\tx-1\t-\n", nil},
		"has two rows":                        {good + "tool:find_x\tlistX\tx-1\t-\n", nil},
		"does not hold 4 fields":              {good + "tool:find_x\tlistX\n", nil},
		"the API has no operation listZ":      {strings.Replace(good, "tool:find_x\tlistX", "tool:find_x\tlistZ", 1), nil},
		"listX has no parameter radius":       {strings.Replace(good, "listX?at", "listX?radius", 1), nil},
		"no parity case x-9":                  {strings.Replace(good, "tool:find_x\tlistX\tx-1", "tool:find_x\tlistX\tx-9", 1), nil},
		"no parity case checks the operation": {strings.Replace(good, "tool:find_x\tlistX\tx-1", "tool:find_x\tlistX\ty-1", 1), nil},
		"no dated owner ruling":               {strings.Replace(good, "owner 2026-11-16: the site's own", "the site's own", 1), nil},
		"which no route of routes.tsv":        {good, []string{"/internal/x/{}/more"}},
		"names the opaque route":              {good, []string{"/internal/y"}},
	} {
		f, _ := inventoryFindings(entries, routes, c.mapping, ops, cases, c.web)
		if got := strings.Join(f, "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}

func TestAPIOperationsReadsSharedParameters(t *testing.T) {
	req.Covers(t, "REL-015")
	ops, err := apiOperations([]byte(`{"paths":{"/v1/x/{id}":{"parameters":[{"$ref":"#/components/parameters/id"}],"get":{"operationId":"getX","parameters":[{"name":"lang"}]}}}}`))
	if err != nil || !slices.Equal(ops["getX"].params, []string{"id", "lang"}) {
		t.Fatalf("%v %v", ops, err)
	}
	if _, err := apiOperations([]byte(`{"paths":{"/x":{"get":"no object"}}}`)); err == nil {
		t.Fatal("an operation that is no object was read")
	}
	if _, err := apiOperations([]byte(`[]`)); err == nil {
		t.Fatal("a document that is no object was read")
	}
}
