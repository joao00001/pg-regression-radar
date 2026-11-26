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
	"regexp"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

func panel(id, typ string, x, y, w, h int) Panel {
	return Panel{ID: id, Type: typ, At: [4]int{x, y, w, h}}
}

// Default is the dashboard you get without a file: what is happening now, one
// deploy in depth, and whether the radar can be trusted. The deploys list is a
// column on the left, and everything about the picked deploy sits to its right.
func Default() *Dashboard {
	return &Dashboard{
		APIVersion: APIVersion, Kind: Kind,
		Boards: []Board{
			{ID: "now", Title: "Now", Range: "6h", Panels: []Panel{
				panel("summary", "summary", 0, 0, 12, 4),
				panel("state", "radar-state", 0, 4, 12, 2),
				panel("deploys", "deploy-cards", 0, 6, 3, 18),
				panel("activity", "decision-feed", 0, 24, 3, 13),
				panel("deploy", "deploy-head", 3, 6, 9, 4),
				panel("canary", "canary-chart", 3, 10, 6, 10),
				panel("figures", "figures", 9, 10, 3, 10),
				panel("why", "why", 3, 20, 9, 4),
				panel("evidence", "evidence-table", 3, 24, 9, 12),
				panel("ledger", "ledger", 3, 36, 9, 5),
			}},
			{ID: "case", Title: "Case", Range: "24h", Panels: []Panel{
				panel("deploys", "deploy-cards", 0, 0, 3, 16),
				panel("figures", "figures", 0, 16, 3, 10),
				panel("deploy", "deploy-head", 3, 0, 9, 4),
				panel("ratios", "ratio-chart", 3, 4, 5, 11),
				panel("evaluations", "evaluations", 8, 4, 4, 11),
				panel("why", "why", 3, 15, 9, 4),
				panel("evidence", "evidence-table", 3, 19, 9, 12),
				panel("ledger", "ledger", 3, 31, 9, 5),
			}},
			{ID: "health", Title: "Source health", Range: "6h", Panels: []Panel{
				panel("state", "radar-state", 0, 0, 12, 2),
				panel("sources", "source-health", 0, 2, 12, 11),
				panel("safety", "safety", 0, 13, 6, 17),
				panel("activity", "decision-feed", 6, 13, 6, 17),
			}},
		},
	}
}

// Marshal writes a dashboard as the YAML a person would write: grid positions on
// one line, options only when set.
func Marshal(d *Dashboard) ([]byte, error) {
	root := mapNode()
	add(root, "apiVersion", str(d.APIVersion))
	add(root, "kind", str(d.Kind))
	boards := &yaml.Node{Kind: yaml.SequenceNode}
	for _, b := range d.Boards {
		bn := mapNode()
		add(bn, "id", str(b.ID))
		add(bn, "title", text(b.Title))
		add(bn, "range", str(b.Range))
		panels := &yaml.Node{Kind: yaml.SequenceNode}
		for _, p := range b.Panels {
			pn := mapNode()
			add(pn, "id", str(p.ID))
			add(pn, "type", str(p.Type))
			at := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			for _, v := range p.At {
				at.Content = append(at.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: itoa(v)})
			}
			add(pn, "at", at)
			if p.Title != "" {
				add(pn, "title", text(p.Title))
			}
			if p.Description != "" {
				add(pn, "description", text(p.Description))
			}
			if p.Query != "" {
				add(pn, "query", text(p.Query))
			}
			if len(p.Options) > 0 {
				on := mapNode()
				for _, o := range orderedOptions(p) {
					add(on, o.name, o.node)
				}
				add(pn, "options", on)
			}
			panels.Content = append(panels.Content, pn)
		}
		add(bn, "panels", panels)
		boards.Content = append(boards.Content, bn)
	}
	add(root, "boards", boards)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

type namedNode struct {
	name string
	node *yaml.Node
}

// orderedOptions lists a panel's options in the catalog's order.
func orderedOptions(p Panel) []namedNode {
	var out []namedNode
	pt, _ := Lookup(p.Type)
	for _, o := range pt.Options {
		v, ok := p.Options[o.Name]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case int:
			out = append(out, namedNode{o.Name, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: itoa(x)}})
		case float64: // from JSON
			out = append(out, namedNode{o.Name, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: itoa(int(x))}})
		case bool:
			val := "false"
			if x {
				val = "true"
			}
			out = append(out, namedNode{o.Name, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: val}})
		case string:
			out = append(out, namedNode{o.Name, str(x)})
		}
	}
	return out
}

func mapNode() *yaml.Node     { return &yaml.Node{Kind: yaml.MappingNode} }
func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

var (
	plainText = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _./()-]*$`)
	reserved  = regexp.MustCompile(`(?i)^(true|false|yes|no|on|off|null|y|n|~)$`)
)

// text is a title or description: plain when that is safe and double-quoted
// otherwise. The browser's Export writes the same rule (see toYAML in layout.js),
// so what it exports and what `config dump` prints are the same bytes.
func text(s string) *yaml.Node {
	n := str(s)
	if !plainText.MatchString(s) || reserved.MatchString(s) || s != strings.TrimSpace(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

func add(m *yaml.Node, k string, v *yaml.Node) {
	m.Content = append(m.Content, str(k), v)
}

// Grid is the geometry the UI lays panels out on.
type Grid struct {
	Columns int `json:"columns"`
	// RowHeight and Gap are in CSS pixels. Panels meet at hairlines, so Gap is 0.
	RowHeight int `json:"rowHeight"`
	Gap       int `json:"gap"`
}

// Bundle is what /dashboard/layout serves: the layout to show, where it came
// from, and what the UI needs to lay it out and to let a person add panels.
type Bundle struct {
	Layout  *Dashboard  `json:"layout"`
	Source  string      `json:"source"` // "default" or the file's path
	Catalog []PanelType `json:"catalog"`
	// QueryFields are the fields a panel's query may use, with what each means.
	QueryFields []QueryField `json:"queryFields"`
	Grid        Grid         `json:"grid"`
}

// NewBundle wraps a validated dashboard for the UI.
func NewBundle(d *Dashboard, source string) Bundle {
	return Bundle{Layout: d, Source: source, Catalog: Catalog(), QueryFields: QueryFields(), Grid: Grid{Columns: Columns, RowHeight: 40, Gap: 0}}
}

// JSON encodes the bundle.
func (b Bundle) JSON() ([]byte, error) { return json.Marshal(b) }

func itoa(i int) string { return strconv.Itoa(i) }
