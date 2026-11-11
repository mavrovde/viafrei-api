package parity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

func TestParseNamesTheCommitOnlyOfAnMCPBundle(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	mcp := strings.Replace(good, `"producer":"api",`, `"producer":"mcp","commit":"`+sha+`",`, 1)
	if b, err := parity.Parse([]byte(mcp), "1.2.3"); err != nil || b.Commit != sha {
		t.Fatalf("an mcp bundle with its commit: %+v, %v", b, err)
	}
	refused := map[string]string{
		"mcp without a commit":     strings.Replace(good, `"producer":"api"`, `"producer":"mcp"`, 1),
		"mcp with a short commit":  strings.Replace(mcp, sha, sha[:12], 1),
		"mcp with an upper commit": strings.Replace(mcp, sha, strings.ToUpper(sha), 1),
		"api naming a commit":      strings.Replace(good, `"producer":"api",`, `"producer":"api","commit":"`+sha+`",`, 1),
	}
	for name, raw := range refused {
		if _, err := parity.Parse([]byte(raw), "1.2.3"); !errors.Is(err, parity.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

const questions = `{"fixture":{"profile":"tiny","seed":1,"sha256":"ab"},
"cases":[{"id":"c1","operation":"listX","counterpart":"x search","request":"/v1/x","key":{"useClass":"consumer","scopes":["x"],"sources":["s"],"accepted":null},
"now":"2026-03-30T00:00:00Z"}]}`

func questionsFS(raw string) fstest.MapFS {
	return fstest.MapFS{parity.QuestionsFile: {Data: []byte(raw)}}
}

func TestLoadQuestionsFailsClosed(t *testing.T) {
	q, err := parity.LoadQuestions(questionsFS(questions))
	if err != nil || len(q.Cases) != 1 || q.Fixture.Seed != 1 {
		t.Fatalf("%+v, %v", q, err)
	}
	first := questions[strings.Index(questions, `{"id"`) : len(questions)-2]
	cases := map[string]struct {
		raw  string
		want error
	}{
		"not json":       {`{`, parity.ErrMalformed},
		"unknown member": {strings.Replace(questions, `"fixture"`, `"extra":1,"fixture"`, 1), parity.ErrMalformed},
		"no cases":       {questions[:strings.Index(questions, `"cases"`)] + `"cases":[]}`, parity.ErrEmpty},
		"no id":          {strings.Replace(questions, `"id":"c1"`, `"id":""`, 1), parity.ErrMalformed},
		"no operation":   {strings.Replace(questions, `"operation":"listX"`, `"operation":""`, 1), parity.ErrMalformed},
		"no request":     {strings.Replace(questions, `"request":"/v1/x"`, `"request":""`, 1), parity.ErrMalformed},
		"no now":         {strings.Replace(questions, `,`+"\n"+`"now":"2026-03-30T00:00:00Z"`, ``, 1), parity.ErrMalformed},
		"duplicate id":   {strings.Replace(questions, `}]}`, `},`+first+`]}`, 1), parity.ErrMalformed},
	}
	for name, c := range cases {
		if _, err := parity.LoadQuestions(questionsFS(c.raw)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if _, err := parity.LoadQuestions(fstest.MapFS{}); err == nil {
		t.Error("no questions file was read as questions")
	}
}

func TestAnswersNamesEveryWayABundleDiffersFromTheQuestions(t *testing.T) {
	q, err := parity.LoadQuestions(questionsFS(questions))
	if err != nil {
		t.Fatal(err)
	}
	b, err := parity.Parse([]byte(good), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if d := parity.Answers(b, q); len(d) != 0 {
		t.Fatalf("the bundle answers the questions, got %v", d)
	}
	mutate := map[string]struct {
		edit func(*parity.Bundle)
		want string
	}{
		"other fixture":     {func(b *parity.Bundle) { b.Fixture.Seed = 2 }, "fixture:"},
		"asked differently": {func(b *parity.Bundle) { b.Cases[0].Request = "/v1/x?limit=1" }, "case c1: asked differently"},
		"other key":         {func(b *parity.Bundle) { b.Cases[0].Key.Sources = []string{"t"} }, "case c1: asked differently"},
		"other now":         {func(b *parity.Bundle) { b.Cases[0].Now = b.Cases[0].Now.Add(1) }, "case c1: asked differently"},
		"not a question":    {func(b *parity.Bundle) { b.Cases[0].ID = "c2" }, "case c2: not a question"},
		"question left out": {func(b *parity.Bundle) { b.Cases[0].ID = "c2" }, "question c1: no case answers it"},
	}
	for name, m := range mutate {
		c := b
		c.Cases = slices.Clone(b.Cases)
		m.edit(&c)
		d := parity.Answers(c, q)
		if !slices.ContainsFunc(d, func(s string) bool { return strings.HasPrefix(s, m.want) }) {
			t.Errorf("%s: %v, want a line starting %q", name, d, m.want)
		}
	}
}
