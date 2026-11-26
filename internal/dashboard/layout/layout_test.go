// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package layout

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

const minimal = `apiVersion: pgrr.io/v1
kind: Dashboard
boards:
  - id: now
    title: Now
    panels:
      - id: summary
        type: summary
        at: [0, 0, 12, 3]
`

func problemsOf(t *testing.T, src string) []Problem {
	t.Helper()
	d, ps := Parse([]byte(src))
	if len(ps) == 0 {
		t.Fatalf("expected problems, parsed %+v", d)
	}
	return ps
}

func wantProblem(t *testing.T, ps []Problem, line int, sub string) {
	t.Helper()
	for _, p := range ps {
		if strings.Contains(p.Msg, sub) && (line == 0 || p.Line == line) {
			return
		}
	}
	t.Fatalf("no problem containing %q at line %d in:\n%v", sub, line, ps)
}

func TestMinimalFileParsesWithDefaults(t *testing.T) {
	d, ps := Parse([]byte(minimal))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	b := d.Boards[0]
	if b.Range != DefaultRange {
		t.Fatalf("range default: %q", b.Range)
	}
	if b.Panels[0].At != [4]int{0, 0, 12, 3} {
		t.Fatalf("at: %v", b.Panels[0].At)
	}
}

func TestDefaultIsValidAndRoundTrips(t *testing.T) {
	def := Default()
	out, err := Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	back, ps := Parse(out)
	if len(ps) > 0 {
		t.Fatalf("the default must pass its own validator: %v\n%s", ps, out)
	}
	if !reflect.DeepEqual(def, back) {
		a, _ := json.Marshal(def)
		b, _ := json.Marshal(back)
		t.Fatalf("round trip changed it:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(out), "at: [0, 0, 12, 4]") {
		t.Fatalf("positions should be on one line:\n%s", out)
	}
}

func TestDefaultUsesOnlyCatalogTypesAndFitsTheGrid(t *testing.T) {
	for _, b := range Default().Boards {
		for _, p := range b.Panels {
			pt, ok := Lookup(p.Type)
			if !ok {
				t.Fatalf("%s/%s: unknown type %s", b.ID, p.ID, p.Type)
			}
			if p.At[2] < pt.MinW || p.At[3] < pt.MinH {
				t.Errorf("%s/%s is smaller than its type's minimum", b.ID, p.ID)
			}
		}
	}
	for _, pt := range Catalog() {
		if pt.W < pt.MinW || pt.H < pt.MinH || pt.W > Columns || pt.MinW < 1 || pt.MinH < 1 {
			t.Errorf("%s has an impossible size: %+v", pt.Type, pt)
		}
		for _, o := range pt.Options {
			if o.Default == nil {
				t.Errorf("%s.%s has no default", pt.Type, o.Name)
			}
		}
	}
}

func TestOptionsRoundTrip(t *testing.T) {
	d := Default()
	byID := func(d *Dashboard, id string) *Panel {
		for i := range d.Boards[0].Panels {
			if d.Boards[0].Panels[i].ID == id {
				return &d.Boards[0].Panels[i]
			}
		}
		t.Fatalf("no panel %q", id)
		return nil
	}
	byID(d, "deploys").Options = map[string]any{"limit": 7}
	byID(d, "canary").Options = map[string]any{"unit": "first"}
	out, _ := Marshal(d)
	back, ps := Parse(out)
	if len(ps) > 0 {
		t.Fatal(ps, string(out))
	}
	if byID(back, "deploys").Options["limit"] != 7 || byID(back, "canary").Options["unit"] != "first" {
		t.Fatalf("options lost: %+v", back.Boards[0].Panels)
	}
}

func TestProblemsCarryLineNumbers(t *testing.T) {
	src := `apiVersion: pgrr.io/v1
kind: Dashboard
boards:
  - id: now
    panels:
      - id: a
        type: sumary
        at: [0, 0, 12, 3]
      - id: b
        type: summary
        at: [0, 0, 13, 3]
      - id: c
        type: summary
        at: [0, 1, 12, 3]
`
	ps := problemsOf(t, src)
	wantProblem(t, ps, 7, `unknown panel type "sumary" (did you mean "summary"?)`)
	wantProblem(t, ps, 11, "ends at column 13")
	wantProblem(t, ps, 14, "overlaps")
	if len(ps) != 3 {
		t.Fatalf("one problem per mistake, got %v", ps)
	}
}

func TestEveryKindOfMistake(t *testing.T) {
	head := "apiVersion: pgrr.io/v1\nkind: Dashboard\n"
	board := func(extra string) string {
		return head + "boards:\n  - id: now\n" + extra
	}
	panelAt := func(at string) string {
		return board("    panels:\n      - id: p\n        type: summary\n        at: " + at + "\n")
	}
	for _, tc := range []struct {
		name, src, want string
	}{
		{"empty", "", "empty"},
		{"comment only", "# nothing\n", "empty"},
		{"not yaml", "boards: [", "not valid YAML"},
		{"two documents", minimal + "---\nkind: x\n", "more than one"},
		{"a list at the top", "- a\n", "mapping"},
		{"no apiVersion", "kind: Dashboard\nboards: []\n", "apiVersion is missing"},
		{"future apiVersion", "apiVersion: pgrr.io/v2\nkind: Dashboard\nboards: []\n", "newer release"},
		{"wrong kind", "apiVersion: pgrr.io/v1\nkind: Board\nboards: []\n", "kind must be Dashboard"},
		{"no kind", "apiVersion: pgrr.io/v1\nboards: []\n", "kind is missing"},
		{"no boards", head, "boards is missing"},
		{"empty boards", head + "boards: []\n", "at least one board"},
		{"unknown top field", minimal + "theme: dark\n", `unknown field "theme"`},
		{"typo in field", head + "boardz: []\n", `did you mean "boards"`},
		{"board id missing", head + "boards:\n  - title: x\n    panels: []\n", "has no id"},
		{"board id bad", head + "boards:\n  - id: Now Board\n", "must be lowercase"},
		{"board id twice", head + "boards:\n  - id: a\n    panels: []\n  - id: a\n    panels: []\n", "two boards with id"},
		{"range bad", board("    range: yesterday\n"), "like 90m, 6h or 7d"},
		{"range too long", board("    range: 400d\n"), "longer than the 90d"},
		{"range zero", board("    range: 0h\n"), "like 90m, 6h or 7d"},
		{"no panels", board(""), "has no panels"},
		{"panels not list", board("    panels: x\n"), "must be a list"},
		{"panel without id", board("    panels:\n      - type: summary\n        at: [0,0,12,3]\n"), "has no id"},
		{"panel without type", board("    panels:\n      - id: p\n        at: [0,0,12,3]\n"), "has no type"},
		{"panel without at", board("    panels:\n      - id: p\n        type: summary\n"), "no position"},
		{"duplicate panel id", board("    panels:\n      - {id: p, type: summary, at: [0,0,12,3]}\n      - {id: p, type: summary, at: [0,3,12,3]}\n"), `id "p"`},
		{"at wrong length", panelAt("[0, 0, 12]"), "four whole numbers"},
		{"at not a number", panelAt("[0, 0, wide, 3]"), "not one"},
		{"at float", panelAt("[0, 0, 12.5, 3]"), "not one"},
		{"at negative", panelAt("[-1, 0, 12, 3]"), "cannot be negative"},
		{"at zero size", panelAt("[0, 0, 0, 3]"), "at least 1 cell"},
		{"at too wide", panelAt("[1, 0, 12, 3]"), "ends at column 13"},
		{"at too deep", panelAt("[0, 59, 12, 3]"), "at most 60 rows"},
		{"below minimum", panelAt("[0, 0, 2, 1]"), "needs at least 4 wide and 3 high"},
		{"unknown panel field", board("    panels:\n      - {id: p, type: summary, at: [0,0,12,3], color: red}\n"), `unknown field "color"`},
		{"options on a type without any", board("    panels:\n      - {id: p, type: radar-state, at: [0,0,12,2], options: {limit: 3}}\n"), "takes no options"},
		{"unknown option", board("    panels:\n      - {id: p, type: deploy-cards, at: [0,0,12,4], options: {limt: 3}}\n"), `did you mean "limit"`},
		{"option out of range", board("    panels:\n      - {id: p, type: deploy-cards, at: [0,0,12,4], options: {limit: 500}}\n"), "between 1 and 50"},
		{"option wrong type", board("    panels:\n      - {id: p, type: deploy-cards, at: [0,0,12,4], options: {limit: many}}\n"), "whole number"},
		{"enum bad", board("    panels:\n      - {id: p, type: canary-chart, at: [0,0,8,9], options: {unit: biggest}}\n"), "one of first, worst"},
		{"title too long", board("    panels:\n      - {id: p, type: summary, at: [0,0,12,3], title: " + strings.Repeat("x", 81) + "}\n"), "at most 80"},
		{"duplicate key", board("    title: a\n    title: b\n    panels: []\n"), "given twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(tc.src))
			if len(ps) == 0 {
				t.Fatal("expected a problem")
			}
			found := false
			for _, p := range ps {
				found = found || strings.Contains(p.Msg, tc.want)
			}
			if !found {
				t.Fatalf("want a problem containing %q, got %v", tc.want, ps)
			}
		})
	}
}

func TestTooManyBoardsAndPanels(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n")
	for i := 0; i < MaxBoards+1; i++ {
		sb.WriteString("  - id: b" + string(rune('a'+i)) + "\n    panels: []\n")
	}
	wantProblem(t, problemsOf(t, sb.String()), 0, "at most 12 boards")

	sb.Reset()
	sb.WriteString("apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: a\n    panels:\n")
	for i := 0; i < MaxPanelsPerBoard+1; i++ {
		sb.WriteString("      - {id: p" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ", type: radar-state, at: [0, " + itoa(i%MaxRows) + ", 3, 1]}\n")
	}
	wantProblem(t, problemsOf(t, sb.String()), 0, "at most 40 panels")
}

func TestProblemsAreCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: a\n    panels:\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("      - {id: p" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ", type: nope, at: [0,0,1,1]}\n")
	}
	if ps := problemsOf(t, sb.String()); len(ps) > maxProblems {
		t.Fatalf("%d problems", len(ps))
	}
}

func TestMigrationsRunInOrderAndKeepPositions(t *testing.T) {
	saved := migrations
	defer func() { migrations = saved }()
	// A made-up older format where panels used "kind" instead of "type".
	migrations = []migration{
		{from: "pgrr.io/v0", to: "pgrr.io/v0.5", apply: func(root *yaml.Node, ps *problems) {}},
		{from: "pgrr.io/v0.5", to: APIVersion, apply: func(root *yaml.Node, ps *problems) {
			walkRename(root, "kind", "type", 2)
		}},
	}
	src := `apiVersion: pgrr.io/v0
kind: Dashboard
boards:
  - id: now
    panels:
      - id: s
        kind: summary
        at: [0, 0, 12, 3]
`
	d, ps := Parse([]byte(src))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if d.APIVersion != APIVersion || d.Boards[0].Panels[0].Type != "summary" {
		t.Fatalf("migrated wrong: %+v", d)
	}
	// A problem after migration still points at the author's line.
	_, ps = Parse([]byte(strings.Replace(src, "[0, 0, 12, 3]", "[0, 0, 13, 3]", 1)))
	wantProblem(t, ps, 8, "ends at column 13")
}

// walkRename renames key at or below depth, to stand in for a real migration.
func walkRename(n *yaml.Node, from, to string, minDepth int) {
	var rec func(n *yaml.Node, depth int)
	rec = func(n *yaml.Node, depth int) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if depth >= minDepth && n.Content[i].Value == from {
					n.Content[i].Value = to
				}
			}
		}
		for _, c := range n.Content {
			rec(c, depth+1)
		}
	}
	rec(n, 0)
}

func TestDescriptionRoundTripsAndIsBounded(t *testing.T) {
	d := Default()
	want := "What this shows: the deploys, with a colon, \"quotes\" and a # sign"
	d.Boards[0].Panels[0].Description = want
	out, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	back, ps := Parse(out)
	if len(ps) > 0 {
		t.Fatal(ps, string(out))
	}
	if got := back.Boards[0].Panels[0].Description; got != want {
		t.Fatalf("description %q, want %q", got, want)
	}
	// It is optional: the default writes none.
	if out, _ := Marshal(Default()); strings.Contains(string(out), "description") {
		t.Fatal("default preset should carry no descriptions")
	}
	bad := "apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: b\n    panels:\n      - {id: p, type: summary, at: [0,0,12,3], description: " + strings.Repeat("x", MaxDescription+1) + "}\n"
	if _, ps := Parse([]byte(bad)); len(ps) == 0 || !strings.Contains(ps[0].Msg, "at most 300") {
		t.Fatalf("too long should be reported: %+v", ps)
	}
}

func TestEveryPanelTypeHasHelpForTheInfoButton(t *testing.T) {
	for _, pt := range catalog {
		n := len([]rune(pt.Help))
		if n < 60 || n > 420 {
			t.Errorf("%s: help is %d characters; the info button wants a few sentences (60 to 420)", pt.Type, n)
		}
		if pt.Help == pt.Doc {
			t.Errorf("%s: help repeats doc", pt.Type)
		}
	}
}

func TestEveryOptionIsDescribedForTheSettingsPane(t *testing.T) {
	for _, pt := range catalog {
		seen := map[string]bool{}
		vizCount := 0
		for _, o := range pt.Options {
			id := pt.Type + "." + o.Name
			if seen[o.Name] {
				t.Errorf("%s: declared twice", id)
			}
			seen[o.Name] = true
			if o.Label == "" || o.Group == "" || len([]rune(o.Doc)) < 10 {
				t.Errorf("%s: the settings pane needs a label, a group and a sentence of help (%q, %q, %q)", id, o.Label, o.Group, o.Doc)
			}
			if o.Name == "viz" {
				vizCount++
				if o.Group != "Visualization" || len(o.Values) < 2 {
					t.Errorf("%s: a visualization option needs the Visualization group and at least two choices", id)
				}
			}
			switch o.Kind {
			case OptEnum:
				if len(o.Values) < 2 || len(o.Labels) != len(o.Values) {
					t.Errorf("%s: %d values but %d labels", id, len(o.Values), len(o.Labels))
				}
				def, ok := o.Default.(string)
				if !ok || !contains(o.Values, def) {
					t.Errorf("%s: default %v is not one of %v", id, o.Default, o.Values)
				}
			case OptInt:
				def, ok := o.Default.(int)
				if !ok || def < o.Min || def > o.Max {
					t.Errorf("%s: default %v outside %d..%d", id, o.Default, o.Min, o.Max)
				}
			case OptBool:
				if _, ok := o.Default.(bool); !ok {
					t.Errorf("%s: default %v is not a bool", id, o.Default)
				}
			}
		}
		for _, o := range pt.Options {
			if o.When == "" {
				continue
			}
			name, val, ok := strings.Cut(o.When, "=")
			var dep *Option
			for i := range pt.Options {
				if pt.Options[i].Name == name {
					dep = &pt.Options[i]
				}
			}
			if !ok || dep == nil || dep.Kind != OptEnum || !contains(dep.Values, val) {
				t.Errorf("%s.%s: when %q must name an enum option of the same panel and one of its values", pt.Type, o.Name, o.When)
			}
		}
		if vizCount > 1 {
			t.Errorf("%s: more than one visualization option", pt.Type)
		}
	}
}

func TestDeployFollowingTypesAreTheOnesThatReadOneDeploy(t *testing.T) {
	want := map[string]bool{"deploy-head": true, "canary-chart": true, "figures": true, "why": true, "evidence-table": true, "ratio-chart": true, "evaluations": true, "ledger": true}
	for _, pt := range catalog {
		if pt.FollowsDeploy != want[pt.Type] {
			t.Errorf("%s: followsDeploy = %v", pt.Type, pt.FollowsDeploy)
		}
	}
}

func TestBundleJSONCarriesCatalogAndGrid(t *testing.T) {
	b, err := NewBundle(Default(), "default").JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Layout  struct{ Boards []struct{ ID string } }
		Source  string
		Catalog []struct{ Type string }
		Grid    Grid
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Layout.Boards) != 3 || got.Source != "default" || len(got.Catalog) != len(catalog) || got.Grid.Columns != 12 {
		t.Fatalf("%s", b)
	}
}

func TestEditDistanceSuggestions(t *testing.T) {
	if s := nearest("evidnce-table", TypeNames()); s != "evidence-table" {
		t.Fatalf("got %q", s)
	}
	if s := nearest("zzzzzzzz", TypeNames()); s != "" {
		t.Fatalf("far words get no suggestion, got %q", s)
	}
}

// The UI's tests read these files, so what Go serves and what the UI exports
// are checked against the same bytes. Regenerate with:
//
//	go test ./internal/dashboard/layout -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden files")

func TestGoldenFiles(t *testing.T) {
	yamlOut, err := Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := NewBundle(Default(), "default").JSON()
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, bundle, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	// Titles and descriptions the browser's Export must write exactly as Go does.
	type textCase struct {
		Text   string          `json:"text"`
		Layout json.RawMessage `json:"layout"`
		YAML   string          `json:"yaml"`
	}
	var cases []textCase
	for _, s := range []string{
		"Shows the deploys", `app:checkout state:regressed`, `query:"SELECT on orders"`, "What this shows: a colon", `has "quotes" and # hash`, "it's fine", "Trailing space ", "yes", "Null",
		"Línea con acentos é", "multi\nline", "- starts with dash", "100% of deploys", `back\slash`, "tab\there",
		"A long description that goes on and on and on and on and on and on and on and on and on and on, well past eighty columns: yes.",
	} {
		d := Default()
		d.Boards[0].Title, d.Boards[0].Panels[0].Title, d.Boards[0].Panels[0].Description, d.Boards[0].Panels[0].Query = s, s, s, s
		y, err := Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		lj, _ := json.Marshal(d)
		cases = append(cases, textCase{s, lj, string(y)})
	}
	var textJSON bytes.Buffer
	enc := json.NewEncoder(&textJSON)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cases); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]byte{"testdata/default.yaml": yamlOut, "testdata/default.bundle.json": pretty.Bytes(), "testdata/text-cases.json": textJSON.Bytes()} {
		if *update {
			if err := os.WriteFile(name, got, 0o644); err != nil { // #nosec G306 -- test data
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s is out of date; run go test ./internal/dashboard/layout -run Golden -update", name)
		}
	}
}
