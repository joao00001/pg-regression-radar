package layout

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type queryCase struct {
	In    string  `json:"in"`
	Terms [][]any `json:"terms"`
	Error *struct {
		Code string `json:"code"`
		Col  int    `json:"col"`
	} `json:"error"`
}

// The browser parses the same grammar; both are held to this file.
func TestQueryGrammarAgreesWithTheSharedCases(t *testing.T) {
	b, err := os.ReadFile("testdata/query-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []queryCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		terms, qe := ParseQuery(c.In)
		if c.Error != nil {
			if qe == nil || qe.Code != c.Error.Code || qe.Col != c.Error.Col {
				t.Errorf("%q: want error %v, got %v", c.In, *c.Error, qe)
			}
			continue
		}
		if qe != nil {
			t.Errorf("%q: unexpected error %v", c.In, qe)
			continue
		}
		if len(terms) != len(c.Terms) {
			t.Errorf("%q: %d terms, want %d", c.In, len(terms), len(c.Terms))
			continue
		}
		for i, w := range c.Terms {
			if terms[i].Field != w[0] || terms[i].Value != w[1] || terms[i].Col != int(w[2].(float64)) {
				t.Errorf("%q term %d: got %+v, want %v", c.In, i, terms[i], w)
			}
		}
	}
}

func TestQueryLengthIsBounded(t *testing.T) {
	if _, qe := ParseQuery("app:" + strings.Repeat("x", MaxQuery-3)); qe == nil || qe.Code != "too-long" {
		t.Fatalf("got %v", qe)
	}
	if _, qe := ParseQuery("app:" + strings.Repeat("x", MaxQuery-4)); qe != nil {
		t.Fatalf("exactly the limit should do: %v", qe)
	}
}

func TestQueryIsCheckedAgainstWhatThePanelTakes(t *testing.T) {
	for _, tc := range []struct{ typ, q, want string }{
		{"deploy-cards", "app:checkout state:regressed grade:a", ""},
		{"deploy-cards", "state:broken", "state is one of regressed, acted, watching, healthy, inconclusive"},
		{"deploy-cards", "grade:E", "grade is one of A, B, C, D"},
		{"deploy-cards", "state:reg*", "exact values"},
		{"deploy-cards", "query:x", `no "query" field`},
		{"deploy-cards", "aap:x", `did you mean "app"`},
		{"canary-chart", `deploy:abc query:"SELECT on orders" table:orders*`, ""},
		{"canary-chart", "deploy:a deploy:b", "give deploy: once"},
		{"canary-chart", "deploy:a*", "exact id"},
		{"radar-state", "app:x", "takes no query"},
		{"safety", "", ""},
	} {
		pt, ok := Lookup(tc.typ)
		if !ok {
			t.Fatal(tc.typ)
		}
		terms, qe := ParseQuery(tc.q)
		if qe != nil {
			t.Fatal(qe)
		}
		msgs := strings.Join(ValidateQuery(pt, terms), "; ")
		if tc.want == "" && msgs != "" || tc.want != "" && !strings.Contains(msgs, tc.want) {
			t.Errorf("%s %q: want %q, got %q", tc.typ, tc.q, tc.want, msgs)
		}
	}
}

func TestEveryQueryFieldOfACatalogTypeExists(t *testing.T) {
	for _, pt := range catalog {
		for _, f := range pt.Query {
			if _, ok := queryField(f); !ok {
				t.Errorf("%s: unknown query field %q", pt.Type, f)
			}
		}
	}
	for _, f := range queryFields {
		if f.Doc == "" {
			t.Errorf("%s has no doc", f.Name)
		}
	}
}

func TestAFileWithAQueryRoundTripsAndBadOnesPointAtTheLine(t *testing.T) {
	src := "apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: now\n    title: Now\n    range: 6h\n    panels:\n      - id: p\n        type: deploy-cards\n        at: [0, 0, 3, 8]\n        query: \"app:checkout state:regressed\"\n"
	d, ps := Parse([]byte(src))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := d.Boards[0].Panels[0].Query; got != "app:checkout state:regressed" {
		t.Fatalf("query = %q", got)
	}
	out, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Fatalf("round trip differs:\n%s", out)
	}
	_, ps = Parse([]byte(strings.Replace(src, "state:regressed", "state:nope", 1)))
	if len(ps) != 1 || ps[0].Line != 11 || !strings.Contains(ps[0].Msg, "state is one of") {
		t.Fatalf("problems: %v", ps)
	}
	_, ps = Parse([]byte(strings.Replace(src, "app:checkout state:regressed", "checkout", 1)))
	if len(ps) != 1 || !strings.Contains(ps[0].Msg, "expected field:value") {
		t.Fatalf("problems: %v", ps)
	}
}
