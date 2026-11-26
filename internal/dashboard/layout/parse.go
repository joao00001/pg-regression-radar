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
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"
)

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	rangeRe = regexp.MustCompile(`^([1-9][0-9]{0,3})([mhd])$`)
	// maxRange keeps a board from asking for more than the longest retention
	// worth charting.
	maxRange = 90 * 24 * 60 // minutes
)

// problems collects what is wrong, without stopping at the first.
type problems struct{ list []Problem }

const maxProblems = 50

func (p *problems) add(n *yaml.Node, path, format string, a ...any) {
	if len(p.list) >= maxProblems {
		return
	}
	pr := Problem{Path: path, Msg: fmt.Sprintf(format, a...)}
	if n != nil {
		pr.Line, pr.Col = n.Line, n.Column
	}
	p.list = append(p.list, pr)
}

// Parse reads a dashboard file. It returns every problem it finds, each with
// the line and column it is at, or the validated Dashboard when there are none.
func Parse(data []byte) (*Dashboard, []Problem) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, errEOF) || err.Error() == "EOF" {
			return nil, []Problem{{Msg: "the file is empty; `pg-regression-radar dashboard config dump` prints a complete one to start from"}}
		}
		return nil, []Problem{syntaxProblem(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, []Problem{{Line: extra.Line, Col: extra.Column, Msg: "the file has more than one YAML document; a dashboard is one document"}}
	}
	if len(doc.Content) == 0 {
		return nil, []Problem{{Msg: "the file is empty"}}
	}
	root := doc.Content[0]
	var ps problems
	if root.Kind != yaml.MappingNode {
		ps.add(root, "", "a dashboard is a mapping with apiVersion, kind and boards")
		return nil, ps.list
	}
	migrate(root, &ps)
	if len(ps.list) > 0 {
		return nil, ps.list
	}
	d := readDashboard(root, &ps)
	if len(ps.list) > 0 {
		return nil, ps.list
	}
	return d, nil
}

var errEOF = errors.New("EOF")

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

func syntaxProblem(err error) Problem {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	p := Problem{Msg: "not valid YAML: " + msg}
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		p.Line, _ = strconv.Atoi(m[1])
		p.Col = 1
	}
	return p
}

// fields returns the value node of each key in a mapping, reporting keys that
// are not in allowed (with the nearest allowed name when one is close) and keys
// given twice.
func fields(n *yaml.Node, path string, allowed []string, ps *problems) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n.Kind != yaml.MappingNode {
		ps.add(n, path, "%s must be a mapping with: %s", nameOf(path), strings.Join(allowed, ", "))
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !contains(allowed, k.Value) {
			msg := fmt.Sprintf("unknown field %q in %s", k.Value, nameOf(path))
			if s := nearest(k.Value, allowed); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			} else {
				msg += "; it takes: " + strings.Join(allowed, ", ")
			}
			ps.add(k, path+"."+k.Value, "%s", msg)
			continue
		}
		if _, dup := out[k.Value]; dup {
			ps.add(k, path+"."+k.Value, "%q is given twice in %s", k.Value, nameOf(path))
			continue
		}
		out[k.Value] = v
	}
	return out
}

func nameOf(path string) string {
	if path == "" {
		return "the file"
	}
	return path
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// nearest returns the allowed name within a small edit distance of s, if any.
func nearest(s string, allowed []string) string {
	best, bd := "", 3
	for _, a := range allowed {
		if d := edit(s, a); d < bd {
			best, bd = a, d
		}
	}
	return best
}

func edit(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

func scalar(n *yaml.Node, path, what string, ps *problems) (string, bool) {
	if n == nil {
		return "", false
	}
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		ps.add(n, path, "%s must be text", what)
		return "", false
	}
	return n.Value, true
}

func readDashboard(root *yaml.Node, ps *problems) *Dashboard {
	f := fields(root, "", []string{"apiVersion", "kind", "boards"}, ps)
	d := &Dashboard{APIVersion: APIVersion, Kind: Kind}
	if k, ok := scalar(f["kind"], "kind", "kind", ps); !ok {
		if f["kind"] == nil {
			ps.add(root, "kind", "kind is missing; it must be %s", Kind)
		}
	} else if k != Kind {
		ps.add(f["kind"], "kind", "kind must be %s, not %q", Kind, k)
	}
	bn := f["boards"]
	if bn == nil {
		ps.add(root, "boards", "boards is missing; a dashboard needs at least one board")
		return d
	}
	if bn.Kind != yaml.SequenceNode || len(bn.Content) == 0 {
		ps.add(bn, "boards", "boards must be a list with at least one board")
		return d
	}
	if len(bn.Content) > MaxBoards {
		ps.add(bn, "boards", "at most %d boards; this has %d", MaxBoards, len(bn.Content))
		return d
	}
	seen := map[string]bool{}
	for i, b := range bn.Content {
		d.Boards = append(d.Boards, readBoard(b, i, seen, ps))
	}
	return d
}

func readBoard(n *yaml.Node, i int, seen map[string]bool, ps *problems) Board {
	path := fmt.Sprintf("boards[%d]", i)
	b := Board{Range: DefaultRange}
	f := fields(n, path, []string{"id", "title", "range", "panels"}, ps)
	if id, ok := scalar(f["id"], path+".id", "a board's id", ps); ok {
		switch {
		case !idRe.MatchString(id):
			ps.add(f["id"], path+".id", "board id %q must be lowercase letters, digits and dashes, starting with a letter (at most 40)", id)
		case seen[id]:
			ps.add(f["id"], path+".id", "there are two boards with id %q; ids are what the address (#/%s) and saved layouts use, so each must be unique", id, id)
		default:
			seen[id] = true
			b.ID = id
		}
	} else if f["id"] == nil && n.Kind == yaml.MappingNode {
		ps.add(n, path+".id", "board %d has no id", i+1)
	}
	b.Title = b.ID
	if t, ok := scalar(f["title"], path+".title", "a board's title", ps); ok {
		if strings.TrimSpace(t) == "" || len(t) > 60 {
			ps.add(f["title"], path+".title", "a board's title must be 1 to 60 characters")
		} else {
			b.Title = t
		}
	}
	if r, ok := scalar(f["range"], path+".range", "range", ps); ok {
		if m := rangeRe.FindStringSubmatch(r); m == nil {
			ps.add(f["range"], path+".range", "range %q must be a number and m, h or d, like 90m, 6h or 7d", r)
		} else if rangeMinutes(m) > maxRange {
			ps.add(f["range"], path+".range", "range %q is longer than the 90d the dashboard looks back", r)
		} else {
			b.Range = r
		}
	}
	pn := f["panels"]
	switch {
	case pn == nil:
		ps.add(n, path+".panels", "board %q has no panels", b.ID)
	case pn.Kind != yaml.SequenceNode:
		ps.add(pn, path+".panels", "panels must be a list")
	case len(pn.Content) > MaxPanelsPerBoard:
		ps.add(pn, path+".panels", "at most %d panels on a board; %q has %d", MaxPanelsPerBoard, b.ID, len(pn.Content))
	default:
		ids := map[string]bool{}
		var nodes []*yaml.Node
		for j, p := range pn.Content {
			pp, at := readPanel(p, path, j, ids, ps)
			b.Panels = append(b.Panels, pp)
			nodes = append(nodes, at)
		}
		checkOverlap(b, nodes, path, ps)
	}
	return b
}

func rangeMinutes(m []string) int {
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "h":
		return n * 60
	case "d":
		return n * 24 * 60
	}
	return n
}

// readPanel returns the panel and the node to point at when its position is the problem.
func readPanel(n *yaml.Node, bpath string, j int, ids map[string]bool, ps *problems) (Panel, *yaml.Node) {
	path := fmt.Sprintf("%s.panels[%d]", bpath, j)
	var p Panel
	f := fields(n, path, []string{"id", "type", "at", "title", "description", "query", "options"}, ps)
	if id, ok := scalar(f["id"], path+".id", "a panel's id", ps); ok {
		switch {
		case !idRe.MatchString(id):
			ps.add(f["id"], path+".id", "panel id %q must be lowercase letters, digits and dashes, starting with a letter (at most 40)", id)
		case ids[id]:
			ps.add(f["id"], path+".id", "two panels on this board have id %q; each needs its own", id)
		default:
			ids[id] = true
			p.ID = id
		}
	} else if f["id"] == nil && n.Kind == yaml.MappingNode {
		ps.add(n, path+".id", "panel %d has no id", j+1)
	}

	var pt PanelType
	var known bool
	if t, ok := scalar(f["type"], path+".type", "a panel's type", ps); ok {
		pt, known = Lookup(t)
		if !known {
			msg := fmt.Sprintf("unknown panel type %q", t)
			if s := nearest(t, TypeNames()); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			} else {
				msg += "; the types are " + strings.Join(TypeNames(), ", ")
			}
			ps.add(f["type"], path+".type", "%s", msg)
		}
		p.Type = t
	} else if f["type"] == nil && n.Kind == yaml.MappingNode {
		ps.add(n, path+".type", "panel %q has no type; the types are %s", p.ID, strings.Join(TypeNames(), ", "))
	}

	p.At = readAt(f["at"], n, path, pt, known, ps)

	if t, ok := scalar(f["title"], path+".title", "a panel's title", ps); ok {
		if len(t) > 80 {
			ps.add(f["title"], path+".title", "a panel's title is at most 80 characters")
		} else {
			p.Title = t
		}
	}
	if t, ok := scalar(f["description"], path+".description", "a panel's description", ps); ok {
		if utf8.RuneCountInString(t) > MaxDescription {
			ps.add(f["description"], path+".description", "a panel's description is at most %d characters", MaxDescription)
		} else {
			p.Description = t
		}
	}
	if q, ok := scalar(f["query"], path+".query", "a panel's query", ps); ok {
		readQuery(f["query"], path+".query", q, pt, known, &p, ps)
	}
	if on := f["options"]; on != nil && known {
		p.Options = readOptions(on, path+".options", pt, ps)
	}
	atNode := f["at"]
	if atNode == nil {
		atNode = n
	}
	return p, atNode
}

func readAt(n, parent *yaml.Node, path string, pt PanelType, known bool, ps *problems) (at [4]int) {
	if n == nil {
		ps.add(parent, path+".at", "panel has no position; give at: [x, y, w, h] in grid cells (the grid is %d wide)", Columns)
		return at
	}
	if n.Kind != yaml.SequenceNode || len(n.Content) != 4 {
		ps.add(n, path+".at", "at must be four whole numbers: [x, y, w, h] in grid cells")
		return at
	}
	for i, c := range n.Content {
		v, err := strconv.Atoi(c.Value)
		if c.Kind != yaml.ScalarNode || c.Tag != "!!int" || err != nil {
			ps.add(c, path+".at", "at must be four whole numbers: [x, y, w, h]; %q is not one", c.Value)
			return at
		}
		at[i] = v
	}
	x, y, w, h := at[0], at[1], at[2], at[3]
	bad := true
	defer func() {
		if bad {
			at = [4]int{} // a position that does not hold is not compared with the others
		}
	}()
	switch {
	case x < 0 || y < 0:
		ps.add(n, path+".at", "x and y cannot be negative")
	case w < 1 || h < 1:
		ps.add(n, path+".at", "width and height must be at least 1 cell")
	case x+w > Columns:
		ps.add(n, path+".at", "this panel ends at column %d but the grid is %d wide (x + w must be at most %d)", x+w, Columns, Columns)
	case y+h > MaxRows:
		ps.add(n, path+".at", "this panel ends at row %d; a board is at most %d rows deep", y+h, MaxRows)
	case known && (w < pt.MinW || h < pt.MinH):
		ps.add(n, path+".at", "a %s panel needs at least %d wide and %d high to show its data; this is %d by %d", pt.Type, pt.MinW, pt.MinH, w, h)
	default:
		bad = false
	}
	return at
}

func readOptions(n *yaml.Node, path string, pt PanelType, ps *problems) map[string]any {
	names := make([]string, 0, len(pt.Options))
	for _, o := range pt.Options {
		names = append(names, o.Name)
	}
	if len(names) == 0 {
		if n.Kind != yaml.MappingNode || len(n.Content) > 0 {
			ps.add(n, path, "a %s panel takes no options", pt.Type)
		}
		return nil
	}
	f := fields(n, path, names, ps)
	out := map[string]any{}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o, _ := pt.option(k)
		v := f[k]
		switch o.Kind {
		case OptInt:
			i, err := strconv.Atoi(v.Value)
			if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || err != nil {
				ps.add(v, path+"."+k, "%s must be a whole number", k)
			} else if i < o.Min || i > o.Max {
				ps.add(v, path+"."+k, "%s must be between %d and %d", k, o.Min, o.Max)
			} else {
				out[k] = i
			}
		case OptBool:
			if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" {
				ps.add(v, path+"."+k, "%s must be true or false", k)
			} else {
				out[k] = v.Value == "true"
			}
		case OptEnum:
			if v.Kind != yaml.ScalarNode || !contains(o.Values, v.Value) {
				ps.add(v, path+"."+k, "%s must be one of %s", k, strings.Join(o.Values, ", "))
			} else {
				out[k] = v.Value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// checkOverlap reports panels that share a cell. The UI compacts a layout as it
// is edited, so a file that overlaps is a mistake, not a state to render.
func checkOverlap(b Board, nodes []*yaml.Node, path string, ps *problems) {
	for j := range b.Panels {
		for i := 0; i < j; i++ {
			a, c := b.Panels[i], b.Panels[j]
			if a.ID == "" || c.ID == "" || a.At[2] == 0 || c.At[2] == 0 {
				continue
			}
			if a.At[0] < c.At[0]+c.At[2] && c.At[0] < a.At[0]+a.At[2] && a.At[1] < c.At[1]+c.At[3] && c.At[1] < a.At[1]+a.At[3] {
				ps.add(nodes[j], fmt.Sprintf("%s.panels[%d].at", path, j), "panel %q overlaps %q on board %q; move one of them (positions are [x, y, w, h])", c.ID, a.ID, b.ID)
				break
			}
		}
	}
}

// readQuery checks a panel's query: its shape, then the fields its type takes.
func readQuery(n *yaml.Node, path, q string, pt PanelType, known bool, p *Panel, ps *problems) {
	terms, err := ParseQuery(q)
	if err != nil {
		ps.add(n, path, "a panel's query: %s", err.Error())
		return
	}
	if known {
		if msgs := ValidateQuery(pt, terms); len(msgs) > 0 {
			for _, m := range msgs {
				ps.add(n, path, "a panel's query, %s", m)
			}
			return
		}
	}
	if strings.TrimSpace(q) != "" {
		p.Query = q
	}
}
