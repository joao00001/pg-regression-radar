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

import "sort"

// OptionKind is the type of a panel option.
type OptionKind string

const (
	OptInt  OptionKind = "int"
	OptEnum OptionKind = "enum"
	OptBool OptionKind = "bool"
)

// Option describes one setting a panel type takes.
type Option struct {
	Name string     `json:"name"`
	Kind OptionKind `json:"kind"`
	// Label is what the settings pane calls it; Group is the section it sits in
	// ("Visualization", "Data", "Display"). The option named viz is the type's
	// choice of visualization and is drawn as a picker.
	Label string `json:"label"`
	Group string `json:"group"`
	Doc   string `json:"doc"`
	// Min and Max bound an int option.
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
	// Values lists an enum option's choices; Default is the first or the stated one.
	Values []string `json:"values,omitempty"`
	// Labels name the choices of an enum, in the same order as Values.
	Labels  []string `json:"labels,omitempty"`
	Default any      `json:"default"`
	// When, as "name=value", says the option only applies while another option of
	// the same panel has that value (the table's columns matter only for the table),
	// so the settings pane shows it only then.
	When string `json:"when,omitempty"`
}

func (o Option) when(cond string) Option {
	o.When = cond
	return o
}

// PanelType is something a board can hold.
type PanelType struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Doc   string `json:"doc"`
	// Help is what the info button on the panel says: what it shows and how to read
	// it, in a few sentences. A panel's own description in the file replaces it.
	Help string `json:"help"`
	// MinW and MinH are the smallest size, in cells, that still shows its data.
	MinW int `json:"minW"`
	MinH int `json:"minH"`
	// W and H are the size it gets when added.
	W int `json:"w"`
	H int `json:"h"`
	// FollowsDeploy is true for a panel that shows the picked deploy, so the focus
	// view offers a deploy picker beside it.
	FollowsDeploy bool `json:"followsDeploy,omitempty"`
	// Query lists the fields this type's query may use; none means it has no query.
	Query   []string `json:"query,omitempty"`
	Options []Option `json:"options,omitempty"`
}

func limit(label, doc string, def, max int) Option {
	return Option{Name: "limit", Kind: OptInt, Label: label, Group: "Data", Doc: doc, Min: 1, Max: max, Default: def}
}

// choice is an enum option; the first value is the default.
func choice(name, label, group, doc string, values, labels []string) Option {
	return Option{Name: name, Kind: OptEnum, Label: label, Group: group, Doc: doc, Values: values, Labels: labels, Default: values[0]}
}

// viz is the type's choice of how to draw it; the first is the default.
func viz(doc string, values, labels []string) Option {
	return choice("viz", "Visualization", "Visualization", doc, values, labels)
}

// toggle is a yes/no option in the Display group.
func toggle(name, label, doc string, def bool) Option {
	return Option{Name: name, Kind: OptBool, Label: label, Group: "Display", Doc: doc, Default: def}
}

// The fields a panel's query may use, by what the panel shows.
var (
	// A list of deploys is filtered by what describes a deploy.
	deployList = []string{"app", "namespace", "state", "grade"}
	// A panel on one deploy: which one (the newest that matches, or a given id).
	oneDeploy = []string{"deploy", "app", "namespace", "state", "grade"}
	// A panel that draws one database query of that deploy.
	oneQuery = []string{"deploy", "app", "namespace", "state", "grade", "query", "statement", "table"}
	// A panel that lists several of that deploy's database queries.
	someQueries = oneQuery
)

var catalog = []PanelType{
	{Type: "summary", Title: "Summary", MinW: 4, MinH: 3, W: 12, H: 4, Query: deployList,
		Help: "The one-sentence answer to “is anything wrong?” for the deploys in the chosen time range. The bar has one segment per deploy, worst first, and the counts under it add up to the same number as the sentence. Colour only ever means state: regressed, acted on, being judged, healthy.",
		Doc:  "One sentence on what is happening now, and what to look at.",
		Options: []Option{
			toggle("bar", "Show the bar", "One segment per deploy, worst first.", true),
			toggle("counts", "Show the counts", "How many deploys are in each state, under the bar.", true),
		}},
	{Type: "radar-state", Title: "Radar state", MinW: 3, MinH: 1, W: 12, H: 2,
		Help: "Whether the radar itself can be trusted right now: how fresh its data is, whether it may act or only watch, whether the circuit breaker is open, whether the Postgres log is flowing, and which namespaces it covers. If something here is off, read the panels below with care.",
		Doc:  "Whether the radar is watching, which source it reads and how fresh that is."},
	{Type: "deploy-cards", Title: "Deploys", MinW: 3, MinH: 4, W: 3, H: 18, Query: deployList,
		Help: "Every deploy the radar tracked in the chosen time range, with the state the radar reached and one sentence on why. Click one to make the panels that follow a deploy show it.",
		Doc:  "The deploys in the window, newest first; the one you pick drives the panels that follow a deploy.",
		Options: []Option{
			viz("How each deploy is drawn.", []string{"cards", "list"}, []string{"Cards", "Compact list"}),
			choice("show", "Show", "Data", "Which deploys to list.", []string{"all", "problems", "watching", "healthy"}, []string{"All deploys", "Regressed or acted on", "Still being judged", "Healthy"}),
			choice("sort", "Order", "Data", "How the deploys are ordered.", []string{"newest", "worst"}, []string{"Newest first", "Worst first"}),
			limit("Most deploys to show", "How many deploys to show.", 12, 50),
		}},
	{Type: "deploy-head", Title: "Deploy", MinW: 4, MinH: 3, W: 9, H: 3, FollowsDeploy: true, Query: oneDeploy,
		Help: "The deploy you picked: what it is, whether the radar judged it a regression, what it did about it, and when it will look again. “Open as a case” shows the same deploy full width.",
		Doc:  "The picked deploy: its grade, what the radar decided and since when."},
	{Type: "canary-chart", Title: "Canary vs control", MinW: 4, MinH: 7, W: 6, H: 10, FollowsDeploy: true, Query: oneQuery,
		Help: "How long the chosen query took over time on the canary pods (red) against the stable pods (blue), with the deploy marked. If the red line leaves the blue one at the marker, the deploy is what changed. Where there are no stable pods to compare, the chart shows the query's own history and its level before the deploy.",
		Doc:  "The picked deploy's measurement over time, canary against control, with the deploy marked.",
		Options: []Option{
			viz("How the measurement is drawn.", []string{"chart", "table"}, []string{"Line chart", "Table"}),
			choice("unit", "Query to open on", "Data", "Which query to chart first: the one that most explains the verdict (the engine's own order, and the ones it keeps a series for), or the flagged query that moved furthest from 1.0, which may have no series.", []string{"first", "worst"}, []string{"The one that explains the verdict", "The one that moved the most"}),
			toggle("baseline", "Show the level before the deploy", "A dashed line at the average before the deploy.", true).when("viz=chart"),
			toggle("samples", "Show how many calls each point rests on", "The small bars under the chart: thin evidence has short bars.", true).when("viz=chart"),
			toggle("zero", "Start the axis at zero", "Off fits the axis to the data, which makes small changes look larger.", true).when("viz=chart"),
		}},
	{Type: "figures", Title: "Figures", MinW: 2, MinH: 5, W: 3, H: 10, FollowsDeploy: true, Query: oneDeploy,
		Help: "The picked deploy in a few numbers: the evidence grade (A to D), how much database time the deploy added, how much of the slowdown is attributed to the deploy, how many queries regressed out of how many were tested, and how much of the load those queries cover.",
		Doc:  "The picked deploy in numbers: ratio, interval, evidence and how many units moved.",
		Options: []Option{
			toggle("grade", "Show the evidence grade", "How much weight the evidence can bear, from A to D.", true),
			toggle("impact", "Show the added database time", "How much database time the deploy added.", true),
			toggle("attribution", "Show the share attributed to the deploy", "How much of the slowdown is put down to the deploy.", true),
			toggle("regressed", "Show the queries regressed", "How many queries regressed out of how many were tested.", true),
			toggle("coverage", "Show the coverage", "How much of the load the tested queries cover.", true),
		}},
	{Type: "why", Title: "Why this grade", MinW: 4, MinH: 3, W: 9, H: 4, FollowsDeploy: true, Query: oneDeploy,
		Help: "The radar's reasoning for the grade, in plain words: what kind of comparison it could make, what the strongest evidence was, and what limited the grade.",
		Doc:  "The reasoning behind the grade, in plain words, and what would change it."},
	{Type: "evidence-table", Title: "Evidence", MinW: 6, MinH: 4, W: 9, H: 9, FollowsDeploy: true, Query: someQueries,
		Help: "Each query the radar tested for the picked deploy: how much slower it got (the 95% interval is on hover), its share of the database's time, any flags that limited the grade, and how many observations stand behind it. Click a query to chart it.",
		Doc:  "Per query: how it moved, with the interval around it.",
		Options: []Option{
			viz("How the queries are drawn.", []string{"table", "ratio"}, []string{"Table", "Ratio chart"}),
			choice("show", "Show", "Data", "Which queries to list.", []string{"all", "flagged"}, []string{"All tested queries", "Only flagged queries"}),
			choice("sort", "Order", "Data", "How the queries are ordered.", []string{"engine", "impact", "slowdown", "name"}, []string{"The radar's order", "Share of database time", "Slowdown", "Name"}).when("viz=table"),
			limit("Most queries to list", "How many queries to list.", 25, 200),
			toggle("flags", "Show the flags", "What limited the grade of each query.", true).when("viz=table"),
			toggle("counts", "Show the observations before and after", "How many observations stand behind each query.", true).when("viz=table"),
		}},
	{Type: "ratio-chart", Title: "Ratio per query", MinW: 4, MinH: 6, W: 5, H: 11, FollowsDeploy: true, Query: someQueries,
		Help: "Every tested query on one axis: the dot is how many times slower (or faster) it got and the bar is the 95% interval. A bar that stays clear of the line at 1.0 means the query really moved. Click one to chart it.",
		Doc:  "Every query's ratio with its interval on one axis, and a line at 1.0 where nothing changed.",
		Options: []Option{
			choice("show", "Show", "Data", "Which queries to draw.", []string{"all", "flagged"}, []string{"All tested queries", "Only flagged queries"}),
			limit("Most queries to draw", "How many queries to draw.", 12, 100),
		}},
	{Type: "evaluations", Title: "Evaluations", MinW: 3, MinH: 5, W: 4, H: 11, FollowsDeploy: true, Query: someQueries,
		Help: "Each time the radar judged the picked deploy, in order. Pick two to see what changed between them. An older judgement keeps only the queries that were flagged then.",
		Doc:  "The evaluations the radar made of the picked deploy, in order; pick two to compare."},
	{Type: "ledger", Title: "Ledger", MinW: 3, MinH: 3, W: 9, H: 5, FollowsDeploy: true, Query: oneDeploy,
		Help: "What the radar did about the picked deploy, step by step and with the time of each: the action it wanted, what it did, and whether it checked that it worked.",
		Doc:  "What the radar did about the picked deploy, step by step, with the time of each."},
	{Type: "decision-feed", Title: "Activity", MinW: 3, MinH: 3, W: 3, H: 13, Query: deployList,
		Help: "Decisions and actions across every deploy, newest first, updating as they happen. Red marks a regression; the deploy names open that deploy.",
		Doc:  "Decisions and actions across all deploys as they happen.",
		Options: []Option{
			viz("How the activity is drawn.", []string{"feed", "table"}, []string{"Feed", "Table"}),
			choice("show", "Show", "Data", "What to list.", []string{"all", "problems", "actions"}, []string{"Everything", "Only problems", "Only actions"}),
			limit("Most entries to show", "How many entries to show.", 20, 200),
		}},
	{Type: "safety", Title: "Safety", MinW: 3, MinH: 3, W: 6, H: 10,
		Help: "What the radar is allowed to do for each watch: its mode (shadow only watches, enforce may act), whether acting is switched on, whether the circuit breaker has stopped it, and which namespaces it may act in.",
		Doc:  "What the radar may do here: whether it can abort, and which namespaces it may act in."},
	{Type: "source-health", Title: "Source health", MinW: 4, MinH: 4, W: 12, H: 8,
		Help: "Whether each source the radar reads is answering, per watch: query statistics and the Postgres log, how recent the newest data is, and what it means for the verdicts if one is missing.",
		Doc:  "Whether each source the radar reads is answering, how recent its last data is, and what is missing."},
}

var catalogByType = func() map[string]*PanelType {
	m := make(map[string]*PanelType, len(catalog))
	for i := range catalog {
		m[catalog[i].Type] = &catalog[i]
	}
	return m
}()

// Catalog returns every panel type, in the order the add menu shows them.
func Catalog() []PanelType { return append([]PanelType(nil), catalog...) }

// Lookup returns a panel type by name.
func Lookup(t string) (PanelType, bool) {
	p, ok := catalogByType[t]
	if !ok {
		return PanelType{}, false
	}
	return *p, true
}

// TypeNames lists the panel types, sorted.
func TypeNames() []string {
	out := make([]string, 0, len(catalog))
	for _, p := range catalog {
		out = append(out, p.Type)
	}
	sort.Strings(out)
	return out
}

func (p PanelType) option(name string) (Option, bool) {
	for _, o := range p.Options {
		if o.Name == name {
			return o, true
		}
	}
	return Option{}, false
}
